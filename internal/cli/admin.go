package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
	"github.com/crisacm/mac-cache-cleaner/internal/cache"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/large"
	"github.com/crisacm/mac-cache-cleaner/internal/orphan"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

// --- report ------------------------------------------------------------

type reportData struct {
	ScanID            string                             `json:"scan_id"`
	Totals            domain.Totals                      `json:"totals"`
	ByClass           map[domain.DataClass]domain.Totals `json:"by_class,omitempty"`
	Apps              []domain.App                       `json:"apps,omitempty"`
	Caches            []domain.CacheEntry                `json:"caches,omitempty"`
	Large             []domain.LargeItem                 `json:"large,omitempty"`
	Orphans           []domain.Orphan                    `json:"orphans,omitempty"`
	InstalledComplete bool                               `json:"installed_complete"`
}

func (a *App) newReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Consolidated disk-usage report",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runReport(cmd.Context(), cmd) },
	}
	cmd.Flags().Bool("json", false, "output JSON")
	cmd.Flags().Bool("md", false, "output Markdown")
	cmd.Flags().Bool("redact", false, "replace home with ~ and mask file names")
	cmd.Flags().Bool("no-color", false, "disable colour output")
	return cmd
}

func (a *App) runReport(ctx context.Context, cmd *cobra.Command) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	asMD, _ := cmd.Flags().GetBool("md")
	redact, _ := cmd.Flags().GetBool("redact")

	scanID, err := a.latestScanID(ctx)
	if err != nil {
		return err
	}
	if scanID == "" {
		fmt.Fprintln(a.Out, "No scan yet. Run `mcc scan` first.")
		return nil
	}

	data := reportData{ScanID: scanID}
	if data.Totals, err = a.Store.Totals(ctx, scanID); err != nil {
		return err
	}
	if data.ByClass, err = a.Store.TotalsByClass(ctx, scanID); err != nil {
		return err
	}
	if data.Apps, err = apps.New(a.Store, a.Home).List(ctx, scanID); err != nil {
		return err
	}
	if data.Caches, err = cache.New(a.Store, a.Home).List(ctx, scanID, ""); err != nil {
		return err
	}
	if data.Large, err = large.New(a.Store, a.Cfg.LargeThreshold).List(ctx, scanID, large.Options{Limit: 20}); err != nil {
		return err
	}
	installed, complete := a.installedBundleIDs(ctx)
	data.InstalledComplete = complete
	if data.Orphans, err = orphan.New(a.Store, a.Policy, a.Home).List(ctx, orphan.Options{
		ScanID: scanID, Installed: installed, DiscoveryComplete: complete,
	}); err != nil {
		return err
	}

	// Limit the lists shown in a report.
	data.Apps = head(data.Apps, 20)
	data.Caches = head(data.Caches, 20)

	if asJSON {
		return writeJSON(a.Out, data)
	}
	if asMD {
		a.renderReportMD(data, redact)
		return nil
	}
	a.renderReportHuman(data, redact)
	return nil
}

func (a *App) renderReportHuman(d reportData, redact bool) {
	path := func(p string) string { return render.Redact(p, a.Home, redact) }
	fmt.Fprintf(a.Out, "Report for scan %s\n\n", d.ScanID)
	fmt.Fprintf(a.Out, "Total: %s on disk (%s logical) across %d entries\n\n",
		render.FormatBytes(d.Totals.AllocatedBytes), render.FormatBytes(d.Totals.LogicalBytes), d.Totals.Entries)

	fmt.Fprintln(a.Out, "By data class:")
	for _, c := range domain.DataClasses {
		if t, ok := d.ByClass[c]; ok {
			fmt.Fprintf(a.Out, "  %-20s %10s\n", c, render.FormatBytes(t.AllocatedBytes))
		}
	}

	section := func(title string, t *table) {
		fmt.Fprintf(a.Out, "\n%s:\n", title)
		t.render(a.Out)
	}
	at := &table{header: []string{"NAME", "BUNDLE ID", "SIZE"}}
	for _, app := range d.Apps {
		at.add(render.Sanitize(app.Name), render.Sanitize(app.BundleID), render.FormatBytes(app.AllocatedBytes))
	}
	section("Applications", at)

	ct := &table{header: []string{"NAME", "CLASS", "SIZE", "PATH"}}
	for _, e := range d.Caches {
		ct.add(render.Sanitize(e.Name), e.DataClass.String(), render.FormatBytes(e.AllocatedBytes), path(e.Path))
	}
	section("Caches", ct)

	lt := &table{header: []string{"SIZE", "PATH"}}
	for _, it := range d.Large {
		lt.add(render.FormatBytes(it.AllocatedBytes), path(it.Path))
	}
	section("Large items", lt)

	ot := &table{header: []string{"CONFIDENCE", "BUNDLE ID", "SIZE", "PATH"}}
	for _, o := range d.Orphans {
		ot.add(string(o.Confidence), render.Sanitize(o.BundleID), render.FormatBytes(o.AllocatedBytes), path(o.Path))
	}
	section("Orphans", ot)
	if !d.InstalledComplete {
		fmt.Fprintln(a.Out, "\nnote: application discovery was incomplete; orphan confidence was degraded")
	}
}

