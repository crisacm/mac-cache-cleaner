# Spec: scan

Status: implemented (Phase 1).

## Capability

`internal/scanner` walks scan roots and records real disk usage. It streams
entries to a sink so the CLI can persist them without holding the tree in
memory.

## Requirements

- **S-1** Size on disk is `st_blocks * 512`; the logical size (`st_size`) is
  stored alongside it (AGENTS.md §5.1).
- **S-2** Hardlinks are deduplicated by `(dev, inode)` (§5.2).
- **S-3** Symlinks are never followed; they are recorded and not descended
  (§5.3).
- **S-4** Devices are not crossed; subdirectories on another device are skipped
  and reported (§5.4).
- **S-5** `EPERM`/`EACCES` and other read errors are non-fatal: they are
  collected, the result is marked partial (exit code 3) and scanning continues
  (§5.5, SEC-ACC-05).
- **S-6** Walking uses a bounded worker pool; goroutines never exceed the
  configured worker count (§5.6, SEC-COD-04).
- **S-7** Library packages (`*.photoslibrary`, …) are opaque: one entry, size
  only, never descended (§5.9, SEC-ACC-03).
- **S-8** Incremental scans reuse a directory's recorded `mtime` to skip and
  copy unchanged subtrees (§5.8).
- **S-9** Metadata only: no file contents are read (SEC-ACC-02).
- **S-10** Every result carries its data class (SEC-ACC-08). Roots that need
  consent or are forbidden are rejected before walking (SEC-ACC-04).

## API

- `New(policy) *Scanner`, `NewWithClock(policy, now)`
- `(*Scanner) Scan(ctx, Options) (Result, error)`
- `Options{ScanID, Roots, Full, Workers, BatchSize, Emit, Incremental}`
- `IncrementalIndex` — `SubtreeMTime`, `CopySubtree`

`store.NewIncremental(prevScan, scanID)` adapts a `*store.Store` to
`IncrementalIndex`.

## Notes / limitations

- Directory roll-up (subtree sizes) is computed in memory from a map of
  directory metadata; for very large trees this is the scanner's main memory
  cost. File entries stream to `Emit` in batches.
- `Result.Totals` sums the subtree sizes of the declared roots; `ByClass` and
  `ByKind` sum leaf-like entries, so they never double count directory sizes.
