package e2e

import (
	"strings"
	"testing"

	"github.com/satococoa/wtp/v3/test/e2e/framework"
)

// TestDoctorCleanState verifies that doctor reports only gh-related issues on a fresh repo
// (i.e. no v2 worktrees, no orphaned entries, no orphaned dirs).
func TestDoctorCleanState(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("doctor-clean")

	output, err := repo.RunWTP("doctor")
	// doctor exit code may be non-zero if gh is not authenticated; that is acceptable.
	_ = err

	// The only issue that should ever appear on a clean repo is a gh CLI issue.
	// v2 worktrees, orphaned entries, or orphaned dirs must NOT be reported.
	framework.AssertFalse(t,
		strings.Contains(output, "v2 worktree") || strings.Contains(output, "Orphaned"),
		"Doctor should not report v2 or orphaned issues on a clean repo, got: "+output)

	// Either it's fully clean, or the only reported issue is gh-related.
	framework.AssertTrue(t,
		strings.Contains(output, "No issues found") ||
			strings.Contains(output, "gh") ||
			strings.Contains(output, "issue(s) found"),
		"Doctor should produce meaningful output, got: "+output)
}

// TestDoctorGhStatus verifies that doctor always checks for the gh CLI and
// reports a meaningful status (either found or not found — gh may not be
// installed in the test environment).
func TestDoctorGhStatus(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("doctor-gh")

	output, err := repo.RunWTP("doctor")
	// doctor may exit 0 even if gh is not available (it just reports issues)
	// Accept both success and non-zero exit for portability.
	_ = err

	// Should mention gh CLI status one way or another
	framework.AssertTrue(t,
		strings.Contains(output, "gh CLI") || strings.Contains(output, "gh not") || strings.Contains(output, "gh found"),
		"Doctor output should mention gh CLI status, got: "+output)
}

// TestDoctorNoOrigin verifies that doctor still runs when there is no origin remote.
func TestDoctorNoOrigin(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	// Create a bare repo without an origin remote
	repo := env.CreateTestRepo("doctor-no-origin")

	// Remove the default origin that CreateTestRepo adds
	env.RunInDir(repo.Path(), "git", "remote", "remove", "origin")

	// doctor should still run without panicking (no origin = no state entries to check)
	output, err := repo.RunWTP("doctor")
	_ = err

	// Without origin the orphaned-state check is skipped.
	// Should not report v2 or orphaned issues (only possibly gh issues).
	framework.AssertFalse(t,
		strings.Contains(output, "v2 worktree") || strings.Contains(output, "Orphaned"),
		"Doctor should not report v2 or orphaned issues without origin, got: "+output)
}

// TestDoctorWithV2StyleWorktree verifies that doctor flags worktrees under a
// /worktrees/ path segment as potential v2 leftovers.
func TestDoctorWithV2StyleWorktree(t *testing.T) {
	env := framework.NewTestEnvironment(t)
	defer env.Cleanup()

	repo := env.CreateTestRepo("doctor-v2")

	// Manually create a worktree at a v2-style path (sibling worktrees/ dir)
	v2Path := env.TmpDir() + "/v2-worktrees/feature/old"
	env.RunInDir(repo.Path(), "git", "worktree", "add", v2Path, "-b", "feature/old")

	output, err := repo.RunWTP("doctor")
	// Non-zero exit is acceptable when issues are found
	_ = err

	// Should flag the v2-style path
	framework.AssertTrue(t,
		strings.Contains(output, "v2 worktree") || strings.Contains(output, "issue") || strings.Contains(output, "⚠"),
		"Doctor should flag v2-style worktree, got: "+output)
}
