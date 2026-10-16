package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// openTest opens a store in a fresh temp directory.
func openTest(t *testing.T) *Store {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mcc")
	s, err := Open(context.Background(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

func TestOpenAppliesRestrictivePermissions(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "mcc")
	s, err := Open(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// SEC-TRN-03: directory 0700, files 0600.
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm(), "storage dir must be 0700")

	fi, err := os.Stat(s.Path())
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "database file must be 0600")
}

func TestScanRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC().Truncate(time.Second)

	scan := domain.Scan{
		ID:        "scan-1",
		StartedAt: now,
		Root:      "/Users/test",
		Full:      false,
	}
	require.NoError(t, s.SaveScan(ctx, scan))

	entries := []domain.Entry{
		{
			EntryID: domain.ComputeEntryID(scan.ID, "/Users/test/Library/Caches"), ScanID: scan.ID,
			Path: "/Users/test/Library/Caches", Parent: "/Users/test/Library",
			AllocatedBytes: 4096, LogicalBytes: 8192, MTime: now, IsDir: true,
			Kind: domain.KindDir, DataClass: domain.ClassRegenerableCache, Dev: 1, Inode: 10,
		},
		{
			EntryID: domain.ComputeEntryID(scan.ID, "/Users/test/Applications/Foo.app"), ScanID: scan.ID,
			Path: "/Users/test/Applications/Foo.app", Parent: "/Users/test/Applications",
			AllocatedBytes: 2048, LogicalBytes: 2048, MTime: now, IsDir: true,
			Kind: domain.KindApp, DataClass: domain.ClassApplication, BundleID: "com.example.foo",
			Dev: 1, Inode: 11,
		},
	}
	require.NoError(t, s.InsertEntries(ctx, entries))
	// A scan only counts as "latest" once it is finished.
	require.NoError(t, s.FinishScan(ctx, scan.ID, now.Add(time.Second), true))

	got, err := s.GetScan(ctx, scan.ID)
	require.NoError(t, err)
	require.Equal(t, scan.ID, got.ID)
	require.Equal(t, "/Users/test", got.Root)
	require.Equal(t, now, got.StartedAt)

	latest, err := s.LatestScan(ctx)
	require.NoError(t, err)
	require.Equal(t, scan.ID, latest.ID)

	totals, err := s.Totals(ctx, scan.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, totals.Entries)
	require.EqualValues(t, 6144, totals.AllocatedBytes)

	byClass, err := s.TotalsByClass(ctx, scan.ID)
	require.NoError(t, err)
	require.Contains(t, byClass, domain.ClassRegenerableCache)

	caches, err := s.EntriesByClass(ctx, scan.ID, domain.ClassRegenerableCache)
	require.NoError(t, err)
	require.Len(t, caches, 1)

	byID, err := s.EntryByID(ctx, entries[1].EntryID)
	require.NoError(t, err)
	require.Equal(t, "com.example.foo", byID.BundleID)

	got, err = s.GetScan(ctx, scan.ID)
	require.NoError(t, err)
	require.True(t, got.Partial)
	require.False(t, got.FinishedAt.IsZero())
}

