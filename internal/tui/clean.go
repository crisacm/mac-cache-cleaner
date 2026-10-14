package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

// cleanState is the cleanup flow state: selection -> plan review -> explicit
// confirmation -> apply.
type cleanState struct {
	origin screen

	plan     domain.Plan
	skipped  []cleaner.Outcome
	active   bool
	step     cleanStep
	input    string
	applying bool
	result   cleaner.ApplyResult
	errText  string
}

type cleanStep int

const (
	cleanReview cleanStep = iota
	cleanConfirm
	cleanResult
)

// applyDoneMsg carries the outcome of an apply run.
type applyDoneMsg struct {
	res cleaner.ApplyResult
	err error
}

func newCleanState() cleanState { return cleanState{} }

// startPlan builds a plan from the current selection and opens the review
// screen. It changes nothing on disk (SEC-DEL-02).
func (m *Model) startPlan(l *listState) (tea.Model, tea.Cmd) {
	ctx := m.ctx
	if len(l.selected) == 0 {
		m.setStatus(statusWarn, "select items first (space)")
		return m, nil
	}
	if !m.hasScan {
		m.setStatus(statusWarn, "run a scan before planning")
		return m, nil
	}

	var candidates []cleaner.Candidate
	for _, r := range l.rows {
		if !l.selected[r.entryID] || r.entryID == "" {
			continue
		}
		entry, err := m.deps.Store.EntryByID(ctx, r.entryID)
		if err != nil {
			continue
		}
		// app_data is only planned after an explicit per-item selection
		// (SEC-DEL-03); selecting it here is that explicit act.
		candidates = append(candidates, cleaner.Candidate{Entry: entry, Explicit: true})
	}
	if len(candidates) == 0 {
		m.setStatus(statusWarn, "no selectable items in the selection")
		return m, nil
	}

	svc := m.deps.Cleaner
	if svc == nil {
		svc = cleaner.New(m.deps.Store, m.deps.Policy, m.deps.Home)
	}
	res, err := svc.Plan(ctx, cleaner.PlanOptions{
		ScanID:     m.latestScan.ID,
		Candidates: candidates,
		TTL:        m.deps.Config.PlanTTL,
		MaxItems:   m.deps.Config.MaxItems,
		MaxBytes:   m.deps.Config.MaxBytes,
	})
	if err != nil {
		m.setStatus(statusError, "plan: "+err.Error())
		return m, nil
	}
	m.clean = cleanState{origin: m.screen, plan: res.Plan, skipped: res.Skipped}
	if len(res.Plan.Items) == 0 {
		m.clean.active = true
		m.clean.step = cleanResult
		m.setStatus(statusWarn, "nothing eligible to clean")
		m.screen = screenClean
		return m, nil
	}
	m.clean.active = true
	m.clean.step = cleanReview
	m.screen = screenClean
	m.setStatus(statusInfo, fmt.Sprintf("plan %s created (dry-run)", res.Plan.PlanID))
	return m, nil
}

func (m *Model) handleCleanKey(k string) (tea.Model, tea.Cmd) {
	switch m.clean.step {
	case cleanReview:
		switch k {
		case "c", "enter":
			m.clean.step = cleanConfirm
			m.clean.input = ""
		case "esc":
			m.screen = m.clean.origin
		}
	case cleanResult:
		if k == "esc" || k == "enter" {
			m.screen = m.clean.origin
		}
	}
	return m, nil
}

// handleConfirmInput requires typing the exact item count (SEC-DEL-02).
func (m *Model) handleConfirmInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.clean.step = cleanReview
		m.clean.input = ""
	case tea.KeyEnter:
		want := strconv.Itoa(len(m.clean.plan.Items))
		if strings.TrimSpace(m.clean.input) != want {
			m.setStatus(statusWarn, "confirmation did not match the item count")
			return m, nil
		}
		m.clean.step = cleanResult
		m.clean.applying = true
		m.setStatus(statusInfo, "applying plan…")
		return m, m.applyCmd()
	case tea.KeyBackspace:
		if len(m.clean.input) > 0 {
			m.clean.input = m.clean.input[:len(m.clean.input)-1]
		}
	case tea.KeyRunes:
		m.clean.input += string(msg.Runes)
	}
	return m, nil
}

