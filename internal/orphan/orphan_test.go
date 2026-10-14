package orphan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

type fakeIndex struct{ sizes map[string]domain.Totals }

func (f fakeIndex) EntryTotalsByPath(_ context.Context, _, path string) (domain.Totals, bool, error) {
	t, ok := f.sizes[path]
	return t, ok, nil
}

func TestClassify(t *testing.T) {
	installed := map[string]struct{}{"com.example.app": {}}
	require.Equal(t, domain.ConfidenceHigh, classify("com.orphan.leftover", installed))
	require.Equal(t, domain.ConfidenceMedium, classify("com.orphan", installed))
	require.Equal(t, domain.ConfidenceLow, classify("MyApp", installed))
	require.Equal(t, domain.ConfidenceMedium, classify("com.example", installed))
}

func TestNormalizeCandidate(t *testing.T) {
	require.Equal(t, "com.example.app", normalizeCandidate("com.example.app.savedState"))
	require.Equal(t, "com.example.app", normalizeCandidate("com.example.app.plist"))
	require.Equal(t, "com.example.app", normalizeCandidate("ABCDE12345.com.example.app"))
	require.Equal(t, "MyApp", normalizeCandidate("MyApp"))
}

func TestServiceDetectsOrphans(t *testing.T) {
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "com.orphan.leftover"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "com.example.app"), 0o750))  // installed
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "com.apple.Safari"), 0o750)) // excluded
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "MyApp"), 0o750))            // low

	idx := fakeIndex{sizes: map[string]domain.Totals{
		filepath.Join(caches, "com.orphan.leftover"): {Entries: 1, AllocatedBytes: 4096},
	}}
	p := policy.New(home, nil)
	svc := New(idx, p, home)

	out, err := svc.List(context.Background(), Options{
		ScanID:            "scan-1",
		Installed:         map[string]struct{}{"com.example.app": {}},
		DiscoveryComplete: true,
	})
	require.NoError(t, err)

	byID := map[string]domain.Orphan{}
	for _, o := range out {
		byID[o.BundleID] = o
	}
	require.Contains(t, byID, "com.orphan.leftover")
	require.Equal(t, domain.ConfidenceHigh, byID["com.orphan.leftover"].Confidence)
	require.EqualValues(t, 4096, byID["com.orphan.leftover"].AllocatedBytes)
	require.NotContains(t, byID, "com.example.app", "installed apps are not orphans")
	require.NotContains(t, byID, "com.apple.Safari", "com.apple.* is excluded")
	require.Equal(t, domain.ConfidenceLow, byID["MyApp"].Confidence)
}

func TestServiceDegradesWhenDiscoveryIncomplete(t *testing.T) {
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "com.orphan.leftover"), 0o750))

	svc := New(fakeIndex{}, policy.New(home, nil), home)
	out, err := svc.List(context.Background(), Options{
		ScanID:            "scan-1",
		DiscoveryComplete: false,
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, domain.ConfidenceMedium, out[0].Confidence, "confidence must be degraded (SEC-ORP-02)")
}

func TestServiceMinConfidence(t *testing.T) {
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "com.orphan.leftover"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "MyApp"), 0o750))

	svc := New(fakeIndex{}, policy.New(home, nil), home)
	out, err := svc.List(context.Background(), Options{
		ScanID:            "scan-1",
		DiscoveryComplete: true,
		MinConfidence:     domain.ConfidenceHigh,
	})
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, "com.orphan.leftover", out[0].BundleID)
}

func TestServiceSkipsTier2(t *testing.T) {
	home := t.TempDir()
	// A Tier 2 path under a candidate location must be skipped.
	chrome := filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	require.NoError(t, os.MkdirAll(chrome, 0o750))

	svc := New(fakeIndex{}, policy.New(home, nil), home)
	out, err := svc.List(context.Background(), Options{ScanID: "s", DiscoveryComplete: true})
	require.NoError(t, err)
	for _, o := range out {
		require.NotContains(t, o.Path, "Google/Chrome")
	}
}
