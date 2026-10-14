package domain

import "time"

// Confidence expresses how sure orphan detection is (SEC-ORP-01).
type Confidence string

const (
	// ConfidenceHigh matches an exact bundle ID.
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium matches a team prefix.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow matches by name only.
	ConfidenceLow Confidence = "low"
)

// Rank returns a numeric weight; higher is more confident.
func (c Confidence) Rank() int {
	switch c {
	case ConfidenceHigh:
		return 3
	case ConfidenceMedium:
		return 2
	case ConfidenceLow:
		return 1
	default:
		return 0
	}
}

// Scan describes one scan run.
type Scan struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Root       string    `json:"root"`
	Roots      []string  `json:"roots,omitempty"`
	Full       bool      `json:"full"`
	Partial    bool      `json:"partial"`
}

// Totals aggregates sizes and entry counts.
type Totals struct {
	Entries        int64 `json:"entries"`
	AllocatedBytes int64 `json:"allocated_bytes"`
	LogicalBytes   int64 `json:"logical_bytes"`
}

// Inaccessible records a path MCC could not read. It is a partial result, not
// a fatal error (SEC-ACC-05).
type Inaccessible struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ScanResult is the structured outcome of a scan.
type ScanResult struct {
	Scan         Scan                   `json:"scan"`
	Totals       Totals                 `json:"totals"`
	ByClass      map[DataClass]Totals   `json:"by_class"`
	Inaccessible []Inaccessible         `json:"inaccessible,omitempty"`
	ByKind       map[Kind]Totals        `json:"by_kind,omitempty"`
	Skipped      []string               `json:"-"`
	Extra        map[string]interface{} `json:"-"`
}

// App is an installed application and the disk space it occupies.
type App struct {
	BundleID       string `json:"bundle_id"`
	Name           string `json:"name"`
	Path           string `json:"path"`
	Version        string `json:"version,omitempty"`
	AllocatedBytes int64  `json:"allocated_bytes"`
	LogicalBytes   int64  `json:"logical_bytes"`
	// DataBytes is the disk space of this app's associated data directories.
	DataBytes int64 `json:"data_bytes"`
	// Tier is the access tier the app bundle lives in.
	Tier Tier `json:"-"`
}

// CacheEntry is one cache location on disk.
type CacheEntry struct {
	Path           string    `json:"path"`
	AppID          string    `json:"app_id,omitempty"`
	Name           string    `json:"name"`
	DataClass      DataClass `json:"data_class"`
	AllocatedBytes int64     `json:"allocated_bytes"`
	LogicalBytes   int64     `json:"logical_bytes"`
	EntryID        string    `json:"entry_id"`
}

// Orphan is leftover data whose owning application is no longer installed.
type Orphan struct {
	Path           string     `json:"path"`
	BundleID       string     `json:"bundle_id,omitempty"`
	Name           string     `json:"name"`
	Confidence     Confidence `json:"confidence"`
	DataClass      DataClass  `json:"data_class"`
	AllocatedBytes int64      `json:"allocated_bytes"`
	LogicalBytes   int64      `json:"logical_bytes"`
	EntryID        string     `json:"entry_id"`
}

// LargeItem is a heavy file or directory (default threshold 1 GB).
type LargeItem struct {
	Path           string    `json:"path"`
	IsDir          bool      `json:"is_dir"`
	Kind           Kind      `json:"kind"`
	DataClass      DataClass `json:"data_class"`
	AllocatedBytes int64     `json:"allocated_bytes"`
	LogicalBytes   int64     `json:"logical_bytes"`
	EntryID        string    `json:"entry_id"`
}

// SubtreeTotals aggregates the sizes of a subtree. It is returned when an
// unchanged subtree is reused during an incremental scan.
type SubtreeTotals struct {
	Totals  Totals               `json:"totals"`
	ByClass map[DataClass]Totals `json:"by_class,omitempty"`
	ByKind  map[Kind]Totals      `json:"by_kind,omitempty"`
}
