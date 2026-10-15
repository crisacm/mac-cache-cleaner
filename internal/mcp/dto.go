package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/render"
)

const (
	schemaVersion   = "1.0"
	defaultPageSize = 50
	maxPageSize     = 500
)

// item is the shared shape returned for every filesystem-derived entry.
type item struct {
	EntryID            string `json:"entry_id,omitempty"`
	Path               string `json:"path"`
	Name               string `json:"name,omitempty"`
	BundleID           string `json:"bundle_id,omitempty"`
	Version            string `json:"version,omitempty"`
	Kind               string `json:"kind,omitempty"`
	DataClass          string `json:"data_class"`
	CleanupEligibility string `json:"cleanup_eligibility"`
	Plannable          bool   `json:"plannable"`
	PlannableReason    string `json:"plannable_reason,omitempty"`
	MayContainUserData bool   `json:"may_contain_user_data"`
	ReportOnly         bool   `json:"report_only"`
	Running            *bool  `json:"running"`
	MTime              string `json:"mtime,omitempty"`
	SizeAllocated      int64  `json:"size_allocated"`
	SizeLogical        int64  `json:"size_logical"`
	DataBytes          int64  `json:"data_bytes,omitempty"`
	Confidence         string `json:"confidence,omitempty"`
	ConfidenceReason   string `json:"confidence_reason,omitempty"`
	IsDir              bool   `json:"is_dir"`
	Expandable         bool   `json:"expandable"`
}

// envelope is the paginated response wrapper.
type envelope struct {
	SchemaVersion string   `json:"schema_version"`
	UntrustedData bool     `json:"untrusted_data"`
	Items         []item   `json:"items"`
	Total         int      `json:"total"`
	NextPageToken string   `json:"next_page_token,omitempty"`
	Truncated     bool     `json:"truncated"`
	Notes         []string `json:"notes,omitempty"`
}

func (e envelope) itemCount() int64 { return int64(len(e.Items)) }

// counted lets the server record how many items a result carried.
type counted interface{ itemCount() int64 }

func itemCount(result any) int64 {
	if c, ok := result.(counted); ok {
		return c.itemCount()
	}
	return 0
}

func newEnvelope(total int) envelope {
	return envelope{SchemaVersion: schemaVersion, UntrustedData: true, Total: total, Items: []item{}}
}

// redact shortens a path to "~".
func (s *Server) redact(p string) string { return render.Redact(p, s.deps.Home, false) }

// clean strips control characters and ANSI sequences (SEC-COD-02, SEC-MCP-05).
func clean(v string) string { return render.Sanitize(v) }

// runningOf reports whether the owning application is running, or nil when the
// path does not look like an application data path.
func runningOf(path string, running map[string]struct{}) *bool {
	base := strings.TrimSuffix(filepath.Base(path), ".savedState")
	if !strings.Contains(base, ".") {
		return nil
	}
	_, ok := running[base]
	return &ok
}

// withPlannable fills the policy-aware "can this be planned right now" fields,
// so agents do not propose items that create_plan would only skip.
func (s *Server) withPlannable(it item, path, bundleID string) item {
	switch {
	case it.ReportOnly:
		it.PlannableReason = "report_only"
	case s.isAllowlistedRoot(path):
		it.PlannableReason = "allowlisted_root"
	case !s.deps.Policy.DeleteVerdict(path, bundleID).Allowed():
		it.PlannableReason = "policy_denied"
	case it.Running != nil && *it.Running:
		it.PlannableReason = "running"
	default:
		it.Plannable = true
	}
	return it
}

// itemFromEntry builds an item from an indexed entry.
func (s *Server) itemFromEntry(e domain.Entry, running map[string]struct{}) item {
	return s.withPlannable(item{
		EntryID:            e.EntryID,
		Path:               s.redact(e.Path),
		Name:               clean(filepath.Base(e.Path)),
		Kind:               string(e.Kind),
		DataClass:          string(e.DataClass),
		CleanupEligibility: string(e.DataClass.Eligibility()),
		MayContainUserData: e.DataClass.MayContainUserData(),
		ReportOnly:         e.DataClass.ReportOnly(),
		Running:            runningOf(e.Path, running),
		MTime:              mtimeString(e.MTime),
		SizeAllocated:      e.AllocatedBytes,
		SizeLogical:        e.LogicalBytes,
		IsDir:              e.IsDir,
		Expandable:         e.IsDir && e.Kind != domain.KindPackage,
	}, e.Path, e.BundleID)
}

