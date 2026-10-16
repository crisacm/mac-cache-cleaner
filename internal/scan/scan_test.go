package scan

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scanner"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	scans      map[string]domain.Scan
	entries    map[string][]domain.Entry
	finished   map[string]bool
	committed  []string
	rolledBack []string
	latest     string
	pruneKeep  []int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		scans:    map[string]domain.Scan{},
		entries:  map[string][]domain.Entry{},
		finished: map[string]bool{},
	}
}

func (f *fakeRepo) BeginScan(_ context.Context, s domain.Scan) (Writer, error) {
	f.scans[s.ID] = s
	f.latest = s.ID
	return &fakeWriter{repo: f, scanID: s.ID}, nil
}

func (f *fakeRepo) ScanSummary(context.Context, []string) (domain.Totals, map[domain.DataClass]domain.Totals, map[domain.Kind]domain.Totals, error) {
	return domain.Totals{}, map[domain.DataClass]domain.Totals{}, map[domain.Kind]domain.Totals{}, nil
}

func (f *fakeRepo) LatestScanID(_ context.Context) (string, bool, error) {
	if f.latest == "" {
		return "", false, nil
	}
	return f.latest, true, nil
}

// fakeWriter models one scan transaction.
type fakeWriter struct {
	repo    *fakeRepo
	scanID  string
	entries []domain.Entry
}

func (w *fakeWriter) InsertEntries(_ context.Context, e []domain.Entry) error {
	w.entries = append(w.entries, e...)
	return nil
}

func (w *fakeWriter) PruneDir(context.Context, string, []string) error { return nil }

func (w *fakeWriter) UnchangedDir(context.Context, string, time.Time) (int64, int64, bool, error) {
	return 0, 0, false, nil
}

func (w *fakeWriter) ReplaceInaccessible(context.Context, []domain.Inaccessible) error { return nil }

func (w *fakeWriter) Commit(_ context.Context, _ time.Time, partial bool, keep int) (int64, error) {
	w.repo.finished[w.scanID] = true
	w.repo.entries[w.scanID] = w.entries
	s := w.repo.scans[w.scanID]
	s.Partial = partial
	w.repo.scans[w.scanID] = s
	w.repo.pruneKeep = append(w.repo.pruneKeep, keep)
	w.repo.committed = append(w.repo.committed, w.scanID)
	return 0, nil
}

func (w *fakeWriter) Rollback(context.Context) error {
	w.repo.rolledBack = append(w.repo.rolledBack, w.scanID)
	return nil
}

// fakePolicy allows a fixed set of roots and blocks the rest.
type fakePolicy struct {
	roots   []string
	blocked map[string]string
}

func (f fakePolicy) AllowlistRoots() []string { return f.roots }
func (f fakePolicy) RootVerdict(_ context.Context, root string) (policy.Verdict, error) {
	if reason, ok := f.blocked[root]; ok {
		return policy.Verdict{Path: root, Decision: policy.DecisionDeny, Reason: reason}, nil
	}
	return policy.Verdict{Path: root, Decision: policy.DecisionAllow}, nil
}

func (f fakePolicy) PathVerdict(_, path string) policy.Verdict {
	return policy.Verdict{Path: path, Decision: policy.DecisionAllow}
}

func (f fakePolicy) Classify(string) (domain.Tier, string, bool) { return domain.TierDefault, "", true }

// fakeWalker returns a canned result or error.
type fakeWalker struct {
	result scanner.Result
	err    error
	calls  int
}

func (f *fakeWalker) Scan(ctx context.Context, opts scanner.Options) (scanner.Result, error) {
	f.calls++
	if f.err != nil {
		return scanner.Result{}, f.err
	}
	if opts.Emit != nil {
		if err := opts.Emit(ctx, []domain.Entry{{EntryID: "e1", ScanID: opts.ScanID, Path: "/x/f"}}); err != nil {
			return scanner.Result{}, err
		}
	}
	return f.result, nil
}

// --- tests -------------------------------------------------------------

