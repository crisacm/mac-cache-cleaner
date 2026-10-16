package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// EntryTotalsByPath returns the recorded sizes of the entry at path in a scan.
// ok is false when the path was not indexed.
func (s *Store) EntryTotalsByPath(ctx context.Context, scanID, path string) (domain.Totals, bool, error) {
	const q = `SELECT allocated_bytes, logical_bytes FROM entries WHERE scan_id = ? AND path = ?`
	var t domain.Totals
	err := s.db.QueryRowContext(ctx, q, scanID, path).Scan(&t.AllocatedBytes, &t.LogicalBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Totals{}, false, nil
	}
	if err != nil {
		return domain.Totals{}, false, fmt.Errorf("store: entry totals %s: %w", path, err)
	}
	t.Entries = 1
	return t, true, nil
}

// TotalsByBundleID sums a scan's entries that carry a bundle ID.
func (s *Store) TotalsByBundleID(ctx context.Context, scanID, bundleID string) (domain.Totals, error) {
	const q = `SELECT COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0)
	           FROM entries WHERE scan_id = ? AND bundle_id = ? AND bundle_id <> ''`
	var t domain.Totals
	err := s.db.QueryRowContext(ctx, q, scanID, bundleID).Scan(&t.Entries, &t.AllocatedBytes, &t.LogicalBytes)
	if err != nil {
		return domain.Totals{}, fmt.Errorf("store: totals by bundle %s: %w", bundleID, err)
	}
	return t, nil
}

// Paths lists every entry path in the index (used by tests and tooling).
func (s *Store) Paths(ctx context.Context, _ string) ([]string, error) {
	const q = `SELECT path FROM entries ORDER BY path`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: query paths: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("store: scan path row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
