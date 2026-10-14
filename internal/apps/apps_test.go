package apps

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

const infoPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.example.foo</string>
<key>CFBundleName</key><string>Foo</string>
<key>CFBundleShortVersionString</key><string>1.2.3</string>
</dict></plist>`

func writeApp(t *testing.T, home, name, plist string) string {
	t.Helper()
	app := filepath.Join(home, "Applications", name)
	require.NoError(t, os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(plist), 0o600))
	return app
}

func TestReadBundleInfo(t *testing.T) {
	home := t.TempDir()
	app := writeApp(t, home, "Foo.app", infoPlist)

	info, err := ReadBundleInfo(app)
	require.NoError(t, err)
	require.Equal(t, "com.example.foo", info.BundleID)
	require.Equal(t, "Foo", info.Name)
	require.Equal(t, "1.2.3", info.Version)
}

func TestReadBundleInfoSizeCap(t *testing.T) {
	home := t.TempDir()
	app := writeApp(t, home, "Foo.app", infoPlist)
	_, err := readBundleInfo(app, 10)
	require.Error(t, err, "Info.plist over the cap must be rejected (SEC-ACC-02)")
}

func TestReadBundleInfoMissing(t *testing.T) {
	_, err := ReadBundleInfo(filepath.Join(t.TempDir(), "Nope.app"))
	require.Error(t, err)
}

func TestServiceList(t *testing.T) {
	home := t.TempDir()
	app := writeApp(t, home, "Foo.app", infoPlist)
	// A directory that is not an app must be ignored.
	require.NoError(t, os.MkdirAll(filepath.Join(home, "Applications", "NotAnApp"), 0o750))

	dataPath := filepath.Join(home, "Library", "Caches", "com.example.foo")
	idx := fakeIndex{sizes: map[string]domain.Totals{
		app:      {Entries: 1, AllocatedBytes: 10_000, LogicalBytes: 12_000},
		dataPath: {Entries: 1, AllocatedBytes: 2_000},
	}}

	svc := New(idx, home, filepath.Join(home, "Applications"))
	list, err := svc.List(context.Background(), "scan-1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "com.example.foo", list[0].BundleID)
	require.Equal(t, "Foo", list[0].Name)
	require.EqualValues(t, 10_000, list[0].AllocatedBytes)
	require.EqualValues(t, 2_000, list[0].DataBytes)
}

func TestServiceWithoutScan(t *testing.T) {
	home := t.TempDir()
	writeApp(t, home, "Foo.app", infoPlist)
	svc := New(fakeIndex{}, home, filepath.Join(home, "Applications"))
	list, err := svc.List(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Zero(t, list[0].AllocatedBytes)
}
