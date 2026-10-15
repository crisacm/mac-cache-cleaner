//go:build darwin

package proc

import (
	"context"
	"os/exec"

	"github.com/crisacm/mac-cache-cleaner/internal/apps"
)

// psPath is the absolute path to ps (SEC-DEL-09).
const psPath = "/bin/ps"

// RunningBundleIDs returns the bundle IDs of running applications by inspecting
// the executable paths of running processes and reading their bundles'
// Info.plist. Failures yield an empty set.
func RunningBundleIDs(ctx context.Context) (map[string]struct{}, error) {
	cmd := exec.CommandContext(ctx, psPath, "-axo", "comm")
	out, err := cmd.Output()
	if err != nil {
		return map[string]struct{}{}, nil
	}

	set := map[string]struct{}{}
	for _, path := range bundlePaths(string(out)) {
		if info, err := apps.ReadBundleInfo(path); err == nil && info.BundleID != "" {
			set[info.BundleID] = struct{}{}
		}
	}
	return set, nil
}
