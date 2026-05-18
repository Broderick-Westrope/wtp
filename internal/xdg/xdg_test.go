package xdg_test

import (
	"os"
	"path/filepath"
	"testing"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

func TestWtpDataDir(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	axdg.Reload()

	got := xdg.WtpDataDir()

	assert.Equal(t, "/custom/data/wtp", got)
}

func TestWtpConfigDir(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	axdg.Reload()

	got := xdg.WtpConfigDir()

	assert.Equal(t, "/custom/config/wtp", got)
}

func TestWtpCacheDir(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_CACHE_HOME", "/custom/cache")
	axdg.Reload()

	got := xdg.WtpCacheDir()

	assert.Equal(t, "/custom/cache/wtp", got)
}

func TestWorktreeStorageRoot(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", "/custom/data")
	axdg.Reload()

	got := xdg.WorktreeStorageRoot()

	assert.Equal(t, "/custom/data/wtp/worktrees", got)
}

func TestWtpDataDir_Default(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", "")
	axdg.Reload()

	got := xdg.WtpDataDir()

	assert.Equal(t, filepath.Join(axdg.DataHome, "wtp"), got)
}

func TestWtpConfigDir_Default(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", "")
	axdg.Reload()

	got := xdg.WtpConfigDir()

	assert.Equal(t, filepath.Join(axdg.ConfigHome, "wtp"), got)
}

func TestWtpCacheDir_Default(t *testing.T) {
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_CACHE_HOME", "")
	axdg.Reload()

	got := xdg.WtpCacheDir()

	assert.Equal(t, filepath.Join(axdg.CacheHome, "wtp"), got)
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
