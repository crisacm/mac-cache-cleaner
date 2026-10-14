package tui

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/consent"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/scanner"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

// hostileName contains an ANSI escape sequence (untrusted data).
const hostileName = "evil\x1b[31mname"

func writeFile(t *testing.T, path string, size int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
}

// testDeps builds a hermetic fixture home plus wired services in a temp dir.
func testDeps(t *testing.T) (*Model, Deps) {
	t.Helper()
	home := t.TempDir()
	caches := filepath.Join(home, "Library", "Caches")
	writeFile(t, filepath.Join(home, "Library", "Caches", "com.example", "data.bin"), 4096)
	writeFile(t, filepath.Join(home, "Library", "Caches", "Homebrew", "bottle.tbz"), 2048)
	writeFile(t, filepath.Join(caches, hostileName, "x.bin"), 128)
	writeFile(t, filepath.Join(home, "Library", "Application Support", "com.example.app", "db"), 256)
	writeFile(t, filepath.Join(home, "Applications", "Foo.app", "Contents", "MacOS", "Foo"), 8192)

	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "mcc"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	pol := policy.New(home, st)
	appRoots := []string{filepath.Join(home, "Applications")}
	deps := Deps{
		Home:     home,
		Config:   config.Default(home),
		Policy:   pol,
		Store:    st,
		IsTTY:    true,
		AppRoots: appRoots,
		Scan:     scan.New(st, pol),
		Cleaner:  cleaner.New(st, pol, home, cleaner.WithTrasher(&nopTrasher{}), cleaner.WithRunningApps(noRunning{})),
		Consent:  consent.New(st, pol, true),
	}
	m := newModel(ctx, deps, lipgloss.NewRenderer(io.Discard))
	m.width, m.height = 120, 40
	m.reload()
	return m, deps
}

// nopTrasher records nothing and removes nothing (tests never touch real files).
type nopTrasher struct{}

func (nopTrasher) Trash(context.Context, string) error { return nil }

// noRunning reports no running applications.
type noRunning struct{}

func (noRunning) BundleIDs(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}

func (m *Model) runScan(t *testing.T, roots ...string) {
	t.Helper()
	_, err := m.deps.Scan.Run(m.ctx, scan.Options{Roots: roots})
	require.NoError(t, err)
	m.reload()
}

