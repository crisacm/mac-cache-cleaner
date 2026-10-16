package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
)

// ScanTx is a transactional, single-generation scan write. Every write of a
// scan goes through ONE transaction, so a cancelled or failed scan rolls back
// to the previous index (SEC-COD-04) instead of leaving a half-written tree.
//
// The index holds one current view of the disk keyed by path, so a scan only
// rewrites the paths it actually walks and prunes the ones that vanished under
// the directories it walked.
type ScanTx struct {
	store  *Store
	tx     *sql.Tx
	scanID string
	// hadRows records whether the index already had entries when the scan
	// started; when false there is nothing to prune.
	hadRows bool
	// indexesDropped records whether we actually dropped the deferred indexes.
	// A fully-reused scan writes nothing, so it must not pay for dropping and
	// rebuilding them.
	indexesDropped bool
}

// BeginScan opens a scan transaction, drops the deferred indexes and records
// the scan row. Call Commit to publish or Rollback to discard. It returns the
// scan.Writer interface so *Store satisfies the consumer-side interface
// without the scan package depending on this one.
func (s *Store) BeginScan(ctx context.Context, sc domain.Scan) (scan.Writer, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin scan: %w", err)
	}
	t := &ScanTx{store: s, tx: tx, scanID: sc.ID}
	if err := t.hadRowsTx(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	const q = `INSERT INTO scans (id, started_at, finished_at, root, full, partial)
	           VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := tx.ExecContext(ctx, q, sc.ID, formatTime(sc.StartedAt),
		formatTime(sc.FinishedAt), sc.Root, boolToInt(sc.Full), boolToInt(sc.Partial)); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("store: save scan: %w", err)
	}
	return t, nil
}

// dropIndexes drops the deferred indexes on first write (idempotent).
func (t *ScanTx) dropIndexes(ctx context.Context) error {
	if t.indexesDropped {
		return nil
	}
	if err := dropIndexesTx(ctx, t.tx); err != nil {
		return err
	}
	t.indexesDropped = true
	return nil
}

func (t *ScanTx) hadRowsTx(ctx context.Context) error {
	var n int
	if err := t.tx.QueryRowContext(ctx, `SELECT 1 FROM entries LIMIT 1`).Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("store: check entries: %w", err)
	}
	t.hadRows = true
	return nil
}

// InsertEntries upserts entries (path is the key) in chunked multi-row INSERTs.
func (t *ScanTx) InsertEntries(ctx context.Context, entries []domain.Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if err := t.dropIndexes(ctx); err != nil {
		return err
	}
	// On the very first scan the index is empty, so a plain INSERT is enough
	// (no per-row conflict lookup for OR REPLACE).
	return insertEntriesTx(ctx, t.tx, entries, t.hadRows)
}

// PruneDir deletes stored children of dir that are not in keep, together with
// their subtrees. It is called for every directory the scan actually walked, so
// paths that vanished are removed while untouched subtrees keep their rows.
func (t *ScanTx) PruneDir(ctx context.Context, dir string, keep []string) error {
	if !t.hadRows {
		return nil
	}
	rows, err := t.tx.QueryContext(ctx, `SELECT path FROM entries WHERE parent = ?`, dir)
	if err != nil {
		return fmt.Errorf("store: list children of %s: %w", dir, err)
	}
	keepSet := make(map[string]struct{}, len(keep))
	for _, p := range keep {
		keepSet[p] = struct{}{}
	}
	var gone []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: scan child row: %w", err)
		}
		if _, ok := keepSet[p]; !ok {
			gone = append(gone, p)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("store: iterate children of %s: %w", dir, err)
	}
	_ = rows.Close()

	if len(gone) == 0 {
		return nil
	}
	if err := t.dropIndexes(ctx); err != nil {
		return err
	}
	for _, p := range gone {
		// Delete the path itself and its whole subtree using the path PK range
		// (index-friendly; LIKE with a prefix is not).
		if _, err := t.tx.ExecContext(ctx,
			`DELETE FROM entries WHERE path = ? OR (path >= ? AND path < ?)`,
			p, p+"/", p+"0"); err != nil {
			return fmt.Errorf("store: prune %s: %w", p, err)
		}
	}
	return nil
}

// UnchangedDir reports whether path is a directory whose stored mtime equals
// mtime, returning its stored subtree sizes so the caller can roll them up
// without walking or rewriting the subtree.
func (t *ScanTx) UnchangedDir(ctx context.Context, path string, mtime time.Time) (alloc, logical int64, ok bool, err error) {
	var isDir int
	var stored string
	err = t.tx.QueryRowContext(ctx,
		`SELECT allocated_bytes, logical_bytes, is_dir, mtime FROM entries WHERE path = ?`, path).
		Scan(&alloc, &logical, &isDir, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("store: dir mtime %s: %w", path, err)
	}
	if isDir == 0 || !parseTime(stored).Equal(mtime) {
		return 0, 0, false, nil
	}
	return alloc, logical, true, nil
}

// ReplaceInaccessible stores the inaccessible paths of this scan, replacing the
// previous generation's list.
func (t *ScanTx) ReplaceInaccessible(ctx context.Context, items []domain.Inaccessible) error {
	return replaceInaccessibleTx(ctx, t.tx, items)
}

// Commit marks the scan finished, prunes older scan rows and recreates the
// indexes in the same transaction, then publishes everything.
func (t *ScanTx) Commit(ctx context.Context, finishedAt time.Time, partial bool, keep int) (int64, error) {
	const q = `UPDATE scans SET finished_at = ?, partial = ? WHERE id = ?`
	if _, err := t.tx.ExecContext(ctx, q, formatTime(finishedAt), boolToInt(partial), t.scanID); err != nil {
		return 0, fmt.Errorf("store: finish scan: %w", err)
	}
	if keep < 0 {
		keep = 0
	}
	prune := fmt.Sprintf(`DELETE FROM scans WHERE id NOT IN (
	                         SELECT id FROM scans WHERE %s
	                         ORDER BY started_at DESC, id DESC LIMIT ?
	                     )`, completedScanFilter)
	res, err := t.tx.ExecContext(ctx, prune, keep)
	if err != nil {
		return 0, fmt.Errorf("store: prune scans: %w", err)
	}
	pruned, _ := res.RowsAffected()

	if t.indexesDropped {
		if err := ensureIndexesTx(ctx, t.tx); err != nil {
			return 0, err
		}
	}
	if err := t.tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit scan: %w", err)
	}
	return pruned, nil
}

// Rollback discards the whole scan (cancelled or failed).
func (t *ScanTx) Rollback(_ context.Context) error {
	if err := t.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return fmt.Errorf("store: rollback scan: %w", err)
	}
	return nil
}

// insertChunkRows is how many rows go into a single multi-row INSERT. Kept well
// below SQLite's variable limit (13 columns × rows).
const insertChunkRows = 500

// insertEntriesTx writes entries (path is the key) in chunked multi-row
// INSERTs. replace selects upsert (INSERT OR REPLACE) semantics; pass false
// only when the table is known to be empty.
func insertEntriesTx(ctx context.Context, tx *sql.Tx, entries []domain.Entry, replace bool) error {
	for start := 0; start < len(entries); start += insertChunkRows {
		end := start + insertChunkRows
		if end > len(entries) {
			end = len(entries)
		}
		if err := insertEntryChunk(ctx, tx, entries[start:end], replace); err != nil {
			return err
		}
	}
	return nil
}

// replaceInaccessibleTx replaces the whole inaccessible list.
func replaceInaccessibleTx(ctx context.Context, tx *sql.Tx, items []domain.Inaccessible) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM inaccessible`); err != nil {
		return fmt.Errorf("store: clear inaccessible: %w", err)
	}
	if len(items) == 0 {
		return nil
	}
	const q = `INSERT OR REPLACE INTO inaccessible (path, reason) VALUES (?, ?)`
	stmt, err := tx.PrepareContext(ctx, q)
	if err != nil {
		return fmt.Errorf("store: prepare inaccessible: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	for _, it := range items {
		if _, err := stmt.ExecContext(ctx, it.Path, it.Reason); err != nil {
			return fmt.Errorf("store: insert inaccessible %s: %w", it.Path, err)
		}
	}
	return nil
}

