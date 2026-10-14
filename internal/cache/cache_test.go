package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

type fakeIndex struct{ sizes map[string]domain.Totals }

func (f fakeIndex) EntryTotalsByPath(_ context.Context, _, path string) (domain.Totals, bool, error) {
	t, ok := f.sizes[path]
	return t, ok, nil
}

func TestListExistingLocations(t *testing.T) {
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	npm := filepath.Join(home, ".npm")
	require.NoError(t, os.MkdirAll(caches, 0o750))
	require.NoError(t, os.MkdirAll(npm, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Library", "Application Support"), 0o750))

	idx := fakeIndex{sizes: map[string]domain.Totals{
		caches: {Entries: 1, AllocatedBytes: 5000},
		npm:    {Entries: 1, AllocatedBytes: 900},
	}}

	svc := New(idx, home)
	list, err := svc.List(context.Background(), "scan-1", "")
	require.NoError(t, err)

	byName := map[string]domain.CacheEntry{}
	for _, e := range list {
		byName[e.Name] = e
	}
	require.Contains(t, byName, "Caches")
	require.Contains(t, byName, "npm cache")
	require.EqualValues(t, 5000, byName["Caches"].AllocatedBytes)
	require.Equal(t, domain.ClassRegenerableCache, byName["Caches"].DataClass)
	require.Equal(t, domain.ClassAppData, byName["Application Support"].DataClass)
	// Non-existent locations are omitted.
	require.NotContains(t, byName, "Docker data")
	// Sorted by size descending.
	require.Equal(t, "Caches", list[0].Name)
}

func TestListFilter(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".npm"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".gradle", "caches"), 0o750))

	svc := New(fakeIndex{}, home)
	list, err := svc.List(context.Background(), "", "npm")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "npm cache", list[0].Name)
}

func TestResolve(t *testing.T) {
	require.Equal(t, "/Users/x/Library/Caches", Resolve("/Users/x", Location{Rel: "Library/Caches"}))
	require.Equal(t, "/Applications", Resolve("/Users/x", Location{Rel: "/Applications"}))
}
