package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

const (
	// DefaultCacheTTL is the default cache TTL used when no config file is present.
	DefaultCacheTTL = 60 * time.Second

	// DefaultArchiveRetention is the default time after which archived worktrees
	// become eligible for permanent cleanup (10 days).
	DefaultArchiveRetention = 240 * time.Hour

	// DefaultMaintenanceInterval is the default interval between background
	// maintenance sweeps.
	DefaultMaintenanceInterval = 10 * time.Minute

	globalConfigFileName    = "config.yml"
	globalConfigPermissions = 0o600
)

// GlobalConfig holds the global wtp configuration stored in $XDG_CONFIG_HOME/wtp/config.yml.
type GlobalConfig struct {
	CacheTTL            time.Duration `yaml:"cache_ttl"`
	ArchiveRetention    time.Duration `yaml:"archive_retention"`
	MaintenanceInterval time.Duration `yaml:"maintenance_interval"`
}

// MarshalYAML serializes GlobalConfig, encoding CacheTTL as a human-readable
// duration string (e.g. "1m0s").
func (c GlobalConfig) MarshalYAML() (any, error) {
	return struct {
		CacheTTL            string `yaml:"cache_ttl"`
		ArchiveRetention    string `yaml:"archive_retention"`
		MaintenanceInterval string `yaml:"maintenance_interval"`
	}{
		CacheTTL:            c.CacheTTL.String(),
		ArchiveRetention:    c.ArchiveRetention.String(),
		MaintenanceInterval: c.MaintenanceInterval.String(),
	}, nil
}

// UnmarshalYAML deserialises GlobalConfig, accepting CacheTTL as either a
// duration string (e.g. "60s", "5m") or an integer number of seconds.
func (c *GlobalConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		CacheTTL            yaml.Node `yaml:"cache_ttl"`
		ArchiveRetention    yaml.Node `yaml:"archive_retention"`
		MaintenanceInterval yaml.Node `yaml:"maintenance_interval"`
	}

	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}

	var parseErr error

	c.CacheTTL, parseErr = parseDurationNode(&r.CacheTTL, DefaultCacheTTL, "cache_ttl")
	if parseErr != nil {
		return parseErr
	}

	c.ArchiveRetention, parseErr = parseDurationNode(&r.ArchiveRetention, DefaultArchiveRetention, "archive_retention")
	if parseErr != nil {
		return parseErr
	}

	c.MaintenanceInterval, parseErr = parseDurationNode(
		&r.MaintenanceInterval, DefaultMaintenanceInterval, "maintenance_interval",
	)
	if parseErr != nil {
		return parseErr
	}

	return nil
}

// parseDurationNode parses a yaml.Node as a duration. It tries time.ParseDuration
// first, falls back to integer seconds, and uses the given default when the node
// is absent (Kind == 0).
func parseDurationNode(node *yaml.Node, defaultVal time.Duration, fieldName string) (time.Duration, error) {
	if node.Kind == 0 {
		return defaultVal, nil
	}

	dur, err := time.ParseDuration(node.Value)
	if err == nil {
		return dur, nil
	}

	var secs int64
	if decErr := node.Decode(&secs); decErr == nil {
		return time.Duration(secs) * time.Second, nil
	}

	return 0, fmt.Errorf("cannot parse %s %q as a duration: %w", fieldName, node.Value, err)
}

// globalConfigPath returns the canonical path to the global config file.
func globalConfigPath() string {
	return filepath.Join(xdg.WtpConfigDir(), globalConfigFileName)
}

// LoadGlobalConfig reads the global config from $XDG_CONFIG_HOME/wtp/config.yml.
// If the file does not exist, a default GlobalConfig is returned without error.
func LoadGlobalConfig() (GlobalConfig, error) {
	path := globalConfigPath()

	data, err := os.ReadFile(path) //nolint:gosec // path is derived from XDG env / home dir
	if errors.Is(err, os.ErrNotExist) {
		return GlobalConfig{
			CacheTTL:            DefaultCacheTTL,
			ArchiveRetention:    DefaultArchiveRetention,
			MaintenanceInterval: DefaultMaintenanceInterval,
		}, nil
	}

	if err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to read global config: %w", err)
	}

	var cfg GlobalConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return GlobalConfig{}, fmt.Errorf("failed to parse global config: %w", err)
	}

	return cfg, nil
}

// SaveGlobalConfig writes cfg to $XDG_CONFIG_HOME/wtp/config.yml.
// The directory is created if it does not exist, and the write is atomic
// (write to a temp file then rename).
func SaveGlobalConfig(cfg GlobalConfig) error {
	path := globalConfigPath()

	if err := xdg.EnsureDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal global config: %w", err)
	}

	// Atomic write: temp file + rename.
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, data, globalConfigPermissions); err != nil {
		return fmt.Errorf("failed to write global config temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Best-effort cleanup of the temp file.
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to finalize global config write: %w", err)
	}

	return nil
}

// EnsureGlobalConfig loads the global config if the file exists, or writes the
// defaults and returns them if it does not. The resulting config is returned
// either way.
func EnsureGlobalConfig() (GlobalConfig, error) {
	path := globalConfigPath()

	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		defaults := GlobalConfig{
			CacheTTL:            DefaultCacheTTL,
			ArchiveRetention:    DefaultArchiveRetention,
			MaintenanceInterval: DefaultMaintenanceInterval,
		}
		if saveErr := SaveGlobalConfig(defaults); saveErr != nil {
			return GlobalConfig{}, fmt.Errorf("failed to create default global config: %w", saveErr)
		}

		return defaults, nil
	}

	cfg, err := LoadGlobalConfig()
	if err != nil {
		return GlobalConfig{}, err
	}

	return cfg, nil
}
