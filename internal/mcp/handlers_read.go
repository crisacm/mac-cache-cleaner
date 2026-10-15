package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
	"github.com/crisacm/mac-cache-cleaner/internal/cache"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/large"
	"github.com/crisacm/mac-cache-cleaner/internal/orphan"
	"github.com/crisacm/mac-cache-cleaner/internal/policy"
	"github.com/crisacm/mac-cache-cleaner/internal/proc"
)

// unmarshalArgs strictly decodes tool arguments.
func unmarshalArgs(raw json.RawMessage, v any) *toolError {
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errTool("invalid_argument", "invalid arguments")
	}
	return nil
}

func (s *Server) latestScanID(ctx context.Context) (string, *toolError) {
	id, ok, err := s.deps.Store.LatestScanID(ctx)
	if err != nil {
		return "", errTool("internal", "internal error")
	}
	if !ok {
		return "", errTool("no_scan", "No scan found. Run a scan before using this tool.")
	}
	return id, nil
}

func (s *Server) runningSet(ctx context.Context) map[string]struct{} {
	set, err := proc.RunningBundleIDs(ctx)
	if err != nil {
		return map[string]struct{}{}
	}
	return set
}

// --- get_status --------------------------------------------------------

type scanInfo struct {
	ID                string   `json:"id"`
	StartedAt         string   `json:"started_at"`
	FinishedAt        string   `json:"finished_at,omitempty"`
	Partial           bool     `json:"partial"`
	InaccessibleCount int      `json:"inaccessible_count"`
	Roots             []string `json:"roots"`
	AgeSeconds        int64    `json:"age_seconds"`
}

type rootInfo struct {
	Path       string `json:"path"`
	Tier       string `json:"tier"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason,omitempty"`
	ReportOnly bool   `json:"report_only"`
}

type ruleInfo struct {
	Prefix string `json:"prefix"`
	Tier   string `json:"tier"`
	Note   string `json:"note,omitempty"`
}

type consentInfo struct {
	Root      string `json:"root"`
	Tier      string `json:"tier"`
	GrantedAt string `json:"granted_at"`
	RevokedAt string `json:"revoked_at,omitempty"`
	Active    bool   `json:"active"`
}

type statusResult struct {
	SchemaVersion string        `json:"schema_version"`
	UntrustedData bool          `json:"untrusted_data"`
	HasScan       bool          `json:"has_scan"`
	Scan          *scanInfo     `json:"scan,omitempty"`
	Stale         bool          `json:"stale"`
	Roots         []rootInfo    `json:"roots"`
	Policy        []ruleInfo    `json:"policy"`
	Consents      []consentInfo `json:"consents"`
	DataDir       string        `json:"data_dir"`
	Notes         []string      `json:"notes,omitempty"`
}

func handleGetStatus(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct{}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	out := statusResult{
		SchemaVersion: schemaVersion,
		UntrustedData: true,
		DataDir:       s.redact(s.deps.Store.Dir()),
		Roots:         []rootInfo{},
		Policy:        []ruleInfo{},
		Consents:      []consentInfo{},
	}

	for _, r := range s.deps.Policy.Rules() {
		out.Policy = append(out.Policy, ruleInfo{
			Prefix: s.redact(r.Prefix), Tier: r.Tier.String(), Note: r.Note,
		})
		v, err := s.deps.Policy.RootVerdict(ctx, r.Prefix)
		if err != nil {
			continue
		}
		out.Roots = append(out.Roots, rootInfo{
			Path: s.redact(v.Path), Tier: v.Tier.String(), Decision: string(v.Decision),
			Reason: clean(s.redact(v.Reason)), ReportOnly: v.ReportOnly,
		})
	}

	consents, err := s.deps.Store.Consents(ctx)
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	now := s.now()
	for _, c := range consents {
		ci := consentInfo{
			Root: s.redact(c.Root), Tier: c.Tier.String(),
			GrantedAt: mtimeString(c.GrantedAt), Active: c.Active(now),
		}
		if !c.RevokedAt.IsZero() {
			ci.RevokedAt = mtimeString(c.RevokedAt)
		}
		out.Consents = append(out.Consents, ci)
	}

	if sc, err := s.deps.Store.LatestScan(ctx); err == nil {
		out.HasScan = true
		info := &scanInfo{
			ID: sc.ID, StartedAt: mtimeString(sc.StartedAt), Partial: sc.Partial, Roots: nil,
		}
		if !sc.FinishedAt.IsZero() {
			info.FinishedAt = mtimeString(sc.FinishedAt)
			info.AgeSeconds = int64(now.Sub(sc.FinishedAt).Seconds())
			out.Stale = info.AgeSeconds > int64((24 * time.Hour).Seconds())
		}
		for _, r := range sc.Roots {
			info.Roots = append(info.Roots, s.redact(r))
		}
		if inacc, err := s.deps.Store.Inaccessible(ctx, sc.ID); err == nil {
			info.InaccessibleCount = len(inacc)
		}
		out.Scan = info
	} else {
		out.Notes = append(out.Notes, "no scan found")
	}
	return out, nil
}

