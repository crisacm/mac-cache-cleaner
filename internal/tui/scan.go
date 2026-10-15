package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/crisacm/mac-cache-cleaner/internal/consent"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/scanner"
)

// rootEntry is one row of the Scan screen's effective-root list.
type rootEntry struct {
	path       string
	tier       domain.Tier
	decision   policy.Decision
	reason     string
	reportOnly bool
}

func (r rootEntry) included() bool { return r.decision == policy.DecisionAllow }

// scanState is the Scan screen state.
type scanState struct {
	roots  []rootEntry
	cursor int

	running    bool
	cancelling bool
	progress   scanner.Progress
	ch         chan scanner.Progress
	done       chan scanOutcome
	cancel     context.CancelFunc

	consentConfirming bool
	consentRoot       string
	consentInput      string
}

// scanProgressMsg carries a progress snapshot.
type scanProgressMsg struct{ p scanner.Progress }

// scanOutcome is the terminal value of a scan run.
type scanOutcome struct {
	out scan.Outcome
	err error
}

// scanDoneMsg signals that a scan finished (or was cancelled).
type scanDoneMsg struct{ outcome scanOutcome }

func newScanState(Deps) scanState { return scanState{} }

// loadRoots rebuilds the effective-root list from the compiled policy table.
func (m *Model) loadRoots() {
	m.scan.roots = nil
	for _, rule := range m.deps.Policy.Rules() {
		v, err := m.deps.Policy.RootVerdict(m.ctx, rule.Prefix)
		if err != nil {
			continue
		}
		m.scan.roots = append(m.scan.roots, rootEntry{
			path: v.Path, tier: v.Tier, decision: v.Decision,
			reason: v.Reason, reportOnly: v.ReportOnly,
		})
	}
	if m.scan.cursor >= len(m.scan.roots) {
		m.scan.cursor = max(0, len(m.scan.roots)-1)
	}
}

// includedRoots returns the paths that may be scanned right now.
func (m *Model) includedRoots() []string {
	var out []string
	for _, r := range m.scan.roots {
		if r.included() {
			out = append(out, r.path)
		}
	}
	return out
}

func (m *Model) handleScanKey(k string) (tea.Model, tea.Cmd) {
	switch k {
	case "up", "k":
		if m.scan.cursor > 0 {
			m.scan.cursor--
		}
	case "down", "j":
		if m.scan.cursor < len(m.scan.roots)-1 {
			m.scan.cursor++
		}
	case "enter":
		return m.startScan()
	case "g":
		if m.scan.cursor < len(m.scan.roots) {
			r := m.scan.roots[m.scan.cursor]
			if r.decision == policy.DecisionConsent {
				m.scan.consentConfirming = true
				m.scan.consentRoot = r.path
				m.scan.consentInput = ""
			} else {
				m.setStatus(statusWarn, "no consent needed for this root")
			}
		}
	case "r":
		m.loadRoots()
		m.setStatus(statusInfo, "roots reloaded")
	}
	return m, nil
}

