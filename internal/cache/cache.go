package cache

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/crisacm/mac-cache-cleaner/internal/domain"
)

// Index provides recorded sizes for indexed paths (implemented by store).
type Index interface {
	EntryTotalsByPath(ctx context.Context, scanID, path string) (domain.Totals, bool, error)
}

// Service reports the sizes of known cache locations.
type Service struct {
	index     Index
	home      string
	locations []Location
}

// New builds a Service using the built-in registry.
func New(index Index, home string) *Service {
	locs := make([]Location, len(Locations))
	copy(locs, Locations)
	return &Service{index: index, home: home, locations: locs}
}

// List returns the existing cache locations. When appFilter is non-empty only
// locations whose AppID or name contains it are returned. When scanID is empty
// or unknown, sizes are zero.
func (s *Service) List(ctx context.Context, scanID, appFilter string) ([]domain.CacheEntry, error) {
	var out []domain.CacheEntry
	for _, loc := range s.locations {
		if appFilter != "" && !matchesFilter(loc, appFilter) {
			continue
		}
		path := Resolve(s.home, loc)
		if _, err := os.Lstat(path); err != nil {
			continue // location does not exist on this machine
		}
		entry := domain.CacheEntry{
			Path:      path,
			AppID:     loc.AppID,
			Name:      loc.Name,
			DataClass: loc.DataClass,
			EntryID:   domain.ComputeEntryID(scanID, path),
		}
		if s.index != nil && scanID != "" {
			if tot, ok, err := s.index.EntryTotalsByPath(ctx, scanID, path); err == nil && ok {
				entry.AllocatedBytes = tot.AllocatedBytes
				entry.LogicalBytes = tot.LogicalBytes
			}
		}
		out = append(out, entry)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].AllocatedBytes != out[j].AllocatedBytes {
			return out[i].AllocatedBytes > out[j].AllocatedBytes
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func matchesFilter(loc Location, filter string) bool {
	f := strings.ToLower(filter)
	return strings.Contains(strings.ToLower(loc.AppID), f) ||
		strings.Contains(strings.ToLower(loc.Name), f) ||
		strings.Contains(strings.ToLower(loc.Rel), f)
}
