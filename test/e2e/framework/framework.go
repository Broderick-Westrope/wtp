// Package framework contains helpers for constructing repositories during end-to-end tests.
package framework

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/satococoa/wtp/v3/internal/testutil"
)

const (
	dirPerm  = 0755
	filePerm = 0600

	// DefaultOriginURL is the git remote URL added to every test repo so that
	// wtp add (which requires an origin remote) resolves the centralized storage
	// path as $XDG_DATA_HOME/wtp/worktrees/test/repo/<branch>.
	DefaultOriginURL = "https://github.com/test/repo.git"
)

// TestEnvironment manages the temporary state for an end-to-end test run.
type TestEnvironment struct {
	t             *testing.T
	tmpDir        string
	xdgDataHome   string
	xdgConfigHome string
	xdgCacheHome  string
	wtpBinary     string
	cleanup       []func()
}

// NewTestEnvironment builds a new test environment and compiles the wtp binary when needed.
func NewTestEnvironment(t *testing.T) *TestEnvironment {
	t.Helper()

	tmpDir := t.TempDir()

	// Create isolated XDG directories so every test run gets its own data/config/cache.
	xdgDataHome := filepath.Join(tmpDir, "xdg-data")
	xdgConfigHome := filepath.Join(tmpDir, "xdg-config")
	xdgCacheHome := filepath.Join(tmpDir, "xdg-cache")

	for _, dir := range []string{xdgDataHome, xdgConfigHome, xdgCacheHome} {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			t.Fatalf("Failed to create XDG directory %s: %v", dir, err)
		}
	}

	env := &TestEnvironment{
		t:             t,
		tmpDir:        tmpDir,
		xdgDataHome:   xdgDataHome,
		xdgConfigHome: xdgConfigHome,
		xdgCacheHome:  xdgCacheHome,
		cleanup:       []func(){},
	}

	env.buildWTP()

	return env
}

// XDGDataHome returns the isolated XDG_DATA_HOME directory for this test environment.
func (e *TestEnvironment) XDGDataHome() string {
	return e.xdgDataHome
}

func (e *TestEnvironment) buildWTP() {
	e.t.Helper()

	wtpBinary := filepath.Join(e.tmpDir, "wtp")
	if runtime := os.Getenv("WTP_E2E_BINARY"); runtime != "" {
		wtpBinary = runtime
		if _, err := os.Stat(wtpBinary); err != nil {
			e.t.Fatalf("Specified WTP binary not found: %s", wtpBinary)
		}
	} else {
		projectRoot := e.findProjectRoot()
		// #nosec G204 -- test helper builds the binary in an isolated temp directory
		cmd := exec.Command("go", "build", "-o", wtpBinary, "./cmd/wtp")
		cmd.Dir = projectRoot
		if output, err := cmd.CombinedOutput(); err != nil {
			e.t.Fatalf("Failed to build wtp binary: %v\nOutput: %s", err, output)
		}
	}

	// Validate the binary path
	wtpBinary = filepath.Clean(wtpBinary)
	if !filepath.IsAbs(wtpBinary) {
		absPath, err := filepath.Abs(wtpBinary)
		if err != nil {
			e.t.Fatalf("Failed to get absolute path for binary: %v", err)
		}
		wtpBinary = absPath
	}

	e.wtpBinary = wtpBinary
}

func (e *TestEnvironment) findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		e.t.Fatalf("Failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			e.t.Fatal("Could not find project root (go.mod)")
		}
		dir = parent
	}
}

// CreateTestRepo initializes a new git repository within the test environment.
// An "origin" remote pointing to DefaultOriginURL is added automatically so that
// commands such as "wtp add" (which require an origin remote) work out of the box.
func (e *TestEnvironment) CreateTestRepo(name string) *TestRepo {
	e.t.Helper()

	repoDir := filepath.Join(e.tmpDir, name)

	e.run("git", "init", repoDir)
	testutil.ConfigureTestRepo(e.t, repoDir, func(dir string, args ...string) {
		e.runInDir(dir, "git", args...)
	})

	// Ensure the default branch is 'main' regardless of global git config
	e.runInDir(repoDir, "git", "config", "init.defaultBranch", "main")

	readmePath := filepath.Join(repoDir, "README.md")
	e.writeFile(readmePath, "# Test Repository")
	e.runInDir(repoDir, "git", "add", ".")
	e.runInDir(repoDir, "git", "commit", "-m", "Initial commit")

	// Explicitly rename the branch to main if it's not already
	e.runInDir(repoDir, "git", "branch", "-m", "main")

	// Add default origin remote so that wtp commands that require it work out of the box.
	e.runInDir(repoDir, "git", "remote", "add", "origin", DefaultOriginURL)

	return &TestRepo{
		env:  e,
		path: repoDir,
	}
}

