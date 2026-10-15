package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

type sortMode int

const (
	sortBySize sortMode = iota
	sortByName
)

func (s sortMode) String() string {
	if s == sortByName {
		return "name"
	}
	return "size"
}

// row is one rendered list entry. All untrusted text is sanitized when the row
// is built (SEC-COD-02).
type row struct {
	title      string
	meta       string
	path       string
	size       int64
	class      domain.DataClass
	confidence domain.Confidence
	entryID    string
	isDir      bool
	canExpand  bool
	// filler marks a non-selectable placeholder row (e.g. "… and N more").
	filler bool
}

// selectable reports whether the row may be added to a plan. user_file and
// application are report-only (SEC-DEL-03).
func (r row) selectable() bool { return !r.filler && !r.class.ReportOnly() }

// treeNode is a row plus its place in the tree.
type treeNode struct {
	row      row
	depth    int
	parent   *treeNode
	children []*treeNode
	loaded   bool
	expanded bool
}

// listState is the shared state for the list screens. Screens whose rows are
// directories (caches/orphans/large) use the tree behaviour (expand/collapse);
// others stay flat.
type listState struct {
	title string
	tree  bool

	roots     []*treeNode
	visible   []*treeNode
	cursor    int
	filter    string
	filtering bool
	sortBy    sortMode
	// showFiles, when false, hides non-directory children inside expanded
	// folders (top-level rows are always shown).
	showFiles bool
	selected  map[string]bool
	err       string
}

func newListState(title string) *listState {
	return &listState{title: title, selected: map[string]bool{}}
}

func newTreeState(title string) *listState {
	return &listState{title: title, tree: true, selected: map[string]bool{}}
}

func (l *listState) setRows(rows []row) {
	l.roots = make([]*treeNode, 0, len(rows))
	for _, r := range rows {
		l.roots = append(l.roots, &treeNode{row: r, depth: 0})
	}
	l.cursor = 0
	l.flatten()
}

// flatten recomputes the visible node list from expansion, filter and sort.
func (l *listState) flatten() {
	needle := strings.ToLower(l.filter)
	l.visible = l.visible[:0]

	var visit func(n *treeNode, force bool)
	visit = func(n *treeNode, force bool) {
		matches := force || needle == "" || l.subtreeMatches(n, needle)
		if !matches {
			return
		}
		l.visible = append(l.visible, n)
		// Auto-expand when filtering so nested matches are reachable.
		expand := n.expanded || (needle != "" && l.tree)
		if !expand {
			return
		}
		children := l.visibleChildren(n)
		for _, c := range children {
			visit(c, needle != "")
		}
	}

	roots := make([]*treeNode, len(l.roots))
	copy(roots, l.roots)
	sort.SliceStable(roots, func(a, b int) bool { return l.less(roots[a].row, roots[b].row) })
	for _, n := range roots {
		visit(n, false)
	}

	if l.cursor >= len(l.visible) {
		l.cursor = max(0, len(l.visible)-1)
	}
}

// visibleChildren returns a node's children honoring expansion, the
// show-files toggle and sorting.
func (l *listState) visibleChildren(n *treeNode) []*treeNode {
	if !n.loaded || !n.expanded {
		return nil
	}
	kids := make([]*treeNode, 0, len(n.children))
	for _, c := range n.children {
		if !l.showFiles && !c.row.isDir {
			continue
		}
		kids = append(kids, c)
	}
	sort.SliceStable(kids, func(a, b int) bool { return l.less(kids[a].row, kids[b].row) })
	return kids
}

func (l *listState) less(a, b row) bool {
	if l.sortBy == sortByName {
		return a.title < b.title
	}
	if a.size != b.size {
		return a.size > b.size
	}
	return a.title < b.title
}

func (l *listState) subtreeMatches(n *treeNode, needle string) bool {
	if strings.Contains(strings.ToLower(n.row.title+" "+n.row.meta+" "+n.row.path), needle) {
		return true
	}
	for _, c := range n.children {
		if l.subtreeMatches(c, needle) {
			return true
		}
	}
	return false
}

func (l *listState) current() (*treeNode, bool) {
	if len(l.visible) == 0 {
		return nil, false
	}
	return l.visible[l.cursor], true
}

func (l *listState) move(delta int) {
	if len(l.visible) == 0 {
		return
	}
	l.cursor += delta
	if l.cursor < 0 {
		l.cursor = 0
	}
	if l.cursor >= len(l.visible) {
		l.cursor = len(l.visible) - 1
	}
}

// toggle selects or deselects the current node. A node may not be selected
// while an ancestor is selected (SEC-DEL: avoid planning a directory and its
// own contents twice).
func (l *listState) toggle() (ok bool, reason string) {
	n, found := l.current()
	if !found {
		return false, ""
	}
	r := n.row
	if !r.selectable() || r.entryID == "" {
		return false, "este ítem es de solo reporte"
	}
	if l.selected[r.entryID] {
		delete(l.selected, r.entryID)
		return true, ""
	}
	for p := n.parent; p != nil; p = p.parent {
		if l.selected[p.row.entryID] {
			return false, "ya hay un directorio padre seleccionado"
		}
	}
	// Selecting a directory drops any selected descendants.
	for _, d := range descendants(n) {
		delete(l.selected, d.row.entryID)
	}
	l.selected[r.entryID] = true
	return true, ""
}

