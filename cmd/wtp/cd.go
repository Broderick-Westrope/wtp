package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/satococoa/wtp/v3/internal/command"
	"github.com/satococoa/wtp/v3/internal/errors"
	"github.com/satococoa/wtp/v3/internal/git"
)

// NewCdCommand creates the cd command definition
func NewCdCommand() *cli.Command {
	return &cli.Command{
		Name:  "cd",
		Usage: "Output absolute path to worktree",
		Description: "Output the absolute path to the specified worktree.\n" +
			"If no worktree is specified, outputs the main worktree path (like cd goes to $HOME).\n\n" +
			"Usage:\n" +
			"  Direct:     cd \"$(wtp cd feature)\"\n" +
			"  With hook:  wtp cd feature\n" +
			"  Go home:    wtp cd\n\n" +
			"To enable the hook for easier navigation:\n" +
			"  Bash: eval \"$(wtp hook bash)\"\n" +
			"  Zsh:  eval \"$(wtp hook zsh)\"\n" +
			"  Fish: wtp hook fish | source",
		ArgsUsage:     "[worktree-name]",
		Action:        cdToWorktree,
		ShellComplete: completeWorktreesForCd,
	}
}

func cdToWorktree(_ context.Context, cmd *cli.Command) error {
	args := cmd.Args()

	// Default to main worktree (@) when no argument provided, like cd goes to $HOME
	worktreeName := "@"
	if args.Len() > 0 {
		worktreeName = args.Get(0)
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	// Initialize repository to check if we're in a git repo
	_, err = git.NewRepository(cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	// Get the writer from cli.Command
	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	// Use CommandExecutor-based implementation
	executor := command.NewRealExecutor()
	return cdCommandWithCommandExecutor(cmd, w, executor, cwd, worktreeName)
}

func cdCommandWithCommandExecutor(
	_ *cli.Command,
	w io.Writer,
	executor command.Executor,
	_ string,
	worktreeName string,
) error {
	// Get worktrees using CommandExecutor
	listCmd := command.GitWorktreeList()
	result, err := executor.Execute([]command.Command{listCmd})
	if err != nil {
		return fmt.Errorf("failed to get worktrees: %w", err)
	}

	// Parse worktrees from command output
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

	// Find the worktree using branch-name resolution
	targetPath, err := resolveWorktreePathByName(worktreeName, worktrees)
	if err != nil {
		return err
	}

	// Output the path for the shell function to cd to
	if _, err := fmt.Fprintln(w, targetPath); err != nil {
		return err
	}

	return nil
}

// getWorktreesForCd gets worktrees for cd command with current position markers and writes them to writer (testable)
func getWorktreesForCd(w io.Writer) error {
	// Get current directory
	cwd, err := os.Getwd()
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

	if err := writeMainWorktreeForCd(w, worktrees, cwd); err != nil {
		return err
	}

	return writeWorktreesForCd(w, worktrees, cwd)
}

func writeMainWorktreeForCd(w io.Writer, worktrees []git.Worktree, cwd string) error {
	for i := range worktrees {
		wt := &worktrees[i]
		if wt.IsMain {
			if wt.Path == cwd {
				if _, err := fmt.Fprintln(w, "@*"); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(w, "@"); err != nil {
					return err
				}
			}
			break
		}
	}

	return nil
}

// writeWorktreesForCd writes all non-main worktrees as branch names with an optional
// current-position marker (*).
func writeWorktreesForCd(w io.Writer, worktrees []git.Worktree, cwd string) error {
	for i := range worktrees {
		wt := &worktrees[i]
		if wt.IsMain || wt.Branch == "" {
			continue
		}
		if wt.Path == cwd {
			if _, err := fmt.Fprintf(w, "%s*\n", wt.Branch); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(w, wt.Branch); err != nil {
				return err
			}
		}
	}

	return nil
}

// completeWorktreesForCd provides worktree name completion for cd command (wrapper for getWorktreesForCd)
func completeWorktreesForCd(_ context.Context, cmd *cli.Command) {
	current, previous := completionArgsFromCommand(cmd)

	if maybeCompleteFlagSuggestions(cmd, current, previous) {
		return
	}

	currentNormalized := strings.TrimSuffix(current, "*")

	if currentNormalized == "" && len(previous) > 0 {
		return
	}

	var buf bytes.Buffer
	if err := getWorktreesForCd(&buf); err != nil {
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
		raw := scanner.Text()
		candidate := strings.TrimSuffix(raw, "*")

		if candidate == "" {
			continue
		}

		if _, exists := used[candidate]; exists {
			continue
		}

		if currentNormalized != "" && candidate == currentNormalized {
			continue
		}

		if _, err := fmt.Println(candidate); err != nil {
			return
		}
	}
}
