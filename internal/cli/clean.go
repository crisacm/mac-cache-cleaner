package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/crisacm/mac-cache-cleaner/internal/cache"
	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

func (a *App) newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Plan or apply cleanup (dry-run by default)",
		Long: "clean builds a plan (--plan) or applies one (--apply). Planning changes\n" +
			"nothing on disk. Applying moves items to the Trash after showing the item\n" +
			"count and total size and asking for explicit confirmation.",
		RunE: func(cmd *cobra.Command, _ []string) error { return a.runClean(cmd.Context(), cmd) },
	}
	cmd.Flags().Bool("plan", false, "create a plan (dry-run)")
	cmd.Flags().Bool("apply", false, "apply an existing plan")
	cmd.Flags().String("plan-id", "", "plan to apply")
	cmd.Flags().StringArray("entry", nil, "explicit entry ID to include (required for app_data)")
	addReadFlags(cmd)
	return cmd
}

func (a *App) runClean(ctx context.Context, cmd *cobra.Command) error {
	plan, _ := cmd.Flags().GetBool("plan")
	apply, _ := cmd.Flags().GetBool("apply")
	switch {
	case plan && apply:
		return fmt.Errorf("%w: use either --plan or --apply", domain.ErrUsage)
	case !plan && !apply:
		return fmt.Errorf("%w: choose --plan or --apply", domain.ErrUsage)
	}
	if apply {
		return a.applyClean(ctx, cmd)
	}
	return a.planClean(ctx, cmd)
}

func (a *App) planClean(ctx context.Context, cmd *cobra.Command) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	entries, _ := cmd.Flags().GetStringArray("entry")

	scanID, err := a.latestScanID(ctx)
	if err != nil {
		return err
	}
	if scanID == "" {
		return fmt.Errorf("%w: run `mcc scan` before planning", domain.ErrUsage)
	}

	candidates, err := a.planCandidates(ctx, scanID, entries)
	if err != nil {
		return err
	}

	svc := cleaner.New(a.Store, a.Policy, a.Home)
	res, err := svc.Plan(ctx, cleaner.PlanOptions{
		ScanID:     scanID,
		Candidates: candidates,
		TTL:        a.Cfg.PlanTTL,
		MaxItems:   a.Cfg.MaxItems,
		MaxBytes:   a.Cfg.MaxBytes,
	})
	if err != nil {
		return err
	}

	if asJSON {
		return writeJSON(a.Out, res)
	}
	if len(res.Plan.Items) == 0 {
		fmt.Fprintln(a.Out, "nothing to clean: no eligible items")
	} else {
		fmt.Fprintf(a.Out, "Plan %s: %d item(s), %s, expires %s\n",
			res.Plan.PlanID, len(res.Plan.Items), render.FormatBytes(res.Plan.TotalSize),
			res.Plan.ExpiresAt.Format(time.RFC3339))
		fmt.Fprintln(a.Out, "Nothing has been deleted. Apply with: mcc clean --apply --plan-id "+res.Plan.PlanID)
	}
	if len(res.Skipped) > 0 {
		fmt.Fprintf(a.Out, "%d item(s) skipped:\n", len(res.Skipped))
		t := &table{header: []string{"PATH", "REASON"}}
		for _, s := range res.Skipped {
			t.add(render.Redact(s.Path, a.Home, false), render.Sanitize(s.Reason))
		}
		t.render(a.Out)
	}
	return nil
}

// planCandidates builds the plan input: automatic regenerable caches from the
// registry plus explicit per-item selections.
//
// For a regenerable location that is itself an allowlisted root (e.g.
// ~/Library/Caches), the roots themselves are never planned (SEC-DEL-05);
// instead its direct children — the real per-app caches — are offered.
func (a *App) planCandidates(ctx context.Context, scanID string, explicitIDs []string) ([]cleaner.Candidate, error) {
	var out []cleaner.Candidate
	seen := map[string]struct{}{}
	add := func(e domain.Entry) {
		if _, ok := seen[e.EntryID]; ok {
			return
		}
		seen[e.EntryID] = struct{}{}
		out = append(out, cleaner.Candidate{Entry: e})
	}

	roots := map[string]struct{}{}
	for _, r := range a.Policy.AllowlistRoots() {
		roots[r] = struct{}{}
	}

	locations, err := cache.New(a.Store, a.Home).List(ctx, scanID, "")
	if err != nil {
		return nil, err
	}
	for _, loc := range locations {
		if loc.DataClass != domain.ClassRegenerableCache {
			continue
		}
		if _, isRoot := roots[loc.Path]; isRoot {
			children, err := a.Store.EntriesByClassAndParent(ctx, scanID, loc.Path, domain.ClassRegenerableCache)
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				add(child)
			}
			continue
		}
		entry, err := a.Store.EntryByID(ctx, loc.EntryID)
		if err != nil {
			continue
		}
		add(entry)
	}

	for _, id := range explicitIDs {
		entry, err := a.Store.EntryByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("entry %s: %w", id, err)
		}
		if _, ok := seen[entry.EntryID]; ok {
			continue
		}
		seen[entry.EntryID] = struct{}{}
		out = append(out, cleaner.Candidate{Entry: entry, Explicit: true})
	}
	return out, nil
}

func (a *App) applyClean(ctx context.Context, cmd *cobra.Command) error {
	planID, _ := cmd.Flags().GetString("plan-id")
	asJSON, _ := cmd.Flags().GetBool("json")
	if planID == "" {
		return fmt.Errorf("%w: --plan-id is required with --apply", domain.ErrUsage)
	}

	svc := cleaner.New(a.Store, a.Policy, a.Home)
	opts := cleaner.ApplyOptions{
		PlanID:  planID,
		Confirm: a.confirm("Move to Trash", "yes"),
	}

	res, err := svc.Apply(ctx, opts)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(a.Out, res)
	}
	fmt.Fprintf(a.Out, "Applied %d, skipped %d, failed %d\n", len(res.Applied), len(res.Skipped), len(res.Failed))
	for _, o := range res.Skipped {
		fmt.Fprintf(a.Out, "  skipped %s (%s)\n", render.Redact(o.Path, a.Home, false), render.Sanitize(o.Reason))
	}
	for _, o := range res.Failed {
		fmt.Fprintf(a.Out, "  failed %s (%s)\n", render.Redact(o.Path, a.Home, false), render.Sanitize(o.Reason))
	}
	return nil
}

// confirm returns a confirmation callback that requires the exact word.
func (a *App) confirm(prompt, word string) func(int, int64) (bool, error) {
	return func(count int, total int64) (bool, error) {
		fmt.Fprintf(a.Out, "%s: %d item(s), %s. Type %q to confirm: ", prompt, count, render.FormatBytes(total), word)
		reader := bufio.NewReader(a.In)
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}
		return strings.EqualFold(strings.TrimSpace(line), word), nil
	}
}
