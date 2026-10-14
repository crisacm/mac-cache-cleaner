package large

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

type fakeIndex struct{ entries []domain.Entry }

func (f fakeIndex) StreamEntries(_ context.Context, _ string, minBytes int64, fn func(domain.Entry) error) error {
	for _, e := range f.entries {
		if e.AllocatedBytes >= minBytes {
			if err := fn(e); err != nil {
				return err
			}
		}
	}
	return nil
}

func entry(path string, bytes int64, class domain.DataClass) domain.Entry {
	return domain.Entry{Path: path, AllocatedBytes: bytes, LogicalBytes: bytes, DataClass: class, Kind: domain.KindFile}
}

func TestListTopN(t *testing.T) {
	idx := fakeIndex{entries: []domain.Entry{
		entry("/a/1", 5, domain.ClassRegenerableCache),
		entry("/a/2", 100, domain.ClassRegenerableCache),
		entry("/a/3", 50, domain.ClassRegenerableCache),
		entry("/a/4", 200, domain.ClassRegenerableCache),
		entry("/a/5", 10, domain.ClassRegenerableCache),
	}}
	svc := New(idx, 1)
	items, err := svc.List(context.Background(), "scan-1", Options{Limit: 2})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "/a/4", items[0].Path)
	require.Equal(t, "/a/2", items[1].Path)
}

func TestListMinThreshold(t *testing.T) {
	idx := fakeIndex{entries: []domain.Entry{
		entry("/a/small", 10, domain.ClassRegenerableCache),
		entry("/a/big", 1000, domain.ClassRegenerableCache),
	}}
	svc := New(idx, 100)
	items, err := svc.List(context.Background(), "scan-1", Options{})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "/a/big", items[0].Path)
}

func TestListPathPrefix(t *testing.T) {
	idx := fakeIndex{entries: []domain.Entry{
		entry("/a/keep/x", 100, domain.ClassRegenerableCache),
		entry("/a/keep-evil/y", 200, domain.ClassRegenerableCache),
	}}
	svc := New(idx, 1)
	items, err := svc.List(context.Background(), "scan-1", Options{PathPrefix: filepath.Join("/a", "keep")})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "/a/keep/x", items[0].Path, "boundary must not match a sibling prefix")
}

func TestListRequiresScanID(t *testing.T) {
	svc := New(fakeIndex{}, 1)
	_, err := svc.List(context.Background(), "", Options{})
	require.ErrorIs(t, err, domain.ErrUsage)
}

func TestTopNHeapBounded(t *testing.T) {
	top := newTopN(3)
	for i := int64(0); i < 1000; i++ {
		top.add(entry("/f", i, domain.ClassRegenerableCache))
	}
	require.Len(t, top.h.items, 3, "heap must stay bounded by the limit")
	got := top.sorted()
	require.EqualValues(t, 999, got[0].AllocatedBytes)
	require.EqualValues(t, 997, got[2].AllocatedBytes)
}
