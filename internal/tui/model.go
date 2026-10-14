package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crisacm/mac-cache-cleaner/internal/cache"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/large"
	"github.com/crisacm/mac-cache-cleaner/internal/orphan"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

type screen int

const (
	screenOverview screen = iota
	screenApps
	screenCaches
	screenOrphans
	screenLarge
	screenScan
	screenPermissions
	screenClean
)

var tabs = []struct {
	s    screen
	name string
}{
	{screenOverview, "Overview"},
	{screenApps, "Apps"},
	{screenCaches, "Caches"},
	{screenOrphans, "Orphans"},
	{screenLarge, "Large"},
	{screenScan, "Scan"},
	{screenPermissions, "Permissions"},
}

type statusKind int

const (
	statusInfo statusKind = iota
	statusWarn
	statusError
	statusGood
)

// overviewData is the Overview screen state.
type overviewData struct {
	ScanID       string
	Totals       domain.Totals
	ByClass      map[domain.DataClass]domain.Totals
	Inaccessible int
	Partial      bool
	FinishedAt   string
	Apps         int
	Caches       int
	Orphans      int
	Large        int
}

// Model is the root Bubble Tea model.
type Model struct {
	ctx    context.Context
	deps   Deps
	st     styles
	keys   keyMap
	width  int
	height int

	screen   screen
	quitting bool

	hasScan    bool
	latestScan domain.Scan
	overview   overviewData

	lists map[screen]*listState

	scan  scanState
	clean cleanState
	perms permsState

	status     string
	statusKind statusKind
}

func newModel(ctx context.Context, deps Deps, r *lipgloss.Renderer) *Model {
	m := &Model{
		ctx:   ctx,
		deps:  deps,
		st:    newStyles(r),
		keys:  defaultKeys(),
		lists: map[screen]*listState{},
	}
	m.lists[screenApps] = newListState("Applications")
	m.lists[screenCaches] = newListState("Caches")
	m.lists[screenOrphans] = newListState("Orphans")
	m.lists[screenLarge] = newListState("Large items")
	m.scan = newScanState(deps)
	m.clean = newCleanState()
	m.perms = newPermsState()
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	m.reload()
	return nil
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case scanProgressMsg:
		return m.handleScanProgress(msg.p)
	case scanDoneMsg:
		return m.handleScanDone(msg)
	case applyDoneMsg:
		return m.handleApplyDone(msg)
	}
	return m, nil
}

// reload loads all data used by the read-only screens.
func (m *Model) reload() {
	ctx := m.ctx
	m.hasScan = false
	if scan, err := m.deps.Store.LatestScan(ctx); err == nil {
		m.hasScan = true
		m.latestScan = scan
	}
	scanID := ""
	if m.hasScan {
		scanID = m.latestScan.ID
	}

	ov := overviewData{}
	if m.hasScan {
		ov.ScanID = m.latestScan.ID
		ov.Partial = m.latestScan.Partial
		if !m.latestScan.FinishedAt.IsZero() {
			ov.FinishedAt = m.latestScan.FinishedAt.Format("2006-01-02 15:04:05")
		}
		ov.Totals, _ = m.deps.Store.Totals(ctx, scanID)
		ov.ByClass, _ = m.deps.Store.TotalsByClass(ctx, scanID)
		if inacc, err := m.deps.Store.Inaccessible(ctx, scanID); err == nil {
			ov.Inaccessible = len(inacc)
		}
	}

	appRows := rowsFromApps(m.listApps(ctx, scanID))
	m.lists[screenApps].setRows(appRows)
	ov.Apps = len(appRows)

	cacheRows := rowsFromCaches(m.listCaches(ctx, scanID))
	m.lists[screenCaches].setRows(cacheRows)
	ov.Caches = len(cacheRows)

	orbRows := rowsFromOrphans(m.listOrphans(ctx, scanID))
	m.lists[screenOrphans].setRows(orbRows)
	ov.Orphans = len(orbRows)

	largeRows := rowsFromLarge(m.listLarge(ctx, scanID))
	m.lists[screenLarge].setRows(largeRows)
	ov.Large = len(largeRows)

	m.overview = ov
	m.perms = newPermsState()
	m.loadPerms()
	m.loadRoots()
}

func (m *Model) listApps(ctx context.Context, scanID string) []domain.App {
	list, err := m.deps.appsService().List(ctx, scanID)
	if err != nil {
		m.setStatus(statusError, "apps: "+err.Error())
		return nil
	}
	return list
}

func (m *Model) listCaches(ctx context.Context, scanID string) []domain.CacheEntry {
	list, err := cache.New(m.deps.Store, m.deps.Home).List(ctx, scanID, "")
	if err != nil {
		m.setStatus(statusError, "caches: "+err.Error())
		return nil
	}
	return list
}

func (m *Model) listOrphans(ctx context.Context, scanID string) []domain.Orphan {
	installed, complete := m.deps.appsService().InstalledBundleIDs(ctx)
	list, err := orphan.New(m.deps.Store, m.deps.Policy, m.deps.Home).List(ctx, orphan.Options{
		ScanID: scanID, Installed: installed, DiscoveryComplete: complete,
	})
	if err != nil {
		m.setStatus(statusError, "orphans: "+err.Error())
		return nil
	}
	return list
}

func (m *Model) listLarge(ctx context.Context, scanID string) []domain.LargeItem {
	list, err := large.New(m.deps.Store, m.deps.LargeThreshold()).List(ctx, scanID, large.Options{})
	if err != nil {
		m.setStatus(statusError, "large: "+err.Error())
		return nil
	}
	return list
}

