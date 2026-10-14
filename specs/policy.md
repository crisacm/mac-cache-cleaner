# Spec: policy

Status: implemented (Phase 1).

## Capability

`internal/policy` is the single package that decides what MCC may read, scan
or delete. Every other package asks it; no other package classifies access
(AGENTS.md §3, SECURITY.md §3).

## Requirements

- **P-1** The tier table (0–3) is compiled into the binary; configuration
  cannot downgrade a tier (SEC-ACC-03).
- **P-2** Tier 0 is allowed without asking; Tier 1 and Tier 3 require recorded,
  per-root consent; Tier 2 is always denied and no consent can enable it
  (SEC-ACC-04).
- **P-3** No code path turns a flag into consent: with an empty consent store a
  Tier 1/3 root is `consent_required` (SEC-ACC-07).
- **P-4** Paths must be absolute, cleaned, free of NUL and `..`, and compared
  with a path-boundary check (`/a/Caches` ≠ `/a/Caches-evil`) (SEC-COD-01).
- **P-5** Symlink resolution must not let a path escape its declared root
  (SEC-ACC-06).
- **P-6** The process refuses `euid == 0` (SEC-ACC-01).
- **P-7** Deletion refuses allowlisted roots themselves, anything outside the
  allowlist, Tier 1/2/3 locations, SIP paths and `com.apple.*` (SEC-DEL-05).
- **P-8** Tier 1 results are report-only (SEC-ACC-04).

## API

- `New(home, ConsentStore) *Policy`
- `(*Policy) Classify(path) (tier, rule, ok)`
- `(*Policy) RootVerdict(ctx, root) (Verdict, error)`
- `(*Policy) PathVerdict(root, path) Verdict`
- `(*Policy) DeleteVerdict(path, bundleID) Verdict`
- `(*Policy) AllowlistRoots() []string`, `(*Policy) Rules() []Rule`
- `NormalizePath`, `ResolvePath`, `UnderRoot`, `IsSIP`, `IsAppleBundleID`
- `EnsureNotRoot()`, `CheckNotRoot(euid)`

## Acceptance criteria

- Tests in `internal/policy/policy_test.go` cover: tier classification,
  Tier 2 win over Tier 0, longest-prefix/sibling-prefix boundary, `..`/NUL,
  symlink escape, consent gating, `--yes`-style non-granting, root refusal,
  and delete refusals (SECURITY.md §10).
