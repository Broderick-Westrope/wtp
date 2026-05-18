package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

var archiveGetwd = os.Getwd

// NewArchiveCommand creates the archive command.
func NewArchiveCommand() *cli.Command {
	return &cli.Command{
		Name:          "archive",
		Usage:         "Mark a worktree as archived",
		UsageText:     "wtp archive <branch>",
		Description:   "Marks the worktree for the given branch as archived, hiding it from 'wtp list'.",
		ShellComplete: completeNonArchivedBranches,
		Action:        archiveCommand,
	}
}

func archiveCommand(_ context.Context, cmd *cli.Command) error {
	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	branch := cmd.Args().Get(0)
	if branch == "" {
		return fmt.Errorf("branch name is required\n\nUsage: wtp archive <branch>")
	}

	cwd, err := archiveGetwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	executor := command.NewRealExecutor()
	result, err := executor.Execute([]command.Command{command.GitWorktreeList()})
	if err != nil {
		return errors.GitCommandFailed("git worktree list", err.Error())
	}
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

	repo, err := git.NewRepository(cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	remoteURL, err := repo.GetRemoteURL("origin")
	if err != nil {
		return fmt.Errorf("cannot archive: no remote 'origin' found")
	}

	repoID, err := remote.Parse(remoteURL)
	if err != nil {
		return fmt.Errorf("cannot archive: failed to parse remote URL: %w", err)
	}

	return archiveCommandCore(w, branch, worktrees, repoID, state.NewStore())
}

// archiveCommandCore is the testable core of the archive command.
func archiveCommandCore(
	w io.Writer,
	branch string,
	worktrees []git.Worktree,
	repoID remote.RepoIdentifier,
	stateStore *state.Store,
) error {
	// Disallow archiving main worktree by special names
	if branch == "@" || branch == "root" {
		return fmt.Errorf("cannot archive the main worktree")
	}

	// Resolve branch to a worktree path
	wtPath, err := resolveWorktreePathByName(branch, worktrees)
	if err != nil {
		return err
	}

	// Find the worktree struct
	var targetWt *git.Worktree
	for i := range worktrees {
		if worktrees[i].Path == wtPath {
			targetWt = &worktrees[i]
			break
		}
	}
	if targetWt == nil {
		return fmt.Errorf("worktree not found: %s", branch)
	}

	if targetWt.IsMain {
		return fmt.Errorf("cannot archive the main worktree")
	}

	key := repoID.StateKey(targetWt.Branch)

	if stateStore.IsArchived(key) {
		return fmt.Errorf("worktree '%s' is already archived", targetWt.Branch)
	}

	if setErr := stateStore.SetArchived(key, true); setErr != nil {
		return fmt.Errorf("failed to archive worktree: %w", setErr)
	}

	_, err = fmt.Fprintf(w, "Archived %s\n", targetWt.Branch)
	return err
}

// NewUnarchiveCommand creates the unarchive command.
func NewUnarchiveCommand() *cli.Command {
	return &cli.Command{
		Name:          "unarchive",
		Usage:         "Remove archived status from a worktree",
		UsageText:     "wtp unarchive <branch>",
		Description:   "Removes the archived status from a worktree, making it visible in 'wtp list' again.",
		ShellComplete: completeArchivedBranches,
		Action:        unarchiveCommand,
	}
}

func unarchiveCommand(_ context.Context, cmd *cli.Command) error {
	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	branch := cmd.Args().Get(0)
	if branch == "" {
		return fmt.Errorf("branch name is required\n\nUsage: wtp unarchive <branch>")
	}

	cwd, err := archiveGetwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	repo, err := git.NewRepository(cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	remoteURL, err := repo.GetRemoteURL("origin")
	if err != nil {
		return fmt.Errorf("cannot unarchive: no remote 'origin' found")
	}

	repoID, err := remote.Parse(remoteURL)
	if err != nil {
		return fmt.Errorf("cannot unarchive: failed to parse remote URL: %w", err)
	}

	return unarchiveCommandCore(w, branch, repoID, state.NewStore())
}

// unarchiveCommandCore is the testable core of the unarchive command.
func unarchiveCommandCore(
	w io.Writer,
	branch string,
	repoID remote.RepoIdentifier,
	stateStore *state.Store,
) error {
	key := repoID.StateKey(branch)

	if !stateStore.IsArchived(key) {
		return fmt.Errorf("worktree '%s' is not archived", branch)
	}

	if err := stateStore.SetArchived(key, false); err != nil {
		return fmt.Errorf("failed to unarchive worktree: %w", err)
	}

	_, err := fmt.Fprintf(w, "Unarchived %s\n", branch)
	return err
}

// completeNonArchivedBranches provides tab completion for archive command (non-archived branches).
func completeNonArchivedBranches(_ context.Context, _ *cli.Command) {
	cwd, err := archiveGetwd()
	if err != nil {
		return
	}

	executor := command.NewRealExecutor()
	result, err := executor.Execute([]command.Command{command.GitWorktreeList()})
	if err != nil {
		return
	}
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

	var repoID *remote.RepoIdentifier
	if repo, err := git.NewRepository(cwd); err == nil {
		if remoteURL, err := repo.GetRemoteURL("origin"); err == nil {
			if id, err := remote.Parse(remoteURL); err == nil {
				repoID = &id
			}
		}
	}

	stateStore := state.NewStore()

	for _, wt := range worktrees {
		if wt.IsMain || wt.Branch == "" || wt.Branch == detachedKeyword {
			continue
		}
		if repoID != nil {
			key := repoID.StateKey(wt.Branch)
			if stateStore.IsArchived(key) {
				continue
			}
		}
		fmt.Println(wt.Branch)
	}
}

// completeArchivedBranches provides tab completion for unarchive command (archived branches only).
func completeArchivedBranches(_ context.Context, _ *cli.Command) {
	cwd, err := archiveGetwd()
	if err != nil {
		return
	}

	executor := command.NewRealExecutor()
	result, err := executor.Execute([]command.Command{command.GitWorktreeList()})
	if err != nil {
		return
	}
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

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

	stateStore := state.NewStore()
	st, err := stateStore.Load()
	if err != nil {
		return
	}

	for _, wt := range worktrees {
		if wt.IsMain || wt.Branch == "" {
			continue
		}
		key := repoID.StateKey(wt.Branch)
		if st.Worktrees[key].Archived {
			fmt.Println(wt.Branch)
		}
	}
}
