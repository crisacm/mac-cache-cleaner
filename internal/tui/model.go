package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"

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

	loading     bool
	loadStep    string
	loadCurrent string
	loadCh      chan loadProgress
	loadDone    chan loadedMsg

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

// loadProgress is a loading step report.
type loadProgress struct {
	Step    string
	Current string
}

// loadProgressMsg carries a loading step.
type loadProgressMsg struct{ p loadProgress }

// loadedMsg carries the finished load.
type loadedMsg struct {
	data loadedData
	err  error
}

// loadedData is everything the read-only screens need.
type loadedData struct {
	hasScan  bool
	scan     domain.Scan
	overview overviewData
	apps     []row
	caches   []row
	orphans  []row
	large    []row
	consents []domain.Consent
	errs     []string
}

// Init implements tea.Model. It starts the asynchronous load so the first frame
// appears immediately.
func (m *Model) Init() tea.Cmd {
	return m.startLoad()
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case loadProgressMsg:
		m.loadStep = msg.p.Step
		if msg.p.Current != "" {
			m.loadCurrent = msg.p.Current
		}
		return m, waitLoadProgress(m.loadCh)
	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.setStatus(statusError, "load: "+msg.err.Error())
			return m, nil
		}
		m.applyLoaded(msg.data)
		if len(msg.data.errs) > 0 {
			m.setStatus(statusWarn, "carga parcial: "+strings.Join(msg.data.errs, "; "))
		}
		return m, nil
	case scanProgressMsg:
		return m.handleScanProgress(msg.p)
	case scanDoneMsg:
		return m.handleScanDone(msg)
	case applyDoneMsg:
		return m.handleApplyDone(msg)
	}
	return m, nil
}

// startLoad begins an asynchronous load and returns the commands that stream
// progress and completion back to the model.
func (m *Model) startLoad() tea.Cmd {
	m.loading = true
	m.loadStep = ""
	m.loadCurrent = ""
	ch := make(chan loadProgress, 64)
	done := make(chan loadedMsg, 1)
	m.loadCh = ch
	m.loadDone = done
	go func() {
		data, err := m.loadData(func(p loadProgress) {
			select {
			case ch <- p:
			default: // drop frames; only the latest matters
			}
		})
		close(ch)
		done <- loadedMsg{data: data, err: err}
	}()
	return tea.Batch(waitLoadProgress(ch), waitLoadDone(done))
}

func waitLoadProgress(ch <-chan loadProgress) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return loadProgressMsg{p}
	}
}

func waitLoadDone(done <-chan loadedMsg) tea.Cmd {
	return func() tea.Msg { return <-done }
}

// loadSync performs the load inline (used by tests).
func (m *Model) loadSync() {
	data, err := m.loadData(func(loadProgress) {})
	m.loading = false
	if err != nil {
		m.setStatus(statusError, "load: "+err.Error())
		return
	}
	m.applyLoaded(data)
}

