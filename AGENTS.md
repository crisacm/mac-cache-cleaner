# AGENTS.md

Instructions for AI coding agents working on **MCC (Mac Cache Cleaner)**: a Go CLI that indexes and reports disk usage of applications, their caches, orphaned app data, and large files/directories (>1 GB) on macOS. A future phase exposes the same capabilities as an **MCP server** so AI agents can scan the system and report what is heavy and removable.

Read this file fully before making changes. **Read `SECURITY.md` too: its rules (`SEC-*`) are mandatory and override feature requests.** When in doubt, prefer the smaller, safer change and ask.

## 1. Goals, Scope and Roadmap

- Index and query disk space used by: installed apps, per-app caches/data, orphaned data, developer caches, and large files/dirs.
- Fast, scriptable CLI (`--json`, `--no-color`) and an optional TUI.
- Safe cleanup: plan -> apply, dry-run by default, Trash, strict allowlist, human confirmation.

| Phase | Deliverable | Status |
|---|---|---|
| 1 | CLI (scan, apps, cache, orphans, large, clean, report, permissions, data) | Implement now |
| 2 | TUI (`mcc tui`) reusing the same services | Implement now |
| 3 | **MCP server** (stdio) exposing read-only + plan tools to agents | Implemented (`internal/mcp`) |

Non-goals: background daemons, GUI, cloud sync, telemetry, network features, automatic deletion, privilege escalation, reading file contents, non-macOS platforms.

## 2. Tech Stack (do not substitute without approval)

| Concern | Choice |
|---|---|
| Language | Go (latest stable, see `go.mod`) |
| CLI / config | `spf13/cobra`, `spf13/viper` |
| TUI (phase 2) | `charmbracelet/bubbletea`, `lipgloss`, `bubbles` |
| Storage | `modernc.org/sqlite` (**CGO-free**) |
| Migrations | Embedded SQL (`embed`) + `pressly/goose` |
| Concurrency | stdlib + `golang.org/x/sync` (`errgroup`, `semaphore`) |
| Plist parsing | `howett.net/plist` |
| Syscalls | `golang.org/x/sys/unix` |
| Logging | `log/slog` |
| Tests | stdlib + `stretchr/testify` |
| Lint / format / security | `golangci-lint` (incl. `depguard`), `gofumpt`, `govulncheck`, `gosec` |
| Release | `goreleaser` |

Do not add a dependency without justifying it in the PR description. Prefer the standard library. Network packages (`net/http`, etc.) are forbidden outside an explicitly approved optional package (`SEC-COD-07`).

## 3. Repository Layout and Architecture

```
cmd/mcc/main.go        # wiring only (manual DI), no logic
internal/
  domain/              # pure types: App, CacheEntry, Orphan, LargeItem, Plan, ScanResult
  policy/              # tiers, allowlist/denylist, consent: the ONLY place that decides access
  scanner/             # concurrent walker + size computation
  scan/                # scan orchestration service (validate roots, persist, rollback)
  apps/                # app discovery + Info.plist parsing
  cache/               # data-driven registry of cache locations/rules
  orphan/              # orphan detection + confidence scoring
  large/               # large file/dir detection
  store/               # SQLite repository + migrations
  cleaner/             # plan (dry-run) -> apply -> Trash
  consent/             # per-root consent service (interactive-only granting)
  platform/            # interfaces + darwin implementation (build tags)
  config/              # viper -> typed struct
  render/              # presentation helpers shared by cli and tui (sanitize, sizes)
  cli/                 # cobra commands (thin)
  tui/                 # bubbletea models (phase 2)
  mcp/                 # MCP stdio server + tools (phase 3)
  proc/                # running-app discovery (ps + Info.plist)
migrations/            # SQL files (embedded)
testdata/              # fake ~/Library fixtures
```

### Dependency rules

- `cli`/`tui` -> services -> `domain`. `domain` imports nothing from this repo.
- `store`, `platform`, `cleaner` are consumed via interfaces **defined in the consumer package**.
- **All access decisions go through `internal/policy`.** No other package decides what may be read, scanned or deleted.
- `cli`/`tui` contain **no business logic**: parse input, call a service, render output. The TUI must not duplicate policy or service logic (it calls `scan`, `cleaner`, `consent` and the list services).
- `render` holds presentation helpers (sanitize, sizes, redaction) shared by `cli` and `tui` so sanitization has one implementation.
- Nothing outside `cmd/` constructs concrete dependencies.

### MCP-readiness (required now, so Phase 3 is additive)

- Services return structured domain values and **never print**; rendering lives in `cli/`/`tui/`.
- `plan` and `apply` are separate service operations. Plans are persisted with a stable `plan_id`.
- Index entries have a stable `entry_id`; plan/apply operations work from IDs, not free-form paths.
- Every result carries its data class (`regenerable_cache`, `app_data`, `user_file`, `application`).
- Do **not** write MCP code, transports or tool definitions in Phases 1-2: Phase 3 adds them, isolated in `internal/mcp` and bound by `SECURITY.md` §8.

