package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/satococoa/wtp/v3/internal/xdg"
)

const (
	// DefaultCacheTTL is the default cache TTL used when no config file is present.
	DefaultCacheTTL = 60 * time.Second

	globalConfigFileName    = "config.yml"
	globalConfigPermissions = 0o600
)

// GlobalConfig holds the global wtp configuration stored in $XDG_CONFIG_HOME/wtp/config.yml.
type GlobalConfig struct {
	CacheTTL time.Duration `yaml:"cache_ttl"`
}

// MarshalYAML serializes GlobalConfig, encoding CacheTTL as a human-readable
// duration string (e.g. "1m0s").
func (c GlobalConfig) MarshalYAML() (interface{}, error) {
	return struct {
		CacheTTL string `yaml:"cache_ttl"`
	}{
		CacheTTL: c.CacheTTL.String(),
	}, nil
}

// UnmarshalYAML deserialises GlobalConfig, accepting CacheTTL as either a
// duration string (e.g. "60s", "5m") or an integer number of seconds.
func (c *GlobalConfig) UnmarshalYAML(value *yaml.Node) error {
	type raw struct {
		CacheTTL yaml.Node `yaml:"cache_ttl"`
	}

	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}

	// Field absent — use default.
	if r.CacheTTL.Kind == 0 {
		c.CacheTTL = DefaultCacheTTL
		return nil
	}

	// Try to parse as a Go duration string first.
	dur, err := time.ParseDuration(r.CacheTTL.Value)
	if err == nil {
		c.CacheTTL = dur
		return nil
	}

	// Fall back to integer seconds.
	var secs int64
	if decErr := r.CacheTTL.Decode(&secs); decErr == nil {
		c.CacheTTL = time.Duration(secs) * time.Second
		return nil
	}

	return fmt.Errorf("cannot parse cache_ttl %q as a duration: %w", r.CacheTTL.Value, err)
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
	if os.IsNotExist(err) {
		return GlobalConfig{CacheTTL: DefaultCacheTTL}, nil
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

	if _, err := os.Stat(path); os.IsNotExist(err) {
		defaults := GlobalConfig{CacheTTL: DefaultCacheTTL}
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
