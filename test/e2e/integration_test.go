package e2e

import (
	"testing"

	"github.com/Broderick-Westrope/wtp/v3/test/e2e/framework"
)

// TestHooksOnlyConfig verifies that the v3 config (hooks-only, no version/base_dir)
// works correctly and that legacy fields are silently ignored.
func TestHooksOnlyConfig(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	t.Run("HooksConfigWorks", func(t *testing.T) {
		repo := env.CreateTestRepo("hooks-config")

		config := `hooks:
  post_create:
    - type: command
      command: touch integration-hook.txt
`
		repo.WriteConfig(config)
		repo.CreateBranch("feature/integration")

		output, err := repo.RunWTP("add", "feature/integration")
		framework.AssertNoError(t, err)
		framework.AssertWorktreeCreated(t, output, "feature/integration")
		framework.AssertOutputContains(t, output, "All hooks executed successfully")

		// Hook should have created a file in the centralized worktree path
		worktreePath := repo.CentralizedWorktreePath("feature/integration")
		framework.AssertTrue(t, env.FileExists(worktreePath+"/integration-hook.txt"),
			"Hook-created file should exist in centralized worktree")
	})

	t.Run("LegacyFieldsIgnored", func(t *testing.T) {
		repo := env.CreateTestRepo("legacy-fields")

		// Old-style config with version and base_dir — should be silently ignored in v3
		config := `version: 1.0
defaults:
  base_dir: "../legacy-worktrees"
hooks:
  post_create:
    - type: command
      command: touch legacy-hook.txt
`
		repo.WriteConfig(config)
		repo.CreateBranch("feature/legacy")

		output, err := repo.RunWTP("add", "feature/legacy")
		framework.AssertNoError(t, err)
		framework.AssertWorktreeCreated(t, output, "feature/legacy")

		// Worktree should be in XDG centralized storage, not in the legacy base_dir
		centralPath := repo.CentralizedWorktreePath("feature/legacy")
		framework.AssertTrue(t, env.FileExists(centralPath), "Worktree should exist in centralized storage")

		legacyPath := env.TmpDir() + "/legacy-worktrees/feature/legacy"
		framework.AssertFalse(t, env.FileExists(legacyPath), "Legacy base_dir path should NOT be created")
	})

	t.Run("ListShowsAllWorktrees", func(t *testing.T) {
		repo := env.CreateTestRepo("list-all")
		repo.CreateBranch("feature/a")
		repo.CreateBranch("feature/b")

		_, _ = repo.RunWTP("add", "feature/a")
		_, _ = repo.RunWTP("add", "feature/b")

		output, err := repo.RunWTP("list")
		framework.AssertNoError(t, err)
		framework.AssertOutputContains(t, output, "feature/a")
		framework.AssertOutputContains(t, output, "feature/b")
		framework.AssertWorktreeCount(t, repo, 3) // main + 2 features
	})
}
