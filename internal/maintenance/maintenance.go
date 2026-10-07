// Package maintenance keeps wtp's view of GitHub current without making
// interactive commands wait on the network.
//
// Reap is the only work done inline before a command: it reads the state file
// and drops expired archive entries. Everything that talks to GitHub lives in
// Runner.Sync, which `wtp sync` runs on demand and the scheduled agent runs in
// the background: it checks each worktree's PR, auto-archives merged or closed
// ones and refreshes the PR/CI cache that `wtp list` reads.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

// ghConcurrency bounds concurrent gh processes per repository, so a sync
// costs a few short-lived processes at a time rather than one per worktree.
const ghConcurrency = 4

// ErrGHUnavailable reports that the gh CLI is not on the PATH.
var ErrGHUnavailable = errors.New("gh CLI not found on PATH")

// Variables for testing.
var (
	isGHAvailable   = github.IsAvailable
	getPRForBranch  = github.GetPRForBranch
	getCIStatus     = github.GetCIStatus
	newExecutor     = command.NewRealExecutor
	isWorktreeDirty = func(ctx context.Context, mainRepoPath, worktreePath string) (bool, error) {
		repo, err := git.NewRepository(mainRepoPath, procenv.From(ctx).Environ)
		if err != nil {
			return false, err
		}
		return repo.IsWorktreeDirty(worktreePath)
	}
	busyPaths = processWorkingDirs
	timeNow   = time.Now
)

// Reap drops expired and legacy archive entries across all repositories. It
// reads the state file without locking and only takes the lock to write when
// something is due, so the common case costs one file read.
func Reap(stateStore *state.Store, retention time.Duration, w io.Writer) error {
	st, err := stateStore.Load()
	if err != nil {
		return err
	}
	now := timeNow()
	if !anyReapable(st, now, retention) {
		return nil
	}

	var legacyCount int
	err = stateStore.WithLock(func(st state.State) (state.State, error) {
		for key, ws := range st.Worktrees {
			switch {
			case ws.IsLegacy():
				delete(st.Worktrees, key)
				legacyCount++
			case isExpired(&ws, now, retention):
				delete(st.Worktrees, key)
			}
		}
		return st, nil
	})
	if err != nil {
		return err
	}

	if legacyCount > 0 {
		_, _ = fmt.Fprintf(w, "Cleaned up %d legacy archive entries (no recovery metadata)\n", legacyCount)
	}
	return nil
}

func anyReapable(st state.State, now time.Time, retention time.Duration) bool {
	for _, ws := range st.Worktrees {
		if ws.IsLegacy() || isExpired(&ws, now, retention) {
			return true
		}
	}
	return false
}

func isExpired(ws *state.WorktreeState, now time.Time, retention time.Duration) bool {
	if !ws.Archived {
		return false
	}
	exp := ws.ExpirationTime()
	return !exp.IsZero() && now.Sub(exp) > retention
}

// Runner syncs a single repository with GitHub.
type Runner struct {
	stateStore   *state.Store
	cacheStore   *cache.Store
	repoID       *remote.RepoIdentifier
	mainRepoPath string
	warn         io.Writer
}

// NewRunner creates a Runner for the repository whose main worktree is at
// mainRepoPath. Per-branch failures are reported to warn.
func NewRunner(
	stateStore *state.Store, cacheStore *cache.Store,
	repoID *remote.RepoIdentifier, mainRepoPath string, warn io.Writer,
) *Runner {
	return &Runner{
		stateStore:   stateStore,
		cacheStore:   cacheStore,
		repoID:       repoID,
		mainRepoPath: mainRepoPath,
		warn:         warn,
	}
}

// Archived describes a worktree that Sync auto-archived.
type Archived struct {
	Repo     string
	Branch   string
	PRNumber int
	PRState  string
}

// String renders the archive the way it is reported to the user.
func (a Archived) String() string {
	return fmt.Sprintf("Auto-archived %s in %s (PR #%d %s)", a.Branch, a.Repo, a.PRNumber, a.PRState)
}

// Result summarizes one repository sync.
type Result struct {
	Checked  int
	Archived []Archived
	Failed   int
}

type branchCheck struct {
	wt    git.Worktree
	key   string
	pr    *github.PRInfo
	ci    *github.CIStatus
	err   error
	ciErr error
}