// startScan runs a scan asynchronously with progress and cancellation.
func (m *Model) startScan() (tea.Model, tea.Cmd) {
	roots := m.includedRoots()
	if len(roots) == 0 {
		m.setStatus(statusWarn, "no scannable roots (consent may be required)")
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	ch := make(chan scanner.Progress, 32)
	done := make(chan scanOutcome, 1)
	m.scan.running = true
	m.scan.cancelling = false
	m.scan.progress = scanner.Progress{}
	m.scan.cancel = cancel
	m.scan.ch = ch
	m.scan.done = done
	m.setStatus(statusInfo, fmt.Sprintf("scanning %d root(s)…", len(roots)))

	go func() {
		out, err := m.deps.Scan.Run(ctx, scan.Options{
			Roots:         roots,
			Workers:       m.deps.Config.Workers,
			ProgressEvery: 64,
			Progress: func(p scanner.Progress) {
				select {
				case ch <- p:
				default:
				}
			},
		})
		close(ch)
		done <- scanOutcome{out: out, err: err}
	}()

	return m, tea.Batch(waitProgress(ch), waitDone(done))
}

func waitProgress(ch <-chan scanner.Progress) tea.Cmd {
	return func() tea.Msg {
		p, ok := <-ch
		if !ok {
			return nil
		}
		return scanProgressMsg{p}
	}
}

func waitDone(done <-chan scanOutcome) tea.Cmd {
	return func() tea.Msg { return scanDoneMsg{outcome: <-done} }
}

func (m *Model) handleScanProgress(p scanner.Progress) (tea.Model, tea.Cmd) {
	m.scan.progress = p
	return m, waitProgress(m.scan.ch)
}

func (m *Model) handleScanDone(msg scanDoneMsg) (tea.Model, tea.Cmd) {
	m.scan.running = false
	m.scan.cancelling = false
	err := msg.outcome.err
	switch {
	case err == nil:
		res := msg.outcome.out.Result
		if res.Partial {
			m.setStatus(statusWarn, fmt.Sprintf("scan complete (partial): %d inaccessible path(s)",
				len(res.Inaccessible)))
		} else {
			m.setStatus(statusGood, fmt.Sprintf("scan complete: %d entries, %s",
				res.Totals.Entries, render.FormatBytes(res.Totals.AllocatedBytes)))
		}
	case isCancelled(err):
		m.setStatus(statusWarn, "scan cancelled — partial index rolled back")
	default:
		m.setStatus(statusError, "scan failed: "+err.Error())
	}
	// Reload the lists with the new (or rolled-back) index.
	return m, m.startLoad()
}

func isCancelled(err error) bool {
	return err == context.Canceled || err == context.DeadlineExceeded
}

// handleConsentInput consumes typing while the consent confirmation is open.
func (m *Model) handleConsentInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.scan.consentConfirming = false
		m.setStatus(statusInfo, "consent not granted")
	case tea.KeyEnter:
		m.scan.consentConfirming = false
		if !strings.EqualFold(strings.TrimSpace(m.scan.consentInput), "grant") {
			m.setStatus(statusWarn, "consent not granted: confirmation word did not match")
			return m, nil
		}
		svc := m.deps.Consent
		if svc == nil {
			svc = consent.New(m.deps.Store, m.deps.Policy, m.deps.IsTTY)
		}
		if _, err := svc.Grant(m.ctx, m.scan.consentRoot); err != nil {
			m.setStatus(statusError, "consent not granted: "+err.Error())
			return m, nil
		}
		m.setStatus(statusGood, "consent recorded for "+render.Redact(m.scan.consentRoot, m.deps.Home, false))
		m.loadRoots()
	case tea.KeyBackspace:
		if len(m.scan.consentInput) > 0 {
			m.scan.consentInput = m.scan.consentInput[:len(m.scan.consentInput)-1]
		}
	case tea.KeyRunes, tea.KeySpace:
		m.scan.consentInput += string(msg.Runes)
	}
	return m, nil
}

// renderScan draws the Scan screen.
func (m *Model) renderScan() string {
	var b strings.Builder
	b.WriteString(m.st.title.Render("Effective scan roots") + "\n")
	b.WriteString(m.st.muted.Render("Tier 2 is never scanned; Tier 1/3 need recorded consent.") + "\n\n")

	if len(m.scan.roots) == 0 {
		b.WriteString("  (no roots)\n")
	}
	for i, r := range m.scan.roots {
		cursor := "  "
		if i == m.scan.cursor {
			cursor = "> "
		}
		flag := "include"
		switch r.decision {
		case policy.DecisionConsent:
			flag = "consent required"
		case policy.DecisionDeny:
			flag = "skipped"
		}
		line := fmt.Sprintf("%s%-42s tier %s  %-16s %s",
			cursor, render.Redact(r.path, m.deps.Home, false), r.tier, flag, render.Redact(r.reason, m.deps.Home, false))
		if !r.included() {
			line = m.st.muted.Render(line)
		}
		b.WriteString(line + "\n")
	}

	if m.scan.consentConfirming {
		b.WriteString("\n" + m.st.warn.Render("Grant consent for "+render.Redact(m.scan.consentRoot, m.deps.Home, false)) + "\n")
		b.WriteString("This records access for this root only. Type \"grant\" and press Enter: ")
		b.WriteString(render.Sanitize(m.scan.consentInput) + "\n")
		return b.String()
	}

	b.WriteString("\n")
	if m.scan.running {
		p := m.scan.progress
		b.WriteString(m.st.good.Render("Scanning…") + "\n")
		fmt.Fprintf(&b, "  directories %d · entries %d · %s\n",
			p.Directories, p.Entries, render.FormatBytes(p.AllocatedBytes))
		if p.Current != "" {
			b.WriteString("  " + m.st.muted.Render(render.Redact(p.Current, m.deps.Home, false)) + "\n")
		}
		b.WriteString("  esc cancels (the partial index is rolled back)\n")
		return b.String()
	}

	fmt.Fprintf(&b, "Press Enter to scan %d root(s). Press g to grant consent for the selected root.\n", len(m.includedRoots()))
	return b.String()
}
