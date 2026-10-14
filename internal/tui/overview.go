package tui

import (
	"fmt"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

// renderOverview draws the Overview screen.
func (m *Model) renderOverview() string {
	var b strings.Builder
	if !m.hasScan {
		b.WriteString(m.st.warn.Render("No scan yet.") + "\n")
		b.WriteString("Go to the Scan tab (tab / shift+tab) and press Enter to index the disk.\n")
		return b.String()
	}

	ov := m.overview
	fmt.Fprintf(&b, "%s %s on disk (%s logical) across %d entries\n",
		m.st.title.Render("Total:"),
		render.FormatBytes(ov.Totals.AllocatedBytes),
		render.FormatBytes(ov.Totals.LogicalBytes),
		ov.Totals.Entries)
	if ov.FinishedAt != "" {
		fmt.Fprintf(&b, "%s %s\n", m.st.muted.Render("Last scan:"), ov.FinishedAt)
	}
	if ov.Partial || ov.Inaccessible > 0 {
		b.WriteString(m.st.warn.Render(fmt.Sprintf("Partial result: %d inaccessible path(s)", ov.Inaccessible)) + "\n")
	}

	fmt.Fprintf(&b, "\n%s  apps %d · caches %d · orphans %d · large %d\n",
		m.st.title.Render("Counts:"), ov.Apps, ov.Caches, ov.Orphans, ov.Large)

	if len(ov.ByClass) > 0 {
		b.WriteString("\n" + m.st.title.Render("By data class:") + "\n")
		for _, c := range domain.DataClasses {
			if t, ok := ov.ByClass[c]; ok {
				fmt.Fprintf(&b, "  %-20s %10s\n", c, render.FormatBytes(t.AllocatedBytes))
			}
		}
	}
	return b.String()
}
