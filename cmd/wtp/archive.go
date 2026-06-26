package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/errors"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

var archiveGetwd = os.Getwd

// gitQuerier abstracts the git.Repository methods needed by archive/unarchive
// so that unit tests can provide mock implementations.
type gitQuerier interface {
	IsWorktreeDirty(worktreePath string) (bool, error)
	HasUnpushedCommits(branch string) (bool, error)
	CommitExists(sha string) (bool, error)
	BranchExists(branch string) (bool, error)
	GetMainWorktreePath() (string, error)
}

// NewArchiveCommand creates the archive command.
func NewArchiveCommand() *cli.Command {
	return &cli.Command{
		Name:      "archive",
		Usage:     "Archive a worktree (removes worktree and branch, records recovery metadata)",
		UsageText: "wtp archive <branch>",
		Description: "Archives the worktree for the given branch by removing it " +
			"from git and recording recovery metadata for later restoration " +
			"via 'wtp unarchive'.",
		ShellComplete: completeNonArchivedBranches,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "force",
				Aliases: []string{"f"},
				Usage:   "Force archive even if worktree is dirty or has unpushed commits",
			},
		},
		Action: archiveCommand,
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

	force := cmd.Bool("force")

	cwd, err := archiveGetwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	executor := command.NewRealExecutor()
	result, err := executor.Execute(
		[]command.Command{command.GitWorktreeList()},
	)
	if err != nil {
		return errors.GitCommandFailed("git worktree list", err.Error())
	}
	worktrees := git.ParseWorktreeListOutput(result.Results[0].Output)

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

	return archiveCommandCore(
		w, branch, cwd, force,
		worktrees, repoID, state.NewStore(), executor, repo,
	)
}

// archiveCommandCore is the testable core of the archive command.
func archiveCommandCore(
	w io.Writer,
	branch string,
	cwd string,
	force bool,
	worktrees []git.Worktree,
	repoID remote.RepoIdentifier,
	stateStore *state.Store,
	executor command.Executor,
	repo gitQuerier,
) error {
	if branch == "@" || branch == "root" {
		return fmt.Errorf("cannot archive the main worktree")
	}

	key := repoID.StateKey(branch)

	ws, err := buildArchiveState(
		key, branch, cwd, force, worktrees, stateStore, repo,
	)
	if err != nil {
		return err
	}

	if archiveErr := state.PerformArchive(executor, stateStore, key, ws); archiveErr != nil {
		return archiveErr
	}

	// Clear SuppressAutoArchive if set (manual archive = auto-archive should resume)
	_ = stateStore.WithLock(func(st state.State) (state.State, error) {
		entry := st.Worktrees[key]
		if entry.SuppressAutoArchive {
			entry.SuppressAutoArchive = false
			st.Worktrees[key] = entry
		}
		return st, nil
	})

	_, writeErr := fmt.Fprintf(w, "Archived %s\n", branch)
	return writeErr
}

// buildArchiveState returns the WorktreeState for archiving. On idempotent
// retries (entry already archived) it loads the existing entry. For fresh
// archives it validates the worktree and constructs new metadata.
func buildArchiveState(
	key, branch, cwd string,
	force bool,
	worktrees []git.Worktree,
	stateStore *state.Store,
	repo gitQuerier,
) (*state.WorktreeState, error) {
	// Idempotency: if already archived, return existing entry for retry
	if stateStore.IsArchived(key) {
		st, err := stateStore.Load()
		if err != nil {
			return nil, fmt.Errorf("failed to load state: %w", err)
		}
		existing := st.Worktrees[key]
		return &existing, nil
	}

	targetWt, err := findArchiveTarget(branch, cwd, worktrees)
	if err != nil {
		return nil, err
	}

	if err := validateArchiveSafety(targetWt, branch, force, repo); err != nil {
		return nil, err
	}

	return &state.WorktreeState{
		Archived:     true,
		ArchivedAt:   time.Now(),
		CommitSHA:    targetWt.HEAD,
		Branch:       targetWt.Branch,
		WorktreePath: targetWt.Path,
	}, nil
}

