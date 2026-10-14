package cleaner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/platform"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

// --- fakes -------------------------------------------------------------

type fakeStore struct {
	plans  map[string]domain.Plan
	items  map[string][]domain.PlanItem
	status map[string]domain.PlanStatus
	audit  []domain.AuditEntry
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		plans:  map[string]domain.Plan{},
		items:  map[string][]domain.PlanItem{},
		status: map[string]domain.PlanStatus{},
	}
}

func (f *fakeStore) CreatePlan(_ context.Context, p domain.Plan) error {
	f.plans[p.PlanID] = p
	f.items[p.PlanID] = p.Items
	f.status[p.PlanID] = p.Status
	return nil
}

func (f *fakeStore) GetPlan(_ context.Context, id string) (domain.Plan, error) {
	p, ok := f.plans[id]
	if !ok {
		return domain.Plan{}, domain.ErrNotFound
	}
	p.Status = f.status[id]
	return p, nil
}

func (f *fakeStore) PlanItems(_ context.Context, id string) ([]domain.PlanItem, error) {
	items, ok := f.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return items, nil
}

func (f *fakeStore) UpdatePlanStatus(_ context.Context, id string, st domain.PlanStatus) error {
	f.status[id] = st
	return nil
}

func (f *fakeStore) AppendAudit(_ context.Context, e domain.AuditEntry) error {
	f.audit = append(f.audit, e)
	return nil
}

type fakeTrasher struct{ trashed []string }

func (f *fakeTrasher) Trash(_ context.Context, path string) error {
	f.trashed = append(f.trashed, path)
	return os.Rename(path, path+".trashed")
}

type fakeRunning struct{ ids map[string]struct{} }

func (f fakeRunning) BundleIDs(context.Context) (map[string]struct{}, error) { return f.ids, nil }

func alwaysConfirm(bool) func(int, int64) (bool, error) {
	return func(int, int64) (bool, error) { return true, nil }
}

// --- helpers -----------------------------------------------------------

func mkEntry(t *testing.T, scanID, path string, class domain.DataClass) domain.Entry {
	t.Helper()
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	si, ok := platform.Stat(fi)
	require.True(t, ok)
	return domain.Entry{
		EntryID:        domain.ComputeEntryID(scanID, path),
		ScanID:         scanID,
		Path:           path,
		Parent:         filepath.Dir(path),
		AllocatedBytes: si.AllocatedBytes,
		LogicalBytes:   si.LogicalBytes,
		MTime:          fi.ModTime(),
		IsDir:          fi.IsDir(),
		Kind:           domain.KindDir,
		DataClass:      class,
		Dev:            si.Dev,
		Inode:          si.Inode,
	}
}

func mkCacheDir(t *testing.T, home, name string) string {
	t.Helper()
	dir := filepath.Join(home, "Library", "Caches", name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.bin"), make([]byte, 4096), 0o600))
	return dir
}

func newService(t *testing.T, home string, st Store, opts ...Option) *Service {
	t.Helper()
	base := []Option{
		WithTrasher(&fakeTrasher{}),
		WithRunningApps(fakeRunning{ids: map[string]struct{}{}}),
	}
	return New(st, policy.New(home, nil), home, append(base, opts...)...)
}

// --- plan --------------------------------------------------------------

func TestPlanEligibility(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.cache")
	appData := filepath.Join(home, "Library", "Application Support", "com.example.app")
	require.NoError(t, os.MkdirAll(appData, 0o750))

	svc := newService(t, home, newFakeStore())
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID: "scan-1",
		Candidates: []Candidate{
			{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)},
			{Entry: mkEntry(t, "scan-1", appData, domain.ClassAppData)}, // not explicit
			{Entry: mkEntry(t, "scan-1", appData, domain.ClassAppData), Explicit: true},
			{Entry: mkEntry(t, "scan-1", cache, domain.ClassUserFile)},
			{Entry: mkEntry(t, "scan-1", cache, domain.ClassApplication)},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Plan.Items, 2, "only regenerable_cache and explicitly selected app_data are planned")
	require.Len(t, res.Skipped, 3)
}

func TestPlanRefusesAllowlistedRootAndApple(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "Library", "Caches")
	require.NoError(t, os.MkdirAll(root, 0o750))
	apple := mkCacheDir(t, home, "com.apple.Safari")
	appleEntry := mkEntry(t, "scan-1", apple, domain.ClassRegenerableCache)
	appleEntry.BundleID = "com.apple.Safari"

	svc := newService(t, home, newFakeStore())
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID: "scan-1",
		Candidates: []Candidate{
			{Entry: mkEntry(t, "scan-1", root, domain.ClassRegenerableCache)},
			{Entry: appleEntry},
		},
	})
	require.NoError(t, err)
	require.Empty(t, res.Plan.Items)
	require.Len(t, res.Skipped, 2)
}

func TestPlanSkipsRunningApp(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.running")
	svc := New(newFakeStore(), policy.New(home, nil), home,
		WithTrasher(&fakeTrasher{}),
		WithRunningApps(fakeRunning{ids: map[string]struct{}{"com.example.running": {}}}),
	)
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)
	require.Empty(t, res.Plan.Items)
	require.Equal(t, "application is running", res.Skipped[0].Reason)
}

// --- apply -------------------------------------------------------------

func TestApplyRequiresConfirmation(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.cache")
	tr := &fakeTrasher{}
	st := newFakeStore()
	svc := New(st, policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)

	_, err = svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID})
	require.ErrorIs(t, err, domain.ErrUsage)
	require.Empty(t, tr.trashed)

	_, err = svc.Apply(context.Background(), ApplyOptions{
		PlanID:  res.Plan.PlanID,
		Confirm: func(int, int64) (bool, error) { return false, nil },
	})
	require.ErrorIs(t, err, domain.ErrBlocked)
	require.Empty(t, tr.trashed, "declined confirmation must delete nothing")
}

