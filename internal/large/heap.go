// Package large finds heavy files and directories with a bounded top-N heap.
package large

import (
	"container/heap"
	"sort"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// DefaultThreshold is the default minimum size (1 GiB).
const DefaultThreshold int64 = 1 << 30

// DefaultLimit is the default number of items returned.
const DefaultLimit = 50

// entryHeap is a min-heap of entries by allocated size, capped at n items.
type entryHeap struct {
	items []domain.Entry
}

func (h entryHeap) Len() int           { return len(h.items) }
func (h entryHeap) Less(i, j int) bool { return h.items[i].AllocatedBytes < h.items[j].AllocatedBytes }
func (h entryHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *entryHeap) Push(x any)        { h.items = append(h.items, x.(domain.Entry)) }
func (h *entryHeap) Pop() any {
	old := h.items
	n := len(old)
	item := old[n-1]
	h.items = old[:n-1]
	return item
}

// topN keeps the n largest entries seen. Memory is bounded by n.
type topN struct {
	h     entryHeap
	limit int
}

func newTopN(limit int) *topN {
	h := entryHeap{items: make([]domain.Entry, 0, limit)}
	heap.Init(&h)
	return &topN{h: h, limit: limit}
}

func (t *topN) add(e domain.Entry) {
	if t.limit <= 0 {
		return
	}
	if t.h.Len() < t.limit {
		heap.Push(&t.h, e)
		return
	}
	if e.AllocatedBytes > t.h.items[0].AllocatedBytes {
		heap.Pop(&t.h)
		heap.Push(&t.h, e)
	}
}

// sorted returns the kept entries, largest first.
func (t *topN) sorted() []domain.Entry {
	out := make([]domain.Entry, len(t.h.items))
	copy(out, t.h.items)
	sort.Slice(out, func(i, j int) bool {
		if out[i].AllocatedBytes != out[j].AllocatedBytes {
			return out[i].AllocatedBytes > out[j].AllocatedBytes
		}
		return out[i].Path < out[j].Path
	})
	return out
}
