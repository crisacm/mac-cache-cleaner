package domain

import "time"

// Kind describes what a scanned entry is.
type Kind string

const (
	// KindDir is a directory.
	KindDir Kind = "dir"
	// KindFile is a regular file.
	KindFile Kind = "file"
	// KindSymlink is a symbolic link. It is recorded but never followed.
	KindSymlink Kind = "symlink"
	// KindPackage is an opaque library package (e.g. *.photoslibrary). Only its
	// size is recorded; it is never descended into.
	KindPackage Kind = "package"
	// KindApp is an application bundle.
	KindApp Kind = "app"
	// KindInaccessible marks a path that could not be read (EPERM/EACCES).
	KindInaccessible Kind = "inaccessible"
)

// Entry is a single indexed filesystem record.
//
// EntryID is stable: it is derived from the scan and the path so that plan and
// apply operations can refer to entries by ID rather than by free-form path
// (AGENTS.md §3, SEC-MCP-03).
type Entry struct {
	EntryID        string    `json:"entry_id"`
	ScanID         string    `json:"scan_id"`
	Path           string    `json:"path"`
	Parent         string    `json:"parent"`
	AllocatedBytes int64     `json:"allocated_bytes"`
	LogicalBytes   int64     `json:"logical_bytes"`
	MTime          time.Time `json:"mtime"`
	IsDir          bool      `json:"is_dir"`
	Kind           Kind      `json:"kind"`
	DataClass      DataClass `json:"data_class"`
	BundleID       string    `json:"bundle_id,omitempty"`
	// Dev and Inode identify the file for hardlink dedup and apply-time
	// revalidation (SEC-DEL-06).
	Dev   uint64 `json:"-"`
	Inode uint64 `json:"-"`
}
