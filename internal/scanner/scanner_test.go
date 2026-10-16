package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
)

// fakePolicy allows any root and defers subdivision checks to the real path
// boundary logic. Used for functional tests with temporary trees.
type fakePolicy struct{}

func (fakePolicy) RootVerdict(_ context.Context, root string) (policy.Verdict, error) {
	return policy.Verdict{Path: root, Tier: domain.TierDefault, Decision: policy.DecisionAllow}, nil
}

func (fakePolicy) PathVerdict(root, path string) policy.Verdict {
	if !policy.UnderRoot(root, path) {
		return policy.Verdict{Path: path, Decision: policy.DecisionDeny, Reason: "outside declared root"}
	}
	return policy.Verdict{Path: path, Tier: domain.TierDefault, Decision: policy.DecisionAllow}
}

func (fakePolicy) Classify(string) (domain.Tier, string, bool) { return domain.TierDefault, "", true }

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
}

// buildFixture creates a fake home tree and returns its path.
func buildFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()

	caches := filepath.Join(home, "Library", "Caches")
	require.NoError(t, os.MkdirAll(filepath.Join(caches, "com.example"), 0o750))
	writeFile(t, filepath.Join(caches, "com.example", "data.bin"), 4096)

	// Hardlinked pair: only one must be counted.
	require.NoError(t, os.WriteFile(filepath.Join(caches, "hardlink-a"), make([]byte, 2048), 0o600))
	require.NoError(t, os.Link(filepath.Join(caches, "hardlink-a"), filepath.Join(caches, "hardlink-b")))

	// Sparse file: logical size 1 MiB, small allocation.
	sparse := filepath.Join(caches, "sparse.bin")
	f, err := os.Create(sparse) // #nosec G304 -- test path under t.TempDir()
	require.NoError(t, err)
	require.NoError(t, f.Truncate(1<<20))
	require.NoError(t, f.Close())

	// Symlink to an outside directory: must not be followed.
	require.NoError(t, os.Symlink("/etc", filepath.Join(caches, "escape")))

	// Hostile file name with an ANSI escape.
	writeFile(t, filepath.Join(caches, "evil\x1b[31mname"), 16)

	// Opaque library package.
	writeFile(t, filepath.Join(home, "Pictures", "iPhoto.photoslibrary", "originals", "a.jpg"), 8192)

	// Application bundle.
	writeFile(t, filepath.Join(home, "Applications", "Foo.app", "Contents", "MacOS", "Foo"), 16384)

	// Tier 1 user data.
	writeFile(t, filepath.Join(home, "Documents", "report.pdf"), 1024)

	return home
}

func scanAll(t *testing.T, p Policy, roots ...string) Result {
	t.Helper()
	s := New(p)
	res, err := s.Scan(context.Background(), Options{ScanID: "scan-1", Roots: roots, Workers: 4})
	require.NoError(t, err)
	return res
}

func TestScanFixture(t *testing.T) {
	home := buildFixture(t)
	res := scanAll(t, fakePolicy{}, home)

	require.Greater(t, res.Totals.AllocatedBytes, int64(0))
	require.Greater(t, res.Totals.Entries, int64(0))

	byPath := map[string]domain.Entry{}
	for _, e := range res.Entries {
		byPath[e.Path] = e
	}

	// Symlink recorded but not followed.
	sym := byPath[filepath.Join(home, "Library", "Caches", "escape")]
	require.Equal(t, domain.KindSymlink, sym.Kind)
	for p := range byPath {
		require.False(t, strings.HasPrefix(p, "/etc/"), "must never descend into a symlink target: %s", p)
	}

	// Hardlink counted once.
	links := 0
	for p, e := range byPath {
		if e.Kind == domain.KindFile && (strings.HasSuffix(p, "hardlink-a") || strings.HasSuffix(p, "hardlink-b")) {
			links++
		}
	}
	require.Equal(t, 1, links, "hardlinked file must be counted once")

	// Sparse file: allocation well below logical size.
	sp := byPath[filepath.Join(home, "Library", "Caches", "sparse.bin")]
	require.EqualValues(t, 1<<20, sp.LogicalBytes)
	require.Less(t, sp.AllocatedBytes, sp.LogicalBytes, "sparse file allocation must use st_blocks")

	// Opaque package: single entry, no descent.
	pkg := byPath[filepath.Join(home, "Pictures", "iPhoto.photoslibrary")]
	require.Equal(t, domain.KindPackage, pkg.Kind)
	require.Greater(t, pkg.AllocatedBytes, int64(0))
	for p := range byPath {
		require.False(t, strings.Contains(p, "iPhoto.photoslibrary/"), "package must not be descended: %s", p)
	}

	// Application bundle and its contents classified as application.
	app := byPath[filepath.Join(home, "Applications", "Foo.app")]
	require.Equal(t, domain.KindApp, app.Kind)
	require.Equal(t, domain.ClassApplication, app.DataClass)

	// Hostile name preserved as data; sanitization happens at render time.
	_, ok := byPath[filepath.Join(home, "Library", "Caches", "evil\x1b[31mname")]
	require.True(t, ok, "hostile-named file must still be indexed as data")

	// Data classes present.
	require.Contains(t, res.ByClass, domain.ClassRegenerableCache)
	require.Contains(t, res.ByClass, domain.ClassApplication)
}

