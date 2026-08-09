// Package git provides helpers for interacting with git repositories and worktrees.
package git

import (
	stdErrors "errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
)

// Repository represents a git repository and offers helper methods for worktree operations.
type Repository struct {
	path string
}

// NewRepository constructs a Repository for the given path after validating it is a git repository.
func NewRepository(path string) (*Repository, error) {
	if !isGitRepository(path) {
		return nil, errors.NotInGitRepository()
	}
	return &Repository{path: path}, nil
}

// Path returns the root path for the repository.
func (r *Repository) Path() string {
	return r.path
}

// GetRepositoryName returns the name of the repository
func (r *Repository) GetRepositoryName() string {
	return filepath.Base(r.path)
}

// GetMainWorktreePath returns the path to the main worktree (original repository)
// This is useful when running commands from within a worktree
func (r *Repository) GetMainWorktreePath() (string, error) {
	// Get the common directory which points to the main repository's .git
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = r.path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get main repository path: %w", err)
	}

	commonDir := strings.TrimSpace(string(output))

	// If commonDir ends with .git, get its parent directory
	if strings.HasSuffix(commonDir, ".git") {
		// Get the parent directory of .git
		parent := filepath.Dir(commonDir)
		// Convert to absolute path if needed
		if !filepath.IsAbs(parent) {
			absPath, absErr := filepath.Abs(filepath.Join(r.path, parent))
			if absErr != nil {
				return "", fmt.Errorf("failed to get absolute path: %w", absErr)
			}
			return absPath, nil
		}
		return parent, nil
	}

	// If commonDir doesn't end with .git, it's likely already the worktree path
	if !filepath.IsAbs(commonDir) {
		absPath, absErr := filepath.Abs(filepath.Join(r.path, commonDir))
		if absErr != nil {
			return "", fmt.Errorf("failed to get absolute path: %w", absErr)
		}
		return absPath, nil
	}

	return commonDir, nil
}

// GetWorktrees lists the worktrees associated with the repository.
func (r *Repository) GetWorktrees() ([]Worktree, error) {
	cmd := exec.Command("git", "worktree", "list", "--porcelain")
	cmd.Dir = r.path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list worktrees: %w", err)
	}

	worktrees := parseWorktreeList(string(output))

	// The first worktree in the list is always the main worktree
	if len(worktrees) > 0 {
		worktrees[0].IsMain = true
	}

	return worktrees, nil
}

// CreateWorktree creates a new worktree at the given path and optionally checks out the branch.
func (r *Repository) CreateWorktree(path, branch string) error {
	args := []string{"worktree", "add"}
	args = append(args, path)
	if branch != "" {
		args = append(args, branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = r.path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create worktree: %w", err)
	}
	return nil
}

// RemoveWorktree removes the worktree at the provided path, optionally forcing removal.
func (r *Repository) RemoveWorktree(path string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)

	cmd := exec.Command("git", args...)
	cmd.Dir = r.path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to remove worktree: %w", err)
	}
	return nil
}

// GetRemoteURL returns the URL for the given remote name.
// It runs `git remote get-url <remoteName>` and returns the trimmed output.
func (r *Repository) GetRemoteURL(remoteName string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", remoteName)
	cmd.Dir = r.path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get URL for remote %q: %w", remoteName, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// ExecuteGitCommand executes a git command in the repository directory
func (r *Repository) ExecuteGitCommand(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.path
	// Debug: print the command being executed
	// fmt.Printf("DEBUG: Executing: git %s\n", strings.Join(args, " "))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.GitCommandFailed(fmt.Sprintf("git %s", strings.Join(args, " ")), string(output))
	}
	return nil
}

// BranchExists checks if a branch exists locally
func (r *Repository) BranchExists(branch string) (bool, error) {
	// Validate branch name to prevent command injection
	if branch == "" || strings.Contains(branch, "..") || strings.ContainsAny(branch, "\n\r") {
		return false, errors.InvalidBranchName(branch)
	}

	// #nosec G204 - branch is validated above
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branch))
	cmd.Dir = r.path
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if stdErrors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("failed to check branch existence: %w", err)
	}
	return true, nil
}

// GetRemoteBranches returns a map of remote branches by remote name
func (r *Repository) GetRemoteBranches(branch string) (map[string]string, error) {
	// Validate branch name to prevent command injection
	if strings.Contains(branch, "..") || strings.ContainsAny(branch, "\n\r") {
		return nil, errors.InvalidBranchName(branch)
	}

	remotes := make(map[string]string)

	// Get all remote branches that match the branch name
	// #nosec G204 - branch is validated above
	cmd := exec.Command("git", "for-each-ref", "--format=%(refname:short)", fmt.Sprintf("refs/remotes/*/%s", branch))
	cmd.Dir = r.path
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get remote branches: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse remote/branch format
		const remotePartCount = 2
		parts := strings.SplitN(line, "/", remotePartCount)
		if len(parts) == remotePartCount {
			remote := parts[0]
			remotes[remote] = line
		}
	}

	return remotes, nil
}

