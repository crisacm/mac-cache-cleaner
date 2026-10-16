package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// SaveScan inserts a scan row and drops the deferred indexes so a following
// bulk load does not maintain them row by row; FinishScan recreates them. The
// transactional path (BeginScan/Commit) is used by the scanner instead.
func (s *Store) SaveScan(ctx context.Context, scan domain.Scan) error {
	const q = `INSERT INTO scans (id, started_at, finished_at, root, full, partial)
	           VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q,
		scan.ID, formatTime(scan.StartedAt), formatTime(scan.FinishedAt),
		scan.Root, boolToInt(scan.Full), boolToInt(scan.Partial))
	if err != nil {
		return fmt.Errorf("store: save scan: %w", err)
	}
	return s.dropIndexes(ctx)
}

// FinishScan records the completion time and partial flag of a scan and
// recreates the deferred indexes dropped by SaveScan.
func (s *Store) FinishScan(ctx context.Context, id string, finishedAt time.Time, partial bool) error {
	const q = `UPDATE scans SET finished_at = ?, partial = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, formatTime(finishedAt), boolToInt(partial), id)
	if err != nil {
		return fmt.Errorf("store: finish scan: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: finish scan %s: %w", id, domain.ErrNotFound)
	}
	return s.ensureIndexes(ctx)
}

