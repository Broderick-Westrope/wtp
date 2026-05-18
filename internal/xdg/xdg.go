// Package xdg provides XDG Base Directory Specification path resolution for wtp.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	dirPermissions = 0o755
)

// DataHome returns the XDG_DATA_HOME directory, defaulting to ~/.local/share.
func DataHome() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".local", "share")
	}

	return filepath.Join(home, ".local", "share")
}

// ConfigHome returns the XDG_CONFIG_HOME directory, defaulting to ~/.config.
func ConfigHome() string {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".config")
	}

	return filepath.Join(home, ".config")
}

// CacheHome returns the XDG_CACHE_HOME directory, defaulting to ~/.cache.
func CacheHome() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return v
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".cache")
	}

	return filepath.Join(home, ".cache")
}

// WtpDataDir returns the wtp data directory under XDG_DATA_HOME.
func WtpDataDir() string {
	return filepath.Join(DataHome(), "wtp")
}

// WtpConfigDir returns the wtp config directory under XDG_CONFIG_HOME.
func WtpConfigDir() string {
	return filepath.Join(ConfigHome(), "wtp")
}

// WtpCacheDir returns the wtp cache directory under XDG_CACHE_HOME.
func WtpCacheDir() string {
	return filepath.Join(CacheHome(), "wtp")
}

// WorktreeStorageRoot returns the root directory where centralized worktrees are stored.
func WorktreeStorageRoot() string {
	return filepath.Join(WtpDataDir(), "worktrees")
}

// EnsureDir creates the directory at path (and all parents) with 0755 permissions.
// It is idempotent — no error is returned if the directory already exists.
func EnsureDir(path string) error {
	if err := os.MkdirAll(path, dirPermissions); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", path, err)
	}

	return nil
}
