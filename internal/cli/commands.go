package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
	"github.com/crisacm/mac-cache-cleaner/internal/cache"
	"github.com/crisacm/mac-cache-cleaner/internal/config"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/large"
	"github.com/crisacm/mac-cache-cleaner/internal/orphan"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
	"github.com/crisacm/mac-cache-cleaner/internal/scan"
	"github.com/crisacm/mac-cache-cleaner/internal/scanner"
	"github.com/crisacm/mac-cache-cleaner/internal/store"
)

// App is the CLI application. It holds the wired dependencies and the I/O
// streams; commands contain no business logic beyond parsing and rendering.
type App struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	IsTTY  bool
	Home   string
	Cfg    config.Config
	Policy *policy.Policy
	Store  *store.Store
}

// New builds an App.
func New(home string, cfg config.Config, p *policy.Policy, st *store.Store, in io.Reader, out, errOut io.Writer, isTTY bool) *App {
	return &App{In: in, Out: out, Err: errOut, IsTTY: isTTY, Home: home, Cfg: cfg, Policy: p, Store: st}
}

// Execute runs the CLI and returns a process exit code.
func (a *App) Execute(ctx context.Context, args []string) int {
	if err := policy.EnsureNotRoot(); err != nil {
		fmt.Fprintln(a.Err, "mcc:", render.Sanitize(err.Error()))
		return exitCode(err)
	}
	root := a.rootCmd()
	root.SetArgs(args)
	root.SetIn(a.In)
	root.SetOut(a.Out)
	root.SetErr(a.Err)
	root.SilenceErrors = true
	root.SilenceUsage = true

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(a.Err, "mcc:", render.Sanitize(err.Error()))
		return exitCode(err)
	}
	return domain.ExitOK
}

func exitCode(err error) int {
	switch {
	case err == nil:
		return domain.ExitOK
	case errors.Is(err, domain.ErrCanceled), errors.Is(err, context.Canceled):
		return domain.ExitCanceled
	case errors.Is(err, domain.ErrUsage):
		return domain.ExitUsage
	case errors.Is(err, domain.ErrBlocked):
		return domain.ExitBlocked
	case errors.Is(err, domain.ErrPartial):
		return domain.ExitPartial
	default:
		return domain.ExitError
	}
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "mcc",
		Short:         "MCC — Mac Cache Cleaner",
		Long:          "MCC indexes disk usage (apps, caches, orphans, large items) and cleans it safely.",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(
		a.newScanCmd(),
		a.newAppsCmd(),
		a.newCacheCmd(),
		a.newOrphansCmd(),
		a.newLargeCmd(),
		a.newCleanCmd(),
		a.newReportCmd(),
		a.newPermissionsCmd(),
		a.newDataCmd(),
		a.newStatusCmd(),
		a.newTUICmd(),
		a.newMCPCmd(),
	)
	return root
}

func addReadFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("json", false, "output JSON")
	cmd.Flags().Bool("no-color", false, "disable colour output")
}

func (a *App) latestScanID(ctx context.Context) (string, error) {
	id, ok, err := a.Store.LatestScanID(ctx)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return id, nil
}

// --- scan --------------------------------------------------------------

func (a *App) newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Index disk usage (incremental by default)",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runScan(ctxOf(cmd), cmd) },
	}
	cmd.Flags().StringArray("root", nil, "scan root (repeatable)")
	cmd.Flags().Bool("full", false, "ignore incremental reuse")
	cmd.Flags().Bool("explain", false, "show which roots are scanned or skipped and exit")
	cmd.Flags().Bool("quiet", false, "do not print scan progress")
	cmd.Flags().Duration("timeout", 0, "cancel (and roll back) the scan after this long (0 = no limit)")
	addReadFlags(cmd)
	return cmd
}

func ctxOf(cmd *cobra.Command) context.Context { return cmd.Context() }

