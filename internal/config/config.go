// Package config loads MCC's typed configuration from an optional file.
//
// Configuration can only tune non-policy knobs (workers, thresholds, paths).
// It can never loosen a policy tier or grant consent, and unknown keys are
// rejected (SEC-COD-05, SEC-ACC-03).
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// knownKeys is the closed set of accepted configuration keys.
var knownKeys = map[string]struct{}{
	"data_dir":        {},
	"workers":         {},
	"large_threshold": {},
	"plan_ttl":        {},
	"max_items":       {},
	"max_bytes":       {},
	"no_color":        {},
	"retain_scans":    {},
}

// Config is MCC's runtime configuration.
type Config struct {
	// DataDir is where the index, plans and consents are stored.
	DataDir string
	// Workers bounds scanner concurrency.
	Workers int
	// LargeThreshold is the default "--min" for `mcc large`, in bytes.
	LargeThreshold int64
	// PlanTTL is how long a cleanup plan stays valid.
	PlanTTL time.Duration
	// MaxItems and MaxBytes bound a single plan / apply.
	MaxItems int
	MaxBytes int64
	// RetainScans is how many completed scans are kept after a scan (index
	// retention; 1 means the index is reset on each scan).
	RetainScans int
	// NoColor disables ANSI colour in human output.
	NoColor bool
}

// Default returns the default configuration for a home directory.
func Default(home string) Config {
	return Config{
		DataDir:        filepath.Join(home, "Library", "Application Support", "mcc"),
		Workers:        0, // 0 lets the scanner choose
		LargeThreshold: 1 << 30,
		PlanTTL:        time.Hour,
		MaxItems:       1000,
		MaxBytes:       0,
		RetainScans:    1,
		NoColor:        false,
	}
}

// Load reads configuration from the config directory (if any) and the
// environment, then returns the typed result.
func Load(home string) (Config, error) {
	cfg := Default(home)

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(filepath.Join(home, ".config", "mcc"))
	v.SetEnvPrefix("MCC")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("data_dir", cfg.DataDir)
	v.SetDefault("workers", cfg.Workers)
	v.SetDefault("large_threshold", cfg.LargeThreshold)
	v.SetDefault("plan_ttl", cfg.PlanTTL.String())
	v.SetDefault("max_items", cfg.MaxItems)
	v.SetDefault("max_bytes", cfg.MaxBytes)
	v.SetDefault("no_color", cfg.NoColor)
	v.SetDefault("retain_scans", cfg.RetainScans)

	if err := v.ReadInConfig(); err != nil {
		if !isNotFound(err) {
			return Config{}, fmt.Errorf("config: read: %w", err)
		}
	}

	for _, key := range v.AllKeys() {
		if _, ok := knownKeys[key]; !ok {
			return Config{}, fmt.Errorf("config: unknown key %q", key)
		}
	}

	cfg.DataDir = v.GetString("data_dir")
	cfg.Workers = v.GetInt("workers")
	cfg.LargeThreshold = v.GetInt64("large_threshold")
	cfg.MaxItems = v.GetInt("max_items")
	cfg.MaxBytes = v.GetInt64("max_bytes")
	cfg.NoColor = v.GetBool("no_color")
	cfg.RetainScans = v.GetInt("retain_scans")

	ttl, err := time.ParseDuration(v.GetString("plan_ttl"))
	if err != nil {
		return Config{}, fmt.Errorf("config: plan_ttl: %w", err)
	}
	cfg.PlanTTL = ttl

	if cfg.DataDir == "" {
		return Config{}, fmt.Errorf("config: data_dir must not be empty")
	}
	if cfg.Workers < 0 {
		return Config{}, fmt.Errorf("config: workers must not be negative")
	}
	if cfg.RetainScans < 1 {
		return Config{}, fmt.Errorf("config: retain_scans must be at least 1")
	}
	return cfg, nil
}

func isNotFound(err error) bool {
	var notFound viper.ConfigFileNotFoundError
	if errors.As(err, &notFound) {
		return true
	}
	// viper may wrap a *os.PathError when the config dir does not exist.
	return os.IsNotExist(err)
}