func (a *App) renderReportMD(d reportData, redact bool) {
	path := func(p string) string { return render.Redact(p, a.Home, redact) }
	fmt.Fprintf(a.Out, "# MCC report — scan %s\n\n", d.ScanID)
	fmt.Fprintf(a.Out, "**Total:** %s on disk (%s logical), %d entries\n\n",
		render.FormatBytes(d.Totals.AllocatedBytes), render.FormatBytes(d.Totals.LogicalBytes), d.Totals.Entries)

	mdTable := func(headers []string, rows [][]string) {
		fmt.Fprintf(a.Out, "| %s |\n", strings.Join(headers, " | "))
		sep := make([]string, len(headers))
		for i := range sep {
			sep[i] = "---"
		}
		fmt.Fprintf(a.Out, "| %s |\n", strings.Join(sep, " | "))
		for _, r := range rows {
			cells := make([]string, len(r))
			for i, c := range r {
				cells[i] = strings.ReplaceAll(render.Sanitize(c), "|", "\\|")
			}
			fmt.Fprintf(a.Out, "| %s |\n", strings.Join(cells, " | "))
		}
		fmt.Fprintln(a.Out)
	}

	fmt.Fprint(a.Out, "## By data class\n\n")
	var classRows [][]string
	for _, c := range domain.DataClasses {
		if t, ok := d.ByClass[c]; ok {
			classRows = append(classRows, []string{string(c), render.FormatBytes(t.AllocatedBytes)})
		}
	}
	mdTable([]string{"Class", "Size"}, classRows)

	fmt.Fprint(a.Out, "## Applications\n\n")
	var appRows [][]string
	for _, app := range d.Apps {
		appRows = append(appRows, []string{render.Sanitize(app.Name), render.Sanitize(app.BundleID), render.FormatBytes(app.AllocatedBytes)})
	}
	mdTable([]string{"Name", "Bundle ID", "Size"}, appRows)

	fmt.Fprint(a.Out, "## Large items\n\n")
	var largeRows [][]string
	for _, it := range d.Large {
		largeRows = append(largeRows, []string{render.FormatBytes(it.AllocatedBytes), path(it.Path)})
	}
	mdTable([]string{"Size", "Path"}, largeRows)
}

// --- permissions -------------------------------------------------------

func (a *App) newPermissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions [revoke|grant <root>]",
		Short: "Show the effective policy and consents, or change one",
		RunE:  func(cmd *cobra.Command, args []string) error { return a.runPermissions(cmd.Context(), cmd, args) },
	}
	addReadFlags(cmd)
	return cmd
}

func (a *App) runPermissions(ctx context.Context, cmd *cobra.Command, args []string) error {
	switch {
	case len(args) == 0:
		return a.showPermissions(ctx, cmd)
	case args[0] == "revoke" && len(args) == 2:
		return a.revokeConsent(ctx, args[1])
	case args[0] == "grant" && len(args) == 2:
		return a.grantConsent(ctx, args[1])
	default:
		return fmt.Errorf("%w: usage: mcc permissions [revoke|grant <root>]", domain.ErrUsage)
	}
}

func (a *App) showPermissions(ctx context.Context, cmd *cobra.Command) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	rules := a.Policy.Rules()
	consents, err := a.Store.Consents(ctx)
	if err != nil {
		return err
	}

	type ruleOut struct {
		Prefix string `json:"prefix"`
		Tier   string `json:"tier"`
		Note   string `json:"note,omitempty"`
	}
	type consentOut struct {
		Root      string `json:"root"`
		Tier      string `json:"tier"`
		GrantedAt string `json:"granted_at"`
		RevokedAt string `json:"revoked_at,omitempty"`
		Active    bool   `json:"active"`
	}
	out := struct {
		DataDir  string       `json:"data_dir"`
		Rules    []ruleOut    `json:"rules"`
		Consents []consentOut `json:"consents"`
	}{DataDir: render.Redact(a.Cfg.DataDir, a.Home, false)}

	for _, r := range rules {
		out.Rules = append(out.Rules, ruleOut{Prefix: render.Redact(r.Prefix, a.Home, false), Tier: r.Tier.String(), Note: r.Note})
	}
	now := time.Now()
	for _, c := range consents {
		co := consentOut{Root: render.Redact(c.Root, a.Home, false), Tier: c.Tier.String(), GrantedAt: c.GrantedAt.Format(time.RFC3339), Active: c.Active(now)}
		if !c.RevokedAt.IsZero() {
			co.RevokedAt = c.RevokedAt.Format(time.RFC3339)
		}
		out.Consents = append(out.Consents, co)
	}

	if asJSON {
		return writeJSON(a.Out, out)
	}
	fmt.Fprintf(a.Out, "Data directory: %s\n\nTier table (compiled in; config cannot loosen it):\n", out.DataDir)
	rt := &table{header: []string{"TIER", "PREFIX", "NOTE"}}
	for _, r := range out.Rules {
		rt.add(r.Tier, r.Prefix, render.Sanitize(r.Note))
	}
	rt.render(a.Out)

	fmt.Fprintln(a.Out, "\nConsents:")
	if len(out.Consents) == 0 {
		fmt.Fprintln(a.Out, "  (none)")
		return nil
	}
	ct := &table{header: []string{"ROOT", "TIER", "GRANTED", "ACTIVE"}}
	for _, c := range out.Consents {
		ct.add(c.Root, c.Tier, c.GrantedAt, fmt.Sprintf("%t", c.Active))
	}
	ct.render(a.Out)
	return nil
}

