package proc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBundlePaths(t *testing.T) {
	out := "/usr/sbin/cfprefsd\n" +
		"/Applications/Safari.app/Contents/MacOS/Safari\n" +
		"/Applications/Safari.app/Contents/MacOS/Safari Helper\n" +
		"/System/Applications/Notes.app/Contents/MacOS/Notes\n" +
		"/Users/x/Applications/My App.app/Contents/MacOS/My App\n" +
		"not-a-path\n" +
		"\n"
	got := bundlePaths(out)
	require.Equal(t, []string{
		"/Applications/Safari.app",
		"/System/Applications/Notes.app",
		"/Users/x/Applications/My App.app",
	}, got, "unique .app bundles in order, without duplicates")
}

func TestBundlePathsEmpty(t *testing.T) {
	require.Empty(t, bundlePaths(""))
}
