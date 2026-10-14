package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/crisacm/mac-cache-cleaner/internal/consent"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

// permsState is the Permissions screen state.
type permsState struct {
	consents []domain.Consent
	cursor   int
}

func newPermsState() permsState { return permsState{} }

func (m *Model) loadPerms() {
	consents, err := m.deps.Store.Consents(m.ctx)
	if err != nil {
		m.setStatus(statusError, "permissions: "+err.Error())
		return
	}
	m.perms = permsState{consents: consents}
}

func (m *Model) handlePermsKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		if m.perms.cursor > 0 {
			m.perms.cursor--
		}
	case "down", "j":
		if m.perms.cursor < len(m.perms.consents)-1 {
			m.perms.cursor++
		}
	case "x":
		if len(m.perms.consents) == 0 {
			m.setStatus(statusWarn, "no consent to revoke")
			return m, nil
		}
		root := m.perms.consents[m.perms.cursor].Root
		svc := m.deps.Consent
		if svc == nil {
			svc = consent.New(m.deps.Store, m.deps.Policy, m.deps.IsTTY)
		}
		if err := svc.Revoke(m.ctx, root); err != nil {
			m.setStatus(statusError, "revoke: "+err.Error())
			return m, nil
		}
		m.setStatus(statusGood, "revoked consent for "+render.Redact(root, m.deps.Home, false))
		m.loadPerms()
	case "r":
		m.loadPerms()
		m.setStatus(statusInfo, "permissions reloaded")
	case "esc":
		m.screen = screenOverview
	}
	return m, nil
}

func (m *Model) renderPerms(_ int) string {
	var b strings.Builder
	b.WriteString(m.st.title.Render("Effective policy (compiled in)") + "\n")
	b.WriteString(m.st.muted.Render("Configuration can never downgrade a tier (SEC-ACC-03).") + "\n\n")
	for _, r := range m.deps.Policy.Rules() {
		fmt.Fprintf(&b, "  tier %s  %-42s %s\n", r.Tier, render.Redact(r.Prefix, m.deps.Home, false), render.Sanitize(r.Note))
	}

	b.WriteString("\n" + m.st.title.Render("Recorded consents") + "\n")
	if len(m.perms.consents) == 0 {
		b.WriteString(m.st.muted.Render("  (none)") + "\n")
		return b.String()
	}
	now := time.Now()
	for i, c := range m.perms.consents {
		cursor := "  "
		if i == m.perms.cursor {
			cursor = "> "
		}
		state := "active"
		if !c.Active(now) {
			state = "revoked"
		}
		fmt.Fprintf(&b, "%s%-42s tier %s  %s  %s\n",
			cursor, render.Redact(c.Root, m.deps.Home, false), c.Tier, state,
			c.GrantedAt.Format("2006-01-02 15:04"))
	}
	b.WriteString("\n" + m.st.muted.Render("Press x to revoke the selected consent.") + "\n")
	return b.String()
}
