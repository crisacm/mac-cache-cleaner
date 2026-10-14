// Package tui implements the interactive terminal interface (`mcc tui`).
//
// It contains no business logic: it calls services, holds screen state and
// renders. Every access decision is delegated to internal/policy (directly or
// through the services), and every untrusted name is rendered through
// internal/render.Sanitize (SEC-COD-02).
package tui

import (
	"context"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/consent"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

// Deps are the services the TUI renders. They are concrete services built by
// cmd/mcc (wiring); the TUI never constructs access decisions itself.
type Deps struct {
	Home    string
	Config  config.Config
	Policy  *policy.Policy
	Store   *store.Store
	Scan    *scan.Service
	Cleaner *cleaner.Service
	Consent *consent.Service

	// IsTTY must be true for consent to be grantable (SEC-ACC-07).
	IsTTY bool

	// AppRoots overrides the application directories (tests use a fixture).
	AppRoots []string
}

// appsService builds the apps service for the configured roots.
func (d Deps) appsService() *apps.Service {
	return apps.New(d.Store, d.Home, d.AppRoots...)
}

// Run starts the TUI. out must be a terminal for interactive use.
func Run(ctx context.Context, deps Deps, in io.Reader, out io.Writer) error {
	m := newModel(ctx, deps, lipgloss.NewRenderer(out))
	prog := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithInput(in),
		tea.WithOutput(out),
		tea.WithAltScreen(),
	)
	_, err := prog.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