// --- list_apps ---------------------------------------------------------

func handleListApps(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		PageSize  int    `json:"page_size"`
		PageToken string `json:"page_token"`
		Sort      string `json:"sort"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	p, terr := parsePage(a.PageSize, a.PageToken)
	if terr != nil {
		return nil, terr
	}
	if a.Sort != "" && a.Sort != "size" && a.Sort != "name" {
		return nil, errTool("invalid_argument", "invalid sort")
	}

	scanID, hasScan, err := s.deps.Store.LatestScanID(ctx) // "" is allowed: sizes are zero
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	list, err := apps.New(s.deps.Store, s.deps.Home).List(ctx, scanID)
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	if a.Sort == "name" {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}

	running := s.runningSet(ctx)
	items := make([]item, 0, len(list))
	for _, app := range list {
		items = append(items, s.itemFromApp(app, running))
	}
	env := newEnvelope(len(items))
	env.Items, env.NextPageToken, env.Truncated = paginate(items, p)
	if !hasScan {
		env.Notes = append(env.Notes, "no scan found: sizes are zero until a scan is run")
	}
	return env, nil
}

// --- list_caches -------------------------------------------------------

func handleListCaches(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		PageSize  int    `json:"page_size"`
		PageToken string `json:"page_token"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	p, terr := parsePage(a.PageSize, a.PageToken)
	if terr != nil {
		return nil, terr
	}
	scanID, terr := s.latestScanID(ctx)
	if terr != nil {
		return nil, terr
	}

	list, err := cache.New(s.deps.Store, s.deps.Home).List(ctx, scanID, "")
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	running := s.runningSet(ctx)
	items := make([]item, 0, len(list))
	for _, c := range list {
		items = append(items, s.itemFromCache(c, running))
	}
	env := newEnvelope(len(items))
	env.Items, env.NextPageToken, env.Truncated = paginate(items, p)
	return env, nil
}

// --- list_orphans ------------------------------------------------------

func handleListOrphans(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		MinConfidence string `json:"min_confidence"`
		PageSize      int    `json:"page_size"`
		PageToken     string `json:"page_token"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	p, terr := parsePage(a.PageSize, a.PageToken)
	if terr != nil {
		return nil, terr
	}
	switch a.MinConfidence {
	case "", "high", "medium", "low":
	default:
		return nil, errTool("invalid_argument", "invalid min_confidence")
	}
	scanID, terr := s.latestScanID(ctx)
	if terr != nil {
		return nil, terr
	}

	installed, complete := apps.New(s.deps.Store, s.deps.Home).InstalledBundleIDs(ctx)
	list, err := orphan.New(s.deps.Store, s.deps.Policy, s.deps.Home).List(ctx, orphan.Options{
		ScanID: scanID, Installed: installed, DiscoveryComplete: complete,
		MinConfidence: domain.Confidence(a.MinConfidence),
	})
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	running := s.runningSet(ctx)
	items := make([]item, 0, len(list))
	for _, o := range list {
		items = append(items, s.itemFromOrphan(o, running))
	}
	env := newEnvelope(len(items))
	env.Items, env.NextPageToken, env.Truncated = paginate(items, p)
	if !complete {
		env.Notes = append(env.Notes, "application discovery was incomplete; confidence is degraded")
	}
	return env, nil
}

// --- list_large --------------------------------------------------------

func handleListLarge(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		MinBytes   int64  `json:"min_bytes"`
		PathPrefix string `json:"path_prefix"`
		PageSize   int    `json:"page_size"`
		PageToken  string `json:"page_token"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	p, terr := parsePage(a.PageSize, a.PageToken)
	if terr != nil {
		return nil, terr
	}
	scanID, terr := s.latestScanID(ctx)
	if terr != nil {
		return nil, terr
	}
	if a.PathPrefix != "" {
		if terr := s.requireAllowedPath(ctx, a.PathPrefix); terr != nil {
			return nil, terr
		}
	}

	list, err := large.New(s.deps.Store, s.deps.Config.LargeThreshold).List(ctx, scanID, large.Options{
		Min: a.MinBytes, PathPrefix: a.PathPrefix, Limit: maxPageSize,
	})
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	running := s.runningSet(ctx)
	items := make([]item, 0, len(list))
	for _, l := range list {
		items = append(items, s.itemFromLarge(l, running))
	}
	env := newEnvelope(len(items))
	env.Items, env.NextPageToken, env.Truncated = paginate(items, p)
	return env, nil
}

