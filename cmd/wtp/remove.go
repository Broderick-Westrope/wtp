package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

// Variable to allow mocking in tests
var removeGetwd = os.Getwd

// NewRemoveCommand creates the remove command definition
func NewRemoveCommand() *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Aliases:   []string{"rm"},
		Usage:     "Remove a worktree",
		UsageText: "wtp remove <worktree-name>",
		Description: "Removes the worktree with the specified branch name.\n" +
			"By default, also deletes the associated branch.\n\n" +
			"Examples:\n" +
			"  wtp remove feature-old                  # Remove worktree and branch\n" +
			"  wtp remove -f feature-dirty             # Force remove dirty worktree\n" +
			"  wtp remove --keep-branch feature-done   # Keep the branch after removing worktree",
		ShellComplete: completeWorktrees,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Usage:   "Force removal even if worktree is dirty",
				Aliases: []string{"f"},
			},
			&cli.BoolFlag{
				Name:    "keep-branch",
				Usage:   "Keep the branch after removing worktree (default is to delete)",
				Aliases: []string{"k"},
			},
			&cli.BoolFlag{
				Name:  "force-branch",
				Usage: "Force branch deletion even if not merged",
			},
		},
		Action: removeCommand,
	}
}

func removeCommand(_ context.Context, cmd *cli.Command) error {
	// Get the writer from cli.Command
	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	// Extract and validate inputs
	worktreeName := cmd.Args().Get(0)
	force := cmd.Bool("force")
	keepBranch := cmd.Bool("keep-branch")
	forceBranch := cmd.Bool("force-branch")

	if err := validateRemoveInput(worktreeName, keepBranch, forceBranch); err != nil {
		return err
	}

	// Get current working directory
	cwd, err := removeGetwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	// Initialize repository to check if we're in a git repo
	_, err = git.NewRepository(cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	// Use CommandExecutor-based implementation
	executor := command.NewRealExecutor()
	return removeCommandWithCommandExecutor(cmd, w, executor, cwd, worktreeName, force, keepBranch, forceBranch)
}

func removeCommandWithCommandExecutor(
	_ *cli.Command,
	w io.Writer,
	executor command.Executor,
	cwd string,
	worktreeName string,
	force, keepBranch, forceBranch bool,
) error {
	// Get worktrees using CommandExecutor
	listCmd := command.GitWorktreeList()
	result, err := executor.Execute([]command.Command{listCmd})
	if err != nil {
		return errors.GitCommandFailed("git worktree list", err.Error())
	}

	// Parse worktrees from command output
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

	// Find target worktree
	targetWorktree, err := findTargetWorktreeFromList(worktrees, worktreeName)
	if err != nil {
		return err
	}

	absTargetPath, err := filepath.Abs(targetWorktree.Path)
	if err != nil {
		return errors.WorktreeRemovalFailed(targetWorktree.Path, err)
	}

	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return errors.DirectoryAccessFailed("access current", cwd, err)
	}

	if isPathWithin(absTargetPath, absCwd) {
		return errors.CannotRemoveCurrentWorktree(worktreeName, absTargetPath)
	}

	// Remove worktree using CommandExecutor
	removeCmd := command.GitWorktreeRemove(targetWorktree.Path, force)
	result, err = executor.Execute([]command.Command{removeCmd})
	if err != nil {
		return errors.WorktreeRemovalFailed(targetWorktree.Path, err)
	}
	if len(result.Results) > 0 && result.Results[0].Error != nil {
		gitOutput := result.Results[0].Output
		if gitOutput != "" {
			combinedError := fmt.Errorf("%w: %s", result.Results[0].Error, gitOutput)
			return errors.WorktreeRemovalFailed(targetWorktree.Path, combinedError)
		}
		return errors.WorktreeRemovalFailed(targetWorktree.Path, result.Results[0].Error)
	}
	// Best-effort: clean up state and cache entries for the removed worktree.
	cleanupWorktreeStateAndCache(cwd, targetWorktree.Branch)
	// Best-effort: remove empty centralized storage directories.
	cleanupCentralizedWorktreeDir(targetWorktree.Path)

	if _, err := fmt.Fprintf(w, "Removed worktree '%s' at %s\n", worktreeName, targetWorktree.Path); err != nil {
		return err
	}

	// Remove branch by default unless --keep-branch is specified
	if !keepBranch && targetWorktree.Branch != "" {
		if err := removeBranchWithCommandExecutor(w, executor, targetWorktree.Branch, forceBranch); err != nil {
			return err
		}
	}

	return nil
}

func validateRemoveInput(worktreeName string, keepBranch, forceBranch bool) error {
	if worktreeName == "" {
		return errors.WorktreeNameRequiredForRemove()
	}
	if forceBranch && keepBranch {
		return fmt.Errorf("--force-branch cannot be used with --keep-branch")
	}
	return nil
}

func isPathWithin(basePath, targetPath string) bool {
	rel, err := filepath.Rel(basePath, targetPath)
	if err != nil {
		return false
	}

	if rel == "." || rel == "" {
		return true
	}

	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}

	return true
}

