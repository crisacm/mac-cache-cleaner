# Spec: tui

Status: in progress (Phase 2).

## Capability

`mcc tui` is an interactive terminal interface (Bubble Tea + Lipgloss + Bubbles)
that reuses the existing services. It contains **no business logic**: it calls
services, holds screen state and renders. Every access decision still goes
through `internal/policy`; the TUI never duplicates or bypasses it
(AGENTS.md §3, SECURITY.md §3).

## Screens

1. **Overview** — totals by data class (apps, caches, orphans, large items),
   last scan time, and a partial-result warning when a scan had inaccessible
   paths (SEC-ACC-05).
2. **Apps / Caches / Orphans / Large** — sortable (size, name) and filterable
   lists with sizes, data-class labels and, for orphans, confidence levels.
3. **Scan** — shows the effective roots and their verdicts first; runs a scan
   with progress; cancellable with `Esc`/`Ctrl+C` via `context`. Tier 1/3 roots
   are only scanned with existing consent (SEC-ACC-04, SEC-ACC-07).
4. **Cleanup** — select items, review the plan (count, total size, data class,
   warnings), confirm explicitly, then apply. Same plan -> apply flow as the
   CLI: persisted plan with ID/hash/expiry, per-item revalidation, Trash by
   default, audit log (SEC-DEL-01..07). Permanent delete is not reachable from
   the TUI.
5. **Permissions** — the effective policy table and recorded consents; revoke a
   consent. Granting requires an explicit confirmation screen naming the root.
6. **Consent** — only reachable from the Scan screen for a Tier 1/3 root;
   requires a typed confirmation naming the root and is impossible
   non-interactively (SEC-ACC-07).

## Requirements

- **T-1** `tui/` holds no business logic; it calls services and renders.
- **T-2** All access decisions go through `internal/policy`.
- **T-3** Control characters and ANSI escape sequences are stripped from every
  file/app name rendered (SEC-COD-02).
- **T-4** `user_file` and `application` rows are report-only: selection for plan
  inclusion is disabled (SEC-DEL-03).
- **T-5** Applying requires an explicit typed confirmation showing the item
  count and total size; Enter alone is not enough (SEC-DEL-02).
- **T-6** Cancelling a scan mid-run rolls the in-progress scan back so the
  previous complete scan stays latest (consistent state; SEC-COD-04).
- **T-7** Consent is recorded only after an interactive, root-naming
  confirmation; no keypress or shortcut grants it silently (SEC-ACC-07).
- **T-8** No file contents are read; no network, telemetry, CGO or unbounded
  goroutines.
- **T-9** Tests are message-driven (`Update`) with golden `View` output, using
  fakes and `t.TempDir()`; the real home is never touched.

## Service reuse (also the future MCP surface)

- `scan.Service.Run` — orchestration + progress (new).
- `scanner.Scanner.Scan` with `Options.Progress` (new structured progress).
- `apps.Service.List`, `cache.Service.List`, `orphan.Service.List`,
  `large.Service.List`.
- `cleaner.Service.Plan` / `Apply` — work from `entry_id` / `plan_id`.
- `consent.Service.List/Grant/Revoke` (new).
- `policy.Policy` verdicts, `store` reads (`Totals`, `TotalsByClass`,
  `LatestScan`, `Inaccessible`, `Consents`).

## Acceptance criteria

- `mcc tui` opens, navigates all screens, scans with progress and cancel,
  builds a plan and applies it against a test fixture with the full safety flow.
- The tests listed in the task exist and pass; `make check` is green.