func TestApplyTrashesRevalidatedItems(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.cache")
	tr := &fakeTrasher{}
	st := newFakeStore()
	svc := New(st, policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)
	require.Len(t, res.Plan.Items, 1)

	out, err := svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID, Confirm: alwaysConfirm(true)})
	require.NoError(t, err)
	require.Len(t, out.Applied, 1)
	require.Len(t, tr.trashed, 1)
	require.Equal(t, domain.PlanApplied, st.status[res.Plan.PlanID])
	require.NotEmpty(t, st.audit)
	require.DirExists(t, cache+".trashed")
}

func TestApplySkipsSymlinkSwapTOCTOU(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "Caches", "com.example.cache")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.bin"), make([]byte, 4096), 0o600))

	tr := &fakeTrasher{}
	st := newFakeStore()
	svc := New(st, policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", dir, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)

	// Swap the directory for a symlink between plan and apply.
	require.NoError(t, os.RemoveAll(dir))
	require.NoError(t, os.Symlink("/etc", dir))

	out, err := svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID, Confirm: alwaysConfirm(true)})
	require.NoError(t, err)
	require.Empty(t, out.Applied)
	require.Len(t, out.Skipped, 1)
	require.Contains(t, out.Skipped[0].Reason, "symlink")
	require.Empty(t, tr.trashed)
}

func TestApplySkipsChangedSize(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "Caches", "com.example.cache")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	file := filepath.Join(dir, "data.bin")
	require.NoError(t, os.WriteFile(file, make([]byte, 4096), 0o600))

	tr := &fakeTrasher{}
	svc := New(newFakeStore(), policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", dir, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)

	// Change the directory's mtime by adding a file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "extra.bin"), make([]byte, 16), 0o600))

	out, err := svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID, Confirm: alwaysConfirm(true)})
	require.NoError(t, err)
	require.Empty(t, out.Applied)
	require.Len(t, out.Skipped, 1)
}

func TestApplyRejectsExpiredPlan(t *testing.T) {
	home := t.TempDir()
	st := newFakeStore()
	tr := &fakeTrasher{}
	svc := New(st, policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))

	past := time.Now().Add(-time.Hour)
	plan := domain.Plan{
		PlanID: "p-expired", Hash: "h", CreatedAt: past, ExpiresAt: past.Add(time.Minute),
		Status: domain.PlanPending,
	}
	require.NoError(t, st.CreatePlan(context.Background(), plan))

	_, err := svc.Apply(context.Background(), ApplyOptions{PlanID: "p-expired", Confirm: alwaysConfirm(true)})
	require.ErrorIs(t, err, domain.ErrBlocked)
	require.Equal(t, domain.PlanExpired, st.status["p-expired"])
}

func TestApplyRejectsHashMismatch(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.cache")
	st := newFakeStore()
	tr := &fakeTrasher{}
	svc := New(st, policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)

	// Tamper with the stored items after planning.
	tampered := st.items[res.Plan.PlanID]
	tampered[0].Size++
	st.items[res.Plan.PlanID] = tampered

	_, err = svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID, Confirm: alwaysConfirm(true)})
	require.ErrorIs(t, err, domain.ErrBlocked)
	require.Empty(t, tr.trashed)
	require.Equal(t, domain.PlanFailed, st.status[res.Plan.PlanID])
}

func TestApplyOnlyAffectsPlannedItems(t *testing.T) {
	home := t.TempDir()
	planned := mkCacheDir(t, home, "com.example.planned")
	unplanned := mkCacheDir(t, home, "com.example.unplanned")

	tr := &fakeTrasher{}
	svc := New(newFakeStore(), policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", planned, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)
	_, err = svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID, Confirm: alwaysConfirm(true)})
	require.NoError(t, err)

	require.Equal(t, []string{planned}, tr.trashed)
	require.DirExists(t, unplanned, "unplanned items must be untouched")
}

func TestApplyPermanentRequiresExtraConfirmation(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.cache")
	svc := New(newFakeStore(), policy.New(home, nil), home, WithTrasher(&fakeTrasher{}), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)

	_, err = svc.Apply(context.Background(), ApplyOptions{
		PlanID:    res.Plan.PlanID,
		Confirm:   alwaysConfirm(true),
		Permanent: true,
	})
	require.ErrorIs(t, err, domain.ErrBlocked)
	require.DirExists(t, cache)
}

func TestApplySkipsRunningAppAtApplyTime(t *testing.T) {
	home := t.TempDir()
	cache := mkCacheDir(t, home, "com.example.late")
	tr := &fakeTrasher{}
	svc := New(newFakeStore(), policy.New(home, nil), home, WithTrasher(tr), WithRunningApps(fakeRunning{}))
	res, err := svc.Plan(context.Background(), PlanOptions{
		ScanID:     "scan-1",
		Candidates: []Candidate{{Entry: mkEntry(t, "scan-1", cache, domain.ClassRegenerableCache)}},
	})
	require.NoError(t, err)

	// The app starts running before apply.
	svc.running = fakeRunning{ids: map[string]struct{}{"com.example.late": {}}}
	out, err := svc.Apply(context.Background(), ApplyOptions{PlanID: res.Plan.PlanID, Confirm: alwaysConfirm(true)})
	require.NoError(t, err)
	require.Empty(t, out.Applied)
	require.Equal(t, "application is running", out.Skipped[0].Reason)
}