func descendants(n *treeNode) []*treeNode {
	var out []*treeNode
	for _, c := range n.children {
		out = append(out, c)
		out = append(out, descendants(c)...)
	}
	return out
}

// selectAll selects the selectable top-level rows only (keeps the
// no-ancestor-and-descendant rule trivially true).
func (l *listState) selectAll() {
	for _, n := range l.roots {
		if n.row.selectable() && n.row.entryID != "" {
			l.selected[n.row.entryID] = true
		}
	}
}

func (l *listState) clearSelection() { l.selected = map[string]bool{} }

func (l *listState) toggleSort() {
	if l.sortBy == sortBySize {
		l.sortBy = sortByName
	} else {
		l.sortBy = sortBySize
	}
	l.flatten()
}

// allNodes returns every node known to the list (roots plus loaded children).
func (l *listState) allNodes() []*treeNode {
	var out []*treeNode
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		out = append(out, n)
		for _, c := range n.children {
			walk(c)
		}
	}
	for _, n := range l.roots {
		walk(n)
	}
	return out
}

// render lists the visible rows.
func (l *listState) render(st styles, height int) string {
	var b strings.Builder
	header := fmt.Sprintf("%s  (%d items, %d selected, sort: %s)",
		st.title.Render(render.Sanitize(l.title)), len(l.visible), len(l.selected), l.sortBy)
	if l.tree {
		state := "solo directorios"
		if l.showFiles {
			state = "todos los elementos"
		}
		header += "  ·  " + state
	}
	b.WriteString(header + "\n")
	if l.filtering || l.filter != "" {
		b.WriteString(st.muted.Render("filter: ") + render.Sanitize(l.filter) + "\n")
	}
	if l.err != "" {
		b.WriteString(st.bad.Render("error: ") + render.Sanitize(l.err) + "\n")
	}
	if len(l.visible) == 0 {
		b.WriteString(st.muted.Render("  (nothing to show)") + "\n")
		return b.String()
	}

	start := 0
	if l.cursor >= height {
		start = l.cursor - height + 1
	}
	end := min(len(l.visible), start+height)
	for i := start; i < end; i++ {
		n := l.visible[i]
		marker := "[ ]"
		switch {
		case !n.row.selectable():
			marker = " - "
		case l.selected[n.row.entryID]:
			marker = "[x]"
		}
		chevron := "  "
		if l.tree && n.row.canExpand {
			if n.expanded {
				chevron = "v "
			} else {
				chevron = "> "
			}
		}
		cursor := "  "
		if i == l.cursor {
			cursor = "> "
		}
		indent := strings.Repeat("  ", n.depth)
		title := truncate(n.row.title, max(8, 28-n.depth*2))
		line := fmt.Sprintf("%s%s%s%s %-24s %10s  %-18s %s",
			cursor, indent, chevron, marker, title, render.FormatBytes(n.row.size),
			render.Sanitize(string(n.row.class)), render.Sanitize(n.row.path))
		if l.selected[n.row.entryID] {
			line = st.selected.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// --- row builders ------------------------------------------------------

func rowFromEntry(e domain.Entry) row {
	return row{
		title:     render.Sanitize(lastSegment(e.Path)),
		meta:      string(e.Kind),
		path:      e.Path,
		size:      e.AllocatedBytes,
		class:     e.DataClass,
		entryID:   e.EntryID,
		isDir:     e.IsDir,
		canExpand: e.IsDir && e.Kind != domain.KindPackage,
	}
}

func rowsFromApps(apps []domain.App) []row {
	out := make([]row, 0, len(apps))
	for _, a := range apps {
		out = append(out, row{
			title:   render.Sanitize(a.Name),
			meta:    render.Sanitize(a.BundleID),
			path:    a.Path,
			size:    a.AllocatedBytes,
			class:   domain.ClassApplication,
			entryID: "", // applications are report-only
		})
	}
	return out
}

func rowsFromCaches(caches []domain.CacheEntry) []row {
	out := make([]row, 0, len(caches))
	for _, c := range caches {
		out = append(out, row{
			title:     render.Sanitize(c.Name),
			meta:      render.Sanitize(c.AppID),
			path:      c.Path,
			size:      c.AllocatedBytes,
			class:     c.DataClass,
			entryID:   c.EntryID,
			isDir:     true,
			canExpand: true,
		})
	}
	return out
}

func rowsFromOrphans(orphans []domain.Orphan) []row {
	out := make([]row, 0, len(orphans))
	for _, o := range orphans {
		out = append(out, row{
			title:      render.Sanitize(o.BundleID),
			meta:       string(o.Confidence),
			path:       o.Path,
			size:       o.AllocatedBytes,
			class:      o.DataClass,
			confidence: o.Confidence,
			entryID:    o.EntryID,
			isDir:      true,
			canExpand:  true,
		})
	}
	return out
}

func rowsFromLarge(items []domain.LargeItem) []row {
	out := make([]row, 0, len(items))
	for _, it := range items {
		out = append(out, row{
			title:     render.Sanitize(lastSegment(it.Path)),
			meta:      string(it.Kind),
			path:      it.Path,
			size:      it.AllocatedBytes,
			class:     it.DataClass,
			entryID:   it.EntryID,
			isDir:     it.IsDir,
			canExpand: it.IsDir && it.Kind != domain.KindPackage,
		})
	}
	return out
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}