// insertEntryChunk issues one multi-row INSERT (path is the primary key).
func insertEntryChunk(ctx context.Context, tx *sql.Tx, batch []domain.Entry, replace bool) error {
	verb := "INSERT INTO"
	if replace {
		verb = "INSERT OR REPLACE INTO"
	}
	var q strings.Builder
	q.WriteString(verb)
	q.WriteString(` entries
	    (path, entry_id, scan_id, parent, allocated_bytes, logical_bytes, mtime,
	     is_dir, kind, data_class, bundle_id, dev, inode) VALUES `)
	args := make([]any, 0, len(batch)*13)
	for i, e := range batch {
		if i > 0 {
			q.WriteByte(',')
		}
		q.WriteString("(?,?,?,?,?,?,?,?,?,?,?,?,?)")
		args = append(args, e.Path, e.EntryID, e.ScanID, e.Parent, e.AllocatedBytes, e.LogicalBytes,
			formatTime(e.MTime), boolToInt(e.IsDir), string(e.Kind), string(e.DataClass),
			e.BundleID, devToInt64(e.Dev), devToInt64(e.Inode))
	}
	if _, err := tx.ExecContext(ctx, q.String(), args...); err != nil {
		return fmt.Errorf("store: insert entries: %w", err)
	}
	return nil
}
