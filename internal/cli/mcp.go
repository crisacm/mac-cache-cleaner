package cli

import (
	"github.com/spf13/cobra"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/mcp"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
)

func (a *App) newMCPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the MCP server (stdio) for AI agents",
		Long: "mcp serves MCC's index over the Model Context Protocol on stdio.\n" +
			"It exposes read-only tools plus a plan tool for agents. Nothing is ever\n" +
			"deleted: applying a plan happens outside MCP (mcc clean --apply) and only\n" +
			"moves items to the Trash.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			srv := mcp.New(mcp.Deps{
				Home:    a.Home,
				Config:  a.Cfg,
				Policy:  a.Policy,
				Store:   a.Store,
				Scan:    scan.New(a.Store, a.Policy),
				Cleaner: cleaner.New(a.Store, a.Policy, a.Home),
			})
			return srv.Serve(cmd.Context(), a.In, a.Out)
		},
	}
}
