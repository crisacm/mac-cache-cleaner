# SECURITY.md

Security policy and mandatory security requirements for **MCC (Mac Cache Cleaner)**.

Audience: maintainers, contributors and AI coding agents. The key words MUST, MUST NOT, SHOULD and MAY are used as in RFC 2119. Rules have stable IDs (e.g. `SEC-ACC-02`) so that specs, PRs and agents can cite them. If a rule conflicts with a feature request, **the rule wins**; propose a change to this file first.

---

## 1. Purpose, Scope and Roadmap

MCC lets a user inspect how much disk space applications, their caches, orphaned app data and heavy files/directories (> 1 GB) occupy, and decide what to clean. In a future phase the same capabilities will be exposed as an **MCP server** so that AI agents can scan the system and report what is heavy and removable.

| Phase | Deliverable | Status |
|---|---|---|
| 1 | CLI: `scan`, `apps`, `cache`, `orphans`, `large`, `clean`, `report` (+ `permissions`, `data`) | In scope now |
| 2 | TUI (`mcc tui`) reusing the same services | In scope now |
| 3 | **MCP server** exposing read-only and plan capabilities to agents | **Future. Not implemented now**, but current code MUST be designed so it can be added without weakening any rule here (see section 8) |

**Out of scope (MUST NOT be built without a security review):** network services or listeners, background daemons/scheduled scanning, automatic cleaning, privilege escalation or privileged helpers, reading/indexing file contents, telemetry of any kind, non-macOS platforms.

**Threat model (summary)**

| Threat | Example | Primary mitigation |
|---|---|---|
| Over-reaching access | Scanning Photos, Documents, SSH keys, external disks without consent | Policy tiers + consent (section 3) |
| Destructive mistakes | Deleting user data mistaken for cache; orphan false positive | Plan/apply, eligibility classes, Trash (section 5, 6) |
| Malicious file names | A file named like an instruction to an agent; ANSI escapes in names | Treat names as untrusted data (section 7, 8) |
| Confused-deputy agent | An agent applies a deletion the user never approved | Human-in-the-loop, no agent consent (section 8) |
| Data exfiltration | Paths/sizes sent to remote services | Local-only, no telemetry (section 4) |
| Path tricks / TOCTOU | Symlink swap between plan and apply | Revalidation, no-follow ops (section 5) |
| Supply chain | Malicious dependency or release | Section 9 |

---

## 2. Core Principles

1. **Least privilege**: run as the invoking user; ask for nothing more.
2. **Metadata only**: MCC reads sizes, timestamps and names, never file contents.
3. **Consent before access**: anything beyond the default roots needs explicit, recorded, revocable user consent.
4. **Transparency**: the user can always see what MCC will access, what it stores and what it did.
5. **Human in control of destruction**: no deletion without a reviewed plan and human confirmation.
6. **Local only**: no data leaves the machine.
7. **Fail closed**: when unsure (unknown path, unreadable state, ambiguous match), skip and report.

---

## 3. Data Access Rules

All access decisions are enforced by a single package, `internal/policy`. CLI, TUI and (later) MCP MUST go through it; no other package decides what is allowed.

- **SEC-ACC-01** MCC MUST run as the invoking user. It MUST refuse to run with `euid == 0` and MUST NOT call `sudo`, install privileged helpers or request elevation.
- **SEC-ACC-02** MCC MUST NOT read file contents. Allowed operations: `readdir`, `lstat`/`stat`. The only exception is read-only parsing of `Info.plist` inside `.app` bundles (bundle ID, name, version), size-capped (<= 1 MiB).
- **SEC-ACC-03** Paths are classified in tiers. The classification table is compiled into the binary; configuration MUST NOT be able to downgrade a tier.

