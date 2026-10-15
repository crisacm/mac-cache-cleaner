// Package orphan detects leftover data whose owning application is no longer
// installed, with a confidence level (SEC-ORP-01..03).
package orphan

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

// Index provides recorded sizes for indexed paths (implemented by store).
type Index interface {
	EntryTotalsByPath(ctx context.Context, scanID, path string) (domain.Totals, bool, error)
}

// Policy decides which locations may be read (implemented by policy.Policy).
type Policy interface {
	PathVerdict(root, path string) policy.Verdict
}

// candidateLocations are the ~/Library subdirectories that may hold per-app
// leftovers.
var candidateLocations = []string{
	"Library/Caches",
	"Library/Application Support",
	"Library/Containers",
	"Library/Group Containers",
	"Library/HTTPStorages",
	"Library/WebKit",
	"Library/Saved Application State",
	"Library/Logs",
	"Library/Preferences",
}

// Service detects orphans.
type Service struct {
	index Index
	home  string
	p     Policy
}

// New builds a Service.
func New(index Index, p Policy, home string) *Service {
	return &Service{index: index, home: home, p: p}
}

// Options configures orphan detection.
type Options struct {
	ScanID string
	// Installed is the set of installed bundle IDs.
	Installed map[string]struct{}
	// DiscoveryComplete is false when an application location could not be
	// read, in which case every confidence is degraded (SEC-ORP-02).
	DiscoveryComplete bool
	// DiscoveryReason explains why discovery was incomplete.
	DiscoveryReason string
	// MinConfidence filters results (empty means all).
	MinConfidence domain.Confidence
	// Progress, when set, receives the path currently being read (for a
	// loading indicator). It must not block.
	Progress func(string)
}

// List returns detected orphans, most confident first then largest.
func (s *Service) List(ctx context.Context, opts Options) ([]domain.Orphan, error) {
	installed := opts.Installed
	if installed == nil {
		installed = map[string]struct{}{}
	}

	var out []domain.Orphan
	report := func(p string) {
		if opts.Progress != nil {
			opts.Progress(p)
		}
	}
	for _, rel := range candidateLocations {
		locPath := filepath.Join(s.home, rel)
		report(locPath)
		des, err := os.ReadDir(locPath)
		if err != nil {
			continue // location missing or unreadable
		}
		for _, de := range des {
			name := normalizeCandidate(de.Name())
			if name == "" || isAppleName(name) {
				continue
			}
			if _, ok := installed[name]; ok {
				continue
			}
			full := filepath.Join(locPath, de.Name())
			report(full)
			if s.p != nil && s.p.PathVerdict(s.home, full).Blocked() {
				continue
			}

			conf := Classify(name, installed)
			if !opts.DiscoveryComplete {
				conf = degrade(conf)
			}
			if !meetsMin(conf, opts.MinConfidence) {
				continue
			}

			o := domain.Orphan{
				Path:       full,
				BundleID:   name,
				Name:       name,
				Confidence: conf,
				DataClass:  domain.ClassAppData,
				EntryID:    domain.ComputeEntryID(opts.ScanID, full),
			}
			if s.index != nil && opts.ScanID != "" {
				if tot, ok, err := s.index.EntryTotalsByPath(ctx, opts.ScanID, full); err == nil && ok {
					o.AllocatedBytes = tot.AllocatedBytes
					o.LogicalBytes = tot.LogicalBytes
				}
			}
			out = append(out, o)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Confidence != out[j].Confidence {
			return out[i].Confidence.Rank() > out[j].Confidence.Rank()
		}
		if out[i].AllocatedBytes != out[j].AllocatedBytes {
			return out[i].AllocatedBytes > out[j].AllocatedBytes
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// VerifyResult re-checks whether a single leftover belongs to an installed
// application right now.
type VerifyResult struct {
	Path              string            `json:"path"`
	BundleID          string            `json:"bundle_id"`
	AppFound          bool              `json:"app_found"`
	Confidence        domain.Confidence `json:"confidence"`
	ConfidenceReason  string            `json:"confidence_reason"`
	DiscoveryComplete bool              `json:"discovery_complete"`
	Reason            string            `json:"reason,omitempty"`
}

// Verify re-checks one candidate against the given installed set. It reports
// whether an owning application exists now and, when discovery was incomplete,
// degrades the confidence with a reason (SEC-ORP-02).
func (s *Service) Verify(path string, opts Options) VerifyResult {
	installed := opts.Installed
	if installed == nil {
		installed = map[string]struct{}{}
	}
	name := normalizeCandidate(filepath.Base(path))
	res := VerifyResult{
		Path:              path,
		BundleID:          name,
		Confidence:        Classify(name, installed),
		ConfidenceReason:  ConfidenceReason(Classify(name, installed)),
		DiscoveryComplete: opts.DiscoveryComplete,
	}
	if name == "" {
		res.Reason = "empty candidate name"
		return res
	}
	if isAppleName(name) {
		res.AppFound = true
		res.Reason = "apple-owned; excluded from orphan detection"
		return res
	}
	if _, ok := installed[name]; ok {
		res.AppFound = true
		res.Reason = "an application with this bundle id is installed"
		return res
	}
	if !opts.DiscoveryComplete {
		res.Confidence = degrade(res.Confidence)
		res.ConfidenceReason = ConfidenceReason(res.Confidence)
		res.Reason = "application discovery was incomplete; confidence degraded"
		return res
	}
	res.Reason = "no installed application matches this identifier"
	return res
}

func meetsMin(c, minConfidence domain.Confidence) bool {
	if minConfidence == "" {
		return true
	}
	return c.Rank() >= minConfidence.Rank()
}
