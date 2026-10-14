// Package cleaner implements the two-step cleanup flow: plan (dry-run,
// persisted) and apply (revalidated, human-confirmed, Trash by default).
//
// The rules it enforces live in SECURITY.md §5: immutable plans with an ID,
// content hash and expiry (SEC-DEL-01); interactive confirmation showing count
// and size (SEC-DEL-02); eligibility by data class (SEC-DEL-03); Trash by
// default (SEC-DEL-04); apply-time revalidation (SEC-DEL-06); running-app
// caches skipped (SEC-DEL-07); append-only audit log (SEC-TRN-07).
package cleaner

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

const (
	// DefaultTTL is how long a plan stays valid.
	DefaultTTL = time.Hour
	// DefaultMaxItems caps how many items a single plan may contain
	// (SEC-DEL-11).
	DefaultMaxItems = 1000
)

// Store persists plans and the audit log (implemented by *store.Store).
type Store interface {
	CreatePlan(ctx context.Context, plan domain.Plan) error
	GetPlan(ctx context.Context, planID string) (domain.Plan, error)
	PlanItems(ctx context.Context, planID string) ([]domain.PlanItem, error)
	UpdatePlanStatus(ctx context.Context, planID string, status domain.PlanStatus) error
	AppendAudit(ctx context.Context, entry domain.AuditEntry) error
}

// Policy is the access-decision interface the cleaner consumes.
type Policy interface {
	DeleteVerdict(path, bundleID string) policy.Verdict
}

// Trasher moves a path to the Trash.
type Trasher interface {
	Trash(ctx context.Context, path string) error
}

// RunningApps reports the bundle IDs of currently running applications.
type RunningApps interface {
	BundleIDs(ctx context.Context) (map[string]struct{}, error)
}

// Option configures a Service.
type Option func(*Service)

// WithTrasher overrides the Trash implementation.
func WithTrasher(t Trasher) Option { return func(s *Service) { s.trash = t } }

// WithRunningApps overrides the running-apps provider.
func WithRunningApps(r RunningApps) Option { return func(s *Service) { s.running = r } }

// WithClock overrides the clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// Service coordinates planning and applying cleanup.
type Service struct {
	store   Store
	policy  Policy
	trash   Trasher
	running RunningApps
	now     func() time.Time
}

// New builds a Service. home locates the Trash directory.
func New(st Store, p Policy, home string, opts ...Option) *Service {
	s := &Service{
		store:   st,
		policy:  p,
		trash:   defaultTrasher(home),
		running: defaultRunningApps(),
		now:     time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Candidate is a proposed plan item.
type Candidate struct {
	Entry domain.Entry
	// Explicit marks a per-item human selection, required for app_data
	// (SEC-DEL-03).
	Explicit bool
}

// PlanOptions configures plan creation.
type PlanOptions struct {
	ScanID     string
	Candidates []Candidate
	TTL        time.Duration
	MaxItems   int
	MaxBytes   int64
}

// PlanResult is the outcome of planning.
type PlanResult struct {
	Plan    domain.Plan
	Skipped []Outcome
}

// Outcome records what happened to a single item.
type Outcome struct {
	EntryID string `json:"entry_id"`
	Path    string `json:"path"`
	Reason  string `json:"reason,omitempty"`
}

// Plan validates candidates and persists an immutable plan. It never touches
// the filesystem destructively (SEC-DEL-02).
func (s *Service) Plan(ctx context.Context, opts PlanOptions) (PlanResult, error) {
	if len(opts.Candidates) == 0 {
		return PlanResult{}, fmt.Errorf("%w: no candidates to plan", domain.ErrUsage)
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	maxItems := opts.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}

	running := map[string]struct{}{}
	if s.running != nil {
		if ids, err := s.running.BundleIDs(ctx); err == nil {
			running = ids
		}
	}

	now := s.now()
	var (
		items   []domain.PlanItem
		skipped []Outcome
		total   int64
	)
	for _, c := range opts.Candidates {
		e := c.Entry
		outcome := Outcome{EntryID: e.EntryID, Path: e.Path}

		switch {
		case e.DataClass.ReportOnly():
			outcome.Reason = fmt.Sprintf("%s is report-only", e.DataClass)
			skipped = append(skipped, outcome)
			continue
		case e.DataClass == domain.ClassAppData && !c.Explicit:
			outcome.Reason = "app_data requires explicit per-item selection (may contain user data)"
			skipped = append(skipped, outcome)
			continue
		case !e.DataClass.PlanEligible() && e.DataClass != domain.ClassAppData:
			outcome.Reason = fmt.Sprintf("%s is not eligible", e.DataClass)
			skipped = append(skipped, outcome)
			continue
		}

		if v := s.policy.DeleteVerdict(e.Path, e.BundleID); !v.Allowed() {
			outcome.Reason = v.Reason
			skipped = append(skipped, outcome)
			continue
		}
		if id := bundleIDFromPath(e.Path); id != "" {
			if _, busy := running[id]; busy {
				outcome.Reason = "application is running"
				skipped = append(skipped, outcome)
				continue
			}
		}
		if len(items) >= maxItems {
			outcome.Reason = "plan item limit reached"
			skipped = append(skipped, outcome)
			continue
		}
		if opts.MaxBytes > 0 && total+e.AllocatedBytes > opts.MaxBytes {
			outcome.Reason = "plan size limit reached"
			skipped = append(skipped, outcome)
			continue
		}

		total += e.AllocatedBytes
		items = append(items, domain.PlanItem{
			EntryID:   e.EntryID,
			Path:      e.Path,
			Dev:       e.Dev,
			Inode:     e.Inode,
			Size:      e.AllocatedBytes,
			MTime:     e.MTime,
			DataClass: e.DataClass,
		})
	}

	if len(items) == 0 {
		return PlanResult{Plan: domain.Plan{Status: domain.PlanPending}, Skipped: skipped}, nil
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })

	planID, err := domain.NewID()
	if err != nil {
		return PlanResult{}, fmt.Errorf("cleaner: generate plan id: %w", err)
	}
	plan := domain.Plan{
		PlanID:    planID,
		Hash:      hashPlanItems(items),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
		Status:    domain.PlanPending,
		Items:     items,
		TotalSize: total,
	}
	if err := s.store.CreatePlan(ctx, plan); err != nil {
		return PlanResult{}, err
	}
	return PlanResult{Plan: plan, Skipped: skipped}, nil
}

// hashPlanItems computes the plan's content hash.
func hashPlanItems(items []domain.PlanItem) string {
	parts := make([]string, 0, len(items)*6)
	for _, it := range items {
		parts = append(parts,
			it.EntryID, it.Path,
			fmt.Sprintf("%d", it.Dev),
			fmt.Sprintf("%d", it.Inode),
			fmt.Sprintf("%d", it.Size),
			it.MTime.UTC().Format(time.RFC3339Nano),
		)
	}
	return domain.HashStrings(parts...)
}

// bundleIDFromPath derives a candidate bundle ID from a data path's last
// component (e.g. ~/Library/Caches/com.example.app).
func bundleIDFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), ".savedState")
	if !strings.Contains(base, ".") {
		return ""
	}
	return base
}

// defaultTrasher returns the platform Trash implementation.
func defaultTrasher(home string) Trasher { return newTrasher(home) }

// defaultRunningApps returns the platform running-app discovery.
func defaultRunningApps() RunningApps { return newRunningApps() }
