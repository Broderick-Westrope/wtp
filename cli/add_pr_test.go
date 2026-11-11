package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/github"
)

const prTestRepoURL = "https://github.com/owner/repo.git"

// prFixture is a clone of a local bare repository that stands in for
// github.com/owner/repo: the clone's remote is configured with the GitHub URL
// and url.<bare>.insteadOf redirects fetches to the bare repository.
type prFixture struct {
	clone string
	// sameRepoSHA is the tip of feature/review, a branch in the base repo.
	sameRepoSHA string
	// forkSHA is the head of PR #7, which only exists as refs/pull/7/head.
	forkSHA string
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return strings.TrimSpace(string(out))
}

func newPRFixture(t *testing.T, remoteName string) *prFixture {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	axdg.Reload()
	t.Cleanup(axdg.Reload)

	gitIdentity := [][]string{
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@example.com"},
		{"config", "commit.gpgsign", "false"},
	}

	seed := filepath.Join(root, "seed")
	require.NoError(t, os.MkdirAll(seed, 0o755))
	runGit(t, seed, "init", "-q", "-b", "main")
	for _, args := range gitIdentity {
		runGit(t, seed, args...)
	}
	runGit(t, seed, "commit", "-q", "--allow-empty", "-m", "initial")
	runGit(t, seed, "checkout", "-q", "-b", "feature/review")
	runGit(t, seed, "commit", "-q", "--allow-empty", "-m", "review me")
	sameRepoSHA := runGit(t, seed, "rev-parse", "HEAD")
	runGit(t, seed, "checkout", "-q", "-b", "fork-work", "main")
	runGit(t, seed, "commit", "-q", "--allow-empty", "-m", "from a fork")
	forkSHA := runGit(t, seed, "rev-parse", "HEAD")

	bare := filepath.Join(root, "remote.git")
	runGit(t, root, "init", "-q", "--bare", bare)
	runGit(t, seed, "push", "-q", bare, "main", "feature/review")
	runGit(t, seed, "push", "-q", bare, "fork-work:refs/pull/7/head")

	clone := filepath.Join(root, "clone")
	runGit(t, root, "clone", "-q", "--single-branch", "-b", "main", "-o", remoteName, bare, clone)
	for _, args := range gitIdentity {
		runGit(t, clone, args...)
	}
	runGit(t, clone, "remote", "set-url", remoteName, prTestRepoURL)
	runGit(t, clone, "config", "url."+bare+".insteadOf", prTestRepoURL)

	return &prFixture{clone: clone, sameRepoSHA: sameRepoSHA, forkSHA: forkSHA}
}

// stubPR makes gh unavailable or answer every lookup with pr.
func stubPR(t *testing.T, available bool, pr *github.PRInfo) *[]string {
	t.Helper()

	origAvailable, origGetPR := addIsGHAvailable, addGetPR
	t.Cleanup(func() { addIsGHAvailable, addGetPR = origAvailable, origGetPR })

	var lookups []string
	addIsGHAvailable = func() bool { return available }
	addGetPR = func(_ context.Context, _, ref string) (*github.PRInfo, error) {
		lookups = append(lookups, ref)
		if pr == nil {
			return nil, fmt.Errorf("no pull request found for %s", ref)
		}
		return pr, nil
	}
	return &lookups
}

func runAddPR(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(t.Context(), append([]string{"wtp", "add"}, args...), Env{
		Dir:    dir,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	return stdout.String(), err
}

// worktreeForBranch returns the path of the worktree with branch checked out.
func worktreeForBranch(t *testing.T, repo, branch string) string {
	t.Helper()
	var path string
	for line := range strings.Lines(runGit(t, repo, "worktree", "list", "--porcelain")) {
		line = strings.TrimSpace(line)
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		}
		if line == "branch refs/heads/"+branch {
			return path
		}
	}
	t.Fatalf("no worktree for branch %s", branch)
	return ""
}

func sameRepoPR(f *prFixture) *github.PRInfo {
	return &github.PRInfo{
		Number:     12,
		State:      "OPEN",
		Title:      "Review me",
		HeadBranch: "feature/review",
		HeadOID:    f.sameRepoSHA,
		URL:        "https://github.com/owner/repo/pull/12",
	}
}

func TestAddPR_SameRepoCreatesTrackingWorktree(t *testing.T) {
	tests := map[string]struct {
		remoteName string
	}{
		"origin":                   {remoteName: "origin"},
		"base repo under upstream": {remoteName: "upstream"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newPRFixture(t, tc.remoteName)
			if tc.remoteName != "origin" {
				runGit(t, f.clone, "remote", "add", "origin", "https://github.com/me/repo.git")
			}
			lookups := stubPR(t, true, sameRepoPR(f))

			out, err := runAddPR(t, f.clone, "--pr", "#12", "--stay")
			require.NoError(t, err, out)

			assert.Equal(t, []string{"12"}, *lookups)
			assert.Contains(t, out, "PR #12: Review me")
			assert.Contains(t, out, "Worktree created successfully")

			wt := worktreeForBranch(t, f.clone, "feature/review")
			assert.Equal(t, f.sameRepoSHA, runGit(t, wt, "rev-parse", "HEAD"))
			assert.Equal(t, tc.remoteName+"/feature/review",
				runGit(t, wt, "rev-parse", "--abbrev-ref", "@{upstream}"))

			// The single-branch clone only fetched main, so the PR branch must
			// have been added to the fetch refspecs for tracking to work.
			assert.Contains(t,
				runGit(t, f.clone, "config", "--get-all", "remote."+tc.remoteName+".fetch"),
				"refs/heads/feature/review")
		})
	}
}

