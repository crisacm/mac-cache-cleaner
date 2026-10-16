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

// Repo persists scan results (implemented by *store.Store).
type Repo interface {
	SaveScan(ctx context.Context, scan domain.Scan) error
	FinishScan(ctx context.Context, id string, finishedAt time.Time, partial bool) error
	InsertEntries(ctx context.Context, entries []domain.Entry) error
	InsertInaccessible(ctx context.Context, id string, items []domain.Inaccessible) error
	LatestScanID(ctx context.Context) (string, bool, error)
	// DeleteScan removes a scan and, by cascade, its entries (rollback).
	DeleteScan(ctx context.Context, id string) error
	// Incremental reuse.
	DirMTime(ctx context.Context, scanID, path string) (time.Time, bool, error)
	CopySubtree(ctx context.Context, srcScan, dstScan, prefix string) (domain.SubtreeTotals, error)
	// PruneScans keeps the newest `keep` completed scans (index retention).
	PruneScans(ctx context.Context, keep int) (int64, error)
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

// Run scans the configured roots. On error or cancellation the in-progress
// scan is rolled back so the previous complete scan stays the latest.
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

	prevID, hasPrev, err := s.repo.LatestScanID(ctx)
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
	if err := s.repo.SaveScan(ctx, scan); err != nil {
		return Outcome{}, err
	}

	walkOpts := scanner.Options{
		ScanID:        scanID,
		Roots:         roots,
		Full:          opts.Full,
		Workers:       opts.Workers,
		BatchSize:     opts.BatchSize,
		Progress:      opts.Progress,
		ProgressEvery: opts.ProgressEvery,
		Emit: func(ctx context.Context, entries []domain.Entry) error {
			return s.repo.InsertEntries(ctx, entries)
		},
	}
	if hasPrev && !opts.Full {
		walkOpts.Incremental = repoIncremental{repo: s.repo, prev: prevID}
	}

	res, err := s.walker.Scan(ctx, walkOpts)
	if err != nil {
		s.rollback(ctx, scanID)
		return Outcome{}, err
	}
	if ctx.Err() != nil {
		s.rollback(ctx, scanID)
		return Outcome{}, ctx.Err()
	}

	if err := s.repo.InsertInaccessible(ctx, scanID, res.Inaccessible); err != nil {
		s.rollback(ctx, scanID)
		return Outcome{}, err
	}
	finished := s.now()
	if err := s.repo.FinishScan(ctx, scanID, finished, res.Partial); err != nil {
		s.rollback(ctx, scanID)
		return Outcome{}, err
	}
	scan.FinishedAt = finished
	scan.Partial = res.Partial

	// Retention: keep the index bounded. Runs only after a successful scan, so
	// a failed or cancelled scan never leaves us without an index.
	keep := opts.RetainScans
	if keep <= 0 {
		keep = DefaultRetainScans
	}
	pruned, _ := s.repo.PruneScans(ctx, keep)

	return Outcome{Scan: scan, Result: res, Pruned: pruned}, nil
}

// rollback deletes a partially written scan, best effort.
func (s *Service) rollback(ctx context.Context, scanID string) {
	// Use a context detached from cancellation so rollback still runs when the
	// scan was cancelled.
	_ = s.repo.DeleteScan(context.WithoutCancel(ctx), scanID)
}

// repoIncremental adapts Repo to scanner.IncrementalIndex.
type repoIncremental struct {
	repo Repo
	prev string
}

// SubtreeMTime implements scanner.IncrementalIndex.
func (r repoIncremental) SubtreeMTime(ctx context.Context, path string) (time.Time, bool, error) {
	return r.repo.DirMTime(ctx, r.prev, path)
}

// CopySubtree implements scanner.IncrementalIndex.
func (r repoIncremental) CopySubtree(ctx context.Context, scanID, prefix string) (domain.SubtreeTotals, error) {
	return r.repo.CopySubtree(ctx, r.prev, scanID, prefix)
}