func TestRunHappyPath(t *testing.T) {
	repo := newFakeRepo()
	walker := &fakeWalker{result: scanner.Result{Totals: domain.Totals{Entries: 1, AllocatedBytes: 10}}}
	svc := New(repo, fakePolicy{roots: []string{"/root"}}, WithWalker(walker))

	out, err := svc.Run(context.Background(), Options{})
	require.NoError(t, err)
	require.NotEmpty(t, out.Scan.ID)
	require.Equal(t, []string{"/root"}, out.Scan.Roots)
	require.True(t, repo.finished[out.Scan.ID])
	require.Len(t, repo.entries[out.Scan.ID], 1)
	require.Empty(t, repo.rolledBack)
}

func TestRunBlocksRootWithoutConsent(t *testing.T) {
	repo := newFakeRepo()
	walker := &fakeWalker{}
	svc := New(repo, fakePolicy{roots: []string{"/root"}, blocked: map[string]string{"/root": "consent required"}}, WithWalker(walker))

	_, err := svc.Run(context.Background(), Options{Roots: []string{"/root"}})
	require.ErrorIs(t, err, domain.ErrBlocked)
	require.Equal(t, 0, walker.calls, "a blocked root must be rejected before walking")
	require.Empty(t, repo.scans, "nothing must be persisted for a blocked root")
}

func TestRunRollsBackOnWalkerError(t *testing.T) {
	repo := newFakeRepo()
	walker := &fakeWalker{err: errors.New("boom")}
	svc := New(repo, fakePolicy{roots: []string{"/root"}}, WithWalker(walker))

	_, err := svc.Run(context.Background(), Options{})
	require.Error(t, err)
	require.Len(t, repo.rolledBack, 1, "a failed scan must be rolled back")
}

func TestRunRollsBackOnCancel(t *testing.T) {
	repo := newFakeRepo()
	ctx, cancel := context.WithCancel(context.Background())
	walker := &cancelWalker{cancel: cancel}
	svc := New(repo, fakePolicy{roots: []string{"/root"}}, WithWalker(walker))

	_, err := svc.Run(ctx, Options{})
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, repo.rolledBack, 1, "a cancelled scan must be rolled back")
	require.Empty(t, repo.finished, "a cancelled scan must not be finished")
	require.Empty(t, repo.entries, "rolled-back entries must be gone")
}

func TestRunUsesAllowlistRoots(t *testing.T) {
	repo := newFakeRepo()
	walker := &fakeWalker{}
	svc := New(repo, fakePolicy{roots: []string{"/a", "/b"}}, WithWalker(walker))
	out, err := svc.Run(context.Background(), Options{})
	require.NoError(t, err)
	require.Equal(t, []string{"/a", "/b"}, out.Scan.Roots)
}

func TestRunNoRoots(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo, fakePolicy{}, WithWalker(&fakeWalker{}))
	_, err := svc.Run(context.Background(), Options{})
	require.ErrorIs(t, err, domain.ErrUsage)
}

// cancelWalker cancels the scan context and returns its error, simulating a
// user pressing Esc mid-scan.
type cancelWalker struct{ cancel context.CancelFunc }

func (w *cancelWalker) Scan(ctx context.Context, _ scanner.Options) (scanner.Result, error) {
	w.cancel()
	return scanner.Result{}, ctx.Err()
}

func TestRunPrunesAfterSuccess(t *testing.T) {
	repo := newFakeRepo()
	walker := &fakeWalker{result: scanner.Result{}}
	svc := New(repo, fakePolicy{roots: []string{"/root"}}, WithWalker(walker))

	_, err := svc.Run(context.Background(), Options{RetainScans: 1})
	require.NoError(t, err)
	require.Equal(t, []int{1}, repo.pruneKeep, "pruning runs only after a successful scan")

	// A failed scan must not prune (the previous index is kept).
	repo2 := newFakeRepo()
	svc2 := New(repo2, fakePolicy{roots: []string{"/root"}}, WithWalker(&fakeWalker{err: errors.New("boom")}))
	_, err = svc2.Run(context.Background(), Options{})
	require.Error(t, err)
	require.Empty(t, repo2.pruneKeep, "a failed scan must never prune")
}

func TestRunPrunesAfterCancelDoesNotHappen(t *testing.T) {
	repo := newFakeRepo()
	ctx, cancel := context.WithCancel(context.Background())
	svc := New(repo, fakePolicy{roots: []string{"/root"}}, WithWalker(&cancelWalker{cancel: cancel}))

	_, err := svc.Run(ctx, Options{})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, repo.pruneKeep, "a cancelled scan must never prune")
}
