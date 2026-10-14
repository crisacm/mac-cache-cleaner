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
}

// selectable reports whether the row may be added to a plan. user_file and
// application are report-only (SEC-DEL-03).
func (r row) selectable() bool { return !r.class.ReportOnly() }

// listState is the shared state for the Apps/Caches/Orphans/Large screens.
type listState struct {
	title     string
	rows      []row
	order     []int
	cursor    int
	filter    string
	filtering bool
	sortBy    sortMode
	selected  map[string]bool
	err       string
}

func newListState(title string) *listState {
	return &listState{title: title, selected: map[string]bool{}}
}

func (l *listState) setRows(rows []row) {
	l.rows = rows
	l.order = nil
	l.cursor = 0
	l.applyFilter()
}

func (l *listState) applyFilter() {
	l.order = l.order[:0]
	needle := strings.ToLower(l.filter)
	for i, r := range l.rows {
		if needle != "" && !strings.Contains(strings.ToLower(r.title+" "+r.meta+" "+r.path), needle) {
			continue
		}
		l.order = append(l.order, i)
	}
	sort.SliceStable(l.order, func(a, b int) bool {
		ra, rb := l.rows[l.order[a]], l.rows[l.order[b]]
		if l.sortBy == sortByName {
			return ra.title < rb.title
		}
		if ra.size != rb.size {
			return ra.size > rb.size
		}
		return ra.title < rb.title
	})
	if l.cursor >= len(l.order) {
		l.cursor = max(0, len(l.order)-1)
	}
}

func (l *listState) current() (row, bool) {
	if len(l.order) == 0 {
		return row{}, false
	}
	return l.rows[l.order[l.cursor]], true
}

func (l *listState) move(delta int) {
	if len(l.order) == 0 {
		return
	}
	l.cursor += delta
	if l.cursor < 0 {
		l.cursor = 0
	}
	if l.cursor >= len(l.order) {
		l.cursor = len(l.order) - 1
	}
}

func (l *listState) toggle() {
	r, ok := l.current()
	if !ok || !r.selectable() || r.entryID == "" {
		return
	}
	if l.selected[r.entryID] {
		delete(l.selected, r.entryID)
		return
	}
	l.selected[r.entryID] = true
}

func (l *listState) selectAll() {
	for _, i := range l.order {
		r := l.rows[i]
		if r.selectable() && r.entryID != "" {
			l.selected[r.entryID] = true
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
	l.applyFilter()
}

// render lists the visible rows.
func (l *listState) render(st styles, height int) string {
	var b strings.Builder
	header := fmt.Sprintf("%s  (%d items, %d selected, sort: %s)",
		st.title.Render(render.Sanitize(l.title)), len(l.order), len(l.selected), l.sortBy)
	b.WriteString(header + "\n")
	if l.filtering || l.filter != "" {
		b.WriteString(st.muted.Render("filter: ") + render.Sanitize(l.filter) + "\n")
	}
	if l.err != "" {
		b.WriteString(st.bad.Render("error: ") + render.Sanitize(l.err) + "\n")
	}
	if len(l.order) == 0 {
		b.WriteString(st.muted.Render("  (nothing to show)") + "\n")
		return b.String()
	}

	start := 0
	if l.cursor >= height {
		start = l.cursor - height + 1
	}
	end := min(len(l.order), start+height)
	for i := start; i < end; i++ {
		r := l.rows[l.order[i]]
		marker := "[ ]"
		switch {
		case !r.selectable():
			marker = " - "
		case l.selected[r.entryID]:
			marker = "[x]"
		}
		cursor := "  "
		if i == l.cursor {
			cursor = "> "
		}
		line := fmt.Sprintf("%s%s %-28s %10s  %-18s %s",
			cursor, marker, truncate(r.title, 28), render.FormatBytes(r.size),
			render.Sanitize(string(r.class)), render.Sanitize(r.path))
		if l.selected[r.entryID] {
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

// rowsFromApps, rowsFromCaches, rowsFromOrphans, rowsFromLarge build rows from
// service results, sanitizing untrusted text.
func rowsFromApps(apps []domain.App) []row {
	out := make([]row, 0, len(apps))
	for _, a := range apps {
		out = append(out, row{
			title:   render.Sanitize(a.Name),
			meta:    render.Sanitize(a.BundleID),
			path:    a.Path,
			size:    a.AllocatedBytes,
			class:   domain.ClassApplication,
			entryID: domain.ComputeEntryID("", a.Path),
		})
	}
	return out
}

func rowsFromCaches(caches []domain.CacheEntry) []row {
	out := make([]row, 0, len(caches))
	for _, c := range caches {
		out = append(out, row{
			title:   render.Sanitize(c.Name),
			meta:    render.Sanitize(c.AppID),
			path:    c.Path,
			size:    c.AllocatedBytes,
			class:   c.DataClass,
			entryID: c.EntryID,
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
		})
	}
	return out
}

func rowsFromLarge(items []domain.LargeItem) []row {
	out := make([]row, 0, len(items))
	for _, it := range items {
		out = append(out, row{
			title:   render.Sanitize(lastSegment(it.Path)),
			meta:    string(it.Kind),
			path:    it.Path,
			size:    it.AllocatedBytes,
			class:   it.DataClass,
			entryID: it.EntryID,
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
