package domain

import "errors"

// Process exit codes (AGENTS.md §4).
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitPartial = 3
	ExitBlocked = 4
)

// Sentinel errors that callers (internal/cli) map to exit codes.
var (
	// ErrNotFound means a requested item (scan, plan, consent) does not exist.
	ErrNotFound = errors.New("not found")
	// ErrUsage means the request was malformed (maps to exit code 2).
	ErrUsage = errors.New("usage error")
	// ErrBlocked means policy refused the operation (maps to exit code 4).
	ErrBlocked = errors.New("blocked by policy")
	// ErrPartial means the operation completed with inaccessible paths
	// (maps to exit code 3).
	ErrPartial = errors.New("partial result")
)