func TestGetScanNotFound(t *testing.T) {
	s := openTest(t)
	_, err := s.GetScan(context.Background(), "missing")
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestInaccessible(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/r"}))
	require.NoError(t, s.InsertInaccessible(ctx, "s1", []domain.Inaccessible{
		{Path: "/r/secret", Reason: "permission denied"},
	}))
	got, err := s.Inaccessible(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "permission denied", got[0].Reason)
}

func TestConsentLifecycle(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC()

	require.NoError(t, s.GrantConsent(ctx, domain.Consent{Root: "/Users/test/Documents", Tier: domain.TierUserData, GrantedAt: now}))

	c, ok, err := s.ActiveConsent(ctx, "/Users/test/Documents", now)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.TierUserData, c.Tier)

	require.NoError(t, s.RevokeConsent(ctx, "/Users/test/Documents", now.Add(time.Minute)))
	_, ok, err = s.ActiveConsent(ctx, "/Users/test/Documents", now.Add(2*time.Minute))
	require.NoError(t, err)
	require.False(t, ok, "revoked consent must not be active")

	all, err := s.Consents(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	require.ErrorIs(t, s.RevokeConsent(ctx, "/nope", now), domain.ErrNotFound)
}

func TestPlanRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC().Truncate(time.Second)

	plan := domain.Plan{
		PlanID:    "plan-1",
		Hash:      "abc123",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
		Status:    domain.PlanPending,
		Items: []domain.PlanItem{
			{
				EntryID: "e1", Path: "/Users/test/Library/Caches/com.example", Dev: 1, Inode: 42,
				Size: 1024, MTime: now, DataClass: domain.ClassRegenerableCache,
			},
		},
	}
	require.NoError(t, s.CreatePlan(ctx, plan))

	got, err := s.GetPlan(ctx, "plan-1")
	require.NoError(t, err)
	require.Equal(t, "abc123", got.Hash)
	require.Equal(t, domain.PlanPending, got.Status)

	items, err := s.PlanItems(ctx, "plan-1")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "e1", items[0].EntryID)
	require.EqualValues(t, 42, items[0].Inode)

	require.NoError(t, s.UpdatePlanStatus(ctx, "plan-1", domain.PlanApplied))
	got, err = s.GetPlan(ctx, "plan-1")
	require.NoError(t, err)
	require.Equal(t, domain.PlanApplied, got.Status)

	plans, err := s.ListPlans(ctx)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	_, err = s.GetPlan(ctx, "missing")
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestAuditAppend(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, s.AppendAudit(ctx, domain.AuditEntry{
		At: now, Action: "trash", Path: "/Users/test/Library/Caches/x", Size: 10,
		PlanID: "plan-1", Result: "ok",
	}))
	log, err := s.AuditLog(ctx, 10)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, "trash", log[0].Action)
	require.Equal(t, now, log[0].At)
}

func TestPurgeClearsData(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/r"}))
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{{
		EntryID: "e1", ScanID: "s1", Path: "/r/f", Kind: domain.KindFile,
		DataClass: domain.ClassRegenerableCache,
	}}))
	require.NoError(t, s.GrantConsent(ctx, domain.Consent{Root: "/r", Tier: domain.TierUserData, GrantedAt: time.Now()}))
	require.NoError(t, s.AppendAudit(ctx, domain.AuditEntry{At: time.Now(), Action: "x", Path: "/r/f"}))

	require.NoError(t, s.Purge(ctx))

	info, err := s.Info(ctx)
	require.NoError(t, err)
	require.Zero(t, info.Scans)
	require.Zero(t, info.Entries)
	require.Zero(t, info.Consents)
	require.Zero(t, info.Audit)
}

func TestScanTxReuseAndPrune(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	mt := time.Now().UTC().Truncate(time.Second)

	// Seed the single-generation index with a directory and two children.
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{
		{
			EntryID: "d", ScanID: "s0", Path: "/root/Library/Caches", Parent: "/root/Library",
			IsDir: true, Kind: domain.KindDir, DataClass: domain.ClassRegenerableCache,
			MTime: mt, AllocatedBytes: 100, LogicalBytes: 100,
		},
		{
			EntryID: "a", ScanID: "s0", Path: "/root/Library/Caches/a", Parent: "/root/Library/Caches",
			Kind: domain.KindFile, DataClass: domain.ClassRegenerableCache, AllocatedBytes: 60, LogicalBytes: 60,
		},
		{
			EntryID: "b", ScanID: "s0", Path: "/root/Library/Caches/b", Parent: "/root/Library/Caches",
			Kind: domain.KindFile, DataClass: domain.ClassRegenerableCache, AllocatedBytes: 40, LogicalBytes: 40,
		},
	}))

	tx, err := s.BeginScan(ctx, domain.Scan{ID: "s1", StartedAt: mt, Root: "/root"})
	require.NoError(t, err)

	// A directory with the same mtime is reusable without walking it.
	alloc, _, ok, err := tx.UnchangedDir(ctx, "/root/Library/Caches", mt)
	require.NoError(t, err)
	require.True(t, ok)
	require.EqualValues(t, 100, alloc)

	// "b" vanished; pruning removes only the paths that are no longer present.
	require.NoError(t, tx.PruneDir(ctx, "/root/Library/Caches", []string{"/root/Library/Caches/a"}))
	require.NoError(t, tx.ReplaceInaccessible(ctx, nil))
	_, err = tx.Commit(ctx, mt.Add(time.Second), false, 1)
	require.NoError(t, err)

	files, err := s.EntriesByKind(ctx, "s1", domain.KindFile)
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "/root/Library/Caches/a", files[0].Path, "the vanished path is gone")

	// A directory whose mtime changed is not reusable.
	tx2, err := s.BeginScan(ctx, domain.Scan{ID: "s2", StartedAt: mt.Add(time.Minute), Root: "/root"})
	require.NoError(t, err)
	_, _, ok, err = tx2.UnchangedDir(ctx, "/root/Library/Caches", mt.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, tx2.Rollback(ctx))
}

