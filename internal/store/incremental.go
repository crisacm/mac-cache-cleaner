package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// LatestScanID returns the newest scan's ID, if any. Call it before creating a
// new scan to obtain the previous scan for incremental reuse.
func (s *Store) LatestScanID(ctx context.Context) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM scans
		 WHERE finished_at IS NOT NULL AND finished_at <> ''
		 ORDER BY started_at DESC, id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: latest scan id: %w", err)
	}
	return id, true, nil
}

// DirMTime returns the mtime recorded for a directory entry in a scan.
func (s *Store) DirMTime(ctx context.Context, scanID, path string) (time.Time, bool, error) {
	var mtime string
	err := s.db.QueryRowContext(ctx,
		`SELECT mtime FROM entries WHERE scan_id = ? AND path = ? AND is_dir = 1`,
		scanID, path).Scan(&mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("store: dir mtime: %w", err)
	}
	return parseTime(mtime), true, nil
}

// CopySubtree copies every entry under prefix from srcScan into dstScan,
// re-deriving entry IDs for the destination scan, and returns the subtree's
// aggregates for the scanner's totals.
func (s *Store) CopySubtree(ctx context.Context, srcScan, dstScan, prefix string) (domain.SubtreeTotals, error) {
	entries, err := s.queryEntries(ctx,
		`WHERE scan_id = ? AND (path = ? OR path LIKE ? ESCAPE '\')`,
		srcScan, prefix, escapeLike(prefix)+"/%")
	if err != nil {
		return domain.SubtreeTotals{}, err
	}
	if len(entries) == 0 {
		return domain.SubtreeTotals{}, fmt.Errorf("store: copy subtree %s: %w", prefix, domain.ErrNotFound)
	}

	sub := domain.SubtreeTotals{
		ByClass: map[domain.DataClass]domain.Totals{},
		ByKind:  map[domain.Kind]domain.Totals{},
	}
	copied := make([]domain.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Path == prefix && e.IsDir {
			sub.Totals.AllocatedBytes = e.AllocatedBytes
			sub.Totals.LogicalBytes = e.LogicalBytes
		}
		// Only leaf-like entries contribute to class/kind totals, avoiding
		// double counting with directory subtree sizes.
		if !e.IsDir || e.Kind == domain.KindPackage {
			accumulate(sub.ByClass, sub.ByKind, e)
		}
		e.EntryID = domain.ComputeEntryID(dstScan, e.Path)
		e.ScanID = dstScan
		copied = append(copied, e)
	}
	sub.Totals.Entries = int64(len(entries))

	if err := s.InsertEntries(ctx, copied); err != nil {
		return domain.SubtreeTotals{}, err
	}
	return sub, nil
}

// Incremental adapts a Store to the scanner's incremental index for a given
// previous scan and the destination scan being written.
type Incremental struct {
	store  *Store
	prev   string
	scanID string
}

// NewIncremental returns an adapter that reuses subtrees from prevScan when
// writing into scanID.
func (s *Store) NewIncremental(prevScan, scanID string) Incremental {
	return Incremental{store: s, prev: prevScan, scanID: scanID}
}

// SubtreeMTime implements scanner.IncrementalIndex.
func (i Incremental) SubtreeMTime(ctx context.Context, path string) (time.Time, bool, error) {
	return i.store.DirMTime(ctx, i.prev, path)
}

// CopySubtree implements scanner.IncrementalIndex.
func (i Incremental) CopySubtree(ctx context.Context, scanID, prefix string) (domain.SubtreeTotals, error) {
	return i.store.CopySubtree(ctx, i.prev, scanID, prefix)
}

func accumulate(byClass map[domain.DataClass]domain.Totals, byKind map[domain.Kind]domain.Totals, e domain.Entry) {
	c := byClass[e.DataClass]
	c.Entries++
	c.AllocatedBytes += e.AllocatedBytes
	c.LogicalBytes += e.LogicalBytes
	byClass[e.DataClass] = c

	k := byKind[e.Kind]
	k.Entries++
	k.AllocatedBytes += e.AllocatedBytes
	k.LogicalBytes += e.LogicalBytes
	byKind[e.Kind] = k
}

// escapeLike escapes LIKE wildcards in a literal path prefix.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
