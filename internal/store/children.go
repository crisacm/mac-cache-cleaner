package store

import (
	"context"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// EntriesByParent returns the entries whose parent is the given directory,
// largest first. The index is single-generation, so scanID is ignored.
func (s *Store) EntriesByParent(ctx context.Context, _, parent string) ([]domain.Entry, error) {
	return s.queryEntries(ctx, `WHERE parent = ? ORDER BY allocated_bytes DESC`, parent)
}

// EntriesByClassAndParent returns entries filtered by data class and parent.
func (s *Store) EntriesByClassAndParent(ctx context.Context, _, parent string, class domain.DataClass) ([]domain.Entry, error) {
	return s.queryEntries(ctx,
		`WHERE parent = ? AND data_class = ? ORDER BY allocated_bytes DESC`,
		parent, string(class))
}
