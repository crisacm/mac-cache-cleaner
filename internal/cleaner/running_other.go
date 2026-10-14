//go:build !darwin

package cleaner

// noRunningApps is the non-darwin stub: nothing is reported as running.
type noRunningApps struct{}

func newRunningApps() RunningApps { return noRunningApps{} }

func (noRunningApps) BundleIDs(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}
