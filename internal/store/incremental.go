package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// LatestScanID returns the newest completed scan's ID, if any. It is used to
// decide whether a scan can reuse unchanged subtrees from the current index.
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
