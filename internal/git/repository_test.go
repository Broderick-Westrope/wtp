package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Broderick-Westrope/wtp/v3/internal/testutil"
)

func setupTestRepo(t *testing.T) string {
	tempDir := t.TempDir()

	runGitCommand := func(dir string, args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to run git %v: %v", args, err)
		}
	}

	// Initialize git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Set default branch to main (works with older git versions too)
	cmd = exec.Command("git", "config", "init.defaultBranch", "main")
	cmd.Dir = tempDir
	_ = cmd.Run() // Ignore error if git version is too old

	testutil.ConfigureTestRepo(t, tempDir, runGitCommand)

	// Create initial commit
	readmeFile := filepath.Join(tempDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Test Repo"), 0644); err != nil {
		t.Fatalf("Failed to write README: %v", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to add README: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tempDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Ensure the default branch is named 'main'
	// Check current branch name
	cmd = exec.Command("git", "branch", "--show-current")
	cmd.Dir = tempDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get current branch: %v", err)
	}

	currentBranch := strings.TrimSpace(string(output))
	if currentBranch == "master" {
		// Rename master to main
		cmd = exec.Command("git", "branch", "-m", "master", "main")
		cmd.Dir = tempDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("Failed to rename branch to main: %v", err)
		}
	}

	return tempDir
}

func TestNewRepository(t *testing.T) {
	// Test with valid git repository
	repoDir := setupTestRepo(t)

	repo, err := NewRepository(repoDir)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if repo.Path() != repoDir {
		t.Errorf("Expected path %s, got %s", repoDir, repo.Path())
	}

	// Test with non-git directory
	tempDir := t.TempDir()
	_, err = NewRepository(tempDir)
	if err == nil {
		t.Error("Expected error for non-git directory, got nil")
	}
}

func TestParseWorktreeList(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []Worktree
	}{
		{
			name: "single worktree",
			output: `worktree /path/to/main
HEAD abcd1234

`,
			expected: []Worktree{
				{
					Path: "/path/to/main",
					HEAD: "abcd1234",
				},
			},
		},
		{
			name: "multiple worktrees",
			output: `worktree /path/to/main
HEAD abcd1234
branch refs/heads/main

worktree /path/to/feature
HEAD efgh5678
branch refs/heads/feature/test

`,
			expected: []Worktree{
				{
					Path:   "/path/to/main",
					HEAD:   "abcd1234",
					Branch: "main",
				},
				{
					Path:   "/path/to/feature",
					HEAD:   "efgh5678",
					Branch: "feature/test",
				},
			},
		},
		{
			name:     "empty output",
			output:   "",
			expected: []Worktree{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseWorktreeList(tt.output)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d worktrees, got %d", len(tt.expected), len(result))
				return
			}

			for i, expected := range tt.expected {
				if result[i].Path != expected.Path {
					t.Errorf("Worktree %d: expected path %s, got %s", i, expected.Path, result[i].Path)
				}
				if result[i].HEAD != expected.HEAD {
					t.Errorf("Worktree %d: expected HEAD %s, got %s", i, expected.HEAD, result[i].HEAD)
				}
				if result[i].Branch != expected.Branch {
					t.Errorf("Worktree %d: expected branch %s, got %s", i, expected.Branch, result[i].Branch)
				}
			}
		})
	}
}

func TestParseWorktreeListOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []Worktree
	}{
		{
			name: "single main worktree",
			output: `worktree /path/to/main
HEAD abcd1234
branch refs/heads/main
`,
			expected: []Worktree{
				{
					Path:   "/path/to/main",
					HEAD:   "abcd1234",
					Branch: "main",
					IsMain: true,
				},
			},
		},
		{
			name: "multiple worktrees with detached",
			output: `worktree /path/to/main
HEAD abcd1234
branch refs/heads/main

worktree /path/to/feature
HEAD efgh5678
branch refs/heads/feature/test

worktree /path/to/detached
HEAD 11112222
detached
`,
			expected: []Worktree{
				{Path: "/path/to/main", HEAD: "abcd1234", Branch: "main", IsMain: true},
				{Path: "/path/to/feature", HEAD: "efgh5678", Branch: "feature/test"},
				{Path: "/path/to/detached", HEAD: "11112222", Branch: "detached"},
			},
		},
		{
			name:     "empty output",
			output:   "",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseWorktreeListOutput(tt.output)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d worktrees, got %d", len(tt.expected), len(result))
				return
			}

			for i, expected := range tt.expected {
				if result[i].Path != expected.Path {
					t.Errorf("Worktree %d: expected path %s, got %s", i, expected.Path, result[i].Path)
				}
				if result[i].HEAD != expected.HEAD {
					t.Errorf("Worktree %d: expected HEAD %s, got %s", i, expected.HEAD, result[i].HEAD)
				}
				if result[i].Branch != expected.Branch {
					t.Errorf("Worktree %d: expected branch %s, got %s", i, expected.Branch, result[i].Branch)
				}
				if result[i].IsMain != expected.IsMain {
					t.Errorf("Worktree %d: expected IsMain %v, got %v", i, expected.IsMain, result[i].IsMain)
				}
			}
		})
	}
}

func TestExecuteGitCommand(t *testing.T) {
	repoDir := setupTestRepo(t)
	repo, err := NewRepository(repoDir)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Test successful command
	err = repo.ExecuteGitCommand("status")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	// Test command with arguments
	err = repo.ExecuteGitCommand("log", "--oneline", "-1")
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	// Test failing command
	err = repo.ExecuteGitCommand("invalid-command")
	if err == nil {
		t.Error("Expected error for invalid command")
	}
}

func TestRepository_GetRepositoryName(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "simple repository name",
			path:     "/Users/user/repos/wtp",
			expected: "wtp",
		},
		{
			name:     "repository with long path",
			path:     "/home/developer/projects/my-awesome-project",
			expected: "my-awesome-project",
		},
		{
			name:     "repository in nested directory",
			path:     "/var/lib/git/repositories/backend-api",
			expected: "backend-api",
		},
		{
			name:     "root directory",
			path:     "/",
			expected: "/",
		},
		{
			name:     "current directory",
			path:     ".",
			expected: ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &Repository{path: tt.path}
			result := repo.GetRepositoryName()
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsGitRepository(t *testing.T) {
	// Test with valid git repository
	repoDir := setupTestRepo(t)
	if !isGitRepository(repoDir) {
		t.Error("Expected true for git repository")
	}

	// Test with non-git directory
	tempDir := t.TempDir()
	if isGitRepository(tempDir) {
		t.Error("Expected false for non-git directory")
	}

	// Test with non-existent directory
	if isGitRepository("/path/that/does/not/exist") {
		t.Error("Expected false for non-existent directory")
	}
}

func TestBranchResolution(t *testing.T) {
	// Create a temporary directory for test repository
	repoDir := setupTestRepo(t)

	// Create local branch
	runCmd(t, repoDir, "git", "branch", "local-feature")

	// Add remotes and their branches
	// Remote "origin"
	runCmd(t, repoDir, "git", "remote", "add", "origin", "https://example.com/repo.git")

	// Create fake remote refs
	originRefsDir := filepath.Join(repoDir, ".git", "refs", "remotes", "origin")
	if err := os.MkdirAll(originRefsDir, 0755); err != nil {
		t.Fatalf("Failed to create origin refs dir: %v", err)
	}

	// Get current HEAD commit
	headCommit := getHeadCommit(t, repoDir)

	// Create remote branches
	if err := os.WriteFile(filepath.Join(originRefsDir, "remote-only"), []byte(headCommit), 0644); err != nil {
		t.Fatalf("Failed to create origin/remote-only: %v", err)
	}
	if err := os.WriteFile(filepath.Join(originRefsDir, "shared-branch"), []byte(headCommit), 0644); err != nil {
		t.Fatalf("Failed to create origin/shared-branch: %v", err)
	}

	// Remote "upstream"
	runCmd(t, repoDir, "git", "remote", "add", "upstream", "https://example.com/upstream.git")
	upstreamRefsDir := filepath.Join(repoDir, ".git", "refs", "remotes", "upstream")
	if err := os.MkdirAll(upstreamRefsDir, 0755); err != nil {
		t.Fatalf("Failed to create upstream refs dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(upstreamRefsDir, "shared-branch"), []byte(headCommit), 0644); err != nil {
		t.Fatalf("Failed to create upstream/shared-branch: %v", err)
	}

	// Create repository instance
	repo, err := NewRepository(repoDir)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Test cases
	tests := []struct {
		name          string
		branch        string
		expectError   bool
		expectRemote  bool
		expectBranch  string
		errorContains string
	}{
		{
			name:         "Local branch exists",
			branch:       "local-feature",
			expectError:  false,
			expectRemote: false,
			expectBranch: "local-feature",
		},
		{
			name:         "Remote branch exists in single remote",
			branch:       "remote-only",
			expectError:  false,
			expectRemote: true,
			expectBranch: "origin/remote-only",
		},
		{
			name:          "Branch exists in multiple remotes",
			branch:        "shared-branch",
			expectError:   true,
			errorContains: "exists in multiple remotes",
		},
		{
			name:          "Branch does not exist",
			branch:        "nonexistent",
			expectError:   true,
			errorContains: "not found in local or remote branches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolvedBranch, isRemote, err := repo.ResolveBranch(tt.branch)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if isRemote != tt.expectRemote {
					t.Errorf("Expected isRemote=%v, got %v", tt.expectRemote, isRemote)
				}
				if resolvedBranch != tt.expectBranch {
					t.Errorf("Expected branch '%s', got '%s'", tt.expectBranch, resolvedBranch)
				}
			}
		})
	}
}