func (e *TestEnvironment) run(command string, args ...string) string {
	e.t.Helper()

	cmd := exec.Command(command, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("Command failed: %s %s\nOutput: %s\nError: %v",
			command, strings.Join(args, " "), output, err)
	}
	return string(output)
}

func (e *TestEnvironment) runInDir(dir, command string, args ...string) string {
	e.t.Helper()

	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("Command failed in %s: %s %s\nOutput: %s\nError: %v",
			dir, command, strings.Join(args, " "), output, err)
	}
	return string(output)
}

func (e *TestEnvironment) writeFile(path, content string) {
	e.t.Helper()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		e.t.Fatalf("Failed to create directory %s: %v", dir, err)
	}

	if err := os.WriteFile(path, []byte(content), filePerm); err != nil {
		e.t.Fatalf("Failed to write file %s: %v", path, err)
	}
}

// RunWTP executes the wtp binary with the provided arguments.
func (e *TestEnvironment) RunWTP(args ...string) (string, error) {
	// Validate args don't contain dangerous characters
	for _, arg := range args {
		if err := validateArg(arg); err != nil {
			return "", fmt.Errorf("invalid argument: %w", err)
		}
	}

	// Create command with validated binary path
	cmd := createSafeCommand(e.wtpBinary, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+e.tmpDir,
		"XDG_DATA_HOME="+e.xdgDataHome,
		"XDG_CONFIG_HOME="+e.xdgConfigHome,
		"XDG_CACHE_HOME="+e.xdgCacheHome,
	)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// TmpDir returns the temporary directory used by the test environment.
func (e *TestEnvironment) TmpDir() string {
	return e.tmpDir
}

// CreateNonRepoDir creates a directory that is not initialized as a git repository.
func (e *TestEnvironment) CreateNonRepoDir(name string) *TestRepo {
	e.t.Helper()

	dir := filepath.Join(e.tmpDir, name)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		e.t.Fatalf("Failed to create directory: %v", err)
	}

	return &TestRepo{
		env:  e,
		path: dir,
	}
}

// WriteFile writes file contents relative to the test environment root.
func (e *TestEnvironment) WriteFile(path, content string) {
	e.writeFile(path, content)
}

// FileExists checks whether a file exists relative to the test environment root.
func (*TestEnvironment) FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// RunInDir executes a command in the specified directory and returns combined output.
func (e *TestEnvironment) RunInDir(dir, command string, args ...string) string {
	return e.runInDir(dir, command, args...)
}

// Cleanup runs registered cleanup callbacks for the environment.
func (e *TestEnvironment) Cleanup() {
	for _, fn := range e.cleanup {
		fn()
	}
}

// TestRepo wraps a git repository created inside the test environment.
type TestRepo struct {
	env  *TestEnvironment
	path string
}

// RunWTP executes the wtp binary from the repository directory.
func (r *TestRepo) RunWTP(args ...string) (string, error) {
	// Validate args don't contain dangerous characters
	for _, arg := range args {
		if err := validateArg(arg); err != nil {
			return "", fmt.Errorf("invalid argument: %w", err)
		}
	}

	// Create command with validated binary path
	cmd := createSafeCommand(r.env.wtpBinary, args...)
	cmd.Dir = r.path
	cmd.Env = append(os.Environ(),
		"HOME="+r.env.tmpDir,
		"XDG_DATA_HOME="+r.env.xdgDataHome,
		"XDG_CONFIG_HOME="+r.env.xdgConfigHome,
		"XDG_CACHE_HOME="+r.env.xdgCacheHome,
	)

	output, err := cmd.CombinedOutput()
	return string(output), err
}

// CreateBranch creates a new branch in the repository.
func (r *TestRepo) CreateBranch(name string) {
	r.env.runInDir(r.path, "git", "branch", name)
}

// CheckoutBranch switches to the specified branch.
func (r *TestRepo) CheckoutBranch(name string) {
	r.env.runInDir(r.path, "git", "checkout", name)
}

// CommitFile writes a file and commits it with the provided message.
func (r *TestRepo) CommitFile(filename, content, message string) {
	r.env.writeFile(filepath.Join(r.path, filename), content)
	r.env.runInDir(r.path, "git", "add", filename)
	r.env.runInDir(r.path, "git", "commit", "-m", message)
}

// AddRemote adds a git remote to the repository.
func (r *TestRepo) AddRemote(name, url string) {
	r.env.runInDir(r.path, "git", "remote", "add", name, url)
}

// SetRemoteURL sets the URL for an existing remote, or adds it if it does not exist.
// Use this instead of AddRemote when the repo already has an origin (set by CreateTestRepo).
func (r *TestRepo) SetRemoteURL(name, url string) {
	// Try set-url first; if it fails (remote doesn't exist), add it.
	cmd := exec.Command("git", "remote", "set-url", name, url)
	cmd.Dir = r.path
	if err := cmd.Run(); err != nil {
		r.env.runInDir(r.path, "git", "remote", "add", name, url)
	}
}