// requireAllowedPath enforces SEC-MCP-03: a read tool that accepts a path only
// allows it under a root the policy currently allows.
func (s *Server) requireAllowedPath(ctx context.Context, path string) *toolError {
	norm, err := policy.NormalizePath(path)
	if err != nil {
		return errTool("invalid_argument", "invalid path")
	}
	for _, r := range s.deps.Policy.Rules() {
		if !policy.UnderRoot(r.Prefix, norm) {
			continue
		}
		v, err := s.deps.Policy.RootVerdict(ctx, r.Prefix)
		if err != nil {
			continue
		}
		if v.Decision == policy.DecisionAllow {
			return nil
		}
	}
	return errTool("blocked", "path is not under an allowed root")
}

// --- list_children -----------------------------------------------------

func handleListChildren(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		EntryID   string `json:"entry_id"`
		ShowFiles bool   `json:"show_files"`
		PageSize  int    `json:"page_size"`
		PageToken string `json:"page_token"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	p, terr := parsePage(a.PageSize, a.PageToken)
	if terr != nil {
		return nil, terr
	}
	scanID, terr := s.latestScanID(ctx)
	if terr != nil {
		return nil, terr
	}
	parent, err := s.deps.Store.EntryByID(ctx, a.EntryID)
	if err != nil {
		return nil, errTool("not_found", "entry_id not found in the current scan")
	}
	if !parent.IsDir || parent.Kind == domain.KindPackage {
		return nil, errTool("invalid_argument", "entry is not an expandable directory")
	}

	children, err := s.deps.Store.EntriesByParent(ctx, scanID, parent.Path)
	if err != nil {
		return nil, errTool("internal", "internal error")
	}
	running := s.runningSet(ctx)
	items := make([]item, 0, len(children))
	for _, c := range children {
		if !a.ShowFiles && !c.IsDir {
			continue
		}
		items = append(items, s.itemFromEntry(c, running))
	}
	env := newEnvelope(len(items))
	env.Items, env.NextPageToken, env.Truncated = paginate(items, p)
	return env, nil
}

// --- get_entry ---------------------------------------------------------

func handleGetEntry(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		EntryID string `json:"entry_id"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	if _, terr := s.latestScanID(ctx); terr != nil {
		return nil, terr
	}
	e, err := s.deps.Store.EntryByID(ctx, a.EntryID)
	if err != nil {
		return nil, errTool("not_found", "entry_id not found in the current scan")
	}
	return map[string]any{
		"schema_version": schemaVersion,
		"untrusted_data": true,
		"item":           s.itemFromEntry(e, s.runningSet(ctx)),
		"policy_verdict": s.verdictString(e.Path, e.BundleID),
	}, nil
}

// --- explain -----------------------------------------------------------

type reasonInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type explainResult struct {
	SchemaVersion             string        `json:"schema_version"`
	UntrustedData             bool          `json:"untrusted_data"`
	EntryID                   string        `json:"entry_id"`
	Path                      string        `json:"path"`
	Name                      string        `json:"name"`
	DataClass                 string        `json:"data_class"`
	CleanupEligibility        string        `json:"cleanup_eligibility"`
	PlanEligible              bool          `json:"plan_eligible"`
	RequiresExplicitSelection bool          `json:"requires_explicit_selection"`
	RemovableNow              bool          `json:"removable_now"`
	ReportOnly                bool          `json:"report_only"`
	AllowlistedRoot           bool          `json:"allowlisted_root"`
	Running                   *bool         `json:"running"`
	MTime                     string        `json:"mtime,omitempty"`
	SizeAllocated             int64         `json:"size_allocated"`
	Reasons                   []reasonInfo  `json:"reasons"`
	Orphan                    *orphanDetail `json:"orphan,omitempty"`
}

