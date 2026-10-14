package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// writeJSON marshals v as indented JSON to w.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table renders simple space-aligned rows.
type table struct {
	header []string
	rows   [][]string
}

func (t *table) add(cells ...string) { t.rows = append(t.rows, cells) }

func (t *table) render(w io.Writer) {
	widths := make([]int, len(t.header))
	for i, h := range t.header {
		widths[i] = len(h)
	}
	for _, row := range t.rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	writeRow := func(cells []string) {
		for i, cell := range cells {
			if i == len(cells)-1 {
				fmt.Fprintf(w, "%s\n", cell)
				continue
			}
			fmt.Fprintf(w, "%-*s  ", widths[i], cell)
		}
	}
	if len(t.header) > 0 {
		writeRow(t.header)
	}
	for _, row := range t.rows {
		writeRow(row)
	}
}
