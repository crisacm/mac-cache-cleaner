// Package proc discovers currently running applications.
//
// It is metadata-only: it inspects process executable paths and reads the
// bundles' Info.plist (the single allowed content read, SEC-ACC-02). It never
// prompts for macOS privacy permissions.
package proc

import "strings"

// appMarker is the suffix that identifies an application bundle in an
// executable path.
const appMarker = ".app/"

// bundlePaths extracts the unique .app bundle paths from `ps` output. It is a
// pure function so it can be tested without touching the process table.
func bundlePaths(psOutput string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, line := range strings.Split(psOutput, "\n") {
		line = strings.TrimSpace(line)
		i := strings.Index(line, appMarker)
		if i < 0 {
			continue
		}
		path := line[:i+len(".app")]
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