type orphanDetail struct {
	Confidence       string `json:"confidence"`
	ConfidenceReason string `json:"confidence_reason"`
}

func handleExplain(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		EntryID string `json:"entry_id"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	if _, terr := s.latestScanID(ctx); terr != nil {
		return nil, terr
	}
	e, err := s.deps.Store.EntryByID(ctx, a.EntryID)
	if err != nil {
		return nil, errTool("not_found", "entry_id not found in the current scan")
	}

	running := s.runningSet(ctx)
	verdict := s.deps.Policy.DeleteVerdict(e.Path, e.BundleID)
	out := explainResult{
		SchemaVersion:      schemaVersion,
		UntrustedData:      true,
		EntryID:            e.EntryID,
		Path:               s.redact(e.Path),
		Name:               clean(fileBase(e.Path)),
		DataClass:          string(e.DataClass),
		CleanupEligibility: string(e.DataClass.Eligibility()),
		ReportOnly:         e.DataClass.ReportOnly(),
		Running:            runningOf(e.Path, running),
		MTime:              mtimeString(e.MTime),
		SizeAllocated:      e.AllocatedBytes,
		Reasons:            []reasonInfo{},
	}
	out.AllowlistedRoot = s.isAllowlistedRoot(e.Path)

	allowed := verdict.Allowed()
	if !allowed {
		out.Reasons = append(out.Reasons, reasonInfo{Code: "policy_denied", Message: clean(verdict.Reason)})
	}
	if out.AllowlistedRoot {
		out.Reasons = append(out.Reasons, reasonInfo{Code: "allowlisted_root", Message: "an allowlisted root directory is never planned"})
	}
	switch e.DataClass.Eligibility() {
	case domain.EligibilityAuto:
		out.PlanEligible = allowed && !out.AllowlistedRoot
		if out.PlanEligible {
			out.Reasons = append(out.Reasons, reasonInfo{Code: "regenerable_cache", Message: "a regenerable cache may be planned"})
		}
	case domain.EligibilityExplicitSelection:
		out.RequiresExplicitSelection = allowed && !out.AllowlistedRoot
		out.PlanEligible = out.RequiresExplicitSelection
		out.Reasons = append(out.Reasons, reasonInfo{Code: "explicit_selection_required", Message: "app data may contain user data and needs explicit per-item selection"})
	default:
		out.Reasons = append(out.Reasons, reasonInfo{Code: "report_only", Message: "this data class is report-only and is never planned"})
	}
	if out.Running != nil && *out.Running {
		out.Reasons = append(out.Reasons, reasonInfo{Code: "running", Message: "the owning application is running; its caches are skipped"})
	}
	out.RemovableNow = out.PlanEligible && e.DataClass.Eligibility() == domain.EligibilityAuto && (out.Running == nil || !*out.Running)

	if e.DataClass == domain.ClassAppData {
		name := orphanCandidateName(e.Path)
		installed, complete := apps.New(s.deps.Store, s.deps.Home).InstalledBundleIDs(ctx)
		c := orphan.Classify(name, installed)
		if !complete {
			// Degraded confidence is reported through confidence_reason only.
			out.Reasons = append(out.Reasons, reasonInfo{Code: "discovery_incomplete", Message: "application discovery was incomplete; orphan confidence is degraded"})
		}
		out.Orphan = &orphanDetail{Confidence: string(c), ConfidenceReason: orphan.ConfidenceReason(c)}
	}
	return out, nil
}

// --- verify_orphan -----------------------------------------------------

func handleVerifyOrphan(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		EntryID string `json:"entry_id"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	if _, terr := s.latestScanID(ctx); terr != nil {
		return nil, terr
	}
	e, err := s.deps.Store.EntryByID(ctx, a.EntryID)
	if err != nil {
		return nil, errTool("not_found", "entry_id not found in the current scan")
	}

	installed, complete := apps.New(s.deps.Store, s.deps.Home).InstalledBundleIDs(ctx)
	svc := orphan.New(s.deps.Store, s.deps.Policy, s.deps.Home)
	res := svc.Verify(e.Path, orphan.Options{Installed: installed, DiscoveryComplete: complete})

	return map[string]any{
		"schema_version":     schemaVersion,
		"untrusted_data":     true,
		"entry_id":           e.EntryID,
		"path":               s.redact(res.Path),
		"bundle_id":          clean(res.BundleID),
		"app_found":          res.AppFound,
		"confidence":         string(res.Confidence),
		"confidence_reason":  res.ConfidenceReason,
		"discovery_complete": res.DiscoveryComplete,
		"reason":             clean(res.Reason),
	}, nil
}

