package store

import (
	"context"
	"fmt"
)

// DeleteScan removes a scan row. The index itself is single-generation and is
// NOT touched: rolling back an in-progress scan happens by discarding its
// transaction (ScanTx.Rollback), so the previous index stays intact
// (SEC-COD-04).
func (s *Store) DeleteScan(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scans WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete scan %s: %w", id, err)
	}
	return nil
}