func TestScanDirectorySubtreeSizes(t *testing.T) {
	home := buildFixture(t)
	res := scanAll(t, fakePolicy{}, home)

	byPath := map[string]domain.Entry{}
	for _, e := range res.Entries {
		byPath[e.Path] = e
	}
	caches := byPath[filepath.Join(home, "Library", "Caches")]
	child := byPath[filepath.Join(home, "Library", "Caches", "com.example", "data.bin")]
	require.Greater(t, caches.AllocatedBytes, child.AllocatedBytes, "directory entry must carry subtree size")
	require.True(t, caches.IsDir)
}

// TestScanTier2NeverTraversed proves SEC-ACC-04 with the real policy.
func TestScanTier2NeverTraversed(t *testing.T) {
	home := t.TempDir()
	support := filepath.Join(home, "Library", "Application Support")
	writeFile(t, filepath.Join(support, "com.example", "data.bin"), 4096)
	// Chrome lives under Application Support but is Tier 2 (browser credentials).
	writeFile(t, filepath.Join(support, "Google", "Chrome", "Default", "Login Data"), 4096)

	p := policy.New(home, nil)
	res := scanAll(t, p, support)

	for _, e := range res.Entries {
		require.False(t, strings.Contains(e.Path, "Google/Chrome"), "Tier 2 path must never be indexed: %s", e.Path)
	}
	require.Contains(t, strings.Join(res.Skipped, "\n"), "Google/Chrome")
}

func TestScanRejectsRootWithoutConsent(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Documents"), 0o750))
	p := policy.New(home, nil)
	s := New(p)
	_, err := s.Scan(context.Background(), Options{ScanID: "s", Roots: []string{filepath.Join(home, "Documents")}})
	require.ErrorIs(t, err, domain.ErrBlocked)
}

func TestScanRejectsTier2Root(t *testing.T) {
	home := t.TempDir()
	keychains := filepath.Join(home, "Library", "Keychains")
	require.NoError(t, os.MkdirAll(keychains, 0o750))
	p := policy.New(home, nil)
	s := New(p)
	_, err := s.Scan(context.Background(), Options{ScanID: "s", Roots: []string{keychains}})
	require.ErrorIs(t, err, domain.ErrBlocked)
}

func TestScanEmitStreamsEntries(t *testing.T) {
	home := buildFixture(t)
	var got int
	s := New(fakePolicy{})
	res, err := s.Scan(context.Background(), Options{
		ScanID:    "scan-1",
		Roots:     []string{home},
		BatchSize: 2,
		Emit: func(_ context.Context, entries []domain.Entry) error {
			got += len(entries)
			return nil
		},
	})
	require.NoError(t, err)
	require.Empty(t, res.Entries, "entries must stream to Emit, not be retained")
	require.Greater(t, got, 0)
}

func TestScanRequiresScanID(t *testing.T) {
	s := New(fakePolicy{})
	_, err := s.Scan(context.Background(), Options{Roots: []string{t.TempDir()}})
	require.ErrorIs(t, err, domain.ErrUsage)
}