func TestScanTxRollbackKeepsIndex(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{
		{EntryID: "a", ScanID: "s0", Path: "/root/a", Kind: domain.KindFile, DataClass: domain.ClassAppData},
	}))

	tx, err := s.BeginScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/root"})
	require.NoError(t, err)
	require.NoError(t, tx.InsertEntries(ctx, []domain.Entry{
		{EntryID: "x", ScanID: "s1", Path: "/root/x", Kind: domain.KindFile, DataClass: domain.ClassAppData},
	}))
	require.NoError(t, tx.Rollback(ctx))

	// The rolled-back scan published nothing and left the previous index intact.
	_, err = s.GetScan(ctx, "s1")
	require.ErrorIs(t, err, domain.ErrNotFound)
	totals, err := s.Totals(ctx, "s1")
	require.NoError(t, err)
	require.EqualValues(t, 1, totals.Entries)
	paths, err := s.Paths(ctx, "s1")
	require.NoError(t, err)
	require.Equal(t, []string{"/root/a"}, paths)
}

func TestDeleteScanKeepsSingleGenerationIndex(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/root"}))
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{{
		EntryID: "e1", ScanID: "s1", Path: "/root/f", Kind: domain.KindFile,
		DataClass: domain.ClassRegenerableCache,
	}}))

	require.NoError(t, s.DeleteScan(ctx, "s1"))

	_, err := s.GetScan(ctx, "s1")
	require.ErrorIs(t, err, domain.ErrNotFound)
	// The index is single-generation: removing a scan row does not remove it.
	totals, err := s.Totals(ctx, "s1")
	require.NoError(t, err)
	require.EqualValues(t, 1, totals.Entries)
}

func TestMCPCallAudit(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.AppendMCPCall(ctx, domain.MCPCall{
		At: time.Now(), Tool: "mcc_list_caches", ArgsSummary: "page_size=50", Status: "ok", DurationMS: 3, ItemCount: 5,
	}))
	calls, err := s.MCPCalls(ctx, 10)
	require.NoError(t, err)
	require.Len(t, calls, 1)
	require.Equal(t, "mcc_list_caches", calls[0].Tool)

	info, err := s.Info(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, info.MCPCalls)

	require.NoError(t, s.Purge(ctx))
	info, err = s.Info(ctx)
	require.NoError(t, err)
	require.Zero(t, info.MCPCalls, "purge must clear the mcp audit")
}

func TestLatestScanIgnoresIncomplete(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	base := time.Now().UTC().Truncate(time.Second)

	// A completed scan...
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "done", StartedAt: base, Root: "/r"}))
	require.NoError(t, s.FinishScan(ctx, "done", base.Add(time.Minute), false))
	// ...and a newer in-progress one (no finished_at).
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "half", StartedAt: base.Add(time.Hour), Root: "/r"}))

	got, err := s.LatestScan(ctx)
	require.NoError(t, err)
	require.Equal(t, "done", got.ID, "an unfinished scan must never be the latest")

	id, ok, err := s.LatestScanID(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "done", id)
}

