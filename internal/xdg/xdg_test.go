package xdg_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/satococoa/wtp/v2/internal/xdg"
)

func TestDataHome_EnvOverride(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")

	got := xdg.DataHome()

	assert.Equal(t, "/custom/data", got)
}

func TestDataHome_Default(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got := xdg.DataHome()

	assert.Equal(t, filepath.Join(home, ".local", "share"), got)
}

func TestConfigHome_EnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")

	got := xdg.ConfigHome()

	assert.Equal(t, "/custom/config", got)
}

func TestConfigHome_Default(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got := xdg.ConfigHome()

	assert.Equal(t, filepath.Join(home, ".config"), got)
}

func TestCacheHome_EnvOverride(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")

	got := xdg.CacheHome()

	assert.Equal(t, "/custom/cache", got)
}

func TestCacheHome_Default(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")

	home, err := os.UserHomeDir()
	require.NoError(t, err)

	got := xdg.CacheHome()

	assert.Equal(t, filepath.Join(home, ".cache"), got)
}

func TestWtpDataDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")

	got := xdg.WtpDataDir()

	assert.Equal(t, "/custom/data/wtp", got)
}

func TestWtpConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")

	got := xdg.WtpConfigDir()

	assert.Equal(t, "/custom/config/wtp", got)
}

func TestWtpCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")

	got := xdg.WtpCacheDir()

	assert.Equal(t, "/custom/cache/wtp", got)
}

func TestWorktreeStorageRoot(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/data")

	got := xdg.WorktreeStorageRoot()

	assert.Equal(t, "/custom/data/wtp/worktrees", got)
}

func TestEnsureDir_CreatesNestedDirectories(t *testing.T) {
	base := t.TempDir()
	nested := filepath.Join(base, "a", "b", "c")

	err := xdg.EnsureDir(nested)
	require.NoError(t, err)

	info, err := os.Stat(nested)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureDir_Idempotent(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "mydir")

	err := xdg.EnsureDir(dir)
	require.NoError(t, err)

	// Call again — should not error.
	err = xdg.EnsureDir(dir)
	require.NoError(t, err)
}
