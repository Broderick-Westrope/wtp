// Package maintenance implements the two-tier pre-command maintenance system.
// The cheap tier reaps expired and legacy state entries (pure file I/O).
// The expensive tier checks PR states via `gh` and auto-archives merged/closed PRs,
// throttled by a per-repo timestamp file.
package maintenance

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

// Variables for testing.
var (
	isGHAvailable   = github.IsAvailable
	getPRForBranch  = github.GetPRForBranch
	newExecutor     = command.NewRealExecutor
	isWorktreeDirty = func(mainRepoPath, worktreePath string) (bool, error) {
		repo, err := git.NewRepository(mainRepoPath)
		if err != nil {
			return false, err
		}
		return repo.IsWorktreeDirty(worktreePath)
	}
	timeNow = time.Now
)

// Runner performs cheap and expensive maintenance for a single repository.
type Runner struct {
	stateStore   *state.Store
	globalCfg    config.GlobalConfig
	repoID       *remote.RepoIdentifier
	mainRepoPath string
	stderr       io.Writer
}

// NewRunner creates a Runner for the given repository.
func NewRunner(
	stateStore *state.Store, globalCfg config.GlobalConfig,
	repoID *remote.RepoIdentifier, mainRepoPath string, stderr io.Writer,
) *Runner {
	return &Runner{
		stateStore:   stateStore,
		globalCfg:    globalCfg,
		repoID:       repoID,
		mainRepoPath: mainRepoPath,
		stderr:       stderr,
	}
}

// RunCheap reaps expired and legacy archive entries for the current repo.
// This is a pure file-I/O operation with no network calls.
func (r *Runner) RunCheap() error {
	st, err := r.stateStore.Load()
	if err != nil {
		return nil //nolint:nilerr // best-effort
	}

	prefix := r.repoID.StoragePath() + "::"
	now := timeNow()

	var toDelete []string
	var legacyCount int

	for key, ws := range st.Worktrees {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		if ws.IsLegacy() {
			toDelete = append(toDelete, key)
			legacyCount++
			continue
		}

		if !ws.Archived {
			continue
		}

		exp := ws.ExpirationTime()
		if exp.IsZero() {
			continue
		}

		if now.Sub(exp) > r.globalCfg.ArchiveRetention {
			toDelete = append(toDelete, key)
		}
	}

	if len(toDelete) == 0 {
		return nil
	}

	_ = r.stateStore.WithLock(func(st state.State) (state.State, error) {
		for _, key := range toDelete {
			delete(st.Worktrees, key)
		}
		return st, nil
	})

	if legacyCount > 0 {
		_, _ = fmt.Fprintf(r.stderr, "Cleaned up %d legacy archive entries (no recovery metadata)\n", legacyCount)
	}

	return nil
}

// maintenanceDir returns the path to the maintenance timestamp directory.
func maintenanceDir() string {
	return filepath.Join(xdg.WtpDataDir(), "maintenance")
}

// sanitizeRepoKey replaces "/" with "--" to produce a flat filename.
func sanitizeRepoKey(storagePath string) string {
	return strings.ReplaceAll(storagePath, "/", "--")
}

// throttleFile returns the path to the per-repo throttle timestamp file.
func throttleFile(repoID *remote.RepoIdentifier) string {
	return filepath.Join(maintenanceDir(), sanitizeRepoKey(repoID.StoragePath()))
}

// isThrottled returns true if the throttle file was modified within interval.
func isThrottled(throttlePath string, interval time.Duration) bool {
	info, err := os.Stat(throttlePath)
	if err != nil {
		return false
	}

	return timeNow().Sub(info.ModTime()) < interval
}

// touchThrottleFile creates or updates the throttle file.
func touchThrottleFile(throttlePath string) error {
	if err := xdg.EnsureDir(filepath.Dir(throttlePath)); err != nil {
		return err
	}

	now := timeNow()
	if err := os.Chtimes(throttlePath, now, now); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// File doesn't exist yet — create it.
		f, createErr := os.Create(throttlePath) //nolint:gosec // path is derived from XDG
		if createErr != nil {
			return createErr
		}
		return f.Close()
	}

	return nil
}

// RunExpensive checks PR states for the current repo's worktrees and
// auto-archives branches with MERGED or CLOSED PRs. Throttled by a per-repo
// timestamp file.
func (r *Runner) RunExpensive(ctx context.Context) error { //nolint:gocyclo // orchestrates many checks per worktree
	tp := throttleFile(r.repoID)
	if isThrottled(tp, r.globalCfg.MaintenanceInterval) {
		return nil
	}

	if !isGHAvailable() {
		return nil
	}

	executor := newExecutor()

	listCmd := command.GitWorktreeList()
	result, err := executor.Execute([]command.Command{listCmd})
	if err != nil {
		return nil //nolint:nilerr // best-effort
	}

	worktrees := git.ParseWorktreeListOutput(result.Results[0].Output)

	st, err := r.stateStore.Load()
	if err != nil {
		return nil //nolint:nilerr // best-effort
	}

	for _, wt := range worktrees {
		if wt.IsMain || wt.Branch == "" || wt.Branch == "detached" {
			continue
		}

		if ctx.Err() != nil {
			break
		}

		key := r.repoID.StateKey(wt.Branch)
		ws := st.Worktrees[key]

		if ws.SuppressAutoArchive {
			continue
		}

		if ws.Archived {
			continue
		}

		pr, prErr := getPRForBranch(ctx, wt.Branch)
		if prErr != nil {
			_, _ = fmt.Fprintf(r.stderr, "warning: failed to check PR for %s: %v\n", wt.Branch, prErr)
			continue
		}

		if pr == nil {
			continue
		}

		if pr.State != github.StateMerged && pr.State != github.StateClosed {
			continue
		}

		dirty, dirtyErr := isWorktreeDirty(r.mainRepoPath, wt.Path)
		if dirtyErr != nil {
			_, _ = fmt.Fprintf(r.stderr, "warning: failed to check dirty status for %s: %v\n", wt.Branch, dirtyErr)
			continue
		}

		if dirty {
			_, _ = fmt.Fprintf(r.stderr, "Skipped auto-archive of %s: worktree has uncommitted changes\n", wt.Branch)
			continue
		}

		now := timeNow()
		archiveWS := &state.WorktreeState{
			Archived:     true,
			ArchivedAt:   now,
			PRClosedAt:   now,
			CommitSHA:    wt.HEAD,
			Branch:       wt.Branch,
			WorktreePath: wt.Path,
		}

		if archiveErr := state.PerformArchive(executor, r.stateStore, key, archiveWS); archiveErr != nil {
			_, _ = fmt.Fprintf(r.stderr, "warning: failed to auto-archive %s: %v\n", wt.Branch, archiveErr)
			continue
		}

		_, _ = fmt.Fprintf(r.stderr, "Auto-archived %s (PR #%d %s)\n", wt.Branch, pr.Number, pr.State)
	}

	_ = touchThrottleFile(tp)

	return nil
}