func (a *App) revokeConsent(ctx context.Context, root string) error {
	resolved, err := a.resolveRoot(root)
	if err != nil {
		return err
	}
	if err := a.Store.RevokeConsent(ctx, resolved, time.Now()); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Revoked consent for %s\n", render.Redact(resolved, a.Home, false))
	return nil
}

func (a *App) grantConsent(ctx context.Context, root string) error {
	if !a.IsTTY {
		return fmt.Errorf("%w: consent can only be granted interactively on a TTY (SEC-ACC-07)", domain.ErrBlocked)
	}
	resolved, err := a.resolveRoot(root)
	if err != nil {
		return err
	}
	verdict, err := a.Policy.RootVerdict(ctx, resolved)
	if err != nil {
		return err
	}
	if verdict.Tier == domain.TierSensitive {
		return fmt.Errorf("%w: tier 2 can never be accessed", domain.ErrBlocked)
	}
	if verdict.Tier == domain.TierDefault {
		fmt.Fprintf(a.Out, "%s is a default root; no consent needed.\n", render.Redact(resolved, a.Home, false))
		return nil
	}

	fmt.Fprintf(a.Out, "Granting access to %s (tier %s). MCC reads metadata only and never includes Tier 1 data in automated plans.\n",
		render.Redact(resolved, a.Home, false), verdict.Tier)
	confirm := a.confirm("Grant consent for "+render.Redact(resolved, a.Home, false), "grant")
	ok, err := confirm(1, 0)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: consent not granted", domain.ErrBlocked)
	}
	if err := a.Store.GrantConsent(ctx, domain.Consent{Root: resolved, Tier: verdict.Tier, GrantedAt: time.Now()}); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Consent recorded.")
	return nil
}

func (a *App) resolveRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		root = filepath.Join(a.Home, root)
	}
	return filepath.Clean(root), nil
}

// --- data --------------------------------------------------------------

func (a *App) newDataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data show | purge",
		Short: "Show or erase what MCC stores",
		RunE:  func(cmd *cobra.Command, args []string) error { return a.runData(cmd.Context(), cmd, args) },
	}
	addReadFlags(cmd)
	return cmd
}

func (a *App) runData(ctx context.Context, cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: usage: mcc data show | purge", domain.ErrUsage)
	}
	switch args[0] {
	case "show":
		return a.dataShow(ctx, cmd)
	case "purge":
		return a.dataPurge(ctx, cmd)
	default:
		return fmt.Errorf("%w: usage: mcc data show | purge", domain.ErrUsage)
	}
}

func (a *App) dataShow(ctx context.Context, cmd *cobra.Command) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	info, err := a.Store.Info(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		info.Dir = render.Redact(info.Dir, a.Home, false)
		info.Path = render.Redact(info.Path, a.Home, false)
		return writeJSON(a.Out, info)
	}
	fmt.Fprintf(a.Out, "Storage directory: %s\n", render.Redact(info.Dir, a.Home, false))
	fmt.Fprintf(a.Out, "Database:          %s\n", render.Redact(info.Path, a.Home, false))
	fmt.Fprintf(a.Out, "Database size:     %s\n", render.FormatBytes(info.SizeBytes))
	fmt.Fprintf(a.Out, "Scans: %d, entries: %d, plans: %d, consents: %d, audit records: %d\n",
		info.Scans, info.Entries, info.Plans, info.Consents, info.Audit)
	fmt.Fprintln(a.Out, "Nothing else is stored; no file contents and no telemetry.")
	return nil
}

func (a *App) dataPurge(ctx context.Context, _ *cobra.Command) error {
	confirm := a.confirm("Erase all MCC data (index, plans, consents, audit log)", "purge")
	ok, err := confirm(1, 0)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: purge cancelled", domain.ErrBlocked)
	}
	if err := a.Store.Purge(ctx); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "All MCC data erased.")
	return nil
}

// --- helpers -----------------------------------------------------------

func head[T any](s []T, n int) []T {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