// ResolveBranch resolves a branch name following git's behavior:
// 1. Check if branch exists locally
// 2. If not, check remote branches
// 3. If multiple remotes have the branch, return an error
func (r *Repository) ResolveBranch(branch string) (resolvedBranch string, isRemote bool, err error) {
	// First check if branch exists locally
	exists, err := r.BranchExists(branch)
	if err != nil {
		return "", false, err
	}
	if exists {
		return branch, false, nil
	}

	// Check remote branches
	remoteBranches, err := r.GetRemoteBranches(branch)
	if err != nil {
		return "", false, err
	}

	if len(remoteBranches) == 0 {
		return "", false, errors.BranchNotFound(branch)
	}

	if len(remoteBranches) > 1 {
		// Multiple remotes have this branch
		remoteNames := make([]string, 0, len(remoteBranches))
		for remote := range remoteBranches {
			remoteNames = append(remoteNames, remote)
		}
		return "", false, errors.MultipleBranchesFound(branch, remoteNames)
	}

	// Single remote has this branch
	for _, remoteBranch := range remoteBranches {
		return remoteBranch, true, nil
	}

	return "", false, nil
}

func isGitRepository(path string) bool {
	// Use git rev-parse to check if we're in a git repository
	// This works for both regular repos and worktrees
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = path
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// CommitExists checks whether the given SHA exists in the repository.
// Returns true if the SHA resolves to a valid object, false if it does not exist.
func (r *Repository) CommitExists(sha string) (bool, error) {
	if strings.Contains(sha, "..") || strings.ContainsAny(sha, "\n\r") || sha == "" {
		return false, fmt.Errorf("invalid SHA: %q", sha)
	}

	// #nosec G204 - sha is validated above
	cmd := exec.Command("git", "cat-file", "-t", sha)
	cmd.Dir = r.path

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if stdErrors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("failed to check commit existence: %w", err)
	}

	return strings.TrimSpace(string(output)) == "commit", nil
}

// IsWorktreeDirty checks whether the worktree at the given path has staged or
// unstaged changes (including untracked files).
func (*Repository) IsWorktreeDirty(worktreePath string) (bool, error) {
	// #nosec G204 - worktreePath comes from trusted callers
	cmd := exec.Command("git", "-C", worktreePath, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to check worktree status: %w", err)
	}

	return strings.TrimSpace(string(output)) != "", nil
}

// HasUnpushedCommits checks whether the given branch has commits not yet pushed
// to its upstream. Returns false when the branch has no upstream configured.
func (r *Repository) HasUnpushedCommits(branch string) (bool, error) {
	if strings.Contains(branch, "..") || strings.ContainsAny(branch, "\n\r") {
		return false, errors.InvalidBranchName(branch)
	}

	revRange := fmt.Sprintf("%s@{u}..%s", branch, branch)
	// #nosec G204 - branch is validated above
	cmd := exec.Command("git", "log", revRange, "--oneline")
	cmd.Dir = r.path

	output, err := cmd.CombinedOutput()
	if err != nil {
		outStr := string(output)
		// No upstream configured — treat as no unpushed commits
		if strings.Contains(outStr, "no upstream configured") ||
			strings.Contains(outStr, "no such branch") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check unpushed commits: %w", err)
	}

	return strings.TrimSpace(string(output)) != "", nil
}

func parseWorktreeList(output string) []Worktree {
	var worktrees []Worktree
	lines := strings.Split(output, "\n")

	var current *Worktree
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if current != nil {
				worktrees = append(worktrees, *current)
				current = nil
			}
			continue
		}

		if after, found := strings.CutPrefix(line, "worktree "); found {
			current = &Worktree{
				Path: after,
			}
		} else if current != nil {
			if after, found := strings.CutPrefix(line, "HEAD "); found {
				current.HEAD = after
			} else if after, found := strings.CutPrefix(line, "branch refs/heads/"); found {
				current.Branch = after
			}
		}
	}

	if current != nil {
		worktrees = append(worktrees, *current)
	}

	return worktrees
}

// DetachedKeyword is the branch value for worktrees with a detached HEAD.
const DetachedKeyword = "detached"

// ParseWorktreeListOutput parses the porcelain output of `git worktree list --porcelain`
// into a slice of Worktree structs. The first worktree is marked as IsMain.
// Detached HEAD worktrees have Branch set to "detached".
func ParseWorktreeListOutput(output string) []Worktree {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var worktrees []Worktree
	var currentWorktree Worktree
	isFirst := true

	for _, line := range lines {
		if line == "" {
			if currentWorktree.Path != "" {
				if isFirst {
					currentWorktree.IsMain = true
					isFirst = false
				}
				worktrees = append(worktrees, currentWorktree)
				currentWorktree = Worktree{}
			}
			continue
		}

		if strings.HasPrefix(line, "worktree ") {
			currentWorktree.Path = strings.TrimPrefix(line, "worktree ")
		} else if strings.HasPrefix(line, "HEAD ") {
			currentWorktree.HEAD = strings.TrimPrefix(line, "HEAD ")
		} else if strings.HasPrefix(line, "branch ") {
			currentWorktree.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		} else if line == DetachedKeyword {
			currentWorktree.Branch = DetachedKeyword
		}
	}

	if currentWorktree.Path != "" {
		if isFirst {
			currentWorktree.IsMain = true
		}
		worktrees = append(worktrees, currentWorktree)
	}

	return worktrees
}
