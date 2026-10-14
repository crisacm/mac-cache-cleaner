package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// SaveScan inserts a scan row.
func (s *Store) SaveScan(ctx context.Context, scan domain.Scan) error {
	const q = `INSERT INTO scans (id, started_at, finished_at, root, full, partial)
	           VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, q,
		scan.ID, formatTime(scan.StartedAt), formatTime(scan.FinishedAt),
		scan.Root, boolToInt(scan.Full), boolToInt(scan.Partial))
	if err != nil {
		return fmt.Errorf("store: save scan: %w", err)
	}
	return nil
}

// FinishScan records the completion time and partial flag of a scan.
func (s *Store) FinishScan(ctx context.Context, id string, finishedAt time.Time, partial bool) error {
	const q = `UPDATE scans SET finished_at = ?, partial = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, formatTime(finishedAt), boolToInt(partial), id)
	if err != nil {
		return fmt.Errorf("store: finish scan: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: finish scan %s: %w", id, domain.ErrNotFound)
	}
	return nil
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

// LatestScan returns the most recent scan.
func (s *Store) LatestScan(ctx context.Context) (domain.Scan, error) {
	const q = `SELECT id, started_at, finished_at, root, full, partial
	           FROM scans ORDER BY started_at DESC, id DESC LIMIT 1`
	row := s.db.QueryRowContext(ctx, q)
	scan, err := scanRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Scan{}, fmt.Errorf("store: latest scan: %w", domain.ErrNotFound)
	}
	return scan, err
}

// InsertEntries inserts or replaces entries in a single transaction.
func (s *Store) InsertEntries(ctx context.Context, entries []domain.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		const q = `INSERT OR REPLACE INTO entries
		    (entry_id, scan_id, path, parent, allocated_bytes, logical_bytes, mtime,
		     is_dir, kind, data_class, bundle_id, dev, inode)
		    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		stmt, err := tx.PrepareContext(ctx, q)
		if err != nil {
			return fmt.Errorf("store: prepare insert entries: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, e := range entries {
			if _, err := stmt.ExecContext(ctx,
				e.EntryID, e.ScanID, e.Path, e.Parent, e.AllocatedBytes, e.LogicalBytes,
				formatTime(e.MTime), boolToInt(e.IsDir), string(e.Kind), string(e.DataClass),
				e.BundleID, devToInt64(e.Dev), devToInt64(e.Inode)); err != nil {
				return fmt.Errorf("store: insert entry %s: %w", e.Path, err)
			}
		}
		return nil
	})
}

// EntriesByClass returns entries of a scan filtered by data class.
func (s *Store) EntriesByClass(ctx context.Context, scanID string, class domain.DataClass) ([]domain.Entry, error) {
	return s.queryEntries(ctx,
		`WHERE scan_id = ? AND data_class = ? ORDER BY allocated_bytes DESC`,
		scanID, string(class))
}

// EntriesByKind returns entries of a scan filtered by kind.
func (s *Store) EntriesByKind(ctx context.Context, scanID string, kind domain.Kind) ([]domain.Entry, error) {
	return s.queryEntries(ctx,
		`WHERE scan_id = ? AND kind = ? ORDER BY allocated_bytes DESC`,
		scanID, string(kind))
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
	// #nosec G202 -- where is a compile-time constant defined in this file, never user input.
	q := `SELECT entry_id, scan_id, path, parent, allocated_bytes, logical_bytes, mtime,
	             is_dir, kind, data_class, bundle_id, dev, inode
	      FROM entries ` + where
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

// InsertInaccessible records paths that could not be read for a scan.
func (s *Store) InsertInaccessible(ctx context.Context, scanID string, items []domain.Inaccessible) error {
	if len(items) == 0 {
		return nil
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		const q = `INSERT OR REPLACE INTO inaccessible (scan_id, path, reason) VALUES (?, ?, ?)`
		stmt, err := tx.PrepareContext(ctx, q)
		if err != nil {
			return fmt.Errorf("store: prepare inaccessible: %w", err)
		}
		defer func() { _ = stmt.Close() }()
		for _, it := range items {
			if _, err := stmt.ExecContext(ctx, scanID, it.Path, it.Reason); err != nil {
				return fmt.Errorf("store: insert inaccessible %s: %w", it.Path, err)
			}
		}
		return nil
	})
}

// Inaccessible returns the inaccessible paths of a scan.
func (s *Store) Inaccessible(ctx context.Context, scanID string) ([]domain.Inaccessible, error) {
	const q = `SELECT path, reason FROM inaccessible WHERE scan_id = ? ORDER BY path`
	rows, err := s.db.QueryContext(ctx, q, scanID)
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

// Totals returns aggregate sizes for a scan.
func (s *Store) Totals(ctx context.Context, scanID string) (domain.Totals, error) {
	const q = `SELECT COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0)
	           FROM entries WHERE scan_id = ?`
	var t domain.Totals
	err := s.db.QueryRowContext(ctx, q, scanID).Scan(&t.Entries, &t.AllocatedBytes, &t.LogicalBytes)
	if err != nil {
		return domain.Totals{}, fmt.Errorf("store: totals: %w", err)
	}
	return t, nil
}

// TotalsByClass returns aggregate sizes per data class for a scan.
func (s *Store) TotalsByClass(ctx context.Context, scanID string) (map[domain.DataClass]domain.Totals, error) {
	const q = `SELECT data_class, COUNT(*), COALESCE(SUM(allocated_bytes), 0), COALESCE(SUM(logical_bytes), 0)
	           FROM entries WHERE scan_id = ? GROUP BY data_class`
	rows, err := s.db.QueryContext(ctx, q, scanID)
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