// findArchiveTarget resolves the branch name to a non-main, non-current worktree.
func findArchiveTarget(
	branch, cwd string,
	worktrees []git.Worktree,
) (*git.Worktree, error) {
	wtPath, err := resolveWorktreePathByName(branch, worktrees)
	if err != nil {
		return nil, err
	}

	var targetWt *git.Worktree
	for i := range worktrees {
		if worktrees[i].Path == wtPath {
			targetWt = &worktrees[i]
			break
		}
	}
	if targetWt == nil {
		return nil, fmt.Errorf("worktree not found: %s", branch)
	}

	if targetWt.IsMain {
		return nil, fmt.Errorf("cannot archive the main worktree")
	}

	absTargetPath, err := filepath.Abs(targetWt.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve worktree path: %w", err)
	}
	absCwd, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current directory: %w", err)
	}
	if isPathWithin(absTargetPath, absCwd) {
		return nil, fmt.Errorf("cannot archive the current worktree")
	}

	return targetWt, nil
}

// validateArchiveSafety checks for dirty worktree and unpushed commits
// unless force is true.
func validateArchiveSafety(
	targetWt *git.Worktree, branch string, force bool, repo gitQuerier,
) error {
	if force {
		return nil
	}

	dirty, err := repo.IsWorktreeDirty(targetWt.Path)
	if err != nil {
		return fmt.Errorf("failed to check worktree status: %w", err)
	}
	if dirty {
		return fmt.Errorf(
			"worktree '%s' has uncommitted changes (use --force to override)",
			branch,
		)
	}

	// Skip unpushed check on error (no upstream configured)
	unpushed, unpushedErr := repo.HasUnpushedCommits(targetWt.Branch)
	if unpushedErr == nil && unpushed {
		return fmt.Errorf(
			"worktree '%s' has unpushed commits (use --force to override)",
			branch,
		)
	}

	return nil
}

// NewUnarchiveCommand creates the unarchive command.
func NewUnarchiveCommand() *cli.Command {
	return &cli.Command{
		Name:      "unarchive",
		Usage:     "Restore an archived worktree from recorded metadata",
		UsageText: "wtp unarchive <branch>",
		Description: "Restores a previously archived worktree by recreating " +
			"it from the recorded commit SHA and re-running post-create hooks.",
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

	executor := command.NewRealExecutor()
	return unarchiveCommandCore(
		w, branch, repoID, state.NewStore(), executor, repo,
	)
}

// unarchiveCommandCore is the testable core of the unarchive command.
func unarchiveCommandCore(
	w io.Writer,
	branch string,
	repoID remote.RepoIdentifier,
	stateStore *state.Store,
	executor command.Executor,
	repo gitQuerier,
) error {
	key := repoID.StateKey(branch)

	entry, err := loadArchivedEntry(stateStore, key, branch)
	if err != nil {
		return err
	}

	if err := validateUnarchivePrereqs(repo, entry, branch); err != nil {
		return err
	}

	worktreePath := resolveUnarchivePath(
		entry.WorktreePath, repoID, branch,
	)

	if ensureErr := xdg.EnsureDir(filepath.Dir(worktreePath)); ensureErr != nil {
		return ensureErr
	}

	if err := createWorktreeFromSHA(
		executor, worktreePath, branch, entry.CommitSHA,
	); err != nil {
		return err
	}

	// Re-run post-create hooks (worktree was recreated from scratch)
	runUnarchiveHooks(w, repo, worktreePath)

	if clearErr := stateStore.ClearArchived(key); clearErr != nil {
		return fmt.Errorf("failed to clear archived state: %w", clearErr)
	}

	_, writeErr := fmt.Fprintf(w, "Unarchived %s at %s\n", branch, worktreePath)
	return writeErr
}

// loadArchivedEntry loads and validates an archived state entry.
func loadArchivedEntry(
	stateStore *state.Store, key, branch string,
) (*state.WorktreeState, error) {
	st, err := stateStore.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load state: %w", err)
	}

	entry, ok := st.Worktrees[key]
	if !ok || !entry.Archived {
		return nil, fmt.Errorf("worktree '%s' is not archived", branch)
	}

	if entry.CommitSHA == "" {
		return nil, fmt.Errorf(
			"cannot unarchive legacy entry — no recovery metadata",
		)
	}

	return &entry, nil
}

