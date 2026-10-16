package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// DataInfo summarizes what is stored (for `mcc data show`, SEC-TRN-04).
type DataInfo struct {
	Dir      string `json:"dir"`
	Path     string `json:"path"`
	Scans    int64  `json:"scans"`
	Entries  int64  `json:"entries"`
	Plans    int64  `json:"plans"`
	Consents int64  `json:"consents"`
	Audit    int64  `json:"audit"`
	MCPCalls int64  `json:"mcp_calls"`

	// SizeBytes is the size of the database file on disk.
	SizeBytes int64 `json:"size_bytes"`
}

// Info returns counts and the storage location.
func (s *Store) Info(ctx context.Context) (DataInfo, error) {
	info := DataInfo{Dir: s.dir, Path: s.path}
	tables := []struct {
		name  string
		field *int64
	}{
		{"scans", &info.Scans},
		{"entries", &info.Entries},
		{"plans", &info.Plans},
		{"consents", &info.Consents},
		{"audit_log", &info.Audit},
		{"mcp_calls", &info.MCPCalls},
	}
	for _, t := range tables {
		// Table names are compile-time constants, never user input.
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+t.name).Scan(t.field); err != nil {
			return DataInfo{}, fmt.Errorf("store: count %s: %w", t.name, err)
		}
	}
	// The database file size on disk; cheaper and more meaningful than page
	// accounting (which scans the whole file).
	if fi, err := os.Stat(s.path); err == nil {
		info.SizeBytes = fi.Size()
	}
	return info, nil
}

// Purge removes all index data, plans, consents and the audit log
// (SEC-TRN-04). The schema is kept.
func (s *Store) Purge(ctx context.Context) error {
	// Table names are compile-time constants, never user input.
	tables := []string{"audit_log", "mcp_calls", "plan_items", "plans", "consents", "inaccessible", "entries", "scans"}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, name := range tables {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+name); err != nil { // #nosec G202 -- name is a compile-time constant
				return fmt.Errorf("store: purge %s: %w", name, err)
			}
		}
		return nil
	})
}
