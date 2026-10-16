package store

import (
	"context"
	"fmt"
	"time"
)

// completedScanFilter selects scans that finished (in-progress or aborted scans
// have an empty finished_at).
const completedScanFilter = `finished_at IS NOT NULL AND finished_at <> ''`

// CleanIncompleteScans deletes scans that never finished and are older than
// cutoff. Such rows can only be leftovers of a process that was killed
// (SIGKILL bypasses the scan rollback). Returns the number removed.
func (s *Store) CleanIncompleteScans(ctx context.Context, cutoff time.Time) (int64, error) {
	q := fmt.Sprintf(`DELETE FROM scans
	                  WHERE NOT (%s) AND started_at < ?`, completedScanFilter)
	res, err := s.db.ExecContext(ctx, q, formatTime(cutoff))
	if err != nil {
		return 0, fmt.Errorf("store: clean incomplete scans: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PruneScans deletes old scans, keeping the newest `keep` *completed* ones.
// Deleting a scan cascades to its entries and inaccessible paths. Incomplete
// scans are always removed. Consents, plans, the audit log and MCP calls are
// never touched. Returns the number of scans removed.
func (s *Store) PruneScans(ctx context.Context, keep int) (int64, error) {
	if keep < 0 {
		keep = 0
	}
	q := fmt.Sprintf(`DELETE FROM scans WHERE id NOT IN (
	                      SELECT id FROM scans WHERE %s
	                      ORDER BY started_at DESC, id DESC LIMIT ?
	                  )`, completedScanFilter)
	res, err := s.db.ExecContext(ctx, q, keep)
	if err != nil {
		return 0, fmt.Errorf("store: prune scans: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// Vacuum rebuilds the database file, reclaiming space freed by pruning. It
// cannot run inside a transaction and may need free disk space temporarily.
func (s *Store) Vacuum(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return fmt.Errorf("store: vacuum: %w", err)
	}
	return s.EnsurePerms()
}
