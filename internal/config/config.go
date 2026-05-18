// Package config defines the configuration schema and helpers for wtp.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

// Config represents the wtp configuration (hooks only; version and base_dir are no longer used).
type Config struct {
	Hooks Hooks `yaml:"hooks,omitempty"`
}

// Hooks represents the post-create hooks configuration
type Hooks struct {
	PostCreate []Hook `yaml:"post_create,omitempty"`
}

// Hook represents a single hook configuration
type Hook struct {
	Type    string            `yaml:"type"` // "copy", "command", or "symlink"
	From    string            `yaml:"from,omitempty"`
	To      string            `yaml:"to,omitempty"`
	Command string            `yaml:"command,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
	WorkDir string            `yaml:"work_dir,omitempty"`
}

const (
	// ConfigFileName is the default filename for the wtp configuration.
	ConfigFileName = ".wtp.yml"
	// HookTypeCopy identifies a hook that copies files.
	HookTypeCopy = "copy"
	// HookTypeCommand identifies a hook that executes a command.
	HookTypeCommand = "command"
	// HookTypeSymlink identifies a hook that creates symlinks.
	HookTypeSymlink       = "symlink"
	configFilePermissions = 0o600
)

// rawConfig is used internally to absorb old YAML fields (version, defaults)
// without exposing them in Config.
type rawConfig struct {
	// Ignored legacy fields — present so yaml.Unmarshal doesn't error on old files.
	Version  interface{} `yaml:"version"`
	Defaults interface{} `yaml:"defaults"`
	Hooks    Hooks       `yaml:"hooks,omitempty"`
}

// LoadConfig loads configuration from .wtp.yml in the repository root.
// Old fields (version, defaults/base_dir) are silently ignored.
func LoadConfig(repoRoot string) (*Config, error) {
	cleanedRoot := filepath.Clean(repoRoot)
	if !filepath.IsAbs(cleanedRoot) {
		absRoot, err := filepath.Abs(cleanedRoot)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve repository root: %w", err)
		}
		cleanedRoot = absRoot
	}

	configPath := filepath.Join(cleanedRoot, ConfigFileName)

	// If config file doesn't exist, return default config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &Config{Hooks: Hooks{}}, nil
	}

	// #nosec G304 -- configPath is derived from the validated repository root and fixed file name
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Use rawConfig to absorb legacy fields without error.
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	config := &Config{Hooks: raw.Hooks}

	// Apply defaults, then validate configuration.
	config.ApplyDefaults()
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// SaveConfig saves configuration to .wtp.yml in the repository root
func SaveConfig(repoRoot string, config *Config) error {
	config.ApplyDefaults()
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	configPath := filepath.Join(repoRoot, ConfigFileName)

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, configFilePermissions); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// ApplyDefaults applies default values to the configuration in-place.
func (c *Config) ApplyDefaults() {
	for i := range c.Hooks.PostCreate {
		c.Hooks.PostCreate[i].ApplyDefaults()
	}
}

// Validate validates the configuration without mutating it.
func (c *Config) Validate() error {
	for i := range c.Hooks.PostCreate {
		if err := c.Hooks.PostCreate[i].Validate(); err != nil {
			return fmt.Errorf("invalid hook %d: %w", i+1, err)
		}
	}

	return nil
}

// ApplyDefaults applies default values to a single hook in-place.
func (h *Hook) ApplyDefaults() {
	if h.Type != HookTypeCopy {
		return
	}
	if h.To != "" || h.From == "" {
		return
	}
	// Only default to=from for relative paths. Absolute paths must be explicit.
	if filepath.IsAbs(h.From) {
		return
	}
	h.To = h.From
}

// Validate validates a single hook configuration without mutating it.
func (h *Hook) Validate() error {
	switch h.Type {
	case HookTypeCopy:
		if h.From == "" {
			return fmt.Errorf("copy hook requires 'from' field")
		}
		if h.To == "" && filepath.IsAbs(h.From) {
			return fmt.Errorf("copy hook with absolute 'from' requires 'to' field")
		}
		if h.Command != "" {
			return fmt.Errorf("copy hook should not have 'command' field")
		}
	case HookTypeCommand:
		if h.Command == "" {
			return fmt.Errorf("command hook requires 'command' field")
		}
		if h.From != "" || h.To != "" {
			return fmt.Errorf("command hook should not have 'from' or 'to' fields")
		}
	case HookTypeSymlink:
		if h.From == "" || h.To == "" {
			return fmt.Errorf("symlink hook requires both 'from' and 'to' fields")
		}
		if h.Command != "" {
			return fmt.Errorf("symlink hook should not have 'command' field")
		}
	default:
		return fmt.Errorf("invalid hook type '%s', must be 'copy', 'command', or 'symlink'", h.Type)
	}

	return nil
}

// HasHooks returns true if the configuration has any post-create hooks
func (c *Config) HasHooks() bool {
	return len(c.Hooks.PostCreate) > 0
}
