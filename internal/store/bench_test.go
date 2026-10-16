package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// BenchmarkInsertEntries measures the bulk write path (one transaction per
// call, multi-row INSERTs, report indexes dropped by SaveScan).
func BenchmarkInsertEntries(b *testing.B) {
	const rows = 20000
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(b.TempDir(), "mcc"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	if err := s.SaveScan(ctx, domain.Scan{ID: "bench", StartedAt: time.Now(), Root: "/x", Roots: []string{"/x"}}); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		batch := make([]domain.Entry, rows)
		for j := range batch {
			batch[j] = domain.Entry{
				EntryID:        fmt.Sprintf("e-%d-%d", i, j),
				ScanID:         "bench",
				Path:           fmt.Sprintf("/x/dir/%d/f%06d.bin", j%500, j),
				Parent:         fmt.Sprintf("/x/dir/%d", j%500),
				AllocatedBytes: 1024,
				LogicalBytes:   1024,
				MTime:          time.Now(),
				Kind:           domain.KindFile,
				DataClass:      domain.ClassAppData,
			}
		}
		if err := s.InsertEntries(ctx, batch); err != nil {
			b.Fatal(err)
		}
	}
}
