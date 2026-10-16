package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// buildBenchTree creates dirs×files small files under a temp root.
func buildBenchTree(b *testing.B, dirs, files int) string {
	b.Helper()
	root := b.TempDir()
	for d := range dirs {
		dir := filepath.Join(root, fmt.Sprintf("dir%04d", d))
		if err := os.MkdirAll(dir, 0o750); err != nil {
			b.Fatal(err)
		}
		for f := range files {
			p := filepath.Join(dir, fmt.Sprintf("f%03d.bin", f))
			if err := os.WriteFile(p, make([]byte, 1024), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	return root
}

// BenchmarkScanWalk measures the walk + aggregation only, with a discarding
// sink, so it isolates the filesystem cost from the store's write cost.
func BenchmarkScanWalk(b *testing.B) {
	root := buildBenchTree(b, 400, 50) // 20k files
	s := New(fakePolicy{})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; b.Loop(); i++ {
		if _, err := s.Scan(context.Background(), Options{
			ScanID: fmt.Sprintf("bench-%d", i),
			Roots:  []string{root},
			Emit:   func(context.Context, []domain.Entry) error { return nil },
		}); err != nil {
			b.Fatal(err)
		}
	}
}