// Sync checks the PR for every non-archived worktree of the repository,
// auto-archives clean worktrees whose PR is merged or closed and refreshes the
// PR/CI cache for the rest. Network calls run with bounded concurrency; git
// mutations run sequentially afterwards so they never contend on repo locks.
//
// Every subprocess runs in the main worktree rather than the caller's working
// directory, so Sync works the same from the scheduled agent as from a shell.
func (r *Runner) Sync(ctx context.Context) (Result, error) {
	if !isGHAvailable() {
		return Result{}, ErrGHUnavailable
	}

	env := *procenv.From(ctx)
	env.Dir, env.DirErr = r.mainRepoPath, nil
	ctx = procenv.WithEnv(ctx, &env)
	executor := newExecutor(&env)

	listResult, err := executor.Execute([]command.Command{command.GitWorktreeList()})
	if err != nil {
		return Result{}, fmt.Errorf("listing worktrees: %w", err)
	}
	if res := listResult.Results[0]; res.Error != nil {
		return Result{}, fmt.Errorf("listing worktrees: %w (output: %s)", res.Error, res.Output)
	}

	st, err := r.stateStore.Load()
	if err != nil {
		return Result{}, fmt.Errorf("loading state: %w", err)
	}

	checks := r.collectChecks(git.ParseWorktreeListOutput(listResult.Results[0].Output), st)
	r.fetchAll(ctx, checks)

	result := Result{Checked: len(checks)}
	entries := make(map[string]cache.WorktreeCache, len(checks))
	var busy []string
	busyLoaded := false

	for i := range checks {
		c := &checks[i]
		if c.err != nil {
			result.Failed++
			_, _ = fmt.Fprintf(r.warn, "warning: failed to check PR for %s: %v\n", c.wt.Branch, c.err)
			continue
		}

		if c.pr != nil && (c.pr.State == github.StateMerged || c.pr.State == github.StateClosed) {
			if !busyLoaded {
				busy, busyLoaded = busyPaths(ctx), true
			}
			if archived, ok := r.tryArchive(ctx, executor, c, busy); ok {
				result.Archived = append(result.Archived, archived)
				continue
			}
		}

		if c.ciErr != nil {
			result.Failed++
			_, _ = fmt.Fprintf(r.warn, "warning: failed to check CI for %s: %v\n", c.wt.Branch, c.ciErr)
			continue
		}
		entries[c.key] = cacheEntry(c.pr, c.ci)
	}

	if err := r.cacheStore.SetBatch(entries); err != nil {
		_, _ = fmt.Fprintf(r.warn, "warning: failed to write PR/CI cache: %v\n", err)
	}

	return result, nil
}

func (r *Runner) collectChecks(worktrees []git.Worktree, st state.State) []branchCheck {
	checks := make([]branchCheck, 0, len(worktrees))
	for _, wt := range worktrees {
		if wt.IsMain || wt.Branch == "" || wt.Branch == git.DetachedKeyword {
			continue
		}
		key := r.repoID.StateKey(wt.Branch)
		if ws := st.Worktrees[key]; ws.Archived || ws.SuppressAutoArchive {
			continue
		}
		checks = append(checks, branchCheck{wt: wt, key: key})
	}
	return checks
}

// fetchAll fetches PR and, for open PRs, CI status for each check. CI is
// skipped for merged and closed PRs because they are about to be archived and
// their checks no longer matter.
func (r *Runner) fetchAll(ctx context.Context, checks []branchCheck) {
	var g errgroup.Group
	g.SetLimit(ghConcurrency)
	for i := range checks {
		c := &checks[i]
		g.Go(func() error {
			if ctx.Err() != nil {
				c.err = ctx.Err()
				return nil
			}
			c.pr, c.err = getPRForBranch(ctx, r.mainRepoPath, c.wt.Branch)
			if c.err != nil || c.pr == nil || c.pr.State == github.StateMerged || c.pr.State == github.StateClosed {
				return nil
			}
			c.ci, c.ciErr = getCIStatus(ctx, r.mainRepoPath, c.wt.Branch)
			return nil
		})
	}
	_ = g.Wait()
}

// tryArchive archives a worktree whose PR is merged or closed. It refuses when
// the worktree has uncommitted changes or a running process (typically a
// shell) has its working directory inside it, since archiving removes the
// directory out from under that process.
func (r *Runner) tryArchive(
	ctx context.Context, executor command.Executor, c *branchCheck, busy []string,
) (Archived, bool) {
	dirty, err := isWorktreeDirty(ctx, r.mainRepoPath, c.wt.Path)
	if err != nil {
		_, _ = fmt.Fprintf(r.warn, "warning: failed to check dirty status for %s: %v\n", c.wt.Branch, err)
		return Archived{}, false
	}
	if dirty {
		_, _ = fmt.Fprintf(r.warn, "Skipped auto-archive of %s: worktree has uncommitted changes\n", c.wt.Branch)
		return Archived{}, false
	}
	if pathInUse(c.wt.Path, busy) {
		_, _ = fmt.Fprintf(r.warn, "Skipped auto-archive of %s: worktree is in use by a running process\n", c.wt.Branch)
		return Archived{}, false
	}

	now := timeNow()
	prClosedAt := c.pr.ClosedAt
	if prClosedAt.IsZero() {
		prClosedAt = now
	}
	ws := &state.WorktreeState{
		Archived:     true,
		ArchivedAt:   now,
		PRClosedAt:   prClosedAt,
		CommitSHA:    c.wt.HEAD,
		Branch:       c.wt.Branch,
		WorktreePath: c.wt.Path,
	}
	if _, err := state.PerformArchive(executor, r.stateStore, c.key, ws, r.warn); err != nil {
		_, _ = fmt.Fprintf(r.warn, "warning: failed to auto-archive %s: %v\n", c.wt.Branch, err)
		return Archived{}, false
	}
	_ = r.cacheStore.Delete(c.key)

	return Archived{
		Repo:     r.repoID.StoragePath(),
		Branch:   c.wt.Branch,
		PRNumber: c.pr.Number,
		PRState:  c.pr.State,
	}, true
}

func cacheEntry(pr *github.PRInfo, ci *github.CIStatus) cache.WorktreeCache {
	entry := cache.WorktreeCache{CIStatus: github.FormatCIStatus(ci)}
	if pr != nil {
		entry.PRNumber = pr.Number
		entry.PRState = pr.State
		entry.PRTitle = pr.Title
	}
	return entry
}

func pathInUse(worktreePath string, busy []string) bool {
	for _, p := range busy {
		if p == worktreePath || strings.HasPrefix(p, worktreePath+"/") {
			return true
		}
	}
	return false
}
