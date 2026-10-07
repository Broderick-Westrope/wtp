package maintenance_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

func TestNotices_QueueThenDrainOnce(t *testing.T) {
	setupTestEnv(t)

	lines, err := maintenance.DrainNotices()
	require.NoError(t, err)
	assert.Empty(t, lines)

	require.NoError(t, maintenance.QueueNotices([]string{"first"}))
	require.NoError(t, maintenance.QueueNotices([]string{"second", "third"}))

	lines, err = maintenance.DrainNotices()
	require.NoError(t, err)
	assert.Equal(t, []string{"first", "second", "third"}, lines)

	lines, err = maintenance.DrainNotices()
	require.NoError(t, err)
	assert.Empty(t, lines, "notices are shown once")
}

func TestIsDue_TracksLastFullSync(t *testing.T) {
	setupTestEnv(t)

	assert.True(t, maintenance.IsDue(time.Hour), "never synced is due")

	require.NoError(t, maintenance.MarkFullSync())
	assert.False(t, maintenance.IsDue(time.Hour))

	maintenance.SetTimeNow(func() time.Time { return time.Now().Add(2 * time.Hour) })
	t.Cleanup(maintenance.RestoreTimeNow)
	assert.True(t, maintenance.IsDue(time.Hour))
}

func TestMarkFullSync_RemovesLegacyThrottleFiles(t *testing.T) {
	setupTestEnv(t)
	legacy := filepath.Join(xdg.WtpDataDir(), "maintenance")
	require.NoError(t, os.MkdirAll(legacy, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(legacy, "owner--repo"), nil, 0o600))

	require.NoError(t, maintenance.MarkFullSync())

	_, err := os.Stat(legacy)
	assert.True(t, os.IsNotExist(err))
}

func TestAcquireLock_IsExclusive(t *testing.T) {
	setupTestEnv(t)

	unlock, err := maintenance.AcquireLock()
	require.NoError(t, err)

	_, err = maintenance.AcquireLock()
	assert.ErrorIs(t, err, maintenance.ErrLocked)

	unlock()
	unlock2, err := maintenance.AcquireLock()
	require.NoError(t, err)
	unlock2()
}

// makeLinkedWorktree lays out a main repo .git directory and a linked
// worktree whose .git file points back at it, as `git worktree add` does.
func makeLinkedWorktree(t *testing.T, mainRepo, worktree, name string) {
	t.Helper()
	adminDir := filepath.Join(mainRepo, ".git", "worktrees", name)
	require.NoError(t, os.MkdirAll(adminDir, 0o755))
	require.NoError(t, os.MkdirAll(worktree, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: "+adminDir+"\n"), 0o600))
}

func TestDiscoverRepos_FindsMainReposOfStoredWorktrees(t *testing.T) {
	root := t.TempDir()
	repoA := filepath.Join(t.TempDir(), "a")
	repoB := filepath.Join(t.TempDir(), "b")

	makeLinkedWorktree(t, repoA, filepath.Join(root, "owner", "a", "feat", "one"), "one")
	makeLinkedWorktree(t, repoA, filepath.Join(root, "owner", "a", "two"), "two")
	makeLinkedWorktree(t, repoB, filepath.Join(root, "host", "owner", "b", "three"), "three")

	// Contents of a worktree are never walked: a nested .git file there
	// (e.g. a vendored submodule) must not be treated as another worktree.
	nested := filepath.Join(root, "owner", "a", "two", "vendor", "dep")
	makeLinkedWorktree(t, filepath.Join(t.TempDir(), "c"), nested, "dep")

	// A .git file that does not point into a worktrees directory is ignored.
	odd := filepath.Join(root, "owner", "odd")
	require.NoError(t, os.MkdirAll(odd, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(odd, ".git"), []byte("gitdir: /nowhere/modules/x\n"), 0o600))

	repos, err := maintenance.DiscoverRepos(root)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{repoA, repoB}, repos)
}

func TestDiscoverRepos_MissingRootIsEmpty(t *testing.T) {
	repos, err := maintenance.DiscoverRepos(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	assert.Empty(t, repos)
}