// loadData gathers everything the read-only screens need. The independent
// loaders run in parallel; progress reports the step and current path.
func (m *Model) loadData(progress func(loadProgress)) (loadedData, error) {
	ctx := m.ctx
	var data loadedData

	progress(loadProgress{Step: "último escaneo"})
	if sc, err := m.deps.Store.LatestScan(ctx); err == nil {
		data.hasScan = true
		data.scan = sc
	}
	scanID := ""
	if data.hasScan {
		scanID = data.scan.ID
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	fail := func(step string, err error) {
		mu.Lock()
		data.errs = append(data.errs, step+": "+err.Error())
		mu.Unlock()
	}

	wg.Add(5)

	// Totals + per-class sizes (the heaviest DB work).
	go func() {
		defer wg.Done()
		progress(loadProgress{Step: "totales"})
		if !data.hasScan {
			return
		}
		ov := overviewData{
			ScanID:     scanID,
			Partial:    data.scan.Partial,
			FinishedAt: "",
		}
		if !data.scan.FinishedAt.IsZero() {
			ov.FinishedAt = data.scan.FinishedAt.Format("2006-01-02 15:04:05")
		}
		byClass, err := m.deps.Store.TotalsByClass(ctx, scanID)
		if err != nil {
			fail("totales", err)
		} else {
			ov.ByClass = byClass
			for _, t := range byClass {
				ov.Totals.Entries += t.Entries
				ov.Totals.AllocatedBytes += t.AllocatedBytes
				ov.Totals.LogicalBytes += t.LogicalBytes
			}
		}
		if inacc, err := m.deps.Store.Inaccessible(ctx, scanID); err == nil {
			ov.Inaccessible = len(inacc)
		}
		mu.Lock()
		data.overview = ov
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		progress(loadProgress{Step: "aplicaciones"})
		rows, err := m.loadApps(ctx, scanID, progress)
		if err != nil {
			fail("apps", err)
			return
		}
		mu.Lock()
		data.apps = rows
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		progress(loadProgress{Step: "cachés"})
		rows, err := m.loadCaches(ctx, scanID)
		if err != nil {
			fail("cachés", err)
			return
		}
		mu.Lock()
		data.caches = rows
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		progress(loadProgress{Step: "huérfanos"})
		rows, err := m.loadOrphans(ctx, scanID, progress)
		if err != nil {
			fail("huérfanos", err)
			return
		}
		mu.Lock()
		data.orphans = rows
		mu.Unlock()
	}()

	go func() {
		defer wg.Done()
		progress(loadProgress{Step: "elementos grandes"})
		rows, err := m.loadLarge(ctx, scanID)
		if err != nil {
			fail("grandes", err)
			return
		}
		mu.Lock()
		data.large = rows
		mu.Unlock()
	}()

	wg.Wait()

	progress(loadProgress{Step: "permisos"})
	consents, err := m.deps.Store.Consents(ctx)
	if err != nil {
		data.errs = append(data.errs, "permisos: "+err.Error())
	}
	data.consents = consents
	progress(loadProgress{Step: "listo"})
	return data, nil
}

// applyLoaded installs a finished load into the model.
func (m *Model) applyLoaded(data loadedData) {
	m.hasScan = data.hasScan
	m.latestScan = data.scan
	ov := data.overview
	ov.Apps = len(data.apps)
	ov.Caches = len(data.caches)
	ov.Orphans = len(data.orphans)
	ov.Large = len(data.large)
	m.overview = ov

	m.lists[screenApps].setRows(data.apps)
	m.lists[screenCaches].setRows(data.caches)
	m.lists[screenOrphans].setRows(data.orphans)
	m.lists[screenLarge].setRows(data.large)

	m.perms = permsState{consents: data.consents}
	m.loadRoots()
}

func (m *Model) loadApps(ctx context.Context, scanID string, progress func(loadProgress)) ([]row, error) {
	list, err := m.deps.appsService().ListWithProgress(ctx, scanID, func(p string) {
		progress(loadProgress{Step: "aplicaciones", Current: p})
	})
	if err != nil {
		return nil, err
	}
	return rowsFromApps(list), nil
}

func (m *Model) loadCaches(ctx context.Context, scanID string) ([]row, error) {
	list, err := cache.New(m.deps.Store, m.deps.Home).List(ctx, scanID, "")
	if err != nil {
		return nil, err
	}
	return rowsFromCaches(list), nil
}

func (m *Model) loadOrphans(ctx context.Context, scanID string, progress func(loadProgress)) ([]row, error) {
	installed, complete := m.deps.appsService().InstalledBundleIDs(ctx)
	list, err := orphan.New(m.deps.Store, m.deps.Policy, m.deps.Home).List(ctx, orphan.Options{
		ScanID: scanID, Installed: installed, DiscoveryComplete: complete,
		Progress: func(p string) {
			progress(loadProgress{Step: "huérfanos", Current: p})
		},
	})
	if err != nil {
		return nil, err
	}
	return rowsFromOrphans(list), nil
}

func (m *Model) loadLarge(ctx context.Context, scanID string) ([]row, error) {
	if scanID == "" {
		return nil, nil // nothing is indexed yet
	}
	list, err := large.New(m.deps.Store, m.deps.LargeThreshold()).List(ctx, scanID, large.Options{})
	if err != nil {
		return nil, err
	}
	return rowsFromLarge(list), nil
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

	if k == "ctrl+c" || (k == "q" && !m.textInputActive()) {
		m.quitting = true
		return m, tea.Quit
	}

	// While loading, only navigation and quit are accepted.
	if m.loading {
		switch k {
		case "tab":
			m.nextTab(1)
		case "shift+tab":
			m.nextTab(-1)
		}
		return m, nil
	}

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
			m.setStatus(statusInfo, "recargando…")
			return m, m.startLoad()
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
		m.setStatus(statusInfo, "recargando…")
		return m, m.startLoad()
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

	if m.loading {
		b.WriteString(m.renderLoading())
		b.WriteString("\n" + m.st.muted.Render(helpText(m.keys.Quit)))
		return b.String()
	}

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

// renderLoading draws the startup loading indicator with the current step and
// the directory being read (nothing is being scanned: the local index is read).
func (m *Model) renderLoading() string {
	var b strings.Builder
	b.WriteString(m.st.title.Render("⏳ Cargando índice…") + "\n\n")
	if m.loadStep != "" {
		fmt.Fprintf(&b, "Paso: %s\n", render.Sanitize(m.loadStep))
	}
	if m.loadCurrent != "" {
		b.WriteString("  " + m.st.muted.Render(render.Redact(m.loadCurrent, m.deps.Home, false)) + "\n")
	}
	b.WriteString("\n" + m.st.muted.Render("Leyendo el índice local; no se está escaneando el disco.") + "\n")
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
