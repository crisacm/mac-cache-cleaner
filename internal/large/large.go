package large

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// Index streams entries of a scan larger than a threshold (implemented by
// store). It is defined here because large is the consumer (AGENTS.md §3).
type Index interface {
	StreamEntries(ctx context.Context, scanID string, minBytes int64, fn func(domain.Entry) error) error
}

// Service reports heavy files and directories.
type Service struct {
	index     Index
	threshold int64
}

// New builds a Service. A threshold <= 0 uses DefaultThreshold.
func New(index Index, threshold int64) *Service {
	if threshold <= 0 {
		threshold = DefaultThreshold
	}
	return &Service{index: index, threshold: threshold}
}

// Options configures a listing.
type Options struct {
	// Min overrides the service threshold when > 0.
	Min int64
	// Limit caps the number of results; <= 0 uses DefaultLimit.
	Limit int
	// PathPrefix restricts results to a subtree when non-empty.
	PathPrefix string
}

// List returns the heaviest items, largest first. Tier 1 results carry the
// user_file class and are report-only (SEC-ACC-04).
func (s *Service) List(ctx context.Context, scanID string, opts Options) ([]domain.LargeItem, error) {
	if scanID == "" {
		return nil, fmt.Errorf("%w: scan id is required", domain.ErrUsage)
	}
	minBytes := opts.Min
	if minBytes <= 0 {
		minBytes = s.threshold
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}

	top := newTopN(limit)
	err := s.index.StreamEntries(ctx, scanID, minBytes, func(e domain.Entry) error {
		if opts.PathPrefix != "" && !hasPrefix(e.Path, opts.PathPrefix) {
			return nil
		}
		top.add(e)
		return nil
	})
	if err != nil {
		return nil, err
	}

	entries := top.sorted()
	out := make([]domain.LargeItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, domain.LargeItem{
			Path:           e.Path,
			IsDir:          e.IsDir,
			Kind:           e.Kind,
			DataClass:      e.DataClass,
			AllocatedBytes: e.AllocatedBytes,
			LogicalBytes:   e.LogicalBytes,
			EntryID:        e.EntryID,
		})
	}
	return out, nil
}

// hasPrefix reports whether path is prefix itself or inside it, using a
// path-boundary comparison.
func hasPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(path, strings.TrimSuffix(prefix, sep)+sep)
}