// CentralizedWorktreePath returns the expected centralized storage path for the given branch
// when the repository uses the default origin URL (DefaultOriginURL = https://github.com/test/repo.git).
// Path format: $XDG_DATA_HOME/wtp/worktrees/test/repo/<branch>
func (r *TestRepo) CentralizedWorktreePath(branch string) string {
	return filepath.Join(r.env.xdgDataHome, "wtp", "worktrees", "test", "repo", branch)
}

// CreateRemoteBranch pushes a branch to the specified remote.
func (r *TestRepo) CreateRemoteBranch(remote, branch string) {
	refPath := filepath.Join(r.path, ".git", "refs", "remotes", remote)
	if err := os.MkdirAll(refPath, dirPerm); err != nil {
		r.env.t.Fatalf("Failed to create remote ref directory: %v", err)
	}

	output := r.env.runInDir(r.path, "git", "rev-parse", "HEAD")
	commit := strings.TrimSpace(output)

	r.env.writeFile(filepath.Join(refPath, branch), commit)
}

// Path returns the filesystem path of the repository.
func (r *TestRepo) Path() string {
	return r.path
}

// WriteConfig writes a .wtp.yml configuration file into the repository.
func (r *TestRepo) WriteConfig(content string) {
	configPath := filepath.Join(r.path, ".wtp.yml")
	r.env.writeFile(configPath, content)
}

// HasFile reports whether a file exists relative to the repository root.
func (r *TestRepo) HasFile(path string) bool {
	fullPath := filepath.Join(r.path, path)
	_, err := os.Stat(fullPath)
	return err == nil
}

// ReadFile returns the contents of a file relative to the repository root.
func (r *TestRepo) ReadFile(path string) string {
	fullPath := filepath.Join(r.path, path)
	// #nosec G304 -- file paths are confined to the temporary test repository
	content, err := os.ReadFile(fullPath)
	if err != nil {
		r.env.t.Fatalf("Failed to read file %s: %v", path, err)
	}
	return string(content)
}

// GitStatus returns the output of `git status --short` for the repository.
func (r *TestRepo) GitStatus() string {
	return r.env.runInDir(r.path, "git", "status", "--porcelain")
}

// CurrentBranch returns the currently checked-out branch name.
func (r *TestRepo) CurrentBranch() string {
	output := r.env.runInDir(r.path, "git", "branch", "--show-current")
	return strings.TrimSpace(output)
}

// GetCommitHash returns the HEAD commit hash.
func (r *TestRepo) GetCommitHash() string {
	output := r.env.runInDir(r.path, "git", "rev-parse", "HEAD")
	return strings.TrimSpace(output)
}

// GetBranchCommitHash returns the commit hash for the specified branch.
func (r *TestRepo) GetBranchCommitHash(branch string) string {
	output := r.env.runInDir(r.path, "git", "rev-parse", branch)
	return strings.TrimSpace(output)
}

// ListWorktrees returns the list of worktrees known to the repository.
func (r *TestRepo) ListWorktrees() []string {
	output := r.env.runInDir(r.path, "git", "worktree", "list", "--porcelain")
	lines := strings.Split(strings.TrimSpace(output), "\n")

	var worktrees []string
	for _, line := range lines {
		if strings.HasPrefix(line, "worktree ") {
			worktrees = append(worktrees, strings.TrimPrefix(line, "worktree "))
		}
	}
	return worktrees
}

// WithTimeout adds a timeout to an exec command for use in helpers.
func WithTimeout(timeout time.Duration) func(cmd *exec.Cmd) {
	return func(cmd *exec.Cmd) {
		timer := time.AfterFunc(timeout, func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		_ = cmd.Start()
		_ = cmd.Wait()
		timer.Stop()
	}
}

// validateArg checks if an argument is safe to pass to exec.Command
func validateArg(arg string) error {
	// Allow common flags and paths
	// This is a whitelist approach for test arguments
	if arg == "" {
		return nil
	}

	// Check for shell metacharacters that could be dangerous
	// Note: { and } are allowed for branch names like branch@{upstream}
	dangerousChars := []string{";", "&", "|", "$", "`", "(", ")", "<", ">", "\n", "\r"}
	for _, char := range dangerousChars {
		if strings.Contains(arg, char) {
			return fmt.Errorf("argument contains potentially dangerous character: %s", char)
		}
	}

	return nil
}

// createSafeCommand creates an exec.Cmd with a validated binary path
func createSafeCommand(binary string, args ...string) *exec.Cmd {
	// The binary path has already been validated during initialization
	// This function separates the concern of command creation from validation
	return exec.Command(binary, args...)
}
