package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
	"github.com/crisacm/mac-cache-cleaner/internal/cache"
	"github.com/crisacm/mac-cache-cleaner/internal/consent"
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
	svc := consent.New(a.Store, a.Policy, a.IsTTY)
	verdict, err := svc.Verdict(ctx, root)
	if err != nil {
		return err
	}
	if err := svc.Revoke(ctx, root); err != nil {
		return err
	}
	fmt.Fprintf(a.Out, "Revoked consent for %s\n", render.Redact(verdict.Path, a.Home, false))
	return nil
}

func (a *App) grantConsent(ctx context.Context, root string) error {
	if !a.IsTTY {
		return fmt.Errorf("%w: consent can only be granted interactively on a TTY (SEC-ACC-07)", domain.ErrBlocked)
	}
	svc := consent.New(a.Store, a.Policy, a.IsTTY)
	verdict, err := svc.Verdict(ctx, root)
	if err != nil {
		return err
	}
	switch {
	case verdict.Blocked():
		return fmt.Errorf("%w: %s: %s", domain.ErrBlocked, render.Redact(verdict.Path, a.Home, false), render.Sanitize(verdict.Reason))
	case verdict.Tier == domain.TierDefault:
		fmt.Fprintf(a.Out, "%s is a default root; no consent needed.\n", render.Redact(verdict.Path, a.Home, false))
		return nil
	}

	fmt.Fprintf(a.Out, "Granting access to %s (tier %s). MCC reads metadata only and never includes Tier 1 data in automated plans.\n",
		render.Redact(verdict.Path, a.Home, false), verdict.Tier)
	ok, err := a.confirm("Grant consent for "+render.Redact(verdict.Path, a.Home, false), "grant")(1, 0)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: consent not granted", domain.ErrBlocked)
	}
	// The service re-checks that we are interactive and that the tier permits it.
	if _, err := svc.Grant(ctx, root); err != nil {
		return err
	}
	fmt.Fprintln(a.Out, "Consent recorded.")
	return nil
}

// --- data --------------------------------------------------------------

func (a *App) newDataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data show | purge | prune",
		Short: "Show, prune or erase what MCC stores",
		RunE:  func(cmd *cobra.Command, args []string) error { return a.runData(cmd.Context(), cmd, args) },
	}
	cmd.Flags().Int("keep", 0, "scans to retain when pruning (default: config retain_scans)")
	cmd.Flags().Bool("vacuum", false, "rebuild the database file to reclaim disk space")
	addReadFlags(cmd)
	return cmd
}

func (a *App) runData(ctx context.Context, cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: usage: mcc data show | purge | prune", domain.ErrUsage)
	}
	switch args[0] {
	case "show":
		return a.dataShow(ctx, cmd)
	case "prune":
		return a.dataPrune(ctx, cmd)
	case "purge":
		return a.dataPurge(ctx, cmd)
	default:
		return fmt.Errorf("%w: usage: mcc data show | purge | prune", domain.ErrUsage)
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
	fmt.Fprintf(a.Out, "Scans: %d, entries: %d, plans: %d, consents: %d, audit records: %d, MCP calls: %d\n",
		info.Scans, info.Entries, info.Plans, info.Consents, info.Audit, info.MCPCalls)
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

// dataPrune removes old scans (keeping the newest `keep` completed ones) and,
// optionally, rebuilds the file to reclaim disk space.
func (a *App) dataPrune(ctx context.Context, cmd *cobra.Command) error {
	keep, _ := cmd.Flags().GetInt("keep")
	vacuum, _ := cmd.Flags().GetBool("vacuum")
	if keep <= 0 {
		keep = a.Cfg.RetainScans
	}
	asJSON, _ := cmd.Flags().GetBool("json")

	removed, err := a.Store.PruneScans(ctx, keep)
	if err != nil {
		return err
	}
	if vacuum {
		if err := a.Store.Vacuum(ctx); err != nil {
			return err
		}
	}
	info, err := a.Store.Info(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.Out, map[string]any{
			"removed_scans": removed, "kept": keep, "vacuumed": vacuum,
			"scans": info.Scans, "entries": info.Entries, "size_bytes": info.SizeBytes,
		})
	}
	fmt.Fprintf(a.Out, "Eliminados %d scan(s) antiguo(s); quedan %d. BD: %s\n",
		removed, info.Scans, render.FormatBytes(info.SizeBytes))
	if !vacuum && removed > 0 {
		fmt.Fprintln(a.Out, "Sugerencia: usa --vacuum para recuperar el espacio en disco.")
	}
	return nil
}

