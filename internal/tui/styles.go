package tui

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/lipgloss"
)

// styles holds the lipgloss styles built from the program's renderer. When the
// output is not a terminal the renderer produces plain text, so tests and
// piping stay deterministic.
type styles struct {
	r         *lipgloss.Renderer
	title     lipgloss.Style
	tab       lipgloss.Style
	tabActive lipgloss.Style
	muted     lipgloss.Style
	selected  lipgloss.Style
	cursor    lipgloss.Style
	good      lipgloss.Style
	warn      lipgloss.Style
	bad       lipgloss.Style
	box       lipgloss.Style
}

func newStyles(r *lipgloss.Renderer) styles {
	return styles{
		r:         r,
		title:     r.NewStyle().Bold(true),
		tab:       r.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("240")),
		tabActive: r.NewStyle().Padding(0, 1).Bold(true).Foreground(lipgloss.Color("39")),
		muted:     r.NewStyle().Foreground(lipgloss.Color("240")),
		selected:  r.NewStyle().Foreground(lipgloss.Color("42")),
		cursor:    r.NewStyle().Bold(true),
		good:      r.NewStyle().Foreground(lipgloss.Color("42")),
		warn:      r.NewStyle().Foreground(lipgloss.Color("214")),
		bad:       r.NewStyle().Foreground(lipgloss.Color("196")),
		box:       r.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
	}
}

// keyMap holds the key bindings shown in the footer.
type keyMap struct {
	Up          key.Binding
	Down        key.Binding
	Select      key.Binding
	SelectAll   key.Binding
	ClearSel    key.Binding
	Filter      key.Binding
	Sort        key.Binding
	Expand      key.Binding
	Collapse    key.Binding
	Files       key.Binding
	Plan        key.Binding
	Refresh     key.Binding
	Confirm     key.Binding
	Back        key.Binding
	Quit        key.Binding
	Tab         key.Binding
	Permissions key.Binding
	Revoke      key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "move up")),
		Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "move down")),
		Select:      key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
		SelectAll:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all")),
		ClearSel:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "clear selection")),
		Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Sort:        key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort")),
		Expand:      key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→", "expand")),
		Collapse:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←", "collapse")),
		Files:       key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "files/dirs")),
		Plan:        key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "plan selected")),
		Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Confirm:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "confirm")),
		Back:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back/cancel")),
		Quit:        key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Tab:         key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next screen")),
		Permissions: key.NewBinding(key.WithKeys("P"), key.WithHelp("P", "permissions")),
		Revoke:      key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "revoke consent")),
	}
}

// helpText renders a compact footer from bindings.
func helpText(bindings ...key.Binding) string {
	out := ""
	for i, b := range bindings {
		if i > 0 {
			out += "  "
		}
		h := b.Help()
		out += h.Key + " " + h.Desc
	}
	return out
}
