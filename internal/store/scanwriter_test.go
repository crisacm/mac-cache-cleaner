package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
)

// TestScanTxHandlesConcurrentUse exercises the transactional writer from many
// goroutines at once (workers call UnchangedDir/PruneDir while the writer calls
// InsertEntries). A *sql.Tx is not safe for concurrent use; without the mutex
// this fails with a connection error. It is the regression test for a full
// scan being reported as "cancelado por el usuario".
func TestScanTxHandlesConcurrentUse(t *testing.T) {
	const rows = 15000
	ctx := context.Background()
	s := openTest(t)

	// Seed a directory with many children so PruneDir holds a read cursor for a
	// while, and hadRows is true.
	seed := make([]domain.Entry, 0, rows+1)
	seed = append(seed, domain.Entry{
		EntryID: "big", ScanID: "s0", Path: "/root/big", Parent: "/root",
		IsDir: true, Kind: domain.KindDir, DataClass: domain.ClassAppData,
	})
	for i := range rows {
		seed = append(seed, domain.Entry{
			EntryID: fmt.Sprintf("seed-%d", i), ScanID: "s0",
			Path: fmt.Sprintf("/root/big/f%05d", i), Parent: "/root/big",
			Kind: domain.KindFile, DataClass: domain.ClassAppData,
		})
	}
	require.NoError(t, s.InsertEntries(ctx, seed))

	tx, err := s.BeginScan(ctx, domain.Scan{ID: "s1", StartedAt: time.Now(), Root: "/root"})
	require.NoError(t, err)

	batch := make([]domain.Entry, rows)
	for i := range batch {
		batch[i] = domain.Entry{
			EntryID: fmt.Sprintf("new-%d", i), ScanID: "s1",
			Path: fmt.Sprintf("/root/other/f%05d", i), Parent: "/root/other",
			Kind: domain.KindFile, DataClass: domain.ClassAppData,
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { // holds a SELECT cursor over 15k rows
		defer wg.Done()
		if err := tx.PruneDir(ctx, "/root/big", nil); err != nil {
			errs <- fmt.Errorf("prune: %w", err)
		}
	}()
	go func() { // drops indexes and bulk-inserts on the same connection
		defer wg.Done()
		if err := tx.InsertEntries(ctx, batch); err != nil {
			errs <- fmt.Errorf("insert: %w", err)
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ScanTx use failed: %v", err)
	}
	require.NoError(t, tx.Rollback(ctx))
}

// TestFullScanOfRealTreeSucceeds runs the real scanner twice through the real
// store: a full re-scan walks every directory and calls PruneDir concurrently
// with the writer, which used to abort the scan.
func TestFullScanOfRealTreeSucceeds(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	for d := range 25 {
		dir := filepath.Join(caches, fmt.Sprintf("d%02d", d))
		require.NoError(t, os.MkdirAll(dir, 0o750))
		for f := range 20 {
			p := filepath.Join(dir, fmt.Sprintf("f%02d.bin", f))
			require.NoError(t, os.WriteFile(p, make([]byte, 1024), 0o600))
		}
	}

	s, err := Open(ctx, filepath.Join(t.TempDir(), "mcc"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	svc := scan.New(s, policy.New(home, s))

	first, err := svc.Run(ctx, scan.Options{Roots: []string{caches}, Workers: 8})
	require.NoError(t, err)
	require.NotZero(t, first.Result.Totals.Entries)

	// A full re-scan exercises PruneDir for every directory.
	second, err := svc.Run(ctx, scan.Options{Roots: []string{caches}, Full: true, Workers: 8})
	require.NoError(t, err)
	require.NotErrorIs(t, err, context.Canceled, "a full scan must not be reported as cancelled")
	require.Equal(t, first.Result.Totals.Entries, second.Result.Totals.Entries)
}
