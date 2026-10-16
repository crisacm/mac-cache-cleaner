package store

import (
	"context"
	"fmt"
)

// reportIndexes are the per-scan indexes used by read queries (list/report/
// MCP). They are NOT needed while a scan bulk-loads rows, so they are dropped
// when a scan starts and recreated when it finishes: maintaining them row by
// row during a 300k-row load is far more expensive than one sort afterwards.
//
// idx_entries_path is deliberately NOT in this set: the incremental scan reads
// it (DirMTime, CopySubtree) while the new scan is still loading.
var reportIndexes = []struct{ name, ddl string }{
	{"idx_entries_scan_kind", `CREATE INDEX IF NOT EXISTS idx_entries_scan_kind ON entries (scan_id, kind)`},
	{"idx_entries_scan_allocated", `CREATE INDEX IF NOT EXISTS idx_entries_scan_allocated ON entries (scan_id, allocated_bytes DESC)`},
	{"idx_entries_scan_data_class", `CREATE INDEX IF NOT EXISTS idx_entries_scan_data_class ON entries (scan_id, data_class)`},
}

// ensureReportIndexes creates every report index if missing (idempotent). It
// runs on Open so that a process killed mid-scan leaves a usable index.
func (s *Store) ensureReportIndexes(ctx context.Context) error {
	for _, idx := range reportIndexes {
		if _, err := s.db.ExecContext(ctx, idx.ddl); err != nil {
			return fmt.Errorf("store: create index %s: %w", idx.name, err)
		}
	}
	return nil
}

// dropReportIndexes removes the report indexes before a bulk load. Best effort:
// a missing index is not an error.
func (s *Store) dropReportIndexes(ctx context.Context) error {
	for _, idx := range reportIndexes {
		if _, err := s.db.ExecContext(ctx, "DROP INDEX IF EXISTS "+idx.name); err != nil {
			return fmt.Errorf("store: drop index %s: %w", idx.name, err)
		}
	}
	return nil
}
