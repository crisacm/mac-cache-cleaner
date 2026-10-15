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

	require.NoError(t, s.FinishScan(ctx, scan.ID, now.Add(time.Second), true))
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

func TestIncrementalCopySubtree(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	mt := time.Now().UTC().Truncate(time.Second)

	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: mt, Root: "/root"}))
	entries := []domain.Entry{
		{
			EntryID: domain.ComputeEntryID("s1", "/root/Library/Caches"), ScanID: "s1",
			Path: "/root/Library/Caches", Parent: "/root/Library", IsDir: true,
			Kind: domain.KindDir, DataClass: domain.ClassRegenerableCache,
			MTime: mt, AllocatedBytes: 100, LogicalBytes: 100,
		},
		{
			EntryID: domain.ComputeEntryID("s1", "/root/Library/Caches/a"), ScanID: "s1",
			Path: "/root/Library/Caches/a", Parent: "/root/Library/Caches",
			Kind: domain.KindFile, DataClass: domain.ClassRegenerableCache,
			AllocatedBytes: 60, LogicalBytes: 60,
		},
		{
			EntryID: domain.ComputeEntryID("s1", "/root/Library/Caches/b"), ScanID: "s1",
			Path: "/root/Library/Caches/b", Parent: "/root/Library/Caches",
			Kind: domain.KindFile, DataClass: domain.ClassRegenerableCache,
			AllocatedBytes: 40, LogicalBytes: 40,
		},
	}
	require.NoError(t, s.InsertEntries(ctx, entries))

	prev, ok, err := s.LatestScanID(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "s1", prev)

	got, ok, err := s.DirMTime(ctx, "s1", "/root/Library/Caches")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, mt, got)

	_, ok, err = s.DirMTime(ctx, "s1", "/root/nope")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s2", StartedAt: mt.Add(time.Minute), Root: "/root"}))

	sub, err := s.CopySubtree(ctx, "s1", "s2", "/root/Library/Caches")
	require.NoError(t, err)
	require.EqualValues(t, 100, sub.Totals.AllocatedBytes)
	require.EqualValues(t, 3, sub.Totals.Entries)
	require.EqualValues(t, 2, sub.ByClass[domain.ClassRegenerableCache].Entries)
	require.EqualValues(t, 100, sub.ByClass[domain.ClassRegenerableCache].AllocatedBytes)

	copied, err := s.EntriesByKind(ctx, "s2", domain.KindFile)
	require.NoError(t, err)
	require.Len(t, copied, 2)
	require.NotEqual(t, entries[1].EntryID, copied[0].EntryID, "entry IDs must be re-derived for the new scan")
}

func TestIncrementalAdapter(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/root"}))
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{{
		EntryID: domain.ComputeEntryID("s1", "/root/c"), ScanID: "s1", Path: "/root/c",
		IsDir: true, Kind: domain.KindDir, DataClass: domain.ClassRegenerableCache, MTime: time.Now(),
	}}))
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s2", StartedAt: time.Now().Add(time.Second), Root: "/root"}))

	inc := s.NewIncremental("s1", "s2")
	_, ok, err := inc.SubtreeMTime(ctx, "/root/c")
	require.NoError(t, err)
	require.True(t, ok)
	_, err = inc.CopySubtree(ctx, "s2", "/root/c")
	require.NoError(t, err)
}

func TestDeleteScanCascades(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	require.NoError(t, s.SaveScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/root"}))
	require.NoError(t, s.InsertEntries(ctx, []domain.Entry{{
		EntryID: "e1", ScanID: "s1", Path: "/root/f", Kind: domain.KindFile,
		DataClass: domain.ClassRegenerableCache,
	}}))
	require.NoError(t, s.InsertInaccessible(ctx, "s1", []domain.Inaccessible{{Path: "/root/x", Reason: "denied"}}))

	require.NoError(t, s.DeleteScan(ctx, "s1"))

	_, err := s.GetScan(ctx, "s1")
	require.ErrorIs(t, err, domain.ErrNotFound)
	totals, err := s.Totals(ctx, "s1")
	require.NoError(t, err)
	require.Zero(t, totals.Entries, "entries must be removed by cascade")
	paths, err := s.Paths(ctx, "s1")
	require.NoError(t, err)
	require.Empty(t, paths)
	inc, err := s.Inaccessible(ctx, "s1")
	require.NoError(t, err)
	require.Empty(t, inc)
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
