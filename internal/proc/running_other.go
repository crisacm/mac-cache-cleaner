//go:build !darwin

package proc

import "context"

// RunningBundleIDs is unavailable off macOS: nothing is reported as running.
func RunningBundleIDs(context.Context) (map[string]struct{}, error) {
	return map[string]struct{}{}, nil
}
