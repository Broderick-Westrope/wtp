package e2e

import (
	"strings"
	"testing"

	"github.com/satococoa/wtp/v3/test/e2e/framework"
)

func TestArchiveWorkflow(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-workflow")
	repo.CreateBranch("feature/archive-me")

	// Create a worktree
	_, err := repo.RunWTP("add", "feature/archive-me")
	framework.AssertNoError(t, err)
	framework.AssertWorktreeCount(t, repo, 2)

	// Verify it appears in plain list
	output, err := repo.RunWTP("list", "--no-sync")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "feature/archive-me")

	// Archive the worktree
	output, err = repo.RunWTP("archive", "feature/archive-me")
	framework.AssertNoError(t, err)
	framework.AssertWorktreeArchived(t, output, "feature/archive-me")

	// Archived worktrees should be hidden from plain list
	output, err = repo.RunWTP("list", "--no-sync")
	framework.AssertNoError(t, err)
	framework.AssertTrue(t,
		!strings.Contains(output, "feature/archive-me"),
		"Archived worktree should be hidden from plain list")

	// Archived worktrees should be visible with --all
	output, err = repo.RunWTP("list", "--all", "--no-sync")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "feature/archive-me")

	// Unarchive the worktree
	output, err = repo.RunWTP("unarchive", "feature/archive-me")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "Unarchived feature/archive-me")

	// Should appear in plain list again
	output, err = repo.RunWTP("list", "--no-sync")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "feature/archive-me")
}

func TestArchiveMainWorktree(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-main")

	// Attempt to archive the main worktree by its special name
	output, err := repo.RunWTP("archive", "@")
	framework.AssertError(t, err)
	framework.AssertOutputContains(t, output, "cannot archive the main worktree")
}

func TestArchiveNonexistent(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-nonexistent")

	// Attempt to archive a branch that has no worktree
	output, err := repo.RunWTP("archive", "nonexistent-branch")
	framework.AssertError(t, err)
	framework.AssertTrue(t,
		strings.Contains(output, "not found") || strings.Contains(output, "worktree not found"),
		"Should report worktree not found, got: "+output)
}

func TestArchiveAlreadyArchived(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-duplicate")
	repo.CreateBranch("feature/double-archive")

	_, err := repo.RunWTP("add", "feature/double-archive")
	framework.AssertNoError(t, err)

	// Archive once — should succeed
	_, err = repo.RunWTP("archive", "feature/double-archive")
	framework.AssertNoError(t, err)

	// Archive again — should error
	output, err := repo.RunWTP("archive", "feature/double-archive")
	framework.AssertError(t, err)
	framework.AssertOutputContains(t, output, "already archived")
}

func TestUnarchiveNotArchived(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("unarchive-not-archived")
	repo.CreateBranch("feature/not-archived")

	_, err := repo.RunWTP("add", "feature/not-archived")
	framework.AssertNoError(t, err)

	// Unarchive a worktree that was never archived — should error
	output, err := repo.RunWTP("unarchive", "feature/not-archived")
	framework.AssertError(t, err)
	framework.AssertOutputContains(t, output, "not archived")
}
