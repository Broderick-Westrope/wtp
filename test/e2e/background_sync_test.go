package e2e

import (
	"path/filepath"
	"testing"

	"github.com/Broderick-Westrope/wtp/v3/test/e2e/framework"
)

// TestListBackgroundRefresh covers the refresh `wtp list` starts when cached
// PR/CI status is missing: it returns without waiting, the detached sync
// writes the cache, and an immediate second listing does not start another.
func TestListBackgroundRefresh(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()
	env.EnableBackgroundSync()

	repo := env.CreateTestRepo("background-refresh")
	repo.CreateBranch("feature/refresh")
	_, err := repo.RunWTP("add", "feature/refresh")
	framework.AssertNoError(t, err)

	output, err := repo.RunWTP("list")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "feature/refresh")

	framework.AssertTrue(t, env.WaitForBackgroundSync(), "list should start a background sync")
	framework.AssertTrue(t,
		env.FileExists(filepath.Join(env.TmpDir(), "xdg-cache", "wtp", "cache.json")),
		"the background sync should write the PR/CI cache")

	refreshMarker := filepath.Join(env.XDGDataHome(), "wtp", "sync", "refresh", "test--repo")
	framework.AssertTrue(t, env.FileExists(refreshMarker), "list should record the refresh attempt")
}
