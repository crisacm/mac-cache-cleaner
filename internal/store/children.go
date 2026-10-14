package store

import (
	"context"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// EntriesByParent returns the entries of a scan whose parent is the given
// directory, largest first.
func (s *Store) EntriesByParent(ctx context.Context, scanID, parent string) ([]domain.Entry, error) {
	return s.queryEntries(ctx,
		`WHERE scan_id = ? AND parent = ? ORDER BY allocated_bytes DESC`,
		scanID, parent)
}

// EntriesByClassAndParent returns entries of a scan filtered by data class and
// parent directory.
func (s *Store) EntriesByClassAndParent(ctx context.Context, scanID, parent string, class domain.DataClass) ([]domain.Entry, error) {
	return s.queryEntries(ctx,
		`WHERE scan_id = ? AND parent = ? AND data_class = ? ORDER BY allocated_bytes DESC`,
		scanID, parent, string(class))
}