| Tier | Locations (examples) | Behavior |
|---|---|---|
| **0 – Default** | `/Applications`, `~/Applications`, `~/Library/Caches`, `~/Library/Logs`, `~/Library/Application Support`, `~/Library/Containers`, `~/Library/Group Containers`, `~/Library/HTTPStorages`, `~/Library/WebKit`, `~/Library/Saved Application State`, developer caches (`~/.gradle/caches`, `~/.android/avd`, Xcode `DerivedData`, `~/.npm`, pnpm store, `GOMODCACHE`, Homebrew cache) | Scanned by default, metadata only |
| **1 – User data** | `~/Documents`, `~/Desktop`, `~/Downloads`, `~/Pictures`, `~/Movies`, `~/Music`, `~/Public`, other user-chosen directories | **Opt-in per root with recorded consent.** Metadata only. Results are **report-only**: never included in automated plans |
| **2 – Sensitive (never accessed)** | Contents of `*.photoslibrary`, `*.musiclibrary` and similar library packages (treated as one opaque item: size only, no descent), `~/Library/Keychains`, `~/Library/Mobile Documents` (iCloud), `~/Library/Messages`, `~/Library/Mail`, `~/Library/Safari`, `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, password-manager data, browser profile credential stores | **Never traversed, never listed, no consent can enable them** |
| **3 – Volumes** | `/Volumes/*`, network mounts, Time Machine backups and snapshots, disk images | Never scanned unless the user passes an explicit `--root` **and** grants consent for that root. Time Machine data is never scanned |

- **SEC-ACC-04** Tier 0 is the only set scanned without asking. Tier 1 and Tier 3 roots require consent (SEC-ACC-07). Tier 2 is always skipped.
- **SEC-ACC-05** MCC MUST NOT bypass, probe or manipulate macOS privacy controls (TCC): no `tccutil`, no repeated access attempts to trigger prompts, no programmatic requests for Full Disk Access. On `EPERM`/`EACCES` MCC records the path, skips it, and reports a partial result (exit code 3). It MAY print an informational note explaining how the **user** can grant access.
- **SEC-ACC-06** Scans MUST stay inside declared roots: symlinks are never followed, devices are not crossed, firmlinks are handled explicitly.
- **SEC-ACC-07** Consent is recorded in SQLite (`consents`: root, tier, timestamp), is **per root** (never global), is granted only interactively on a TTY, and is revocable (`mcc permissions revoke <root>`). Flags such as `--yes`/`--force` MUST NOT grant Tier 1/3 consent. Non-interactive callers (scripts, MCP) can only use consents that already exist.
- **SEC-ACC-08** Data-class labels travel with every result: `regenerable_cache`, `app_data` (may contain user data), `user_file`, `application`. Cleanup eligibility (section 5) depends on this label.

---

## 4. Transparency and Data Handling

- **SEC-TRN-01** Before scanning, MCC MUST be able to show exactly which roots will be scanned and which are skipped and why (`mcc permissions`, `mcc scan --explain`).
- **SEC-TRN-02** MCC stores only: path, kind, allocated/logical size, mtime, bundle ID, scan metadata, consents, plans and the audit log. It MUST NOT store file contents, credentials or tokens.
- **SEC-TRN-03** Storage lives in `~/Library/Application Support/mcc/` with directory mode `0700` and files `0600`. No data is written to world-readable locations.
- **SEC-TRN-04** `mcc data show` MUST print the storage location and what is stored; `mcc data purge` MUST remove the index, plans and consents on request.
- **SEC-TRN-05** **No telemetry, analytics, crash reporting or update checks.** MCC makes **no network calls by default**. Any future network feature (e.g. an optional external AI provider) MUST be opt-in, off by default, documented, and MUST show the user exactly what will be sent before sending; raw paths and file names are not sent unless the user approves that specific payload.
- **SEC-TRN-06** Logs MUST NOT contain file contents. Full paths are logged only at debug level. A `--redact` option replaces the home directory with `~` and masks file names in reports.
- **SEC-TRN-07** The audit log is append-only for applied actions (path, size, action, timestamp, plan ID).

---

## 5. Destructive Actions (Cleanup)

- **SEC-DEL-01** Cleanup is always **plan -> apply**. A plan is immutable, has an ID, a content hash and an expiry (default 1 hour). `apply` acts **only** on items in a valid, unexpired plan.
- **SEC-DEL-02** Dry-run/plan is the default. `--apply` requires interactive human confirmation that shows the item count and total size; the user must confirm explicitly (not just press Enter).
- **SEC-DEL-03** Eligibility by data class:

| Class | In plans? | Notes |
|---|---|---|
| `regenerable_cache` | Yes | Default Trash |
| `app_data` (e.g. Application Support, Containers, orphans) | Only by explicit per-item human selection | Warn "may contain user data" |
| `user_file` (Tier 1) | **Never automated** | Report-only; deletion only via explicit per-item human action in an interactive session |
| `application` | No (MCC does not uninstall apps) | Report-only |

- **SEC-DEL-04** The default action is **move to Trash**. Permanent deletion requires a separate explicit flag plus an extra confirmation and MUST NOT be reachable from MCP.
- **SEC-DEL-05** MCC MUST NOT delete: allowlisted root directories themselves (e.g. `~/Library/Caches`), anything outside the allowlist, SIP-protected paths, `com.apple.*`, or Tier 2/3 locations.
- **SEC-DEL-06** At apply time every item is **revalidated**: still inside an allowlisted root, not a symlink, same `(dev, inode)` as planned, and size/mtime unchanged within tolerance. Changed or unverifiable items are skipped and reported.
- **SEC-DEL-07** Caches belonging to a **running** application are skipped by default and reported.
- **SEC-DEL-08** Filesystem operations SHOULD use no-follow semantics (`O_NOFOLLOW`, `openat`-relative operations via `x/sys/unix`) to avoid TOCTOU races. Deleting through a shell (`rm -rf`) is forbidden.
- **SEC-DEL-09** Subprocesses (e.g. `osascript` for Trash) MUST use `exec.Command` with an absolute binary path and explicit argv. No `sh -c`, no string-interpolated paths; paths are passed as arguments (e.g. `on run argv`), never embedded in script text.
- **SEC-DEL-10** Failures leave a consistent state, are reported item by item, and MUST NOT trigger retries with elevated privileges.
- **SEC-DEL-11** Per-apply limits (max items/bytes) are configurable; large batches require stronger confirmation.

---

## 6. Orphan Detection Safety

Orphan detection is heuristic and can produce false positives.

- **SEC-ORP-01** Every orphan carries a confidence level (`high` exact bundle ID, `medium` team-prefix, `low` name-only). `low` is never included in a plan unless the user selects it explicitly.
- **SEC-ORP-02** If any app-discovery location is inaccessible or a volume that may host apps is unmounted, confidence is degraded and the report says why.
- **SEC-ORP-03** Orphan items are `app_data` (SEC-DEL-03) and show the "may contain user data" warning. `com.apple.*` and shared/team-ID data are excluded.

---

## 7. Input Validation and Secure Coding

- **SEC-COD-01** Paths MUST be cleaned, absolute, symlink-resolved and checked against roots with a **path-boundary** comparison (`/a/Caches` must not match `/a/Caches-evil`). Reject `..` and NUL bytes.
- **SEC-COD-02** File and app names are **untrusted data**. Terminal output MUST strip or escape control characters and ANSI escape sequences. JSON output MUST be valid UTF-8.
- **SEC-COD-03** SQL uses parameterized queries only.
- **SEC-COD-04** Concurrency, memory and time are bounded (worker pool, top-N heap, timeouts, context cancellation).
- **SEC-COD-05** Policy configuration (consents, allowlist additions) is read from a file with strict permissions; unknown keys are rejected; environment variables MUST NOT silently loosen policy.
- **SEC-COD-06** Errors do not expose more than necessary; no secrets in messages.
- **SEC-COD-07** `net/http` and other network packages MUST NOT be imported outside an explicitly approved optional package (enforce with `depguard`).

---

## 8. MCP Server Requirements (Phase 3 – design now, build later)

Purpose: let agents scan the system and learn the weight of caches, apps and heavy locations, and propose what to remove. **Agents are untrusted callers.**

- **SEC-MCP-01** Transport is **stdio only**. No HTTP/SSE listener. Any remote transport needs a separate security review.
- **SEC-MCP-02** Tool tiers:
  - *Read-only*: list apps, caches, orphans, large items, get report/summary.
  - *Plan*: `create_plan` persists a plan; it changes nothing on disk.
  - *Mutating*: `apply_plan`. **Disabled by default.** Enabling it requires a setting made by the user outside MCP, and **each call** requires human confirmation through a channel the agent cannot answer (terminal/TUI/OS dialog). Confirmation through tool arguments or agent text is never sufficient.
  - The first MCP release SHOULD ship read-only + plan only.
- **SEC-MCP-03** Plan/apply operations take **opaque IDs** (`entry_id`, `plan_id`) obtained from earlier results, never free-form paths. Read tools that accept paths are restricted to roots with existing consent.
- **SEC-MCP-04** Agents MUST NOT be able to grant consent, change configuration, tiers or allowlists, purge data or the audit log, or trigger scans of roots without consent.
- **SEC-MCP-05** Output is structured JSON. Paths are shortened with `~`. Names are sanitized (control characters stripped, length capped). Every response containing filesystem-derived strings declares them as untrusted data (e.g. `"untrusted_data": true`). File contents are never returned.
- **SEC-MCP-06** Tool descriptions and error messages are static: filesystem-derived text MUST NOT appear in tool descriptions or in anything phrased as an instruction (prompt-injection defense).
- **SEC-MCP-07** Data minimization: summaries by default, details on request, pagination with a default cap (e.g. top 50).
- **SEC-MCP-08** Every MCP call is audit-logged locally (tool, arguments, status, time). Rate limits and timeouts apply; one scan at a time.
- **SEC-MCP-09** Tier 2 is never exposed. Tier 1 metadata is exposed only with existing consent and always flagged `report_only`.

**MCP-readiness requirements for Phase 1/2 code (so Phase 3 is additive):**
- Services return structured domain values and never print; rendering lives in `cli/`/`tui/`.
- `plan` and `apply` are separate service operations; plans are persisted with stable IDs.
- Index entries have stable `entry_id`s.
- All access decisions go through `internal/policy`.

---

## 9. Supply Chain and Releases

- **SEC-SUP-01** Keep dependencies minimal and justified; commit `go.sum`; pin tool versions in CI.
- **SEC-SUP-02** CI runs `govulncheck` and `gosec`; dependency updates via Dependabot/Renovate; secret scanning (e.g. `gitleaks`) SHOULD be enabled.
- **SEC-SUP-03** Releases are built with `goreleaser` with published checksums. Signing and notarization of macOS binaries SHOULD be added before public distribution.
- **SEC-SUP-04** No `curl | sh` installers.

---

## 10. Required Security Tests

Changes touching access, cleanup or output MUST include tests for the relevant cases:

- Tier 2 paths never traversed; Tier 1/3 require consent; `--yes` does not grant consent.
- Path-boundary checks (`Caches` vs `Caches-evil`), `..`, symlink escape, NUL bytes.
- TOCTOU: item swapped for a symlink between plan and apply is skipped.
- Plan expiry, plan hash integrity, apply only affects planned items.
- Refusal to run as root; refusal to delete allowlisted roots, SIP paths, `com.apple.*`.
- Running-app caches skipped.
- Terminal-escape sanitization of hostile file names.
- Store directory `0700` / files `0600`.
- No network imports outside the approved package (lint).
- Never use the real home directory: use `t.TempDir()` and `testdata/`.

---

## 11. Reporting a Vulnerability

Do **not** open a public issue for security problems. Report privately to `<security-contact-email>` (placeholder: set before publishing) with a description, impact and reproduction steps. Expect an acknowledgment within 7 days. Only the latest released version is supported.

---

## 12. Security Definition of Done (PR / agent checklist)

- [ ] No file contents are read (SEC-ACC-02); no new access outside `internal/policy`.
- [ ] No Tier 2 access; Tier 1/3 gated by consent.
- [ ] Destructive paths go through plan -> apply with revalidation, Trash by default.
- [ ] No new network access, telemetry or privileged operation.
- [ ] Hostile file names are sanitized in every output.
- [ ] Subprocesses use explicit argv; SQL is parameterized.
- [ ] Required tests from section 10 added; `make check` and `govulncheck` pass.
- [ ] README, AGENTS.md and CHANGELOG.md updated if behavior or policy changed.