// --- get_report --------------------------------------------------------

type reportResult struct {
	SchemaVersion string                             `json:"schema_version"`
	UntrustedData bool                               `json:"untrusted_data"`
	ScanID        string                             `json:"scan_id"`
	Partial       bool                               `json:"partial"`
	Totals        domain.Totals                      `json:"totals"`
	ByClass       map[domain.DataClass]domain.Totals `json:"by_class"`
	Counts        map[string]int                     `json:"counts"`
	TopApps       []item                             `json:"top_apps"`
	TopCaches     []item                             `json:"top_caches"`
	TopLarge      []item                             `json:"top_large"`
	Notes         []string                           `json:"notes,omitempty"`
}

func handleGetReport(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		TopN int `json:"top_n"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	top := a.TopN
	if top <= 0 {
		top = 10
	}
	if top > 50 {
		top = 50
	}
	scanID, terr := s.latestScanID(ctx)
	if terr != nil {
		return nil, terr
	}

	out := reportResult{SchemaVersion: schemaVersion, UntrustedData: true, ScanID: scanID, Counts: map[string]int{}}
	if sc, err := s.deps.Store.GetScan(ctx, scanID); err == nil {
		out.Partial = sc.Partial
	}
	if totals, err := s.deps.Store.Totals(ctx, scanID); err == nil {
		out.Totals = totals
	}
	if byClass, err := s.deps.Store.TotalsByClass(ctx, scanID); err == nil {
		out.ByClass = byClass
	}
	running := s.runningSet(ctx)

	if list, err := apps.New(s.deps.Store, s.deps.Home).List(ctx, scanID); err == nil {
		out.Counts["apps"] = len(list)
		for i, app := range list {
			if i >= top {
				break
			}
			out.TopApps = append(out.TopApps, s.itemFromApp(app, running))
		}
	}
	if list, err := cache.New(s.deps.Store, s.deps.Home).List(ctx, scanID, ""); err == nil {
		out.Counts["caches"] = len(list)
		for i, c := range list {
			if i >= top {
				break
			}
			out.TopCaches = append(out.TopCaches, s.itemFromCache(c, running))
		}
	}
	if list, err := large.New(s.deps.Store, s.deps.Config.LargeThreshold).List(ctx, scanID, large.Options{Limit: top}); err == nil {
		out.Counts["large"] = len(list)
		for _, l := range list {
			out.TopLarge = append(out.TopLarge, s.itemFromLarge(l, running))
		}
	}
	installed, complete := apps.New(s.deps.Store, s.deps.Home).InstalledBundleIDs(ctx)
	orphans, err := orphan.New(s.deps.Store, s.deps.Policy, s.deps.Home).List(ctx, orphan.Options{
		ScanID: scanID, Installed: installed, DiscoveryComplete: complete,
	})
	if err == nil {
		out.Counts["orphans"] = len(orphans)
	}
	if !complete {
		out.Notes = append(out.Notes, "application discovery was incomplete; orphan confidence is degraded")
	}
	return out, nil
}

// --- helpers -----------------------------------------------------------

func (s *Server) isAllowlistedRoot(path string) bool {
	for _, r := range s.deps.Policy.AllowlistRoots() {
		if path == r {
			return true
		}
	}
	return false
}

func (s *Server) verdictString(path, bundleID string) string {
	v := s.deps.Policy.DeleteVerdict(path, bundleID)
	if v.Allowed() {
		return "allow"
	}
	return "deny"
}

func fileBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 && i < len(p)-1 {
		return p[i+1:]
	}
	return p
}

func orphanCandidateName(path string) string {
	return strings.TrimSuffix(fileBase(path), ".savedState")
}
