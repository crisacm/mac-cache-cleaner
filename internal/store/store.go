// Package store is the SQLite repository for MCC's local index.
//
// It stores only metadata (paths, sizes, timestamps, bundle IDs), consents,
// plans and the audit log — never file contents (SEC-TRN-02). The storage
// directory is 0700 and files are 0600 (SEC-TRN-03).
//
// Consumers depend on small interfaces they define themselves; this package
// exposes a concrete *Store (AGENTS.md §3).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // CGO-free SQLite driver (registered as "sqlite")

	"github.com/crisacm/mac-cache-cleaner/migrations"
)

const (
	dirMode  os.FileMode = 0o700
	fileMode os.FileMode = 0o600
	dbName               = "mcc.db"
)

// Store is a handle to the on-disk index.
type Store struct {
	db   *sql.DB
	dir  string
	path string
}

// DefaultDir returns the default storage directory under the given home:
// ~/Library/Application Support/mcc/.
func DefaultDir(home string) string {
	return filepath.Join(home, "Library", "Application Support", "mcc")
}

// Open creates (if needed) dir with mode 0700, opens the embedded database and
// applies pending migrations.
func Open(ctx context.Context, dir string) (*Store, error) {
	if err := ensureDir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, dbName)
	if err := ensureFile(path); err != nil {
		return nil, err
	}

	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// A single connection avoids SQLite locking surprises and keeps writes
	// serialized; the CLI is single-process (SEC-COD-04).
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}

	s := &Store{db: db, dir: dir, path: path}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.EnsurePerms(); err != nil {
		_ = db.Close()
		return nil, err
	}
	// Leftovers from a killed scan must never be read as the current index.
	if _, err := s.CleanIncompleteScans(ctx, time.Now().Add(-incompleteScanGrace)); err != nil {
		_ = db.Close()
		return nil, err
	}
	// A process killed mid-scan may have left the report indexes dropped.
	if err := s.ensureReportIndexes(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// incompleteScanGrace is how long an unfinished scan is tolerated before it is
// considered a leftover of a killed process.
const incompleteScanGrace = time.Hour

// EnsurePerms forces 0600 on the database file and its WAL sidecars
// (SEC-TRN-03).
func (s *Store) EnsurePerms() error {
	for _, p := range []string{s.path, s.path + "-wal", s.path + "-shm"} {
		if err := os.Chmod(p, fileMode); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("store: chmod %s: %w", p, err)
		}
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	_ = s.EnsurePerms()
	return s.db.Close()
}

// Dir returns the storage directory.
func (s *Store) Dir() string { return s.dir }

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

func (s *Store) migrate(ctx context.Context) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.db, migrations.FS)
	if err != nil {
		return fmt.Errorf("store: migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	return nil
}

func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("store: create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, dirMode); err != nil {
		return fmt.Errorf("store: chmod %s: %w", dir, err)
	}
	return nil
}

func ensureFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, fileMode) // #nosec G304 -- path is the store dir from config, not user input
	if err != nil {
		return fmt.Errorf("store: create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("store: close %s: %w", path, err)
	}
	return os.Chmod(path, fileMode)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// devToInt64 converts a device/inode number for storage in a SQLite INTEGER.
// macOS dev_t and ino_t values fit in an int64 in practice (SEC-DEL-06 relies
// on this identity for apply-time revalidation).
func devToInt64(v uint64) int64 {
	return int64(v) // #nosec G115 -- macOS dev_t/ino_t fit in int64
}

// int64ToDev is the inverse of devToInt64.
func int64ToDev(v int64) uint64 {
	return uint64(v) // #nosec G115 -- see devToInt64
}