## 4. Commands (CLI contract)

```
mcc scan [--root PATH] [--full] [--explain]
mcc apps [--sort size] [--json]
mcc cache [--app ID] [--json]
mcc orphans [--min-confidence high|medium|low]
mcc large [--min 1GB] [--path PATH]
mcc clean --plan | --apply [--plan-id ID]
mcc report [--json|--md] [--redact]
mcc permissions [show|grant|revoke <root>]        # show effective policy / consents
mcc data show | purge                  # what is stored, where; wipe it
mcc tui                                # phase 2
mcc mcp                                # phase 3: MCP server (stdio) for agents
```

- Read commands support `--json` and `--no-color`. JSON output is a stable contract: do not rename/remove fields without a SemVer-aware changelog entry.
- Human output goes to stdout; logs/diagnostics go to stderr.
- Exit codes: `0` success, `1` generic error, `2` usage error, `3` partial result (inaccessible paths), `4` blocked by policy / consent required.

**`mcc tui` (Phase 2)** reuses the same services and contains no logic of its own:

- Startup never blocks: a loading screen shows the current step and the directory being read; data loads in the background and in parallel.
- Caches/Orphans/Large are trees: `→`/`l` expands or enters, `←`/`h` collapses or returns to the parent; children load lazily from the index (capped). `f` toggles showing only directories vs. all elements inside expanded folders.
- Selecting a directory and something inside it at once is prevented.
- Scan shows the effective roots with their verdicts; `Esc` cancels and rolls back. Consent needs an interactive, root-naming confirmation.
- Cleanup is select -> plan review -> typed item count -> apply, and only ever **moves to the Trash** (no permanent delete).
- All names are sanitized before rendering.

## 5. Disk-Usage Correctness (critical)

1. Size on disk = `st_blocks * 512`, **not** `st_size`. Store both (`allocated_bytes`, `logical_bytes`).
2. Deduplicate hardlinks by `(dev, inode)`.
3. Never follow symlinks. Use `Lstat`; record the link, do not descend.
4. Do not cross devices unless explicitly requested and consented. Be aware of firmlinks (`/System/Volumes/Data`); use explicit scan roots.
5. `EPERM`/`EACCES` (TCC / Full Disk Access) are **non-fatal**: collect them, continue, report "N inaccessible paths", exit `3`. Never try to bypass or provoke macOS privacy prompts (`SEC-ACC-05`).
6. Walk with `filepath.WalkDir` and a bounded worker pool; never spawn unbounded goroutines.
7. Large-item detection keeps a bounded top-N (min-heap); do not load the whole tree in memory.
8. Incremental scans: reuse directory `mtime` to skip unchanged subtrees.
9. Library packages (`*.photoslibrary`, etc.) are opaque single items: size only, never descended.

## 6. Locations and Access Tiers

Full table and rules in `SECURITY.md` section 3. Summary:

- **Tier 0 (default scan):** `/Applications`, `~/Applications`, `~/Library/Caches|Logs|Application Support|Containers|Group Containers|HTTPStorages|WebKit|Saved Application State`, developer caches (`~/.gradle/caches`, `~/.android/avd`, Xcode `DerivedData`, `~/.npm`, pnpm store, `GOMODCACHE`, Homebrew cache).
- **Tier 1 (user data):** Documents, Desktop, Downloads, Pictures, Movies, Music, Public. Opt-in per root with recorded consent; metadata only; **report-only**, never in automated plans.
- **Tier 2 (never accessed):** Keychains, iCloud (`Mobile Documents`), Messages, Mail, Safari, `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, password managers, browser credential stores, contents of library packages. No consent can enable them. The table is compiled in; config cannot downgrade it.
- **Tier 3 (volumes):** `/Volumes/*`, network mounts, Time Machine. Only with explicit `--root` + consent; Time Machine never.
- **Orphans:** bundle IDs under `~/Library/*` with no matching installed app. Exclude `com.apple.*` and SIP paths. Output a **confidence level** (exact ID / team-prefix / name-only), never a bare boolean. If app-discovery locations are inaccessible, degrade confidence.

Cache locations live in a data-driven registry in `internal/cache`. Add new locations as data, not as new code paths.

## 7. Safety Rules (non-negotiable; see `SECURITY.md` §3-§6)

- **Metadata only**: never read file contents. Sole exception: `Info.plist` inside `.app` bundles (<= 1 MiB).
- Refuse to run as root; never call `sudo` or install privileged helpers.
- Consent is per root, granted only interactively on a TTY, recorded in SQLite, revocable. `--yes` never grants consent.
- Cleanup is **plan -> apply**: immutable plan with ID, hash and expiry; apply acts only on planned items and **revalidates** each (inside allowlisted root, not a symlink, same `(dev, inode)`, size/mtime unchanged).
- Eligibility by class: `regenerable_cache` may be planned; `app_data` (incl. orphans) only by explicit per-item human selection with a "may contain user data" warning; `user_file` and `application` are report-only.
- All cleanup **moves items to the Trash**; MCC never deletes from disk. There is no permanent-delete path. If moving to the Trash fails, the item is reported and left untouched.
- Never delete allowlisted roots themselves, anything outside the allowlist, SIP paths, `com.apple.*`, Tier 2/3 locations. Skip caches of running apps.
- Subprocesses (e.g. `osascript` for Trash): absolute binary path, explicit argv, no `sh -c`, never interpolate paths into script text.
- File and app names are **untrusted data**: strip/escape control and ANSI sequences in terminal output.
- Store dir `0700`, files `0600`, no telemetry, no network, audit log of every applied action.
- All I/O honors `context.Context`; Ctrl+C cancels cleanly without leaving partial state.
- **Never** run destructive operations against the real home directory in tests. Use `t.TempDir()` and `testdata/`.

## 8. Code Conventions

- `context.Context` is the first parameter of any function doing I/O.
- Wrap errors with `%w`; use sentinel or typed errors where callers must distinguish them. No `panic` outside `main` for unrecoverable wiring errors.
- Small interfaces, defined at the consumer. Accept interfaces, return structs.
- No package-level mutable state; no `init()` with side effects. Constructors and dependency injection.
- Abstract the filesystem (`fs.FS` or a small local interface) so logic is testable without touching disk.
- Platform-specific code uses build tags (`//go:build darwin`) with a stub for other platforms.
- Use `log/slog` (structured). No `fmt.Println` for diagnostics.
- Paths: clean, absolute, symlink-resolved, compared with a **path-boundary** check (`/a/Caches` must not match `/a/Caches-evil`); reject `..` and NUL.
- SQL: parameterized queries only.
- Format with `gofumpt`; pass `golangci-lint` (`errcheck`, `govet`, `staticcheck`, `gosec`, `revive`, `depguard`).
- Exported identifiers have doc comments. Keep functions short and single-purpose.
- Constants/magic numbers (e.g., the 1 GB threshold) are named and configurable. Policy config cannot be loosened by environment variables.

## 9. Testing

- Table-driven tests; `testify` for assertions. Fixtures in `t.TempDir()` plus a fake `~/Library` tree in `testdata/`.
- CLI output verified with **golden files** (`testdata/*.golden`); update them deliberately.
- Functional coverage: hardlink dedup, sparse files, symlink non-following, `EPERM` handling, orphan scoring, dry-run vs apply.
- **Security coverage (required, see `SECURITY.md` §10):** Tier 2 never traversed; Tier 1/3 need consent; `--yes` grants nothing; path-boundary and `..`/symlink escape; TOCTOU swap between plan and apply; plan expiry/hash integrity; refuse root; refuse allowlisted roots, SIP, `com.apple.*`; running-app caches skipped; terminal-escape sanitization; store permissions `0700/0600`; no network imports outside approved package.
- Concurrency code is run with `-race` in CI.
- New behavior requires tests in the same change. Bug fixes require a regression test.

## 10. Definition of Done

All must pass locally and in CI:

```
make fmt        # gofumpt
make lint       # golangci-lint (incl. depguard)
make vuln       # govulncheck ./...
make test       # go test -race ./...
make build      # CGO_ENABLED=0 go build ./...
make check      # runs all of the above
```

Also required:
- No new CGO dependency.
- `SECURITY.md` §12 checklist satisfied.
- Docs/specs updated if behavior, CLI contract or policy changed.
- `CHANGELOG.md` updated under `[Unreleased]` (Keep a Changelog format, **written in Spanish**).

## 11. Workflow

- **Spec-driven development (SDD)** with OpenSpec: one spec per capability (`scan`, `apps`, `cache`, `orphans`, `large`, `clean`, `policy`, `tui`). Implement against the spec; if the spec is wrong or incomplete, propose a spec change first.
- Keep tasks small and independent, each with explicit acceptance criteria.
- Build order: `domain` + `store` + `policy` -> `scanner` -> `apps` + `cache` -> `large` -> `orphan` -> `cleaner` -> `cli` -> `tui`.
- Commits follow **Conventional Commits** (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `chore:`). Versioning follows **SemVer**.
- One logical change per PR/commit. Do not mix refactors with behavior changes.
- If a `SECURITY.md` rule cannot be satisfied, stop, document it and ask; do not work around it.

## 12. Do / Don't

**Do**
- Route every access decision through `internal/policy`.
- Keep `cli/`/`tui/` thin and services free of printing.
- Treat inaccessible paths as partial results.
- Make new cache locations data-driven.
- Ask before adding dependencies or changing the JSON contract.

**Don't**
- Don't read file contents or touch Tier 2 paths, ever.
- Don't use `st_size` as disk usage.
- Don't follow symlinks or cross devices implicitly.
- Don't delete without plan -> apply, revalidation, allowlist validation and Trash (never delete from disk).
- Don't add network calls, telemetry, CGO, global state or unbounded goroutines.
- Don't add MCP code outside `internal/mcp`, and never expose a mutating MCP tool (`SEC-MCP-02`).
- Don't touch the real home directory in tests.
- Don't write logic in `cmd/mcc/main.go`.
