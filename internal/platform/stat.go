// Package platform isolates macOS-specific syscalls behind build tags.
//
// On other platforms the stubs return ok=false so the rest of the program can
// degrade gracefully (AGENTS.md §8).
package platform

import "os"

// StatInfo carries the stat(2) fields MCC needs.
type StatInfo struct {
	// Dev is the device ID (st_dev); used to avoid crossing devices.
	Dev uint64
	// Inode is the inode number (st_ino); used for hardlink dedup.
	Inode uint64
	// AllocatedBytes is st_blocks * 512 — the real size on disk (AGENTS.md §5).
	AllocatedBytes int64
	// LogicalBytes is st_size — the apparent size.
	LogicalBytes int64
	// Nlink is the hardlink count (st_nlink).
	Nlink uint64
}

// Stat returns the platform fields for info. ok is false when unavailable.
func Stat(info os.FileInfo) (StatInfo, bool) {
	return stat(info)
}