// applyCmd runs the plan. The explicit confirmation already happened on the
// confirmation screen, so the service callback returns true.
func (m *Model) applyCmd() tea.Cmd {
	planID := m.clean.plan.PlanID
	svc := m.deps.Cleaner
	if svc == nil {
		svc = cleaner.New(m.deps.Store, m.deps.Policy, m.deps.Home)
	}
	return func() tea.Msg {
		res, err := svc.Apply(context.Background(), cleaner.ApplyOptions{
			PlanID:  planID,
			Confirm: func(int, int64) (bool, error) { return true, nil },
		})
		return applyDoneMsg{res: res, err: err}
	}
}

func (m *Model) handleApplyDone(msg applyDoneMsg) (tea.Model, tea.Cmd) {
	m.clean.applying = false
	if msg.err != nil {
		m.clean.errText = msg.err.Error()
		m.setStatus(statusError, "apply failed: "+msg.err.Error())
		return m, nil
	}
	m.clean.result = msg.res
	m.setStatus(statusGood, fmt.Sprintf("applied %d, skipped %d, failed %d",
		len(msg.res.Applied), len(msg.res.Skipped), len(msg.res.Failed)))
	m.reload()
	return m, nil
}

// renderClean draws the cleanup flow.
func (m *Model) renderClean() string {
	var b strings.Builder
	if !m.clean.active {
		return m.st.muted.Render("No plan. Select items in a list and press p.")
	}
	items := m.clean.plan.Items
	b.WriteString(m.st.title.Render("Plan review (dry-run — nothing deleted yet)") + "\n")
	fmt.Fprintf(&b, "Plan %s\n  items: %d\n  total: %s\n  expires: %s\n",
		m.clean.plan.PlanID, len(items), render.FormatBytes(m.clean.plan.TotalSize),
		m.clean.plan.ExpiresAt.Format("15:04:05"))

	if len(items) > 0 {
		b.WriteString("\n" + m.st.title.Render("Items:") + "\n")
		for i, it := range items {
			if i >= 15 {
				fmt.Fprintf(&b, "  … and %d more\n", len(items)-15)
				break
			}
			warn := ""
			if it.DataClass.MayContainUserData() {
				warn = m.st.warn.Render("  ⚠ may contain user data")
			}
			fmt.Fprintf(&b, "  %-18s %10s  %s%s\n", it.DataClass, render.FormatBytes(it.Size),
				render.Redact(it.Path, m.deps.Home, false), warn)
		}
	}
	if len(m.clean.skipped) > 0 {
		fmt.Fprintf(&b, "\n%d item(s) skipped (not eligible):\n", len(m.clean.skipped))
		for i, s := range m.clean.skipped {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "  %s — %s\n", render.Redact(s.Path, m.deps.Home, false), render.Redact(s.Reason, m.deps.Home, false))
		}
	}

	switch m.clean.step {
	case cleanReview:
		b.WriteString("\n" + m.st.warn.Render("Press c to confirm and apply (items move to Trash).") + "\n")
	case cleanConfirm:
		b.WriteString("\n" + m.st.warn.Render("Type the item count and press Enter to apply:") + " ")
		b.WriteString(render.Sanitize(m.clean.input) + "\n")
		b.WriteString(m.st.muted.Render(fmt.Sprintf("(expected: %d — Enter alone is not enough)", len(items))) + "\n")
	case cleanResult:
		if m.clean.applying {
			b.WriteString("\nApplying…\n")
		} else if m.clean.errText != "" {
			b.WriteString("\n" + m.st.bad.Render("Apply failed: "+render.Sanitize(m.clean.errText)) + "\n")
		} else {
			fmt.Fprintf(&b, "\n%s applied %d, skipped %d, failed %d\n",
				m.st.good.Render("Done:"),
				len(m.clean.result.Applied), len(m.clean.result.Skipped), len(m.clean.result.Failed))
			for _, o := range m.clean.result.Skipped {
				fmt.Fprintf(&b, "  skipped %s — %s\n", render.Redact(o.Path, m.deps.Home, false), render.Redact(o.Reason, m.deps.Home, false))
			}
		}
	}
	return b.String()
}
