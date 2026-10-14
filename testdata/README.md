# testdata

Fixtures for MCC.

Because some required fixture elements cannot be committed faithfully to git
(hardlinks, sparse files, symlinks, file names containing control characters),
the fake `~/Library` tree is **generated at test time inside `t.TempDir()`** by
Go helpers in the test files (see `internal/scanner/scanner_test.go`). Nothing
destructive is ever run against the real home directory (AGENTS.md §9,
SECURITY.md §10).

This directory holds committed artifacts only: golden files for CLI output
(`*.golden`) and static fragments that do not require special filesystem
features.
