//go:build darwin

package platform

import (
	"os"
	"syscall"
)

// stat reads the st_dev/st_ino/st_blocks/st_size fields on macOS.
func stat(info os.FileInfo) (StatInfo, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return StatInfo{}, false
	}
	return StatInfo{
		Dev:            uint64(st.Dev), // #nosec G115 -- dev_t is non-negative
		Inode:          uint64(st.Ino), // #nosec G115 -- ino_t is non-negative
		AllocatedBytes: int64(st.Blocks) * 512,
		LogicalBytes:   st.Size,
		Nlink:          uint64(st.Nlink), // #nosec G115 -- nlink is non-negative
	}, true
}
