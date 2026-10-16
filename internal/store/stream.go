package store

import (
	"context"
	"fmt"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// StreamEntries calls fn for every entry in a scan whose allocated size is at
// least minBytes, largest first. It never loads the whole result set into
// memory (SEC-COD-04); callers keep a bounded top-N.
func (s *Store) StreamEntries(ctx context.Context, _ string, minBytes int64, fn func(domain.Entry) error) error {
	const q = `SELECT entry_id, scan_id, path, parent, allocated_bytes, logical_bytes, mtime,
	                  is_dir, kind, data_class, bundle_id, dev, inode
	           FROM entries
	           WHERE allocated_bytes >= ?
	           ORDER BY allocated_bytes DESC`
	rows, err := s.db.QueryContext(ctx, q, minBytes)
	if err != nil {
		return fmt.Errorf("store: stream entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

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
			return fmt.Errorf("store: scan entry row: %w", err)
		}
		e.MTime = parseTime(mtime)
		e.IsDir = isDir != 0
		e.Kind = domain.Kind(kind)
		e.DataClass = domain.DataClass(class)
		e.Dev = int64ToDev(dev)
		e.Inode = int64ToDev(inode)
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}