type scanOutput struct {
	ScanID       string                             `json:"scan_id"`
	Roots        []string                           `json:"roots"`
	Entries      int64                              `json:"entries"`
	Allocated    int64                              `json:"allocated_bytes"`
	Logical      int64                              `json:"logical_bytes"`
	ByClass      map[domain.DataClass]domain.Totals `json:"by_class"`
	Inaccessible []domain.Inaccessible              `json:"inaccessible,omitempty"`
	Partial      bool                               `json:"partial"`
	Pruned       int64                              `json:"pruned,omitempty"`
}

func (a *App) runScan(ctx context.Context, cmd *cobra.Command) error {
	roots, _ := cmd.Flags().GetStringArray("root")
	full, _ := cmd.Flags().GetBool("full")
	explain, _ := cmd.Flags().GetBool("explain")
	asJSON, _ := cmd.Flags().GetBool("json")
	quiet, _ := cmd.Flags().GetBool("quiet")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	if explain {
		if len(roots) == 0 {
			roots = a.Policy.AllowlistRoots()
		}
		return a.explainRoots(ctx, roots, asJSON)
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	svc := scan.New(a.Store, a.Policy)
	opts := scan.Options{Roots: roots, Full: full, Workers: a.Cfg.Workers, RetainScans: a.Cfg.RetainScans}
	if !quiet {
		fmt.Fprintln(a.Err, "Escaneando… (progreso en stderr; nada se modifica hasta terminar)")
		// Report often, but print at most ~twice a second so the display stays
		// alive even when a single directory holds tens of thousands of files.
		opts.ProgressEvery = 64
		last := time.Time{}
		opts.Progress = func(p scanner.Progress) {
			now := time.Now()
			if !last.IsZero() && now.Sub(last) < 500*time.Millisecond {
				return
			}
			last = now
			fmt.Fprintf(a.Err, "  %d dirs · %d entradas · %s · %s\n",
				p.Directories, p.Entries, render.FormatBytes(p.AllocatedBytes),
				render.Redact(p.Current, a.Home, false))
		}
	}
	out, err := svc.Run(ctx, opts)
	if err != nil {
		// Ctrl+C: the scanner stopped and scan.Run rolled the partial index back.
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("%w: scan cancelado; el índice parcial se revirtió", domain.ErrCanceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("scan cancelado por --timeout %s; el índice parcial se revirtió", timeout)
		}
		return err
	}

	res := out.Result
	output := scanOutput{
		ScanID: out.Scan.ID, Roots: out.Scan.Roots,
		Entries: res.Totals.Entries, Allocated: res.Totals.AllocatedBytes,
		Logical: res.Totals.LogicalBytes, ByClass: res.ByClass,
		Inaccessible: res.Inaccessible, Partial: res.Partial, Pruned: out.Pruned,
	}
	if asJSON {
		if err := writeJSON(a.Out, output); err != nil {
			return err
		}
	} else {
		a.renderScan(res, out.Scan.Roots)
		if out.Pruned > 0 {
			fmt.Fprintf(a.Out, "Índice: %d scan(s) antiguo(s) eliminado(s) (retención: %d)\n", out.Pruned, a.Cfg.RetainScans)
		}
	}
	if res.Partial {
		return domain.ErrPartial
	}
	return nil
}

func (a *App) renderScan(res scanner.Result, roots []string) {
	fmt.Fprintf(a.Out, "Scanned %d entries in %d root(s)\n", res.Totals.Entries, len(roots))
	fmt.Fprintf(a.Out, "On disk: %s  (logical: %s)\n", render.FormatBytes(res.Totals.AllocatedBytes), render.FormatBytes(res.Totals.LogicalBytes))
	if len(res.ByClass) > 0 {
		fmt.Fprintln(a.Out, "By data class:")
		classes := make([]domain.DataClass, 0, len(res.ByClass))
		for c := range res.ByClass {
			classes = append(classes, c)
		}
		sort.Slice(classes, func(i, j int) bool {
			return res.ByClass[classes[i]].AllocatedBytes > res.ByClass[classes[j]].AllocatedBytes
		})
		for _, c := range classes {
			t := res.ByClass[c]
			fmt.Fprintf(a.Out, "  %-20s %10s  (%d)\n", c, render.FormatBytes(t.AllocatedBytes), t.Entries)
		}
	}
	if len(res.Inaccessible) > 0 {
		fmt.Fprintf(a.Out, "%d path(s) were inaccessible (partial result)\n", len(res.Inaccessible))
	}
}

func (a *App) explainRoots(ctx context.Context, roots []string, asJSON bool) error {
	type explanation struct {
		Root     string `json:"root"`
		Tier     string `json:"tier"`
		Decision string `json:"decision"`
		Reason   string `json:"reason,omitempty"`
	}
	var out []explanation
	for _, r := range roots {
		v, err := a.Policy.RootVerdict(ctx, r)
		if err != nil {
			return err
		}
		out = append(out, explanation{Root: render.Redact(v.Path, a.Home, false), Tier: v.Tier.String(), Decision: string(v.Decision), Reason: v.Reason})
	}
	if asJSON {
		return writeJSON(a.Out, out)
	}
	t := &table{header: []string{"ROOT", "TIER", "DECISION", "REASON"}}
	for _, e := range out {
		t.add(e.Root, e.Tier, e.Decision, render.Sanitize(e.Reason))
	}
	t.render(a.Out)
	return nil
}

// --- apps --------------------------------------------------------------

func (a *App) newAppsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apps",
		Short: "List applications and their disk usage",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runApps(cmd.Context(), cmd) },
	}
	cmd.Flags().String("sort", "size", "sort by size or name")
	addReadFlags(cmd)
	return cmd
}