func TestAddPR_ReusesExistingWorktree(t *testing.T) {
	f := newPRFixture(t, "origin")
	stubPR(t, true, sameRepoPR(f))

	_, err := runAddPR(t, f.clone, "--pr", "12", "--stay")
	require.NoError(t, err)
	wt := worktreeForBranch(t, f.clone, "feature/review")

	runGit(t, wt, "reset", "-q", "--hard", "HEAD~1")

	out, err := runAddPR(t, f.clone, "--pr", "12", "--stay", "--exec", "touch reviewed")
	require.NoError(t, err, out)

	assert.Contains(t, out, "Using existing worktree for PR #12")
	assert.Contains(t, out, "not the PR head "+f.sameRepoSHA[:shortSHALen])
	assert.Contains(t, out, "git pull")
	assert.FileExists(t, filepath.Join(wt, "reviewed"))
	assert.Equal(t, wt, worktreeForBranch(t, f.clone, "feature/review"))
}

func TestAddPR_ExistingLocalBranchIsLeftAlone(t *testing.T) {
	f := newPRFixture(t, "origin")
	stubPR(t, true, sameRepoPR(f))

	runGit(t, f.clone, "branch", "feature/review", "main")
	mainSHA := runGit(t, f.clone, "rev-parse", "main")

	out, err := runAddPR(t, f.clone, "--pr", "12", "--stay")
	require.NoError(t, err, out)

	wt := worktreeForBranch(t, f.clone, "feature/review")
	assert.Equal(t, mainSHA, runGit(t, wt, "rev-parse", "HEAD"))
	assert.Contains(t, out, "not the PR head")
}

func TestAddPR_ForkUsesPullRef(t *testing.T) {
	f := newPRFixture(t, "origin")
	stubPR(t, true, &github.PRInfo{
		Number:            7,
		State:             "OPEN",
		Title:             "From a fork",
		HeadBranch:        "main",
		HeadOID:           f.forkSHA,
		URL:               "https://github.com/owner/repo/pull/7",
		IsCrossRepository: true,
	})

	mainBefore := runGit(t, f.clone, "rev-parse", "refs/heads/main")

	out, err := runAddPR(t, f.clone, "--pr", "7", "--stay")
	require.NoError(t, err, out)

	wt := worktreeForBranch(t, f.clone, "pr/7")
	assert.Equal(t, f.forkSHA, runGit(t, wt, "rev-parse", "HEAD"))
	assert.Equal(t, mainBefore, runGit(t, f.clone, "rev-parse", "refs/heads/main"),
		"the fork's head branch is also called main, which must not touch the local main")

	out, err = runAddPR(t, f.clone, "--pr", "7", "--stay")
	require.NoError(t, err, out)
	assert.Contains(t, out, "Using existing worktree for PR #7")
}

func TestAddPR_URLFindsCloneFromOutsideRepo(t *testing.T) {
	f := newPRFixture(t, "origin")
	pr := sameRepoPR(f)
	lookups := stubPR(t, true, pr)

	// Give the clone a wtp worktree so it can be discovered.
	_, err := runAddPR(t, f.clone, "--pr", "12", "--stay")
	require.NoError(t, err)

	outside := t.TempDir()
	out, err := runAddPR(t, outside, "--pr", "https://github.com/OWNER/repo/pull/12/files", "--stay")
	require.NoError(t, err, out)

	assert.Equal(t, "https://github.com/OWNER/repo/pull/12", (*lookups)[1])
	assert.Contains(t, out, "Using existing worktree for PR #12")
}

func TestAddPR_URLWithoutLocalClone(t *testing.T) {
	newPRFixture(t, "origin")
	pr := &github.PRInfo{Number: 3, HeadBranch: "x", URL: "https://github.com/other/thing/pull/3"}
	stubPR(t, true, pr)

	_, err := runAddPR(t, t.TempDir(), "--pr", "https://github.com/other/thing/pull/3")
	require.ErrorContains(t, err, "no local clone of other/thing found")
}

func TestAddPR_Validation(t *testing.T) {
	tests := map[string]struct {
		args        []string
		ghAvailable bool
		wantErr     string
	}{
		"branch argument": {
			args: []string{"--pr", "12", "feature/x"}, ghAvailable: true,
			wantErr: "--pr cannot be combined",
		},
		"branch flag": {
			args: []string{"--pr", "12", "-b", "feature/x"}, ghAvailable: true,
			wantErr: "--pr cannot be combined",
		},
		"not a PR": {
			args: []string{"--pr", "feature/x"}, ghAvailable: true,
			wantErr: "not a pull request number or URL",
		},
		"gh missing": {
			args:    []string{"--pr", "12"},
			wantErr: "needs the GitHub CLI",
		},
		"lookup failure": {
			args: []string{"--pr", "99"}, ghAvailable: true,
			wantErr: "look up pull request 99",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := newPRFixture(t, "origin")
			stubPR(t, tc.ghAvailable, nil)

			_, err := runAddPR(t, f.clone, tc.args...)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestAddPR_NumberOutsideRepo(t *testing.T) {
	newPRFixture(t, "origin")
	stubPR(t, true, nil)

	_, err := runAddPR(t, t.TempDir(), "--pr", "12")
	require.ErrorContains(t, err, "not in a git repository")
}
