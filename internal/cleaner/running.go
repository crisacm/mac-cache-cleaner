package cleaner

import (
	"context"

	"github.com/crisacm/mac-cache-cleaner/internal/proc"
)

// procRunningApps adapts internal/proc to the cleaner's RunningApps interface,
// so there is a single implementation of running-app discovery.
type procRunningApps struct{}

func (procRunningApps) BundleIDs(ctx context.Context) (map[string]struct{}, error) {
	return proc.RunningBundleIDs(ctx)
}

func newRunningApps() RunningApps { return procRunningApps{} }