func TestCleanIncompleteScans(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	now := time.Now().UTC()

	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "old-half", StartedAt: now.Add(-2 * time.Hour), Root: "/r"}))
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "recent-half", StartedAt: now.Add(-time.Minute), Root: "/r"}))
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "done", StartedAt: now.Add(-3 * time.Hour), Root: "/r"}))
	require.NoError(t, s.FinishScan(ctx, "done", now.Add(-3*time.Hour), false))

	n, err := s.CleanIncompleteScans(ctx, now.Add(-time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "only the old unfinished scan is removed")

	_, err = s.GetScan(ctx, "old-half")
	require.ErrorIs(t, err, domain.ErrNotFound)
	_, err = s.GetScan(ctx, "recent-half")
	require.NoError(t, err, "a recent unfinished scan may belong to another process")
	_, err = s.GetScan(ctx, "done")
	require.NoError(t, err, "completed scans are never touched")
}

func TestPruneScansKeepsNewestAndProtectsData(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	base := time.Now().UTC().Truncate(time.Second)

	// Three completed scans, oldest to newest, each with an entry.
	for i, id := range []string{"s1", "s2", "s3"} {
		at := base.Add(time.Duration(i) * time.Minute)
		require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: id, StartedAt: at, Root: "/r"}))
		require.NoError(t, s.InsertEntries(ctx, []domain.Entry{{
			EntryID: domain.ComputeEntryID(id, "/r/f"), ScanID: id, Path: "/r/f",
			Kind: domain.KindFile, DataClass: domain.ClassRegenerableCache,
		}}))
		require.NoError(t, s.FinishScan(ctx, id, at.Add(time.Second), false))
	}
	// Protected data that must survive pruning.
	require.NoError(t, s.GrantConsent(ctx, domain.Consent{Root: "/r", Tier: domain.TierUserData, GrantedAt: base}))
	require.NoError(t, s.CreatePlan(ctx, domain.Plan{PlanID: "p1", Hash: "h", CreatedAt: base, ExpiresAt: base.Add(time.Hour), Status: domain.PlanPending}))
	require.NoError(t, s.AppendAudit(ctx, domain.AuditEntry{At: base, Action: "trash", Path: "/r/f"}))
	require.NoError(t, s.AppendMCPCall(ctx, domain.MCPCall{At: base, Tool: "mcc_get_status", Status: "ok"}))

	n, err := s.PruneScans(ctx, 1)
	require.NoError(t, err)
	require.EqualValues(t, 2, n)
	require.Equal(t, int64(1), countScans(t, s), "only the newest scan is retained")

	// Retained: newest scan and its entry.
	totals, err := s.Totals(ctx, "s3")
	require.NoError(t, err)
	require.EqualValues(t, 1, totals.Entries)
	// Protected tables are untouched.
	plan, err := s.GetPlan(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, "p1", plan.PlanID)
	consents, err := s.Consents(ctx)
	require.NoError(t, err)
	require.Len(t, consents, 1)
	log, err := s.AuditLog(ctx, 10)
	require.NoError(t, err)
	require.Len(t, log, 1)
	calls, err := s.MCPCalls(ctx, 10)
	require.NoError(t, err)
	require.Len(t, calls, 1)
}

func countScans(t *testing.T, s *Store) int64 {
	t.Helper()
	var n int64
	require.NoError(t, s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM scans`).Scan(&n))
	return n
}

func TestStoreFilesAreRestricted(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "mcc")
	s, err := Open(ctx, dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Write something so the WAL sidecar exists.
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/r"}))
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{{
		EntryID: "e1", ScanID: "s1", Path: "/r/f", Kind: domain.KindFile, DataClass: domain.ClassRegenerableCache,
	}}))
	require.NoError(t, s.FinishScan(ctx, "s1", time.Now(), false))
	require.NoError(t, s.EnsurePerms())

	for _, p := range []string{s.Path(), s.Path() + "-wal", s.Path() + "-shm"} {
		fi, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "%s must be 0600", p)
	}
}

func TestVacuumKeepsDatabaseUsable(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/r"}))
	require.NoError(t, s.FinishScan(ctx, "s1", time.Now(), false))
	require.NoError(t, s.Vacuum(ctx))
	_, err := s.LatestScan(ctx)
	require.NoError(t, err)
}