func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func typeString(m *Model, s string) {
	for _, r := range s {
		_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestHostileNamesAreSanitized(t *testing.T) {
	m, deps := testDeps(t)
	m.runScan(t, filepath.Join(deps.Home, "Library", "Caches"))

	m.screen = screenOrphans
	view := m.View()
	require.NotContains(t, view, "\x1b", "rendered views must not contain escape sequences")
	require.Contains(t, view, "evilname", "the sanitized name must appear")
}

func TestReportOnlyRowsAreNotSelectable(t *testing.T) {
	l := newListState("test")
	l.setRows([]row{
		{title: "cache", class: domain.ClassRegenerableCache, entryID: "e1", size: 10},
		{title: "appdata", class: domain.ClassAppData, entryID: "e2", size: 20},
		{title: "userfile", class: domain.ClassUserFile, entryID: "e3", size: 30},
		{title: "application", class: domain.ClassApplication, entryID: "e4", size: 40},
	})

	// Select every row.
	l.selectAll()
	require.True(t, l.selected["e1"], "regenerable_cache is selectable")
	require.True(t, l.selected["e2"], "app_data is selectable (explicit)")
	require.False(t, l.selected["e3"], "user_file is report-only")
	require.False(t, l.selected["e4"], "application is report-only")

	// Toggling a report-only row does nothing.
	l.cursor = indexOf(l, "e3")
	l.toggle()
	require.False(t, l.selected["e3"])

	// Report-only rows render with a non-selectable marker.
	require.Contains(t, l.render(newStyles(lipgloss.NewRenderer(io.Discard)), 10), "userfile")
}

func indexOf(l *listState, entryID string) int {
	for i, idx := range l.order {
		if l.rows[idx].entryID == entryID {
			return i
		}
	}
	return -1
}

func TestApplyRequiresItemCount(t *testing.T) {
	m, _ := testDeps(t)
	m.clean = cleanState{
		active: true,
		step:   cleanConfirm,
		plan:   domain.Plan{PlanID: "p1", Items: make([]domain.PlanItem, 3)},
	}
	m.screen = screenClean

	// Enter alone, or the wrong number, must not apply.
	_, cmd := m.Update(keyMsg("enter"))
	require.Nil(t, cmd, "Enter with an empty confirmation must not apply")
	require.Equal(t, cleanConfirm, m.clean.step)

	typeString(m, "2")
	_, cmd = m.Update(keyMsg("enter"))
	require.Nil(t, cmd, "the wrong count must not apply")
	require.Equal(t, cleanConfirm, m.clean.step)

	// The exact count applies.
	m.clean.input = ""
	typeString(m, "3")
	_, cmd = m.Update(keyMsg("enter"))
	require.NotNil(t, cmd, "the exact count must trigger apply")
	require.Equal(t, cleanResult, m.clean.step)
}

// blockWalker blocks until its context is cancelled, simulating a long scan.
type blockWalker struct{ started chan struct{} }

func (w *blockWalker) Scan(ctx context.Context, _ scanner.Options) (scanner.Result, error) {
	if w.started != nil {
		close(w.started)
	}
	<-ctx.Done()
	return scanner.Result{}, ctx.Err()
}

func TestCancelMidScanRollsBack(t *testing.T) {
	m, deps := testDeps(t)
	started := make(chan struct{})
	deps.Scan = scan.New(deps.Store, deps.Policy, scan.WithWalker(&blockWalker{started: started}))
	m.deps = deps
	m.loadRoots()

	_, _ = m.startScan()
	<-started

	// Cancel with Esc.
	_, _ = m.Update(keyMsg("esc"))
	require.True(t, m.scan.cancelling || m.scan.running)

	// Drain the terminal scan message.
	msg := waitDone(m.scan.done)()
	_, _ = m.Update(msg)

	require.False(t, m.scan.running)
	require.Contains(t, strings.ToLower(m.status), "cancel")
	_, ok, err := m.deps.Store.LatestScanID(m.ctx)
	require.NoError(t, err)
	require.False(t, ok, "a cancelled scan must be rolled back")
}

func TestConsentCannotBeGrantedNonInteractively(t *testing.T) {
	m, deps := testDeps(t)
	deps.IsTTY = false
	deps.Consent = consent.New(deps.Store, deps.Policy, false)
	m.deps = deps

	m.scan.consentConfirming = true
	m.scan.consentRoot = filepath.Join(deps.Home, "Documents")
	m.scan.consentInput = "grant"
	_, _ = m.Update(keyMsg("enter"))

	consents, err := deps.Store.Consents(m.ctx)
	require.NoError(t, err)
	require.Empty(t, consents, "consent must not be granted non-interactively (SEC-ACC-07)")
	require.NotEmpty(t, m.status)
}

func TestConsentNeedsMatchingWordAndRecords(t *testing.T) {
	m, deps := testDeps(t)
	root := filepath.Join(deps.Home, "Documents")

	// Wrong word: nothing recorded.
	m.scan.consentConfirming = true
	m.scan.consentRoot = root
	m.scan.consentInput = "nope"
	_, _ = m.Update(keyMsg("enter"))
	consents, err := deps.Store.Consents(m.ctx)
	require.NoError(t, err)
	require.Empty(t, consents)

	// Right word: recorded.
	m.scan.consentConfirming = true
	m.scan.consentRoot = root
	m.scan.consentInput = "grant"
	_, _ = m.Update(keyMsg("enter"))
	consents, err = deps.Store.Consents(m.ctx)
	require.NoError(t, err)
	require.Len(t, consents, 1)
	require.Equal(t, domain.TierUserData, consents[0].Tier)
}

func TestPermissionsRevoke(t *testing.T) {
	m, deps := testDeps(t)
	root := filepath.Join(deps.Home, "Documents")
	require.NoError(t, deps.Store.GrantConsent(m.ctx, domain.Consent{Root: root, Tier: domain.TierUserData}))
	m.loadPerms()
	require.Len(t, m.perms.consents, 1)

	m.screen = screenPermissions
	_, _ = m.Update(keyMsg("x"))
	m.loadPerms()
	require.Len(t, m.perms.consents, 1)
	require.False(t, m.perms.consents[0].Active(time.Now()), "revoked consent must be inactive")
}

// --- golden views ------------------------------------------------------

var update = flag.Bool("update", false, "update golden files")

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

func TestScanScreenGolden(t *testing.T) {
	m, _ := testDeps(t)
	m.screen = screenScan
	assertGolden(t, "scan_screen.golden", m.View())
}

func TestPermissionsScreenGolden(t *testing.T) {
	m, _ := testDeps(t)
	m.screen = screenPermissions
	assertGolden(t, "permissions_screen.golden", m.View())
}

// selectByTitle moves the cursor to a row and toggles it.
func selectByTitle(_ *Model, l *listState, title string) bool {
	for i, idx := range l.order {
		if l.rows[idx].title == title {
			l.cursor = i
			l.toggle()
			return true
		}
	}
	return false
}

func TestCleanupFlowEndToEnd(t *testing.T) {
	m, deps := testDeps(t)
	m.runScan(t, filepath.Join(deps.Home, "Library", "Caches"))

	// Select the Homebrew cache (a regenerable cache that is not an
	// allowlisted root, so it is eligible for a plan).
	m.screen = screenCaches
	l := m.lists[screenCaches]
	require.True(t, selectByTitle(m, l, "Homebrew cache"), "Homebrew cache row must exist")
	require.Len(t, l.selected, 1)

	// Build the plan (dry-run).
	_, _ = m.Update(keyMsg("p"))
	require.Equal(t, screenClean, m.screen)
	require.Equal(t, cleanReview, m.clean.step)
	require.Len(t, m.clean.plan.Items, 1, "the selected cache must be planned")

	// Review -> confirmation: the exact count is required.
	_, _ = m.Update(keyMsg("c"))
	require.Equal(t, cleanConfirm, m.clean.step)

	typeString(m, "1")
	_, cmd := m.Update(keyMsg("enter"))
	require.NotNil(t, cmd, "the exact count must trigger apply")

	// Execute the apply command and feed the result back.
	msg := cmd()
	_, _ = m.Update(msg)
	require.Equal(t, cleanResult, m.clean.step)
	require.Len(t, m.clean.result.Applied, 1, "the planned cache must be applied")
	require.Empty(t, m.clean.result.Failed)
}

func TestQuitKeySetsQuitting(t *testing.T) {
	m, _ := testDeps(t)
	_, cmd := m.Update(keyMsg("q"))
	require.True(t, m.quitting, "q must request quit")
	require.NotNil(t, cmd, "q must return a command (tea.Quit)")
	require.Empty(t, m.View(), "a quitting model renders nothing")
}

func TestTabNavigation(t *testing.T) {
	m, _ := testDeps(t)
	require.Equal(t, screenOverview, m.screen)

	tab := tea.KeyMsg{Type: tea.KeyTab}
	shiftTab := tea.KeyMsg{Type: tea.KeyShiftTab}

	_, _ = m.Update(tab)
	require.Equal(t, screenApps, m.screen)
	for i := 0; i < len(tabs)-2; i++ {
		_, _ = m.Update(tab)
	}
	require.Equal(t, screenPermissions, m.screen, "tab cycles forward to the last tab")
	_, _ = m.Update(tab)
	require.Equal(t, screenOverview, m.screen, "tab wraps around")
	_, _ = m.Update(shiftTab)
	require.Equal(t, screenPermissions, m.screen, "shift+tab cycles backward")

	// Every screen renders without panicking.
	for _, s := range []screen{screenOverview, screenApps, screenCaches, screenOrphans, screenLarge, screenScan, screenPermissions} {
		m.screen = s
		require.NotEmpty(t, m.View())
	}
}