// --- status ------------------------------------------------------------

type statusOutput struct {
	HasScan           bool   `json:"has_scan"`
	ScanID            string `json:"scan_id,omitempty"`
	StartedAt         string `json:"started_at,omitempty"`
	FinishedAt        string `json:"finished_at,omitempty"`
	AgeSeconds        int64  `json:"age_seconds,omitempty"`
	Partial           bool   `json:"partial,omitempty"`
	InaccessibleCount int    `json:"inaccessible_count,omitempty"`
	Entries           int64  `json:"entries,omitempty"`
	Allocated         int64  `json:"allocated_bytes,omitempty"`
	Logical           int64  `json:"logical_bytes,omitempty"`
	RetainedScans     int64  `json:"retained_scans"`
	RetainScans       int    `json:"retain_scans"`
	Plans             int64  `json:"plans"`
	Consents          int64  `json:"consents"`
	MCPCalls          int64  `json:"mcp_calls"`
	DatabaseBytes     int64  `json:"database_bytes"`
	DataDir           string `json:"data_dir"`
}

func (a *App) newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the state of the local index (no scan, no disk access)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runStatus(cmd.Context(), cmd) },
	}
	addReadFlags(cmd)
	return cmd
}

func (a *App) runStatus(ctx context.Context, cmd *cobra.Command) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	info, err := a.Store.Info(ctx)
	if err != nil {
		return err
	}
	out := statusOutput{
		RetainedScans: info.Scans,
		RetainScans:   a.Cfg.RetainScans,
		Plans:         info.Plans,
		Consents:      info.Consents,
		MCPCalls:      info.MCPCalls,
		DatabaseBytes: info.SizeBytes,
		DataDir:       render.Redact(info.Dir, a.Home, false),
	}
	if sc, err := a.Store.LatestScan(ctx); err == nil {
		out.HasScan = true
		out.ScanID = sc.ID
		out.StartedAt = sc.StartedAt.Format(time.RFC3339)
		out.Partial = sc.Partial
		if !sc.FinishedAt.IsZero() {
			out.FinishedAt = sc.FinishedAt.Format(time.RFC3339)
			out.AgeSeconds = int64(time.Since(sc.FinishedAt).Seconds())
		}
		if totals, err := a.Store.Totals(ctx, sc.ID); err == nil {
			out.Entries, out.Allocated, out.Logical = totals.Entries, totals.AllocatedBytes, totals.LogicalBytes
		}
		if inacc, err := a.Store.Inaccessible(ctx, sc.ID); err == nil {
			out.InaccessibleCount = len(inacc)
		}
	}

	if asJSON {
		return writeJSON(a.Out, out)
	}
	if !out.HasScan {
		fmt.Fprintln(a.Out, "Sin índice. Ejecuta `mcc scan` (o pide a una persona que lo haga).")
	} else {
		fmt.Fprintf(a.Out, "Último escaneo: %s (%s, hace %s)\n", out.ScanID[:12], out.StartedAt, humanAge(out.AgeSeconds))
		fmt.Fprintf(a.Out, "Entradas: %d · %s en disco (%s lógico)\n", out.Entries, render.FormatBytes(out.Allocated), render.FormatBytes(out.Logical))
		if out.Partial || out.InaccessibleCount > 0 {
			fmt.Fprintf(a.Out, "Resultado parcial: %d ruta(s) inaccesible(s)\n", out.InaccessibleCount)
		}
	}
	fmt.Fprintf(a.Out, "Retención: %d scan(s) (retain_scans=%d) · planes %d · consentimientos %d · llamadas MCP %d\n",
		out.RetainedScans, out.RetainScans, out.Plans, out.Consents, out.MCPCalls)
	fmt.Fprintf(a.Out, "Base de datos: %s en %s\n", render.FormatBytes(out.DatabaseBytes), out.DataDir)
	return nil
}

func humanAge(seconds int64) string {
	switch {
	case seconds < 0:
		return "?"
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}

// --- helpers -----------------------------------------------------------

func head[T any](s []T, n int) []T {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