func (s *Server) itemFromApp(a domain.App, running map[string]struct{}) item {
	return s.withPlannable(item{
		Path:               s.redact(a.Path),
		Name:               clean(a.Name),
		BundleID:           clean(a.BundleID),
		Version:            clean(a.Version),
		DataClass:          string(domain.ClassApplication),
		CleanupEligibility: string(domain.ClassApplication.Eligibility()),
		ReportOnly:         true,
		Running:            runningOf(a.Path, running),
		SizeAllocated:      a.AllocatedBytes,
		SizeLogical:        a.LogicalBytes,
		DataBytes:          a.DataBytes,
		IsDir:              true,
	}, a.Path, a.BundleID)
}

func (s *Server) itemFromCache(c domain.CacheEntry, running map[string]struct{}) item {
	return s.withPlannable(item{
		EntryID:            c.EntryID,
		Path:               s.redact(c.Path),
		Name:               clean(c.Name),
		BundleID:           clean(c.AppID),
		DataClass:          string(c.DataClass),
		CleanupEligibility: string(c.DataClass.Eligibility()),
		MayContainUserData: c.DataClass.MayContainUserData(),
		ReportOnly:         c.DataClass.ReportOnly(),
		Running:            runningOf(c.Path, running),
		SizeAllocated:      c.AllocatedBytes,
		SizeLogical:        c.LogicalBytes,
		IsDir:              true,
		Expandable:         true,
	}, c.Path, c.AppID)
}

func (s *Server) itemFromOrphan(o domain.Orphan, running map[string]struct{}) item {
	return s.withPlannable(item{
		EntryID:            o.EntryID,
		Path:               s.redact(o.Path),
		Name:               clean(o.Name),
		BundleID:           clean(o.BundleID),
		DataClass:          string(o.DataClass),
		CleanupEligibility: string(o.DataClass.Eligibility()),
		MayContainUserData: true,
		Running:            runningOf(o.Path, running),
		SizeAllocated:      o.AllocatedBytes,
		SizeLogical:        o.LogicalBytes,
		Confidence:         string(o.Confidence),
		ConfidenceReason:   confidenceReason(o.Confidence),
		IsDir:              true,
		Expandable:         true,
	}, o.Path, o.BundleID)
}

func (s *Server) itemFromLarge(l domain.LargeItem, running map[string]struct{}) item {
	return s.withPlannable(item{
		EntryID:            l.EntryID,
		Path:               s.redact(l.Path),
		Name:               clean(filepath.Base(l.Path)),
		Kind:               string(l.Kind),
		DataClass:          string(l.DataClass),
		CleanupEligibility: string(l.DataClass.Eligibility()),
		MayContainUserData: l.DataClass.MayContainUserData(),
		ReportOnly:         l.DataClass.ReportOnly(),
		Running:            runningOf(l.Path, running),
		SizeAllocated:      l.AllocatedBytes,
		SizeLogical:        l.LogicalBytes,
		IsDir:              l.IsDir,
		Expandable:         l.IsDir && l.Kind != domain.KindPackage,
	}, l.Path, "")
}

// --- pagination --------------------------------------------------------

type page struct {
	offset int
	size   int
}

func parsePage(size int, token string) (page, *toolError) {
	p := page{size: defaultPageSize}
	if size > 0 {
		p.size = size
	}
	if p.size > maxPageSize {
		p.size = maxPageSize
	}
	if token != "" {
		off, err := decodePageToken(token)
		if err != nil {
			return page{}, errTool("invalid_argument", "invalid page_token")
		}
		p.offset = off
	}
	if p.offset < 0 {
		return page{}, errTool("invalid_argument", "invalid page_token")
	}
	return p, nil
}

func encodePageToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("off:%d", offset)))
}

func decodePageToken(token string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	var off int
	if _, err := fmt.Sscanf(string(raw), "off:%d", &off); err != nil {
		return 0, err
	}
	return off, nil
}

// paginate slices items and returns the next token when more remain.
func paginate(items []item, p page) ([]item, string, bool) {
	total := len(items)
	if p.offset >= total {
		return []item{}, "", false
	}
	end := p.offset + p.size
	truncated := false
	if end < total {
		truncated = true
	} else {
		end = total
	}
	out := items[p.offset:end]
	next := ""
	if truncated {
		next = encodePageToken(end)
	}
	return out, next, truncated
}

// --- audit helpers -----------------------------------------------------

// argsSummary renders a compact, filesystem-free summary of tool arguments for
// the audit log (arrays become counts, strings are redacted).
func argsSummary(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ""
	}
	parts := make([]string, 0, len(obj))
	for k, v := range obj {
		switch val := v.(type) {
		case []any:
			parts = append(parts, fmt.Sprintf("%s=%d", k, len(val)))
		case string:
			parts = append(parts, fmt.Sprintf("%s=<str:%d>", k, len(val)))
		case float64, bool:
			parts = append(parts, fmt.Sprintf("%s=%v", k, val))
		default:
			parts = append(parts, fmt.Sprintf("%s=<%T>", k, v))
		}
	}
	return strings.Join(parts, ",")
}