// validateUnarchivePrereqs checks that the SHA exists and the branch name
// is not already taken.
func validateUnarchivePrereqs(
	repo gitQuerier, entry *state.WorktreeState, branch string,
) error {
	shaExists, err := repo.CommitExists(entry.CommitSHA)
	if err != nil {
		return fmt.Errorf("failed to check commit existence: %w", err)
	}
	if !shaExists {
		return fmt.Errorf(
			"commit %s no longer exists in the object store",
			entry.CommitSHA,
		)
	}

	branchExists, err := repo.BranchExists(branch)
	if err != nil {
		return fmt.Errorf("failed to check branch existence: %w", err)
	}
	if branchExists {
		return fmt.Errorf("branch '%s' already exists locally", branch)
	}

	return nil
}

// createWorktreeFromSHA runs git worktree add -b <branch> <path> <sha>.
func createWorktreeFromSHA(
	executor command.Executor,
	worktreePath, branch, sha string,
) error {
	addCmd := command.GitWorktreeAdd(
		worktreePath, sha,
		command.GitWorktreeAddOptions{Branch: branch},
	)
	result, err := executor.Execute([]command.Command{addCmd})
	if err != nil {
		return fmt.Errorf("failed to restore worktree: %w", err)
	}
	if len(result.Results) > 0 && result.Results[0].Error != nil {
		return fmt.Errorf(
			"failed to restore worktree: %s", result.Results[0].Output,
		)
	}
	return nil
}

// runUnarchiveHooks loads the repo config and runs post-create hooks.
// Failures are printed as warnings rather than causing the unarchive to fail.
func runUnarchiveHooks(w io.Writer, repo gitQuerier, worktreePath string) {
	mainPath, err := repo.GetMainWorktreePath()
	if err != nil {
		return
	}
	cfg, err := config.LoadConfig(mainPath)
	if err != nil {
		return
	}
	if hookErr := executePostCreateHooks(w, cfg, mainPath, worktreePath); hookErr != nil {
		_, _ = fmt.Fprintf(w, "Warning: Hook execution failed: %v\n", hookErr)
	}
}

// resolveUnarchivePath determines where to restore an archived worktree.
// It tries the original recorded path first; if the parent doesn't exist or
// the path is already occupied, it falls back to centralized storage.
func resolveUnarchivePath(
	originalPath string,
	repoID remote.RepoIdentifier,
	branch string,
) string {
	if originalPath != "" {
		parentDir := filepath.Dir(originalPath)
		if info, err := os.Stat(parentDir); err == nil && info.IsDir() {
			if _, statErr := os.Stat(originalPath); os.IsNotExist(statErr) {
				return originalPath
			}
		}
	}
	return filepath.Join(
		xdg.WorktreeStorageRoot(), repoID.StoragePath(), branch,
	)
}

// completeNonArchivedBranches provides tab completion for the archive command.
func completeNonArchivedBranches(_ context.Context, _ *cli.Command) {
	cwd, err := archiveGetwd()
	if err != nil {
		return
	}

	executor := command.NewRealExecutor()
	result, err := executor.Execute(
		[]command.Command{command.GitWorktreeList()},
	)
	if err != nil {
		return
	}
	worktrees := git.ParseWorktreeListOutput(result.Results[0].Output)

	var repoID *remote.RepoIdentifier
	if repo, repoErr := git.NewRepository(cwd); repoErr == nil {
		if remoteURL, urlErr := repo.GetRemoteURL("origin"); urlErr == nil {
			if id, parseErr := remote.Parse(remoteURL); parseErr == nil {
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

// completeArchivedBranches provides tab completion for the unarchive command
// by reading archived entries from state.json.
func completeArchivedBranches(_ context.Context, _ *cli.Command) {
	cwd, err := archiveGetwd()
	if err != nil {
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

	stateStore := state.NewStore()
	st, err := stateStore.Load()
	if err != nil {
		return
	}

	prefix := repoID.StoragePath() + "::"
	for key, entry := range st.Worktrees {
		if entry.Archived && strings.HasPrefix(key, prefix) {
			_, branch := remote.ParseStateKey(key)
			if branch != "" {
				fmt.Println(branch)
			}
		}
	}
}