func runCmd(t *testing.T, dir, _ string, args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("command failed: %s\nOutput: %s", err, output)
	}
}

func getHeadCommit(t *testing.T, dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Failed to get HEAD commit: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func TestGetRemoteURL(t *testing.T) {
	repoDir := setupTestRepo(t)

	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	t.Run("returns URL for configured remote", func(t *testing.T) {
		originURL := "https://github.com/testowner/testrepo.git"
		runGit("remote", "add", "origin", originURL)
		defer runGit("remote", "remove", "origin")

		repo, err := NewRepository(repoDir)
		if err != nil {
			t.Fatalf("NewRepository: %v", err)
		}

		got, err := repo.GetRemoteURL("origin")
		if err != nil {
			t.Fatalf("GetRemoteURL: %v", err)
		}
		if got != originURL {
			t.Errorf("expected %q, got %q", originURL, got)
		}
	})

	t.Run("returns error for missing remote", func(t *testing.T) {
		repo, err := NewRepository(repoDir)
		if err != nil {
			t.Fatalf("NewRepository: %v", err)
		}

		_, err = repo.GetRemoteURL("no-such-remote")
		if err == nil {
			t.Error("expected error for missing remote, got nil")
		}
	})
}

func TestCommitExists(t *testing.T) {
	repoDir := setupTestRepo(t)
	repo, err := NewRepository(repoDir)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}

	headSHA := getHeadCommit(t, repoDir)

	t.Run("existing commit returns true", func(t *testing.T) {
		exists, err := repo.CommitExists(headSHA)
		if err != nil {
			t.Fatalf("CommitExists: %v", err)
		}
		if !exists {
			t.Error("expected true for existing commit")
		}
	})

	t.Run("fake SHA returns false", func(t *testing.T) {
		exists, err := repo.CommitExists("deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
		if err != nil {
			t.Fatalf("CommitExists: %v", err)
		}
		if exists {
			t.Error("expected false for fake SHA")
		}
	})

	t.Run("invalid SHA returns error", func(t *testing.T) {
		_, err := repo.CommitExists("abc..def")
		if err == nil {
			t.Error("expected error for invalid SHA with '..'")
		}
	})

	t.Run("empty SHA returns error", func(t *testing.T) {
		_, err := repo.CommitExists("")
		if err == nil {
			t.Error("expected error for empty SHA")
		}
	})
}

func TestIsWorktreeDirty(t *testing.T) {
	repoDir := setupTestRepo(t)
	repo, err := NewRepository(repoDir)
	if err != nil {
		t.Fatalf("NewRepository: %v", err)
	}

	t.Run("clean worktree returns false", func(t *testing.T) {
		dirty, err := repo.IsWorktreeDirty(repoDir)
		if err != nil {
			t.Fatalf("IsWorktreeDirty: %v", err)
		}
		if dirty {
			t.Error("expected false for clean worktree")
		}
	})

	t.Run("dirty worktree returns true", func(t *testing.T) {
		// Create an untracked file
		untrackedFile := filepath.Join(repoDir, "untracked.txt")
		if err := os.WriteFile(untrackedFile, []byte("dirty"), 0644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}

		dirty, err := repo.IsWorktreeDirty(repoDir)
		if err != nil {
			t.Fatalf("IsWorktreeDirty: %v", err)
		}
		if !dirty {
			t.Error("expected true for dirty worktree")
		}
	})
}

// setupCloneWithUpstream creates a bare repo, clones it, configures it,
// creates an initial commit, and pushes. Returns (cloneDir, branchName).
func setupCloneWithUpstream(t *testing.T) (cloneDir, branch string) {
	t.Helper()

	bareDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = bareDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to init bare repo: %v", err)
	}

	cloneDir = t.TempDir()
	cmd = exec.Command("git", "clone", bareDir, cloneDir)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to clone: %v", err)
	}

	runGit := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = cloneDir
		if output, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	testutil.ConfigureTestRepo(t, cloneDir, func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if err := c.Run(); err != nil {
			t.Fatalf("git config: %v", err)
		}
	})

	readmeFile := filepath.Join(cloneDir, "README.md")
	if err := os.WriteFile(readmeFile, []byte("# Test"), 0o644); err != nil {
		t.Fatalf("Failed to write README: %v", err)
	}
	runGit("add", "README.md")
	runGit("commit", "-m", "Initial commit")
	runGit("push", "-u", "origin", "HEAD")

	branchCmd := exec.Command("git", "branch", "--show-current")
	branchCmd.Dir = cloneDir
	branchOutput, err := branchCmd.Output()
	if err != nil {
		t.Fatalf("Failed to get branch: %v", err)
	}

	return cloneDir, strings.TrimSpace(string(branchOutput))
}

