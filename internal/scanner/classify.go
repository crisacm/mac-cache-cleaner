package scanner

import (
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// libraryPackageSuffixes are opaque library packages: MCC records their size
// but never descends into them (AGENTS.md §5.9, SEC-ACC-03 Tier 2).
var libraryPackageSuffixes = []string{
	".photoslibrary",
	".photolibrary",
	".musiclibrary",
	".tvlibrary",
	".imovielibrary",
	".aplibrary",
	".fcpbundle",
	".logicx",
	".sparsebundle",
	".mylibrary",
}

// isLibraryPackage reports whether path is an opaque library package.
func isLibraryPackage(path string) bool {
	lower := strings.ToLower(path)
	for _, s := range libraryPackageSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return false
}

// isAppBundle reports whether path is an application bundle.
func isAppBundle(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".app")
}

// regenerableMarkers identify cache-like locations, whose data an application
// can regenerate (SEC-ACC-08). They are matched as path segments.
var regenerableMarkers = []string{
	"/Library/Caches",
	"/Library/Logs",
	"/Library/HTTPStorages",
	"/Library/WebKit",
	"/Library/Saved Application State",
	"/Library/Developer/Xcode/DerivedData",
	"/.gradle/caches",
	"/.android/avd",
	"/.npm",
	"/.cache/pnpm",
	"/Library/pnpm/store",
	"/go/pkg/mod",
}

// isRegenerable reports whether path lies under a regenerable cache location.
func isRegenerable(path string) bool {
	for _, m := range regenerableMarkers {
		if path == m || strings.HasPrefix(path, m+"/") || strings.Contains(path, m+"/") {
			return true
		}
	}
	return false
}

// classFor derives the data class of an entry from its path, tier and kind
// (SEC-ACC-08).
func classFor(path string, tier domain.Tier, kind domain.Kind) domain.DataClass {
	switch {
	case kind == domain.KindApp || strings.Contains(path, ".app/"):
		return domain.ClassApplication
	case tier == domain.TierUserData:
		return domain.ClassUserFile
	case isRegenerable(path):
		return domain.ClassRegenerableCache
	default:
		return domain.ClassAppData
	}
}
