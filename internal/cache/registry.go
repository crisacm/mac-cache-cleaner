// Package cache holds the data-driven registry of cache locations and the
// service that reports their sizes. New locations are added as data, never as
// new code paths (AGENTS.md §6).
package cache

import (
	"path/filepath"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// Location is one known cache or app-data location.
type Location struct {
	Name      string           `json:"name"`
	Rel       string           `json:"rel"`
	DataClass domain.DataClass `json:"data_class"`
	AppID     string           `json:"app_id,omitempty"`
	Tier      domain.Tier      `json:"tier"`
}

// Locations is the data-driven registry. Rel is home-relative unless it starts
// with "/".
var Locations = []Location{
	{Name: "Caches", Rel: "Library/Caches", DataClass: domain.ClassRegenerableCache},
	{Name: "Logs", Rel: "Library/Logs", DataClass: domain.ClassRegenerableCache},
	{Name: "Application Support", Rel: "Library/Application Support", DataClass: domain.ClassAppData},
	{Name: "Containers", Rel: "Library/Containers", DataClass: domain.ClassAppData},
	{Name: "Group Containers", Rel: "Library/Group Containers", DataClass: domain.ClassAppData},
	{Name: "HTTPStorages", Rel: "Library/HTTPStorages", DataClass: domain.ClassRegenerableCache},
	{Name: "WebKit", Rel: "Library/WebKit", DataClass: domain.ClassRegenerableCache},
	{Name: "Saved Application State", Rel: "Library/Saved Application State", DataClass: domain.ClassRegenerableCache},
	{Name: "Xcode DerivedData", Rel: "Library/Developer/Xcode/DerivedData", DataClass: domain.ClassRegenerableCache},
	{Name: "Xcode Archives", Rel: "Library/Developer/Xcode/Archives", DataClass: domain.ClassAppData},
	{Name: "Xcode DeviceSupport", Rel: "Library/Developer/Xcode/iOS DeviceSupport", DataClass: domain.ClassRegenerableCache},
	{Name: "Gradle caches", Rel: ".gradle/caches", DataClass: domain.ClassRegenerableCache},
	{Name: "Android AVD", Rel: ".android/avd", DataClass: domain.ClassRegenerableCache},
	{Name: "npm cache", Rel: ".npm", DataClass: domain.ClassRegenerableCache},
	{Name: "pnpm store", Rel: ".cache/pnpm", DataClass: domain.ClassRegenerableCache},
	{Name: "pnpm store (Library)", Rel: "Library/pnpm/store", DataClass: domain.ClassRegenerableCache},
	{Name: "Go module cache", Rel: "go/pkg/mod", DataClass: domain.ClassRegenerableCache},
	{Name: "Homebrew cache", Rel: "Library/Caches/Homebrew", DataClass: domain.ClassRegenerableCache},
	{Name: "CocoaPods cache", Rel: "Library/Caches/CocoaPods", DataClass: domain.ClassRegenerableCache},
	{Name: "Yarn cache", Rel: "Library/Caches/Yarn", DataClass: domain.ClassRegenerableCache},
	{Name: "Cargo registry", Rel: ".cargo/registry", DataClass: domain.ClassRegenerableCache},
	{Name: "Docker data", Rel: "Library/Containers/com.docker.docker", DataClass: domain.ClassAppData, AppID: "com.docker.docker"},
}

// Resolve expands a location's Rel against home.
func Resolve(home string, loc Location) string {
	if strings.HasPrefix(loc.Rel, "/") {
		return filepath.Clean(loc.Rel)
	}
	return filepath.Join(home, loc.Rel)
}
