package store

import (
	"context"
	"fmt"
)

// DeleteScan removes a scan and, by foreign-key cascade, its entries and
// inaccessible paths. It is used to roll back a cancelled or failed scan so
// the previous complete scan stays the latest (SEC-COD-04).
func (s *Store) DeleteScan(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM scans WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete scan %s: %w", id, err)
	}
	return nil
}
