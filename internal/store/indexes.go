package store

import (
	"context"
	"database/sql"
	"fmt"
)

// deferredIndexes are the indexes that are NOT needed while a scan upserts
// rows, so they are dropped at scan start and recreated once at commit: keeping
// them up to date row by row is far more expensive than one sort afterwards.
//
// idx_entries_parent is deliberately NOT here: PruneDir looks up a directory's
// children (`WHERE parent = ?`) during the scan.
var deferredIndexes = []struct{ name, ddl string }{
	{"idx_entries_entry_id", `CREATE UNIQUE INDEX IF NOT EXISTS idx_entries_entry_id ON entries (entry_id)`},
	{"idx_entries_kind", `CREATE INDEX IF NOT EXISTS idx_entries_kind ON entries (kind)`},
	{"idx_entries_allocated", `CREATE INDEX IF NOT EXISTS idx_entries_allocated ON entries (allocated_bytes DESC)`},
	{"idx_entries_data_class", `CREATE INDEX IF NOT EXISTS idx_entries_data_class ON entries (data_class)`},
}

// ensureIndexes creates every deferred index if missing (idempotent). It runs
// on Open so a process killed mid-scan leaves a fully usable index.
func (s *Store) ensureIndexes(ctx context.Context) error {
	return ensureIndexesTx(ctx, s.db)
}

// dropIndexes removes the deferred indexes. Best effort: a missing index is not
// an error.
func (s *Store) dropIndexes(ctx context.Context) error {
	return dropIndexesTx(ctx, s.db)
}

// execer is implemented by both *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func dropIndexesTx(ctx context.Context, db execer) error {
	for _, idx := range deferredIndexes {
		if _, err := db.ExecContext(ctx, "DROP INDEX IF EXISTS "+idx.name); err != nil {
			return fmt.Errorf("store: drop index %s: %w", idx.name, err)
		}
	}
	return nil
}

func ensureIndexesTx(ctx context.Context, db execer) error {
	for _, idx := range deferredIndexes {
		if _, err := db.ExecContext(ctx, idx.ddl); err != nil {
			return fmt.Errorf("store: create index %s: %w", idx.name, err)
		}
	}
	return nil
}
