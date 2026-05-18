// Package xdg provides XDG Base Directory paths for wtp,
// delegating to github.com/adrg/xdg for platform-native resolution.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"

	axdg "github.com/adrg/xdg"
)

const dirPermissions = 0o755

// WtpDataDir returns the wtp data directory under XDG_DATA_HOME.
func WtpDataDir() string {
	return filepath.Join(axdg.DataHome, "wtp")
}

// WtpConfigDir returns the wtp config directory under XDG_CONFIG_HOME.
func WtpConfigDir() string {
	return filepath.Join(axdg.ConfigHome, "wtp")
}

// WtpCacheDir returns the wtp cache directory under XDG_CACHE_HOME.
func WtpCacheDir() string {
	return filepath.Join(axdg.CacheHome, "wtp")
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
