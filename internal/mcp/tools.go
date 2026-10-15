package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
	"github.com/crisacm/mac-cache-cleaner/internal/orphan"
)

// toolCatalog returns the static tool definitions. Descriptions are constant
// strings: filesystem-derived text is never placed here (SEC-MCP-06).
func toolCatalog() []tool {
	return []tool{
		{
			Name: "mcc_get_status",
			Description: "Report the state of the local index: whether a scan exists, when it ran, " +
				"whether it was partial, the effective scan roots with their policy verdict, the effective " +
				"tier table and the recorded consents. Call this first.",
			InputSchema: schema(nil),
		},
		{
			Name: "mcc_list_apps",
			Description: "List installed applications with their bundle id, location and disk usage " +
				"(application + associated data). Applications are report-only: MCC never uninstalls them.",
			InputSchema: schema(map[string]any{
				"page_size":  intProp("Maximum items per page (default 50, max 500)."),
				"page_token": strProp("Opaque token from a previous response."),
				"sort":       enumProp("Sort order.", "size", "name"),
			}),
		},
		{
			Name: "mcc_list_caches",
			Description: "List known cache locations with their size, data class and cleanup eligibility. " +
				"Use mcc_explain on an entry_id to learn whether it is actually removable.",
			InputSchema: schema(map[string]any{
				"page_size":  intProp("Maximum items per page (default 50, max 500)."),
				"page_token": strProp("Opaque token from a previous response."),
			}),
		},
		{
			Name: "mcc_list_orphans",
			Description: "List leftover data whose owning application appears to be uninstalled, with a " +
				"confidence level and reason. Verify with mcc_verify_orphan before proposing removal; orphan " +
				"items may contain user data and require explicit per-item selection.",
			InputSchema: schema(map[string]any{
				"min_confidence": enumProp("Only return items at least this confident.", "high", "medium", "low"),
				"page_size":      intProp("Maximum items per page (default 50, max 500)."),
				"page_token":     strProp("Opaque token from a previous response."),
			}),
		},
		{
			Name: "mcc_list_large",
			Description: "List heavy files and directories above a size threshold, with their data class " +
				"and cleanup eligibility.",
			InputSchema: schema(map[string]any{
				"min_bytes":   intProp("Minimum allocated size in bytes (default: configured threshold)."),
				"path_prefix": strProp("Restrict to a subtree (must be under a scanned root)."),
				"page_size":   intProp("Maximum items per page (default 50, max 500)."),
				"page_token":  strProp("Opaque token from a previous response."),
			}),
		},
		{
			Name: "mcc_list_children",
			Description: "List the direct children of an indexed directory, addressed by entry_id. " +
				"Use it to drill into a cache or leftover before planning.",
			InputSchema: schema(map[string]any{
				"entry_id":   strProp("entry_id of a directory from a previous listing."),
				"show_files": boolProp("Include non-directory children (default false)."),
				"page_size":  intProp("Maximum items per page (default 50, max 500)."),
				"page_token": strProp("Opaque token from a previous response."),
			}, "entry_id"),
		},
		{
			Name:        "mcc_get_entry",
			Description: "Return one indexed entry by entry_id together with its policy verdict.",
			InputSchema: schema(map[string]any{"entry_id": strProp("entry_id from a previous listing.")}, "entry_id"),
		},
		{
			Name: "mcc_explain",
			Description: "Explain whether an entry is removable and why: data class, cleanup eligibility, " +
				"whether it is an allowlisted root, whether its application is running, and (for leftovers) " +
				"the orphan confidence. Call this before proposing anything for cleanup.",
			InputSchema: schema(map[string]any{"entry_id": strProp("entry_id from a previous listing.")}, "entry_id"),
		},
		{
			Name: "mcc_verify_orphan",
			Description: "Re-check whether the application that would own a leftover is installed right now. " +
				"Reports app_found and the confidence before/after, degrading it when application discovery " +
				"is incomplete.",
			InputSchema: schema(map[string]any{"entry_id": strProp("entry_id of an orphan from mcc_list_orphans.")}, "entry_id"),
		},
		{
			Name: "mcc_get_report",
			Description: "Consolidated summary: totals by data class, item counts and the heaviest " +
				"applications, caches and large items.",
			InputSchema: schema(map[string]any{
				"top_n": intProp("How many items per section (default 10, max 50)."),
			}),
		},
		{
			Name: "mcc_create_plan",
			Description: "Create a cleanup plan from explicit entry_ids. This only persists a plan; nothing " +
				"is changed on disk. Items that are not eligible are returned in 'skipped' with a reason. " +
				"The plan is applied outside MCP, and applying only ever moves items to the Trash.",
			InputSchema: schema(map[string]any{
				"entry_ids":   arrayProp("entry_ids to include in the plan."),
				"ttl_seconds": intProp("Plan validity in seconds (default 3600)."),
			}, "entry_ids"),
		},
		{
			Name:        "mcc_get_plan",
			Description: "Return a previously created plan by plan_id.",
			InputSchema: schema(map[string]any{"plan_id": strProp("plan_id from mcc_create_plan.")}, "plan_id"),
		},
	}
}

// handlerFor maps a tool name to its handler.
func handlerFor(name string) handlerFunc {
	switch name {
	case "mcc_get_status":
		return handleGetStatus
	case "mcc_list_apps":
		return handleListApps
	case "mcc_list_caches":
		return handleListCaches
	case "mcc_list_orphans":
		return handleListOrphans
	case "mcc_list_large":
		return handleListLarge
	case "mcc_list_children":
		return handleListChildren
	case "mcc_get_entry":
		return handleGetEntry
	case "mcc_explain":
		return handleExplain
	case "mcc_verify_orphan":
		return handleVerifyOrphan
	case "mcc_get_report":
		return handleGetReport
	case "mcc_create_plan":
		return handleCreatePlan
	case "mcc_get_plan":
		return handleGetPlan
	default:
		return func(context.Context, *Server, json.RawMessage) (any, error) {
			return nil, errTool("invalid_argument", "unknown tool")
		}
	}
}

// --- schema helpers ----------------------------------------------------

func schema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "additionalProperties": false}
	if props == nil {
		props = map[string]any{}
	}
	m["properties"] = props
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func arrayProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

// --- small helpers -----------------------------------------------------

func mtimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func confidenceReason(c domain.Confidence) string { return orphan.ConfidenceReason(c) }