func (a *App) runApps(ctx context.Context, cmd *cobra.Command) error {
	sortBy, _ := cmd.Flags().GetString("sort")
	asJSON, _ := cmd.Flags().GetBool("json")

	scanID, err := a.latestScanID(ctx)
	if err != nil {
		return err
	}
	list, err := apps.New(a.Store, a.Home).List(ctx, scanID)
	if err != nil {
		return err
	}
	if sortBy == "name" {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	if asJSON {
		return writeJSON(a.Out, list)
	}
	t := &table{header: []string{"NAME", "BUNDLE ID", "SIZE", "DATA", "PATH"}}
	for _, app := range list {
		t.add(render.Sanitize(app.Name), render.Sanitize(app.BundleID), render.FormatBytes(app.AllocatedBytes), render.FormatBytes(app.DataBytes), render.Redact(app.Path, a.Home, false))
	}
	t.render(a.Out)
	return nil
}

// --- cache -------------------------------------------------------------

func (a *App) newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "List cache locations and their sizes",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runCache(cmd.Context(), cmd) },
	}
	cmd.Flags().String("app", "", "filter by bundle ID, name or path")
	addReadFlags(cmd)
	return cmd
}

func (a *App) runCache(ctx context.Context, cmd *cobra.Command) error {
	filter, _ := cmd.Flags().GetString("app")
	asJSON, _ := cmd.Flags().GetBool("json")

	scanID, err := a.latestScanID(ctx)
	if err != nil {
		return err
	}
	list, err := cache.New(a.Store, a.Home).List(ctx, scanID, filter)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.Out, list)
	}
	t := &table{header: []string{"NAME", "CLASS", "SIZE", "PATH"}}
	for _, e := range list {
		t.add(render.Sanitize(e.Name), e.DataClass.String(), render.FormatBytes(e.AllocatedBytes), render.Redact(e.Path, a.Home, false))
	}
	t.render(a.Out)
	return nil
}

// --- orphans -----------------------------------------------------------

func (a *App) newOrphansCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "orphans",
		Short: "List leftover data from uninstalled applications",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runOrphans(cmd.Context(), cmd) },
	}
	cmd.Flags().String("min-confidence", "", "minimum confidence: high, medium or low")
	addReadFlags(cmd)
	return cmd
}

