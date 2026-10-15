# Spec: mcp

Status: in progress (Phase 3).

## Capability

`mcc mcp` exposes MCC's indexed, read-only information and its plan operation to
AI agents over **stdio** JSON-RPC (MCP). It lets an agent answer "what is heavy
and what is actually removable" and then *propose* a cleanup plan — without ever
being able to apply it, grant consent, scan, or delete anything.

The server contains **no business logic**: it calls the existing services,
paginates, sanitizes and renders structured JSON. All access decisions go
through `internal/policy`.

## Hard security requirements (`SECURITY.md` §8)

- **MCP-1** stdio only; no HTTP/SSE listener (`SEC-MCP-01`).
- **MCP-2** Agents are untrusted callers. `apply_plan` is **not exposed**;
  applying happens outside MCP (CLI/TUI) and only ever moves to the Trash
  (`SEC-MCP-02`, `SEC-DEL-04`).
- **MCP-3** Plan operations take opaque `entry_id` / `plan_id`, never free-form
  paths (`SEC-MCP-03`). No tool accepts a path.
- **MCP-4** No tool grants consent, changes config/tiers/allowlists, purges data
  or the audit log, or triggers a scan (`SEC-MCP-04`).
- **MCP-5** Structured JSON; paths shortened to `~`; names sanitized (control
  chars/ANSI stripped); every response with filesystem-derived strings carries
  `"untrusted_data": true`. File contents are never returned (`SEC-MCP-05`).
- **MCP-6** Tool descriptions and error messages are **static**: filesystem
  text never appears there (`SEC-MCP-06`).
- **MCP-7** Summaries by default, details on request, pagination with a default
  cap (50) and a hard maximum (500) (`SEC-MCP-07`).
- **MCP-8** Every call is audit-logged locally (tool, argument summary, status,
  duration); per-call timeout and a rate limit apply; one scan at a time
  (MCP never scans) (`SEC-MCP-08`).
- **MCP-9** Tier 2 is never exposed. Tier 1 is exposed only with existing
  consent and always flagged `report_only` (`SEC-MCP-09`).

## Transport and lifecycle

- Same binary: `mcc mcp`. Newline-delimited JSON-RPC 2.0 over stdin/stdout.
- Methods implemented: `initialize`, `notifications/initialized`, `ping`,
  `tools/list`, `tools/call`. Unknown methods return a JSON-RPC error.
- No scan is triggered. Tools read the latest scan; `get_status` reports when
  there is none or it is stale.

## Tools

Shared item shape:

```
entry_id, path (~), name, kind, data_class,
cleanup_eligibility: auto_plan | explicit_selection_required | report_only,
may_contain_user_data (bool), report_only (bool),
size_allocated, size_logical, running (bool|null), mtime (RFC3339|null)
```

### Read-only

| Tool | Input | Notes |
|---|---|---|
| `get_status` | — | last scan (id, times, partial, inaccessible count), effective roots + verdict, effective policy, recorded consents, `stale` |
| `list_apps` | `page_size?`, `page_token?`, `sort?` | apps: name, bundle_id, version, path, app + data sizes |
| `list_caches` | `page_size?`, `page_token?` | registry cache locations with eligibility |
| `list_orphans` | `min_confidence?`, `page_size?`, `page_token?` | confidence + reason, `may_contain_user_data: true`, `discovery_complete` |
| `list_large` | `min_bytes?`, `path_prefix?`, pagination | heavy files/dirs |
| `list_children` | `entry_id`, `show_files?`, pagination | direct children (tree drill-down by ID) |
| `get_entry` | `entry_id` | one item + policy verdict |
| `explain` | `entry_id` | `removable`, `reasons[]`, policy verdict, running, mtime, allowlisted_root, orphan detail |
| `verify_orphan` | `entry_id` | re-checks whether the app is installed now; reports `confidence_before/after` and degrades confidence (`SEC-ORP-02`) |
| `get_report` | `top_n?` | totals, by class, counts, top lists |

### Plan (no disk change)

| Tool | Input | Notes |
|---|---|---|
| `create_plan` | `entry_ids[]`, `ttl_seconds?` | returns `plan_id`, items, total size, **skipped with reason**, expiry |
| `get_plan` | `plan_id` | status, items, total, expiry |

**Deliberately absent** (verified by tests): any `apply`/`delete`/`scan`/
`grant_consent`/`purge`/config-mutation tool.

## Contract

- Response envelope: `schema_version`, `untrusted_data`, `items`, `total`,
  `next_page_token`, `truncated`.
- Pagination: `page_size` default 50, maximum 500; `page_token` is opaque.
- Errors: static codes (`invalid_argument`, `not_found`, `no_scan`, `blocked`,
  `rate_limited`, `internal`); no filesystem-derived text in messages.
- Limits: 30 s per call, 120 calls/minute, `entry_ids` capped by the plan
  `MaxItems` (1000).
- Audit: every call appends to `mcp_calls(at, tool, args_summary, status,
  duration_ms, item_count)`.

## Service reuse

`scan` (read latest scan), `apps`/`cache`/`orphan`/`large.List`, `store`
(`LatestScan`, `Totals`, `TotalsByClass`, `Inaccessible`, `Consents`,
`EntriesByParent`, `EntryByID`, `Paths`), `policy` verdicts, `cleaner.Plan`,
`domain.DataClass.Eligibility()`, `proc.RunningBundleIDs`, `orphan.Service.Verify`.

## Acceptance criteria

- `mcc mcp` speaks the stdio handshake and answers `tools/list`/`tools/call`.
- All requirements above are covered by tests (see the task's test list) and
  `make check` is green.
