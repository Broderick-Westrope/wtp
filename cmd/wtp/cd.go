package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	wtperrors "github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/fzf"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
)

// NewCdCommand creates the cd command definition
func NewCdCommand() *cli.Command {
	return &cli.Command{
		Name:  "cd",
		Usage: "Output absolute path to worktree",
		Description: "Output the absolute path to the specified worktree.\n" +
			"If no worktree is specified and fzf is installed, an interactive picker is shown.\n" +
			"If no worktree is specified and fzf is not installed, outputs the main worktree path.\n\n" +
			"Usage:\n" +
			"  Direct:     cd \"$(wtp cd feature)\"\n" +
			"  With hook:  wtp cd feature\n" +
			"  Go home:    wtp cd @\n" +
			"  Browse:     wtp cd          (requires fzf)\n\n" +
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

	// Empty string signals "no argument provided" to the core function.
	// This lets it distinguish between `wtp cd` and `wtp cd @`.
	var worktreeName string
	if args.Len() > 0 {
		worktreeName = args.Get(0)
	}

	// Get current directory
	cwd, err := os.Getwd()
	if err != nil {
		return wtperrors.DirectoryAccessFailed("access current", ".", err)
	}

	// Initialize repository to check if we're in a git repo
	_, err = git.NewRepository(cwd)
	if err != nil {
		return wtperrors.NotInGitRepository()
	}

	// Get the writer from cli.Command
	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	// Use CommandExecutor-based implementation
	executor := command.NewRealExecutor()
	finder := fzf.NewFinder()
	return cdCommandWithCommandExecutor(w, executor, worktreeName, finder)
}

func cdCommandWithCommandExecutor(
	w io.Writer,
	executor command.Executor,
	worktreeName string,
	finder fzf.Finder,
) error {
	// Get worktrees using CommandExecutor
	listCmd := command.GitWorktreeList()
	result, err := executor.Execute([]command.Command{listCmd})
	if err != nil {
		return fmt.Errorf("failed to get worktrees: %w", err)
	}

	// Parse worktrees from command output
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)
	names := availableWorktreeNames(worktrees)

	// No argument provided: interactive picker or fall back to main worktree
	if worktreeName == "" {
		if finder != nil && finder.Available() {
			selected, fzfErr := finder.Find(names, "")
			if fzfErr != nil {
				if errors.Is(fzfErr, fzf.ErrCanceled) {
					return nil // user dismissed picker, do nothing
				}
				return fzfErr
			}
			worktreeName = selected
		} else {
			// No fzf: fall back to main worktree (like cd goes to $HOME)
			worktreeName = "@"
		}
	}

	// Try exact match first
	targetPath, err := resolveWorktreePathByName(worktreeName, worktrees)
	if err != nil {
		// No exact match: try fuzzy selection if fzf is available
		if finder != nil && finder.Available() {
			selected, fzfErr := finder.Find(names, worktreeName)
			if fzfErr != nil {
				if errors.Is(fzfErr, fzf.ErrCanceled) {
					return nil // user dismissed picker
				}
				// fzf failed too; return the original resolution error
				return err
			}
			targetPath, err = resolveWorktreePathByName(selected, worktrees)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// Output the path for the shell function to cd to
	_, err = fmt.Fprintln(w, targetPath)
	return err
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