func (a *App) runOrphans(ctx context.Context, cmd *cobra.Command) error {
	minConf, _ := cmd.Flags().GetString("min-confidence")
	asJSON, _ := cmd.Flags().GetBool("json")

	confidence := domain.Confidence(minConf)
	switch confidence {
	case "", domain.ConfidenceHigh, domain.ConfidenceMedium, domain.ConfidenceLow:
	default:
		return fmt.Errorf("%w: invalid confidence %q", domain.ErrUsage, minConf)
	}

	scanID, err := a.latestScanID(ctx)
	if err != nil {
		return err
	}
	installed, complete := a.installedBundleIDs(ctx)
	svc := orphan.New(a.Store, a.Policy, a.Home)
	list, err := svc.List(ctx, orphan.Options{
		ScanID: scanID, Installed: installed, DiscoveryComplete: complete,
		MinConfidence: confidence,
	})
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.Out, list)
	}
	if !complete {
		fmt.Fprintln(a.Out, "note: application discovery was incomplete; confidence levels were degraded")
	}
	t := &table{header: []string{"CONFIDENCE", "BUNDLE ID", "SIZE", "PATH"}}
	for _, o := range list {
		t.add(string(o.Confidence), render.Sanitize(o.BundleID), render.FormatBytes(o.AllocatedBytes), render.Redact(o.Path, a.Home, false))
	}
	t.render(a.Out)
	return nil
}

func (a *App) installedBundleIDs(ctx context.Context) (map[string]struct{}, bool) {
	return apps.New(a.Store, a.Home).InstalledBundleIDs(ctx)
}

// --- large -------------------------------------------------------------

func (a *App) newLargeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "large",
		Short: "List heavy files and directories",
		RunE:  func(cmd *cobra.Command, _ []string) error { return a.runLarge(cmd.Context(), cmd) },
	}
	cmd.Flags().String("min", "", "minimum size (e.g. 1GB); default 1GB")
	cmd.Flags().String("path", "", "restrict to a subtree")
	addReadFlags(cmd)
	return cmd
}

func (a *App) runLarge(ctx context.Context, cmd *cobra.Command) error {
	minStr, _ := cmd.Flags().GetString("min")
	path, _ := cmd.Flags().GetString("path")
	asJSON, _ := cmd.Flags().GetBool("json")

	minBytes := a.Cfg.LargeThreshold
	if minStr != "" {
		parsed, err := ParseSize(minStr)
		if err != nil {
			return err
		}
		minBytes = parsed
	}
	scanID, err := a.latestScanID(ctx)
	if err != nil {
		return err
	}
	svc := large.New(a.Store, a.Cfg.LargeThreshold)
	list, err := svc.List(ctx, scanID, large.Options{Min: minBytes, PathPrefix: path})
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.Out, list)
	}
	t := &table{header: []string{"SIZE", "KIND", "CLASS", "PATH"}}
	for _, it := range list {
		t.add(render.FormatBytes(it.AllocatedBytes), string(it.Kind), it.DataClass.String(), render.Redact(it.Path, a.Home, false))
	}
	t.render(a.Out)
	return nil
}

// ParseSize parses a human size such as "1GB", "512MB", "2048" into bytes.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	if s == "" {
		return 0, fmt.Errorf("%w: empty size", domain.ErrUsage)
	}
	mult := int64(1)
	for _, suffix := range []struct {
		s string
		m int64
	}{
		{"TIB", 1 << 40},
		{"TB", 1 << 40},
		{"TI", 1 << 40},
		{"T", 1 << 40},
		{"GIB", 1 << 30},
		{"GB", 1 << 30},
		{"GI", 1 << 30},
		{"G", 1 << 30},
		{"MIB", 1 << 20},
		{"MB", 1 << 20},
		{"MI", 1 << 20},
		{"M", 1 << 20},
		{"KIB", 1 << 10},
		{"KB", 1 << 10},
		{"KI", 1 << 10},
		{"K", 1 << 10},
		{"B", 1},
	} {
		if strings.HasSuffix(s, suffix.s) {
			mult = suffix.m
			s = strings.TrimSpace(strings.TrimSuffix(s, suffix.s))
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid size", domain.ErrUsage)
	}
	return int64(f * float64(mult)), nil
}
