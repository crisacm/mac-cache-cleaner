# Spec: clean

Status: implemented (Phase 1).

## Capability

`internal/cleaner` implements the two-step cleanup flow: `Plan` (dry-run,
persisted) and `Apply` (revalidated, human-confirmed). Plan and apply are
separate service operations so the flow stays MCP-ready (AGENTS.md §3).

## Requirements

- **CL-1** A plan is immutable with an ID, a content hash and an expiry
  (default 1 hour); apply acts only on a valid, unexpired plan (SEC-DEL-01).
- **CL-2** Planning changes nothing on disk; apply requires interactive
  confirmation that shows the item count and total size and must be answered
  explicitly (SEC-DEL-02).
- **CL-3** Eligibility by class: `regenerable_cache` is planned automatically;
  `app_data` needs explicit per-item selection and a "may contain user data"
  warning; `user_file` and `application` are report-only (SEC-DEL-03).
- **CL-4** Default action is move to Trash (`~/.Trash` rename, falling back to
  Finder via `osascript` with explicit argv). Permanent deletion needs a
  separate flag **and** an extra confirmation (SEC-DEL-04, SEC-DEL-09).
- **CL-5** Every item is revalidated at apply time: still allowed by policy,
  not a symlink, same `(dev, inode)`, size and mtime unchanged (SEC-DEL-06).
- **CL-6** Caches of running applications are skipped at plan and apply time
  (SEC-DEL-07).
- **CL-7** Every applied/skipped/failed item is written to the append-only
  audit log (SEC-TRN-07).
- **CL-8** Apply-time item/byte limits are configurable (SEC-DEL-11).
- **CL-9** No shell is used; subprocesses use absolute paths and explicit argv
  (SEC-DEL-09).

## API

- `New(store, policy, home, opts...) *Service`
- `(*Service) Plan(ctx, PlanOptions) (PlanResult, error)`
- `(*Service) Apply(ctx, ApplyOptions) (ApplyResult, error)`
- `Options`: `WithTrasher`, `WithRunningApps`, `WithClock`

## Notes

- Running-app detection uses `ps` plus `Info.plist` reads (metadata only, no
  TCC prompt).
- The hash covers entry ID, path, dev, inode, size and mtime of every item.
