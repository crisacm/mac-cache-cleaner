//go:build !darwin

package platform

import "os"

// stat is unavailable off macOS; callers fall back to logical sizes.
func stat(os.FileInfo) (StatInfo, bool) { return StatInfo{}, false }