func removeBranchWithCommandExecutor(
	w io.Writer,
	executor command.Executor,
	branchName string,
	forceBranch bool,
) error {
	branchCmd := command.GitBranchDelete(branchName, forceBranch)
	result, err := executor.Execute([]command.Command{branchCmd})
	if err != nil {
		return errors.BranchRemovalFailed(branchName, err, forceBranch)
	}
	if len(result.Results) > 0 && result.Results[0].Error != nil {
		gitOutput := result.Results[0].Output
		if gitOutput != "" {
			combinedError := fmt.Errorf("%w: %s", result.Results[0].Error, gitOutput)
			return errors.BranchRemovalFailed(branchName, combinedError, forceBranch)
		}
		return errors.BranchRemovalFailed(branchName, result.Results[0].Error, forceBranch)
	}
	_, err = fmt.Fprintf(w, "Removed branch '%s'\n", branchName)
	return err
}

// cleanupCentralizedWorktreeDir removes the leaf worktree directory and any empty parent
// directories up to WorktreeStorageRoot() after a successful worktree removal.
// Only acts on paths inside centralized storage; silently no-ops otherwise.
func cleanupCentralizedWorktreeDir(worktreePath string) {
	storageRoot := xdg.WorktreeStorageRoot()
	if storageRoot == "" {
		return
	}

	absPath, err := filepath.Abs(worktreePath)
	if err != nil {
		return
	}

	absRoot, err := filepath.Abs(storageRoot)
	if err != nil {
		return
	}

	// Only clean up strict subdirectories of the storage root.
	prefix := absRoot + string(os.PathSeparator)
	if !strings.HasPrefix(absPath, prefix) {
		return
	}

	// Walk upward from leaf, removing empty dirs until we reach storageRoot.
	dir := absPath
	for dir != absRoot && dir != filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			// Non-empty dir or other error — stop climbing.
			break
		}
		dir = filepath.Dir(dir)
	}
}

// cleanupWorktreeStateAndCache removes state and cache entries for the given branch after a
// successful worktree removal. All errors are silently ignored (best-effort).
func cleanupWorktreeStateAndCache(cwd, branch string) {
	if branch == "" {
		return
	}

	repo, err := git.NewRepository(cwd)
	if err != nil {
		return
	}

	remoteURL, err := repo.GetRemoteURL("origin")
	if err != nil {
		return
	}

	repoID, err := remote.Parse(remoteURL)
	if err != nil {
		return
	}

	key := repoID.StateKey(branch)

	_ = state.NewStore().WithLock(func(st state.State) (state.State, error) {
		delete(st.Worktrees, key)
		return st, nil
	})

	_ = cache.NewStore().Delete(key)
}

// findTargetWorktreeFromList finds a non-main worktree by branch name.
// All non-main worktrees participate — there is no managed/unmanaged filter.
func findTargetWorktreeFromList(worktrees []git.Worktree, worktreeName string) (*git.Worktree, error) {
	var availableWorktrees []string

	for i, wt := range worktrees {
		// Skip main worktree — it cannot be removed via wtp remove
		if wt.IsMain {
			continue
		}

		availableWorktrees = append(availableWorktrees, wt.Branch)

		if wt.Branch == worktreeName {
			return &worktrees[i], nil
		}
	}

	return nil, errors.WorktreeNotFound(worktreeName, availableWorktrees)
}

// getWorktreesForRemove gets worktrees for remove command and writes them to writer (testable)
func getWorktreesForRemove(w io.Writer) error {
	// Get current directory
	cwd, err := removeGetwd() // Use mockable function for tests
	if err != nil {
		return err
	}

	// Initialize repository
	repo, err := git.NewRepository(cwd)
	if err != nil {
		return err
	}

	// Get all worktrees
	worktrees, err := repo.GetWorktrees()
	if err != nil {
		return err
	}

	// Print branch names for all non-main worktrees
	for i := range worktrees {
		wt := &worktrees[i]
		if !wt.IsMain && wt.Branch != "" {
			if _, err := fmt.Fprintln(w, wt.Branch); err != nil {
				return err
			}
		}
	}

	return nil
}

// completeWorktrees provides worktree name completion for urfave/cli (wrapper for getWorktreesForRemove)
func completeWorktrees(_ context.Context, cmd *cli.Command) {
	current, previous := completionArgsFromCommand(cmd)

	if maybeCompleteFlagSuggestions(cmd, current, previous) {
		return
	}

	currentNormalized := strings.TrimSuffix(current, "*")

	var buf bytes.Buffer
	if err := getWorktreesForRemove(&buf); err != nil {
		return
	}

	used := make(map[string]struct{}, len(previous))
	for _, arg := range previous {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		key := strings.TrimSuffix(arg, "*")
		used[key] = struct{}{}
	}

	// Output each line using fmt.Println for urfave/cli compatibility
	scanner := bufio.NewScanner(&buf)
	for scanner.Scan() {
		name := scanner.Text()
		if _, exists := used[name]; exists {
			continue
		}
		if currentNormalized != "" && name == currentNormalized {
			continue
		}
		if _, err := fmt.Println(name); err != nil {
			return
		}
	}
}
