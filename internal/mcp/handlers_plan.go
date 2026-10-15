package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/cleaner"
	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// skipInfo explains why an entry was not included in a plan.
type skipInfo struct {
	EntryID string `json:"entry_id,omitempty"`
	Path    string `json:"path,omitempty"`
	Reason  string `json:"reason"`
}

// planItemOut is one planned item.
type planItemOut struct {
	EntryID            string `json:"entry_id"`
	Path               string `json:"path"`
	Name               string `json:"name"`
	DataClass          string `json:"data_class"`
	CleanupEligibility string `json:"cleanup_eligibility"`
	MayContainUserData bool   `json:"may_contain_user_data"`
	SizeAllocated      int64  `json:"size_allocated"`
}

type planOutput struct {
	SchemaVersion string        `json:"schema_version"`
	UntrustedData bool          `json:"untrusted_data"`
	PlanID        string        `json:"plan_id,omitempty"`
	CreatedAt     string        `json:"created_at,omitempty"`
	ExpiresAt     string        `json:"expires_at,omitempty"`
	Status        string        `json:"status,omitempty"`
	ItemCount     int           `json:"item_count"`
	TotalSize     int64         `json:"total_size"`
	Items         []planItemOut `json:"items"`
	Skipped       []skipInfo    `json:"skipped,omitempty"`
	NextPageToken string        `json:"next_page_token,omitempty"`
	Truncated     bool          `json:"truncated"`
	Total         int           `json:"total"`
	Notes         []string      `json:"notes,omitempty"`
}

func (p planOutput) itemCount() int64 { return int64(len(p.Items)) }

func (s *Server) planItem(it domain.PlanItem) planItemOut {
	return planItemOut{
		EntryID:            it.EntryID,
		Path:               s.redact(it.Path),
		Name:               clean(fileBase(it.Path)),
		DataClass:          string(it.DataClass),
		CleanupEligibility: string(it.DataClass.Eligibility()),
		MayContainUserData: it.DataClass.MayContainUserData(),
		SizeAllocated:      it.Size,
	}
}

func handleCreatePlan(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		EntryIDs   []string `json:"entry_ids"`
		TTLSeconds int      `json:"ttl_seconds"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	if len(a.EntryIDs) == 0 {
		return nil, errTool("invalid_argument", "entry_ids must not be empty")
	}
	maxItems := s.deps.Config.MaxItems
	if maxItems <= 0 {
		maxItems = cleaner.DefaultMaxItems
	}
	if len(a.EntryIDs) > maxItems {
		return nil, errTool("invalid_argument", "too many entry_ids")
	}
	scanID, terr := s.latestScanID(ctx)
	if terr != nil {
		return nil, terr
	}
	var ttl time.Duration
	if a.TTLSeconds > 0 {
		ttl = time.Duration(a.TTLSeconds) * time.Second
	}

	svc := s.deps.Cleaner
	if svc == nil {
		svc = cleaner.New(s.deps.Store, s.deps.Policy, s.deps.Home)
	}

	var (
		candidates []cleaner.Candidate
		skipped    []skipInfo
	)
	seen := map[string]struct{}{}
	for _, id := range a.EntryIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		e, err := s.deps.Store.EntryByID(ctx, id)
		if err != nil {
			skipped = append(skipped, skipInfo{EntryID: id, Reason: "entry_id not found in the current scan"})
			continue
		}
		// Passing an explicit entry_id is the per-item selection required for
		// app_data (SEC-DEL-03).
		candidates = append(candidates, cleaner.Candidate{Entry: e, Explicit: true})
	}

	res, err := svc.Plan(ctx, cleaner.PlanOptions{
		ScanID:     scanID,
		Candidates: candidates,
		TTL:        ttl,
		MaxItems:   maxItems,
		MaxBytes:   s.deps.Config.MaxBytes,
	})
	if err != nil {
		return nil, errTool("internal", "internal error")
	}

	out := planOutput{
		SchemaVersion: schemaVersion,
		UntrustedData: true,
		PlanID:        res.Plan.PlanID,
		ItemCount:     len(res.Plan.Items),
		TotalSize:     res.Plan.TotalSize,
		Items:         []planItemOut{},
		Skipped:       skipped,
		Total:         len(res.Plan.Items),
	}
	if !res.Plan.CreatedAt.IsZero() {
		out.CreatedAt = mtimeString(res.Plan.CreatedAt)
	}
	if !res.Plan.ExpiresAt.IsZero() {
		out.ExpiresAt = mtimeString(res.Plan.ExpiresAt)
	}
	for _, it := range res.Plan.Items {
		out.Items = append(out.Items, s.planItem(it))
	}
	for _, sk := range res.Skipped {
		out.Skipped = append(out.Skipped, skipInfo{EntryID: sk.EntryID, Path: s.redact(sk.Path), Reason: clean(sk.Reason)})
	}
	if len(out.Items) == 0 {
		out.Notes = append(out.Notes, "no eligible items; no plan was created")
	} else {
		out.Notes = append(out.Notes, "plan persisted only; apply it outside MCP with: mcc clean --apply --plan-id "+out.PlanID)
	}
	return out, nil
}

func handleGetPlan(ctx context.Context, s *Server, args json.RawMessage) (any, error) {
	var a struct {
		PlanID string `json:"plan_id"`
	}
	if terr := unmarshalArgs(args, &a); terr != nil {
		return nil, terr
	}
	if a.PlanID == "" {
		return nil, errTool("invalid_argument", "plan_id is required")
	}
	plan, err := s.deps.Store.GetPlan(ctx, a.PlanID)
	if err != nil {
		return nil, errTool("not_found", "plan_id not found")
	}
	items, err := s.deps.Store.PlanItems(ctx, a.PlanID)
	if err != nil {
		return nil, errTool("internal", "internal error")
	}

	out := planOutput{
		SchemaVersion: schemaVersion,
		UntrustedData: true,
		PlanID:        plan.PlanID,
		CreatedAt:     mtimeString(plan.CreatedAt),
		ExpiresAt:     mtimeString(plan.ExpiresAt),
		Status:        string(plan.Status),
		ItemCount:     len(items),
		TotalSize:     plan.TotalSize,
		Items:         []planItemOut{},
		Total:         len(items),
	}
	for _, it := range items {
		out.Items = append(out.Items, s.planItem(it))
	}
	if plan.Expired(s.now()) && plan.Status == domain.PlanPending {
		out.Notes = append(out.Notes, "plan has expired")
	}
	return out, nil
}