// GetScan returns a scan by ID.
func (s *Store) GetScan(ctx context.Context, id string) (domain.Scan, error) {
	const q = `SELECT id, started_at, finished_at, root, full, partial FROM scans WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, id)
	scan, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Scan{}, fmt.Errorf("store: scan %s: %w", id, domain.ErrNotFound)
	}
	return scan, err
}

// LatestScan returns the most recent *completed* scan. In-progress or aborted
// scans (no finished_at) are ignored so readers never use a half-written index.
func (s *Store) LatestScan(ctx context.Context) (domain.Scan, error) {
	const q = `SELECT id, started_at, finished_at, root, full, partial
	           FROM scans
	           WHERE finished_at IS NOT NULL AND finished_at <> ''
	           ORDER BY started_at DESC, id DESC LIMIT 1`
	row := s.db.QueryRowContext(ctx, q)
	scan, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Scan{}, fmt.Errorf("store: latest scan: %w", domain.ErrNotFound)
	}
	return scan, err
}

// InsertEntries upserts entries (keyed by path) in one transaction. Used by
// tests and non-scan writers; the scanner writes through ScanTx.
func (s *Store) InsertEntries(ctx context.Context, entries []domain.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return insertEntriesTx(ctx, tx, entries)
	})
}

// EntriesByClass returns entries filtered by data class, largest first. The
// index is single-generation, so scanID is ignored (kept for API stability).
func (s *Store) EntriesByClass(ctx context.Context, _ string, class domain.DataClass) ([]domain.Entry, error) {
	return s.queryEntries(ctx, `WHERE data_class = ? ORDER BY allocated_bytes DESC`, string(class))
}

// EntriesByKind returns entries filtered by kind, largest first.
func (s *Store) EntriesByKind(ctx context.Context, _ string, kind domain.Kind) ([]domain.Entry, error) {
	return s.queryEntries(ctx, `WHERE kind = ? ORDER BY allocated_bytes DESC`, string(kind))
}

// EntryByID returns a single entry by its stable ID.
func (s *Store) EntryByID(ctx context.Context, entryID string) (domain.Entry, error) {
	rows, err := s.queryEntries(ctx, `WHERE entry_id = ?`, entryID)
	if err != nil {
		return domain.Entry{}, err
	}
	if len(rows) == 0 {
		return domain.Entry{}, fmt.Errorf("store: entry %s: %w", entryID, domain.ErrNotFound)
	}
	return rows[0], nil
}

func (s *Store) queryEntries(ctx context.Context, where string, args ...any) ([]domain.Entry, error) {
	// #nosec G202 -- where is a compile-time constant defined in this package, never user input.
	q := `SELECT entry_id, scan_id, path, parent, allocated_bytes, logical_bytes, mtime,
	             is_dir, kind, data_class, bundle_id, dev, inode
	      FROM entries ` + where
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

func scanEntries(rows *sql.Rows) ([]domain.Entry, error) {
	var out []domain.Entry
	for rows.Next() {
		var (
			e     domain.Entry
			mtime string
			isDir int
			kind  string
			class string
			dev   int64
			inode int64
		)
		if err := rows.Scan(&e.EntryID, &e.ScanID, &e.Path, &e.Parent,
			&e.AllocatedBytes, &e.LogicalBytes, &mtime, &isDir, &kind, &class,
			&e.BundleID, &dev, &inode); err != nil {
			return nil, fmt.Errorf("store: scan entry row: %w", err)
		}
		e.MTime = parseTime(mtime)
		e.IsDir = isDir != 0
		e.Kind = domain.Kind(kind)
		e.DataClass = domain.DataClass(class)
		e.Dev = int64ToDev(dev)
		e.Inode = int64ToDev(inode)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate entries: %w", err)
	}
	return out, nil
}

// InsertInaccessible replaces the stored inaccessible paths (the index is
// single-generation). scanID is ignored.
func (s *Store) InsertInaccessible(ctx context.Context, _ string, items []domain.Inaccessible) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		return replaceInaccessibleTx(ctx, tx, items)
	})
}

// Inaccessible returns the stored inaccessible paths.
func (s *Store) Inaccessible(ctx context.Context, _ string) ([]domain.Inaccessible, error) {
	const q = `SELECT path, reason FROM inaccessible ORDER BY path`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: query inaccessible: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Inaccessible
	for rows.Next() {
		var it domain.Inaccessible
		if err := rows.Scan(&it.Path, &it.Reason); err != nil {
			return nil, fmt.Errorf("store: scan inaccessible row: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// Totals returns aggregate sizes of the current index.
func (s *Store) Totals(ctx context.Context, _ string) (domain.Totals, error) {
	const q = `SELECT COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0) FROM entries`
	var t domain.Totals
	if err := s.db.QueryRowContext(ctx, q).Scan(&t.Entries, &t.AllocatedBytes, &t.LogicalBytes); err != nil {
		return domain.Totals{}, fmt.Errorf("store: totals: %w", err)
	}
	return t, nil
}

// TotalsByClass returns aggregate sizes per data class over all entries.
func (s *Store) TotalsByClass(ctx context.Context, _ string) (map[domain.DataClass]domain.Totals, error) {
	const q = `SELECT data_class, COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0)
	           FROM entries GROUP BY data_class`
	return s.totalsBy(ctx, q)
}

// TotalsByKind returns aggregate sizes per kind over all entries.
func (s *Store) TotalsByKind(ctx context.Context, _ string) (map[domain.Kind]domain.Totals, error) {
	const q = `SELECT kind, COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0)
	           FROM entries GROUP BY kind`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: totals by kind: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[domain.Kind]domain.Totals{}
	for rows.Next() {
		var (
			kind string
			t    domain.Totals
		)
		if err := rows.Scan(&kind, &t.Entries, &t.AllocatedBytes, &t.LogicalBytes); err != nil {
			return nil, fmt.Errorf("store: scan totals row: %w", err)
		}
		out[domain.Kind(kind)] = t
	}
	return out, rows.Err()
}

func (s *Store) totalsBy(ctx context.Context, q string) (map[domain.DataClass]domain.Totals, error) {
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("store: totals by class: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[domain.DataClass]domain.Totals{}
	for rows.Next() {
		var (
			class string
			t     domain.Totals
		)
		if err := rows.Scan(&class, &t.Entries, &t.AllocatedBytes, &t.LogicalBytes); err != nil {
			return nil, fmt.Errorf("store: scan totals row: %w", err)
		}
		out[domain.DataClass(class)] = t
	}
	return out, rows.Err()
}

// ScanSummary reproduces the scanner's result aggregates from the stored index:
// Totals sums the declared roots' subtree sizes, and by-class/by-kind totals
// count only leaf-like entries (files, symlinks, packages) to avoid counting
// directory subtree sizes twice.
func (s *Store) ScanSummary(ctx context.Context, roots []string) (domain.Totals, map[domain.DataClass]domain.Totals, map[domain.Kind]domain.Totals, error) {
	var total domain.Totals
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&total.Entries); err != nil {
		return domain.Totals{}, nil, nil, fmt.Errorf("store: scan summary count: %w", err)
	}
	if len(roots) > 0 {
		q := `SELECT COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0)
		      FROM entries WHERE path IN (` + placeholders(len(roots)) + `)`
		args := make([]any, len(roots))
		for i, r := range roots {
			args[i] = r
		}
		if err := s.db.QueryRowContext(ctx, q, args...).Scan(&total.AllocatedBytes, &total.LogicalBytes); err != nil {
			return domain.Totals{}, nil, nil, fmt.Errorf("store: scan summary roots: %w", err)
		}
	}

	const leafFilter = `WHERE kind IN ('file', 'symlink', 'package')`
	byClass, err := s.totalsBy(ctx, `SELECT data_class, COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0) FROM entries `+leafFilter+` GROUP BY data_class`)
	if err != nil {
		return domain.Totals{}, nil, nil, err
	}
	byKindRows, err := s.db.QueryContext(ctx, `SELECT kind, COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0) FROM entries `+leafFilter+` GROUP BY kind`)
	if err != nil {
		return domain.Totals{}, nil, nil, fmt.Errorf("store: scan summary by kind: %w", err)
	}
	defer func() { _ = byKindRows.Close() }()
	byKind := map[domain.Kind]domain.Totals{}
	for byKindRows.Next() {
		var (
			kind string
			t    domain.Totals
		)
		if err := byKindRows.Scan(&kind, &t.Entries, &t.AllocatedBytes, &t.LogicalBytes); err != nil {
			return domain.Totals{}, nil, nil, fmt.Errorf("store: scan summary kind row: %w", err)
		}
		byKind[domain.Kind(kind)] = t
	}
	return total, byClass, byKind, byKindRows.Err()
}

// placeholders returns "?, ?, ?" for n arguments.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*3-2)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, '?')
	}
	return string(b)
}

func scanRow(row *sql.Row) (domain.Scan, error) {
	var (
		scan       domain.Scan
		started    string
		finished   sql.NullString
		full, part int
	)
	if err := row.Scan(&scan.ID, &started, &finished, &scan.Root, &full, &part); err != nil {
		return domain.Scan{}, err
	}
	scan.StartedAt = parseTime(started)
	scan.FinishedAt = parseTime(finished.String)
	scan.Full = full != 0
	scan.Partial = part != 0
	return scan, nil
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit tx: %w", err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