func TestScanReportsProgress(t *testing.T) {
	home := buildFixture(t)
	var snaps []Progress
	s := New(fakePolicy{})
	_, err := s.Scan(context.Background(), Options{
		ScanID:        "scan-1",
		Roots:         []string{home},
		Workers:       2,
		ProgressEvery: 1, // report on every directory
		Progress:      func(p Progress) { snaps = append(snaps, p) },
	})
	require.NoError(t, err)
	require.NotEmpty(t, snaps, "progress must be reported")

	// The final snapshot is emitted unconditionally and reflects the run.
	last := snaps[len(snaps)-1]
	require.Equal(t, 1, last.Roots)
	require.Greater(t, last.Directories, int64(0))
	require.Greater(t, last.Entries, int64(0))
	require.Greater(t, last.AllocatedBytes, int64(0))

	// Directory count is monotonic across snapshots.
	for i := 1; i < len(snaps); i++ {
		require.GreaterOrEqual(t, snaps[i].Directories, snaps[i-1].Directories)
	}
}

func TestScanNoProgressCallbackIsSafe(t *testing.T) {
	home := buildFixture(t)
	s := New(fakePolicy{})
	_, err := s.Scan(context.Background(), Options{ScanID: "scan-1", Roots: []string{home}, ProgressEvery: 1})
	require.NoError(t, err)
}

// stubIncremental reports a directory as unchanged when its mtime matches the
// recorded one, exercising the incremental path without a store.
type stubIncremental struct {
	mtime   time.Time
	alloc   int64
	logical int64
	calls   int
}

func (s *stubIncremental) UnchangedDir(_ context.Context, _ string, mtime time.Time) (int64, int64, bool, error) {
	s.calls++
	if mtime.Equal(s.mtime) {
		return s.alloc, s.logical, true, nil
	}
	return 0, 0, false, nil
}

// TestScanWriteErrorIsNotMaskedAsCancel verifies that a sink failure is reported
// as that failure, never as a user cancellation (the walker cancels its own
// context to stop the workers, which used to mask the real error).
func TestScanWriteErrorIsNotMaskedAsCancel(t *testing.T) {
	home := buildFixture(t)
	boom := errors.New("emit boom")
	_, err := New(fakePolicy{}).Scan(context.Background(), Options{
		ScanID: "e1",
		Roots:  []string{home},
		Emit:   func(context.Context, []domain.Entry) error { return boom },
	})
	require.ErrorIs(t, err, boom, "a writer failure must surface as itself")
	require.NotErrorIs(t, err, context.Canceled, "a writer failure must not look like Ctrl+C")
}

// TestIncrementalReuseSkipsUnchangedDir verifies that a directory whose mtime is
// unchanged is not walked and not re-emitted.
func TestIncrementalReuseSkipsUnchangedDir(t *testing.T) {
	home := buildFixture(t)
	caches := filepath.Join(home, "Library", "Caches")
	fi, err := os.Stat(caches)
	require.NoError(t, err)

	inc := &stubIncremental{mtime: fi.ModTime(), alloc: 1234, logical: 1234}
	s := New(fakePolicy{})
	res, err := s.Scan(context.Background(), Options{
		ScanID:      "scan-1",
		Roots:       []string{caches},
		Incremental: inc,
	})
	require.NoError(t, err)
	require.Equal(t, 1, inc.calls, "the unchanged root must be checked and reused once")
	require.EqualValues(t, 1, res.ReusedDirs, "the reused directory is reported")
	require.EqualValues(t, 0, res.Totals.Entries, "a reused subtree is not walked")
	for _, e := range res.Entries {
		require.NotEqual(t, caches, e.Path, "a reused directory must not be re-emitted")
	}
}

// TestScanCancellationStopsWalk cancels the context while the walk is running
// and requires the scan to stop promptly with context.Canceled (Ctrl+C).
func TestScanCancellationStopsWalk(t *testing.T) {
	home := buildFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := New(fakePolicy{}).Scan(ctx, Options{
			ScanID:        "cancel-1",
			Roots:         []string{home},
			ProgressEvery: 1,
			Progress:      func(Progress) { cancel() },
		})
		done <- err
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled, "a cancelled walk must report cancellation")
	case <-time.After(10 * time.Second):
		t.Fatal("scan did not stop after cancellation")
	}
}
