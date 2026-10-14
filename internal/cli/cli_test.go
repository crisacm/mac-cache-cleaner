package cli

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

var update = flag.Bool("update", false, "update golden files")

const testHome = "/Users/tester"

func newTestApp(t *testing.T) *App {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "mcc"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Default(testHome)
	var out, errOut bytes.Buffer
	return New(testHome, cfg, policy.New(testHome, st), st, strings.NewReader(""), &out, &errOut, false)
}

func (a *App) run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	out := a.Out.(*bytes.Buffer)
	errOut := a.Err.(*bytes.Buffer)
	out.Reset()
	errOut.Reset()
	code := a.Execute(context.Background(), args)
	return out.String(), errOut.String(), code
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))
		return
	}
	want, err := os.ReadFile(path) // #nosec G304 -- golden path is a test literal
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}

func TestPermissionsGolden(t *testing.T) {
	app := newTestApp(t)
	out, _, code := app.run(t, "permissions")
	require.Equal(t, domain.ExitOK, code)
	assertGolden(t, "permissions.golden", out)
}

func TestScanExplainGolden(t *testing.T) {
	app := newTestApp(t)
	out, _, code := app.run(t, "scan", "--explain")
	require.Equal(t, domain.ExitOK, code)
	assertGolden(t, "scan_explain.golden", out)
}

func TestNoScanReportIsFriendly(t *testing.T) {
	app := newTestApp(t)
	out, _, code := app.run(t, "report")
	require.Equal(t, domain.ExitOK, code)
	require.Contains(t, out, "No scan yet")
}

func TestUsageErrorExitCode(t *testing.T) {
	app := newTestApp(t)
	_, errOut, code := app.run(t, "clean")
	require.Equal(t, domain.ExitUsage, code)
	require.Contains(t, errOut, "usage")
}

func TestScanBlockedRootExitCode(t *testing.T) {
	app := newTestApp(t)
	// Tier 2 is never accessible: exit code 4.
	_, _, code := app.run(t, "scan", "--root", filepath.Join(testHome, "Library", "Keychains"))
	require.Equal(t, domain.ExitBlocked, code)
}

func TestParseSize(t *testing.T) {
	tests := map[string]int64{
		"1GB": 1 << 30, "1GiB": 1 << 30, "512MB": 512 << 20,
		"2T": 2 << 40, "1024": 1024, "1.5MB": (3 << 20) / 2,
	}
	for in, want := range tests {
		got, err := ParseSize(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := ParseSize("nope")
	require.ErrorIs(t, err, domain.ErrUsage)
}

func TestExitCodeMapping(t *testing.T) {
	require.Equal(t, domain.ExitOK, exitCode(nil))
	require.Equal(t, domain.ExitUsage, exitCode(domain.ErrUsage))
	require.Equal(t, domain.ExitBlocked, exitCode(domain.ErrBlocked))
	require.Equal(t, domain.ExitPartial, exitCode(domain.ErrPartial))
}
