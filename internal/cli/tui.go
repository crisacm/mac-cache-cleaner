package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/consent"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/tui"
)

func (a *App) newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal interface",
		Long: "tui opens an interactive interface over the same services as the CLI:\n" +
			"overview, app/cache/orphan/large lists, scan (with progress and cancel),\n" +
			"the plan -> apply cleanup flow and the permissions view.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !a.IsTTY {
				return fmt.Errorf("%w: mcc tui needs an interactive terminal", domain.ErrUsage)
			}
			deps := tui.Deps{
				Home:    a.Home,
				Config:  a.Cfg,
				Policy:  a.Policy,
				Store:   a.Store,
				IsTTY:   a.IsTTY,
				Scan:    scan.New(a.Store, a.Policy),
				Cleaner: cleaner.New(a.Store, a.Policy, a.Home),
				Consent: consent.New(a.Store, a.Policy, a.IsTTY),
			}
			return tui.Run(cmd.Context(), deps, a.In, a.Out)
		},
	}
}
