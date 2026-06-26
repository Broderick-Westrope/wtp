package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Broderick-Westrope/wtp/v3/test/e2e/framework"
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

	wtPath := repo.CentralizedWorktreePath("feature/archive-me")

	// Verify it appears in plain list
	output, err := repo.RunWTP("list", "--no-sync")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "feature/archive-me")

	// Archive the worktree
	output, err = repo.RunWTP("archive", "feature/archive-me")
	framework.AssertNoError(t, err)
	framework.AssertWorktreeArchived(t, output, "feature/archive-me")

	// Branch should NOT appear in git branch
	branchOutput := env.RunInDir(repo.Path(), "git", "branch")
	framework.AssertTrue(t,
		!strings.Contains(branchOutput, "feature/archive-me"),
		"Archived branch should not appear in git branch output")

	// Worktree directory should NOT exist on disk
	framework.AssertFalse(t,
		env.FileExists(wtPath),
		"Worktree directory should not exist after archive")

	// Archived worktrees should be hidden from plain list
	output, err = repo.RunWTP("list", "--no-sync")
	framework.AssertNoError(t, err)
	framework.AssertTrue(t,
		!strings.Contains(output, "feature/archive-me"),
		"Archived worktree should be hidden from plain list")

	// Unarchive the worktree
	output, err = repo.RunWTP("unarchive", "feature/archive-me")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "Unarchived feature/archive-me")

	// Branch should appear in git branch after unarchive
	branchOutput = env.RunInDir(repo.Path(), "git", "branch")
	framework.AssertOutputContains(t, branchOutput, "feature/archive-me")

	// Directory should exist after unarchive
	framework.AssertTrue(t, env.FileExists(wtPath),
		"Worktree directory should exist after unarchive")

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

	// Archive again — should succeed (idempotent retry)
	output, err := repo.RunWTP("archive", "feature/double-archive")
	framework.AssertNoError(t, err)
	framework.AssertWorktreeArchived(t, output, "feature/double-archive")
}

func TestArchiveForce(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-force")
	repo.CreateBranch("feature/dirty")

	_, err := repo.RunWTP("add", "feature/dirty")
	framework.AssertNoError(t, err)

	// Add an untracked file to make the worktree dirty
	wtPath := repo.CentralizedWorktreePath("feature/dirty")
	env.WriteFile(filepath.Join(wtPath, "dirty.txt"), "dirty content")

	// Without --force: should fail
	output, err := repo.RunWTP("archive", "feature/dirty")
	framework.AssertError(t, err)
	framework.AssertOutputContains(t, output, "uncommitted changes")

	// With --force: should succeed
	output, err = repo.RunWTP("archive", "--force", "feature/dirty")
	framework.AssertNoError(t, err)
	framework.AssertWorktreeArchived(t, output, "feature/dirty")
}

func TestArchiveIdempotent(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-idempotent")
	repo.CreateBranch("feature/idempotent")

	_, err := repo.RunWTP("add", "feature/idempotent")
	framework.AssertNoError(t, err)

	// Archive the worktree
	_, err = repo.RunWTP("archive", "feature/idempotent")
	framework.AssertNoError(t, err)

	// Manually recreate the worktree via git
	wtPath := repo.CentralizedWorktreePath("feature/idempotent")
	env.RunInDir(repo.Path(), "git", "worktree", "add", "-b", "feature/idempotent", wtPath, "HEAD")

	// Re-run archive — should succeed by re-removing
	output, err := repo.RunWTP("archive", "feature/idempotent")
	framework.AssertNoError(t, err)
	framework.AssertWorktreeArchived(t, output, "feature/idempotent")

	// Verify branch is gone again
	branchOutput := env.RunInDir(repo.Path(), "git", "branch")
	framework.AssertTrue(t,
		!strings.Contains(branchOutput, "feature/idempotent"),
		"Branch should be removed after idempotent archive")
}

func TestArchiveCurrentWorktree(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("archive-current")
	repo.CreateBranch("feature/current")

	_, err := repo.RunWTP("add", "feature/current")
	framework.AssertNoError(t, err)

	wtPath := repo.CentralizedWorktreePath("feature/current")

	// Run archive from inside the worktree directory
	output, err := env.RunWTPInDir(wtPath, "archive", "feature/current")
	framework.AssertError(t, err)
	framework.AssertOutputContains(t, output, "cannot archive the current worktree")
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

func TestUnarchiveRestoredWorktreeHasContent(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("unarchive-content")
	repo.CreateBranch("feature/content")

	_, err := repo.RunWTP("add", "feature/content")
	framework.AssertNoError(t, err)

	// Commit a file in the worktree
	wtPath := repo.CentralizedWorktreePath("feature/content")
	env.WriteFile(filepath.Join(wtPath, "important.txt"), "important content")
	env.RunInDir(wtPath, "git", "add", "important.txt")
	env.RunInDir(wtPath, "git", "commit", "-m", "Add important file")

	// Archive
	_, err = repo.RunWTP("archive", "feature/content")
	framework.AssertNoError(t, err)

	// Verify file is gone
	framework.AssertFalse(t,
		env.FileExists(filepath.Join(wtPath, "important.txt")),
		"File should be gone after archive")

	// Unarchive
	output, err := repo.RunWTP("unarchive", "feature/content")
	framework.AssertNoError(t, err)
	framework.AssertOutputContains(t, output, "Unarchived feature/content")

	// Verify the committed file exists in the restored worktree
	// The path may be the original or a fallback; extract from output
	framework.AssertTrue(t,
		env.FileExists(filepath.Join(wtPath, "important.txt")),
		"Committed file should be restored after unarchive")

	// Verify file content
	data, readErr := os.ReadFile(filepath.Join(wtPath, "important.txt"))
	framework.AssertNoError(t, readErr)
	framework.AssertOutputContains(t, string(data), "important content")
}