func TestHasUnpushedCommits(t *testing.T) {
	t.Run("no upstream returns false", func(t *testing.T) {
		repoDir := setupTestRepo(t)
		repo, err := NewRepository(repoDir)
		if err != nil {
			t.Fatalf("NewRepository: %v", err)
		}

		has, err := repo.HasUnpushedCommits("main")
		if err != nil {
			t.Fatalf("HasUnpushedCommits: %v", err)
		}
		if has {
			t.Error("expected false when no upstream configured")
		}
	})

	t.Run("with upstream and no unpushed", func(t *testing.T) {
		cloneDir, branch := setupCloneWithUpstream(t)

		repo, err := NewRepository(cloneDir)
		if err != nil {
			t.Fatalf("NewRepository: %v", err)
		}

		has, err := repo.HasUnpushedCommits(branch)
		if err != nil {
			t.Fatalf("HasUnpushedCommits: %v", err)
		}
		if has {
			t.Error("expected false when no unpushed commits")
		}
	})

	t.Run("with unpushed commits", func(t *testing.T) {
		cloneDir, branch := setupCloneWithUpstream(t)

		extraFile := filepath.Join(cloneDir, "extra.txt")
		if err := os.WriteFile(extraFile, []byte("extra"), 0o644); err != nil {
			t.Fatalf("Failed to write file: %v", err)
		}
		c := exec.Command("git", "add", "extra.txt")
		c.Dir = cloneDir
		if output, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, output)
		}
		c = exec.Command("git", "commit", "-m", "Unpushed commit")
		c.Dir = cloneDir
		if output, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git commit: %v\n%s", err, output)
		}

		repo, err := NewRepository(cloneDir)
		if err != nil {
			t.Fatalf("NewRepository: %v", err)
		}

		has, err := repo.HasUnpushedCommits(branch)
		if err != nil {
			t.Fatalf("HasUnpushedCommits: %v", err)
		}
		if !has {
			t.Error("expected true when there are unpushed commits")
		}
	})
}
