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

	"github.com/satococoa/wtp/v2/internal/command"
	"github.com/satococoa/wtp/v2/internal/errors"
	"github.com/satococoa/wtp/v2/internal/git"
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
		Description: "Removes the worktree with the specified directory name.\n" +
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
