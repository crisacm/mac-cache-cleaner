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
	scans        map[string]domain.Scan
	entries      map[string][]domain.Entry
	inaccessible map[string][]domain.Inaccessible
	finished     map[string]bool
	deleted      []string
	latest       string
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		scans:        map[string]domain.Scan{},
		entries:      map[string][]domain.Entry{},
		inaccessible: map[string][]domain.Inaccessible{},
		finished:     map[string]bool{},
	}
}

func (f *fakeRepo) SaveScan(_ context.Context, s domain.Scan) error {
	f.scans[s.ID] = s
	f.latest = s.ID
	return nil
}

func (f *fakeRepo) FinishScan(_ context.Context, id string, _ time.Time, partial bool) error {
	f.finished[id] = true
	s := f.scans[id]
	s.Partial = partial
	f.scans[id] = s
	return nil
}

func (f *fakeRepo) InsertEntries(_ context.Context, e []domain.Entry) error {
	if len(e) == 0 {
		return nil
	}
	f.entries[e[0].ScanID] = append(f.entries[e[0].ScanID], e...)
	return nil
}

func (f *fakeRepo) InsertInaccessible(_ context.Context, id string, items []domain.Inaccessible) error {
	f.inaccessible[id] = items
	return nil
}

func (f *fakeRepo) LatestScanID(_ context.Context) (string, bool, error) {
	if f.latest == "" {
		return "", false, nil
	}
	return f.latest, true, nil
}

func (f *fakeRepo) DeleteScan(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	delete(f.scans, id)
	delete(f.entries, id)
	if f.latest == id {
		f.latest = ""
	}
	return nil
}

func (f *fakeRepo) DirMTime(context.Context, string, string) (time.Time, bool, error) {
	return time.Time{}, false, nil
}

func (f *fakeRepo) CopySubtree(context.Context, string, string, string) (domain.SubtreeTotals, error) {
	return domain.SubtreeTotals{}, nil
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
	require.Empty(t, repo.deleted)
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
	require.Len(t, repo.deleted, 1, "a failed scan must be rolled back")
}

func TestRunRollsBackOnCancel(t *testing.T) {
	repo := newFakeRepo()
	ctx, cancel := context.WithCancel(context.Background())
	walker := &cancelWalker{cancel: cancel}
	svc := New(repo, fakePolicy{roots: []string{"/root"}}, WithWalker(walker))

	_, err := svc.Run(ctx, Options{})
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, repo.deleted, 1, "a cancelled scan must be rolled back")
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
