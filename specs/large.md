# Spec: large

Status: implemented (Phase 1).

## Capability

`internal/large` lists heavy files and directories with a configurable
threshold (default 1 GiB) using a bounded top-N heap.

## Requirements

- **L-1** Default threshold is 1 GiB and is overridable with `--min`
  (AGENTS.md §8).
- **L-2** Results are bounded by a top-N limit (default 50); memory stays
  proportional to the limit, not to the tree (AGENTS.md §5.7, SEC-COD-04).
- **L-3** Entries stream from the index (`StreamEntries`); the whole tree is
  never loaded.
- **L-4** Tier 1 results carry the `user_file` class and are report-only; they
  never enter automated plans (SEC-ACC-04, SEC-DEL-03).
- **L-5** `--path` restricts results with a path-boundary comparison.

## API

- `New(index, threshold) *Service`
- `(*Service) List(ctx, scanID, Options) ([]domain.LargeItem, error)`
- `Options{Min, Limit, PathPrefix}`
