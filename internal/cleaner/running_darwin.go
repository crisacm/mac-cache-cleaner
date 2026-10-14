//go:build darwin

package cleaner

import (
	"context"
	"os/exec"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
)

// psPath is the absolute path to ps (SEC-DEL-09).
const psPath = "/bin/ps"

// psRunningApps derives running application bundle IDs by inspecting the
// executable paths of running processes and reading their bundles' Info.plist
// (metadata only, no consent prompt).
type psRunningApps struct{}

func newRunningApps() RunningApps { return psRunningApps{} }

// BundleIDs returns the bundle IDs of running applications. Failures yield an
// empty set: the cleaner then simply skips nothing.
func (psRunningApps) BundleIDs(ctx context.Context) (map[string]struct{}, error) {
	cmd := exec.CommandContext(ctx, psPath, "-axo", "comm")
	out, err := cmd.Output()
	if err != nil {
		return map[string]struct{}{}, nil
	}

	set := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, ".app/")
		if i < 0 {
			continue
		}
		appPath := line[:i+len(".app")]
		if _, ok := seen[appPath]; ok {
			continue
		}
		seen[appPath] = struct{}{}
		if info, err := apps.ReadBundleInfo(appPath); err == nil && info.BundleID != "" {
			set[info.BundleID] = struct{}{}
		}
	}
	return set, nil
}