func (m *Model) setStatus(kind statusKind, text string) {
	m.status = text
	m.statusKind = kind
}

// textInputActive reports whether a screen is capturing typed text.
func (m *Model) textInputActive() bool {
	if l := m.lists[m.screen]; l != nil && l.filtering {
		return true
	}
	return m.clean.step == cleanConfirm || m.scan.consentConfirming
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()

	// A running scan reacts only to cancel/quit keys.
	if m.scan.running {
		switch k {
		case "esc", "ctrl+c":
			m.scan.cancel()
			m.setStatus(statusWarn, "cancelling scan…")
		case "q":
			m.scan.cancel()
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil
	}

	// Text input modes consume keys first.
	if m.scan.consentConfirming {
		return m.handleConsentInput(msg)
	}
	if m.clean.step == cleanConfirm {
		return m.handleConfirmInput(msg)
	}
	if l := m.lists[m.screen]; l != nil && l.filtering {
		return m.handleFilterInput(l, msg)
	}

	switch k {
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "tab":
		m.nextTab(1)
		return m, nil
	case "shift+tab":
		m.nextTab(-1)
		return m, nil
	case "P":
		m.screen = screenPermissions
		return m, nil
	}

	switch m.screen {
	case screenOverview:
		if k == "r" {
			m.reload()
			m.setStatus(statusInfo, "refreshed")
		}
	case screenApps, screenCaches, screenOrphans, screenLarge:
		return m.handleListKey(m.lists[m.screen], k)
	case screenScan:
		return m.handleScanKey(k)
	case screenPermissions:
		return m.handlePermsKey(k)
	case screenClean:
		return m.handleCleanKey(k)
	}
	return m, nil
}

func (m *Model) nextTab(delta int) {
	idx := 0
	for i, t := range tabs {
		if t.s == m.screen {
			idx = i
		}
	}
	idx = (idx + delta + len(tabs)) % len(tabs)
	m.screen = tabs[idx].s
}

func (m *Model) handleListKey(l *listState, k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		l.move(-1)
	case "down", "j":
		l.move(1)
	case " ":
		l.toggle()
	case "a":
		l.selectAll()
	case "n":
		l.clearSelection()
	case "/":
		l.filtering = true
		l.filter = ""
	case "s":
		l.toggleSort()
	case "r":
		m.reload()
		m.setStatus(statusInfo, "refreshed")
	case "p":
		return m.startPlan(l)
	}
	return m, nil
}

func (m *Model) handleFilterInput(l *listState, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		l.filtering = false
		l.filter = ""
	case tea.KeyEnter:
		l.filtering = false
	case tea.KeyBackspace:
		if len(l.filter) > 0 {
			l.filter = l.filter[:len(l.filter)-1]
		}
		l.applyFilter()
	case tea.KeyRunes, tea.KeySpace:
		l.filter += string(msg.Runes)
		l.applyFilter()
	}
	return m, nil
}

// View implements tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.renderTabs() + "\n\n")

	bodyHeight := m.bodyHeight()
	switch m.screen {
	case screenOverview:
		b.WriteString(m.renderOverview())
	case screenApps, screenCaches, screenOrphans, screenLarge:
		b.WriteString(m.lists[m.screen].render(m.st, bodyHeight))
	case screenScan:
		b.WriteString(m.renderScan())
	case screenPermissions:
		b.WriteString(m.renderPerms(bodyHeight))
	case screenClean:
		b.WriteString(m.renderClean())
	}

	b.WriteString("\n" + m.renderStatus())
	return b.String()
}

func (m *Model) bodyHeight() int {
	h := m.height - 6
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) renderTabs() string {
	parts := make([]string, 0, len(tabs))
	for _, t := range tabs {
		style := m.st.tab
		if t.s == m.screen {
			style = m.st.tabActive
		}
		parts = append(parts, style.Render(t.name))
	}
	left := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return m.st.title.Render("MCC — Mac Cache Cleaner") + "\n" + left
}

func (m *Model) renderStatus() string {
	text := render.Sanitize(m.status)
	if text == "" {
		return m.st.muted.Render(m.footerHelp())
	}
	var style lipgloss.Style
	switch m.statusKind {
	case statusError:
		style = m.st.bad
	case statusWarn:
		style = m.st.warn
	case statusGood:
		style = m.st.good
	default:
		style = m.st.muted
	}
	return style.Render(text) + "\n" + m.st.muted.Render(m.footerHelp())
}

func (m *Model) footerHelp() string {
	switch m.screen {
	case screenApps, screenCaches, screenOrphans, screenLarge:
		return helpText(m.keys.Up, m.keys.Down, m.keys.Select, m.keys.Plan, m.keys.Filter, m.keys.Sort, m.keys.Tab, m.keys.Quit)
	case screenScan:
		return helpText(m.keys.Confirm, m.keys.Back, m.keys.Quit)
	case screenClean:
		return helpText(m.keys.Confirm, m.keys.Back, m.keys.Quit)
	case screenPermissions:
		return helpText(m.keys.Up, m.keys.Down, m.keys.Revoke, m.keys.Back, m.keys.Quit)
	default:
		return helpText(m.keys.Tab, m.keys.Refresh, m.keys.Quit)
	}
}

// LargeThreshold returns the configured large-item threshold.
func (d Deps) LargeThreshold() int64 {
	if d.Config.LargeThreshold > 0 {
		return d.Config.LargeThreshold
	}
	return large.DefaultThreshold
}
