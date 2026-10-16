// Package scan orchestrates a scan: it validates roots through policy, persists
// the scan, streams entries into the repository, and finishes — or rolls the
// in-progress scan back on error or cancellation (SEC-COD-04).
//
// It returns structured values and never prints (AGENTS.md §3), so both the CLI
// and the TUI (and a future MCP server) can reuse it.
package scan

import (
	"context"
	"fmt"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scanner"
)

// Writer is a transactional, single-generation scan write. Everything a
// scan writes goes through one writer; Commit publishes it and Rollback
// discards it, so a cancelled scan leaves the previous index intact.
type Writer interface {
	InsertEntries(ctx context.Context, entries []domain.Entry) error
	PruneDir(ctx context.Context, dir string, keep []string) error
	UnchangedDir(ctx context.Context, path string, mtime time.Time) (alloc, logical int64, ok bool, err error)
	ReplaceInaccessible(ctx context.Context, items []domain.Inaccessible) error
	Commit(ctx context.Context, finishedAt time.Time, partial bool, keep int) (int64, error)
	Rollback(ctx context.Context) error
}

// Repo persists scan results (implemented by *store.Store).
type Repo interface {
	LatestScanID(ctx context.Context) (string, bool, error)
	// BeginScan opens a transactional scan writer.
	BeginScan(ctx context.Context, scan domain.Scan) (Writer, error)
	// ScanSummary recomputes the aggregate result from the stored index.
	ScanSummary(ctx context.Context, roots []string) (domain.Totals, map[domain.DataClass]domain.Totals, map[domain.Kind]domain.Totals, error)
}

// DefaultRetainScans is how many completed scans are kept after a scan.
const DefaultRetainScans = 1

// Policy decides which roots may be scanned (implemented by *policy.Policy).
type Policy interface {
	RootVerdict(ctx context.Context, root string) (policy.Verdict, error)
	PathVerdict(root, path string) policy.Verdict
	Classify(path string) (domain.Tier, string, bool)
	AllowlistRoots() []string
}

// Walker runs the directory walk (implemented by *scanner.Scanner).
type Walker interface {
	Scan(ctx context.Context, opts scanner.Options) (scanner.Result, error)
}

// Options configures a scan run.
type Options struct {
	// Roots to scan. Empty means the policy's default allowlist.
	Roots []string
	// Full disables incremental reuse.
	Full bool
	// Workers bounds concurrency (0 = scanner default).
	Workers   int
	BatchSize int
	// Progress and ProgressEvery are forwarded to the scanner.
	Progress      func(scanner.Progress)
	ProgressEvery int
	// ScanID, when empty, is generated.
	ScanID string
	// RetainScans is how many completed scans to keep afterwards (<= 0 uses
	// DefaultRetainScans).
	RetainScans int
}

// Outcome is the result of a completed scan.
type Outcome struct {
	Scan   domain.Scan
	Result scanner.Result
	// Pruned is how many old scans were removed after this one completed.
	Pruned int64
}

// Service orchestrates scans.
type Service struct {
	repo   Repo
	policy Policy
	walker Walker
	now    func() time.Time
}

// Option configures a Service.
type Option func(*Service)

// WithWalker overrides the walker (for tests).
func WithWalker(w Walker) Option { return func(s *Service) { s.walker = w } }

// WithClock overrides the clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// New builds a Service with the real scanner and clock.
func New(repo Repo, p Policy, opts ...Option) *Service {
	s := &Service{repo: repo, policy: p, now: time.Now}
	for _, o := range opts {
		o(s)
	}
	if s.walker == nil {
		s.walker = scanner.New(p)
	}
	return s
}

// DefaultRoots returns the roots that are scanned when none are given.
func (s *Service) DefaultRoots() []string { return s.policy.AllowlistRoots() }

// RootVerdict exposes policy's decision for a root (for the TUI/CLI to show
// the effective roots before scanning).
func (s *Service) RootVerdict(ctx context.Context, root string) (policy.Verdict, error) {
	return s.policy.RootVerdict(ctx, root)
}

// Run scans the configured roots. On error or cancellation the whole scan is
// rolled back so the previous complete index stays current.
func (s *Service) Run(ctx context.Context, opts Options) (Outcome, error) {
	roots := opts.Roots
	if len(roots) == 0 {
		roots = s.policy.AllowlistRoots()
	}
	if len(roots) == 0 {
		return Outcome{}, fmt.Errorf("%w: no roots to scan", domain.ErrUsage)
	}

	// Validate every root before writing anything (SEC-ACC-04).
	for _, r := range roots {
		v, err := s.policy.RootVerdict(ctx, r)
		if err != nil {
			return Outcome{}, err
		}
		if v.Blocked() || v.NeedsConsent() {
			return Outcome{}, fmt.Errorf("%w: root %s: %s", domain.ErrBlocked, v.Path, v.Reason)
		}
	}

	scanID := opts.ScanID
	if scanID == "" {
		id, err := domain.NewID()
		if err != nil {
			return Outcome{}, err
		}
		scanID = id
	}

	_, hasPrev, err := s.repo.LatestScanID(ctx)
	if err != nil {
		return Outcome{}, err
	}

	scan := domain.Scan{
		ID:        scanID,
		StartedAt: s.now(),
		Root:      roots[0],
		Roots:     roots,
		Full:      opts.Full,
	}

	tx, err := s.repo.BeginScan(ctx, scan)
	if err != nil {
		return Outcome{}, err
	}
	// Any failure from here on discards the whole scan.
	rollback := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }

	walkOpts := scanner.Options{
		ScanID:        scanID,
		Roots:         roots,
		Full:          opts.Full,
		Workers:       opts.Workers,
		BatchSize:     opts.BatchSize,
		Progress:      opts.Progress,
		ProgressEvery: opts.ProgressEvery,
		Emit:          tx.InsertEntries,
		PruneDir:      tx.PruneDir,
	}
	if hasPrev && !opts.Full {
		walkOpts.Incremental = tx
	}

	res, err := s.walker.Scan(ctx, walkOpts)
	if err != nil {
		rollback()
		return Outcome{}, err
	}
	if ctx.Err() != nil {
		rollback()
		return Outcome{}, ctx.Err()
	}

	if err := tx.ReplaceInaccessible(ctx, res.Inaccessible); err != nil {
		rollback()
		return Outcome{}, err
	}

	keep := opts.RetainScans
	if keep <= 0 {
		keep = DefaultRetainScans
	}
	finished := s.now()
	pruned, err := tx.Commit(ctx, finished, res.Partial, keep)
	if err != nil {
		rollback()
		return Outcome{}, err
	}

	// Recompute the aggregates from the committed index: reused subtrees were
	// not walked, so the scanner's in-memory totals would undercount.
	totals, byClass, byKind, err := s.repo.ScanSummary(ctx, roots)
	if err != nil {
		return Outcome{}, err
	}
	res.Totals, res.ByClass, res.ByKind = totals, byClass, byKind

	scan.FinishedAt = finished
	scan.Partial = res.Partial

	return Outcome{Scan: scan, Result: res, Pruned: pruned}, nil
}
