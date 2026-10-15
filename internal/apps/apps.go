// Package apps discovers installed applications and reads their Info.plist.
//
// It reads metadata only. The single exception to MCC's no-content rule is
// read-only parsing of Info.plist inside .app bundles, capped at 1 MiB
// (SEC-ACC-02).
package apps

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// maxPlistSize caps how much of Info.plist MCC will read (SEC-ACC-02).
const maxPlistSize = 1 << 20

// Index provides recorded sizes for indexed paths (implemented by store).
type Index interface {
	EntryTotalsByPath(ctx context.Context, scanID, path string) (domain.Totals, bool, error)
}

// Service lists installed applications.
type Service struct {
	index Index
	home  string
	roots []string
}

// New builds a Service. roots are the application directories to scan
// (defaults to ~/Applications and /Applications).
func New(index Index, home string, roots ...string) *Service {
	if len(roots) == 0 {
		roots = []string{
			filepath.Join(home, "Applications"),
			"/Applications",
		}
	}
	return &Service{index: index, home: home, roots: roots}
}

// List returns the discovered applications, sorted by size (descending) then
// name. When scanID is empty or unknown, sizes are zero.
func (s *Service) List(ctx context.Context, scanID string) ([]domain.App, error) {
	return s.ListWithProgress(ctx, scanID, nil)
}

// ListWithProgress is List with an optional progress callback that receives the
// path currently being read (for a loading indicator). The callback must not
// block.
func (s *Service) ListWithProgress(ctx context.Context, scanID string, progress func(string)) ([]domain.App, error) {
	report := func(p string) {
		if progress != nil {
			progress(p)
		}
	}
	var out []domain.App
	for _, root := range s.roots {
		des, err := os.ReadDir(root)
		if err != nil {
			// Missing or unreadable application directories are not fatal.
			continue
		}
		for _, de := range des {
			if !de.IsDir() || !strings.HasSuffix(strings.ToLower(de.Name()), ".app") {
				continue
			}
			full := filepath.Join(root, de.Name())
			report(full)
			app := domain.App{Path: full, Name: strings.TrimSuffix(de.Name(), ".app"), Tier: domain.TierDefault}

			if info, err := ReadBundleInfo(full); err == nil {
				if info.BundleID != "" {
					app.BundleID = info.BundleID
				}
				if info.Name != "" {
					app.Name = info.Name
				}
				app.Version = info.Version
			}

			if s.index != nil && scanID != "" {
				if tot, ok, err := s.index.EntryTotalsByPath(ctx, scanID, full); err == nil && ok {
					app.AllocatedBytes = tot.AllocatedBytes
					app.LogicalBytes = tot.LogicalBytes
				}
				app.DataBytes = s.dataSize(ctx, scanID, app.BundleID)
			}
			out = append(out, app)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].AllocatedBytes != out[j].AllocatedBytes {
			return out[i].AllocatedBytes > out[j].AllocatedBytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// dataSize sums the known per-app data locations for a bundle ID.
func (s *Service) dataSize(ctx context.Context, scanID, bundleID string) int64 {
	if bundleID == "" || s.index == nil {
		return 0
	}
	var total int64
	for _, p := range dataPaths(s.home, bundleID) {
		if tot, ok, err := s.index.EntryTotalsByPath(ctx, scanID, p); err == nil && ok {
			total += tot.AllocatedBytes
		}
	}
	return total
}

// dataPaths returns the conventional data locations for a bundle ID.
func dataPaths(home, bundleID string) []string {
	return []string{
		filepath.Join(home, "Library", "Caches", bundleID),
		filepath.Join(home, "Library", "Application Support", bundleID),
		filepath.Join(home, "Library", "Containers", bundleID),
		filepath.Join(home, "Library", "HTTPStorages", bundleID),
		filepath.Join(home, "Library", "WebKit", bundleID),
		filepath.Join(home, "Library", "Saved Application State", bundleID+".savedState"),
	}
}

// InstalledBundleIDs returns the bundle IDs and display names of installed
// applications, plus whether discovery was complete (every application root
// was readable). It is used by orphan detection (SEC-ORP-02).
func (s *Service) InstalledBundleIDs(_ context.Context) (map[string]struct{}, bool) {
	set := map[string]struct{}{}
	complete := true
	for _, root := range s.roots {
		des, err := os.ReadDir(root)
		if err != nil {
			complete = false
			continue
		}
		for _, de := range des {
			if !de.IsDir() || !strings.HasSuffix(strings.ToLower(de.Name()), ".app") {
				continue
			}
			set[strings.TrimSuffix(de.Name(), ".app")] = struct{}{}
			if info, err := ReadBundleInfo(filepath.Join(root, de.Name())); err == nil && info.BundleID != "" {
				set[info.BundleID] = struct{}{}
			}
		}
	}
	return set, complete
}

// BundleInfo is the subset of Info.plist MCC reads.
type BundleInfo struct {
	BundleID string
	Name     string
	Version  string
}

// ReadBundleInfo parses the Info.plist of an .app bundle, size-capped.
func ReadBundleInfo(appPath string) (BundleInfo, error) {
	return readBundleInfo(appPath, maxPlistSize)
}

func readBundleInfo(appPath string, limitBytes int64) (BundleInfo, error) {
	plistPath := filepath.Join(appPath, "Contents", "Info.plist")
	fi, err := os.Stat(plistPath)
	if err != nil {
		return BundleInfo{}, fmt.Errorf("apps: stat %s: %w", plistPath, err)
	}
	if fi.Size() > limitBytes {
		return BundleInfo{}, fmt.Errorf("apps: %s exceeds %d bytes", plistPath, limitBytes)
	}

	f, err := os.Open(plistPath) // #nosec G304 -- path is derived from a discovered .app bundle
	if err != nil {
		return BundleInfo{}, fmt.Errorf("apps: open %s: %w", plistPath, err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, limitBytes))
	if err != nil {
		return BundleInfo{}, fmt.Errorf("apps: read %s: %w", plistPath, err)
	}

	var raw map[string]any
	if err := decodePlist(data, &raw); err != nil {
		return BundleInfo{}, fmt.Errorf("apps: decode %s: %w", plistPath, err)
	}
	return BundleInfo{
		BundleID: str(raw["CFBundleIdentifier"]),
		Name:     firstNonEmpty(str(raw["CFBundleDisplayName"]), str(raw["CFBundleName"])),
		Version:  str(raw["CFBundleShortVersionString"]),
	}, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
