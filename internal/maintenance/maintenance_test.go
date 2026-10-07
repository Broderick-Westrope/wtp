package maintenance_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/cache"
	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/procenv"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

const retention = 240 * time.Hour

func setupTestEnv(t *testing.T) *state.Store {
	t.Helper()

	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	axdg.Reload()

	return state.NewStore()
}

func repoID() *remote.RepoIdentifier {
	return &remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
}

// ─── Reap tests ─────────────────────────────────────────────────────────────

func TestReap_ReapsExpiredEntriesAcrossRepos(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	otherID := remote.RepoIdentifier{Owner: "other", Repo: "other-repo"}
	expired := time.Now().Add(-retention - time.Hour)

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("old"):        {Archived: true, ArchivedAt: expired, CommitSHA: "abc", Branch: "old"},
			otherID.StateKey("older"): {Archived: true, ArchivedAt: expired, CommitSHA: "def", Branch: "older"},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, maintenance.Reap(store, retention, &buf))

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
}

func TestReap_KeepsFreshAndUnarchivedEntries(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("fresh"):      {Archived: true, ArchivedAt: time.Now().Add(-time.Hour), CommitSHA: "abc"},
			id.StateKey("suppressed"): {SuppressAutoArchive: true},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, maintenance.Reap(store, retention, &buf))

	st, err := store.Load()
	require.NoError(t, err)
	assert.Len(t, st.Worktrees, 2)
}

func TestReap_ReapsLegacyEntries(t *testing.T) {
	store := setupTestEnv(t)

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{repoID().StateKey("legacy"): {Archived: true}},
	}))

	var buf bytes.Buffer
	require.NoError(t, maintenance.Reap(store, retention, &buf))

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
	assert.Contains(t, buf.String(), "Cleaned up 1 legacy archive entries")
}

func TestReap_UsesPRClosedAtBeforeArchivedAt(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	now := time.Now()

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("pr-expired"): {
				Archived:   true,
				ArchivedAt: now.Add(-time.Hour),
				PRClosedAt: now.Add(-retention - time.Hour),
				CommitSHA:  "abc",
			},
		},
	}))

	var buf bytes.Buffer
	require.NoError(t, maintenance.Reap(store, retention, &buf))

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
}

// ─── Sync tests ─────────────────────────────────────────────────────────────

// recordingExecutor returns worktree list output for `git worktree list` and
// records every other command.
type recordingExecutor struct {
	mu       sync.Mutex
	output   string
	commands []command.Command
}

func (m *recordingExecutor) Execute(cmds []command.Command) (*command.ExecutionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	results := make([]command.Result, 0, len(cmds))
	for _, c := range cmds {
		m.commands = append(m.commands, c)
		out := ""
		if len(c.Args) > 1 && c.Args[0] == "worktree" && c.Args[1] == "list" {
			out = m.output
		}
		results = append(results, command.Result{Command: c, Output: out})
	}
	return &command.ExecutionResult{Results: results}, nil
}

func porcelain(entries ...string) string {
	return strings.Join(entries, "\n")
}

func wtEntry(path, head, branch string) string {
	s := "worktree " + path + "\nHEAD " + head + "\n"
	if branch != "" {
		s += "branch refs/heads/" + branch + "\n"
	} else {
		s += "detached\n"
	}
	return s
}

type syncFixture struct {
	store    *state.Store
	cache    *cache.Store
	executor *recordingExecutor
	envDir   string
	prCalls  map[string]int
	ciCalls  map[string]int
	mu       sync.Mutex
}

// newSyncFixture stubs every external dependency of Sync. prs maps branch to
// the PR gh would report; branches without an entry have no PR.
func newSyncFixture(t *testing.T, worktreeList string, prs map[string]*github.PRInfo) *syncFixture {
	t.Helper()
	f := &syncFixture{
		store:    setupTestEnv(t),
		cache:    cache.NewStore(),
		executor: &recordingExecutor{output: worktreeList},
		prCalls:  map[string]int{},
		ciCalls:  map[string]int{},
	}

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)
	maintenance.SetNewExecutor(func(env *procenv.Env) command.Executor {
		f.envDir = env.Dir
		return f.executor
	})
	t.Cleanup(maintenance.RestoreNewExecutor)
	maintenance.SetGetPRForBranch(func(_ context.Context, branch string) (*github.PRInfo, error) {
		f.mu.Lock()
		f.prCalls[branch]++
		f.mu.Unlock()
		return prs[branch], nil
	})
	t.Cleanup(maintenance.RestoreGetPRForBranch)
	maintenance.SetGetCIStatus(func(_ context.Context, branch string) (*github.CIStatus, error) {
		f.mu.Lock()
		f.ciCalls[branch]++
		f.mu.Unlock()
		return &github.CIStatus{State: "passing", Total: 2, Passing: 2}, nil
	})
	t.Cleanup(maintenance.RestoreGetCIStatus)
	maintenance.SetIsWorktreeDirty(func(_, _ string) (bool, error) { return false, nil })
	t.Cleanup(maintenance.RestoreIsWorktreeDirty)
	maintenance.SetBusyPaths(nil)
	t.Cleanup(maintenance.RestoreBusyPaths)
	return f
}

func (f *syncFixture) run(t *testing.T) (result maintenance.Result, warnings string) {
	t.Helper()
	var buf bytes.Buffer
	runner := maintenance.NewRunner(f.store, f.cache, repoID(), "/main", &buf)
	result, err := runner.Sync(t.Context())
	require.NoError(t, err)
	return result, buf.String()
}

func TestSync_ReturnsErrGHUnavailable(t *testing.T) {
	store := setupTestEnv(t)
	maintenance.SetIsGHAvailable(func() bool { return false })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	runner := maintenance.NewRunner(store, cache.NewStore(), repoID(), "/main", &bytes.Buffer{})
	_, err := runner.Sync(t.Context())
	assert.ErrorIs(t, err, maintenance.ErrGHUnavailable)
}

func TestSync_RunsInMainWorktree(t *testing.T) {
	f := newSyncFixture(t, porcelain(wtEntry("/main", "aaa", "main")), nil)
	f.run(t)
	assert.Equal(t, "/main", f.envDir, "subprocesses must not depend on the caller's directory")
}

func TestSync_AutoArchivesMergedAndClosedPRs(t *testing.T) {
	f := newSyncFixture(t, porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/feat", "bbb111", "feat"),
		wtEntry("/wt/fix", "ccc222", "fix"),
	), map[string]*github.PRInfo{
		"feat": {Number: 42, State: github.StateMerged},
		"fix":  {Number: 7, State: github.StateClosed},
	})

	result, _ := f.run(t)

	require.Len(t, result.Archived, 2)
	assert.Equal(t, "Auto-archived feat in owner/repo (PR #42 MERGED)", result.Archived[0].String())
	assert.Equal(t, "Auto-archived fix in owner/repo (PR #7 CLOSED)", result.Archived[1].String())

	st, err := f.store.Load()
	require.NoError(t, err)
	ws := st.Worktrees[repoID().StateKey("feat")]
	assert.True(t, ws.Archived)
	assert.Equal(t, "bbb111", ws.CommitSHA)
	assert.Zero(t, f.ciCalls["feat"], "CI is irrelevant for a PR being archived")
}

func TestSync_RefreshesCacheForOpenPRs(t *testing.T) {
	f := newSyncFixture(t, porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/open", "bbb", "open"),
		wtEntry("/wt/nopr", "ccc", "nopr"),
	), map[string]*github.PRInfo{"open": {Number: 3, State: "OPEN", Title: "Open PR"}})

	result, _ := f.run(t)
	assert.Equal(t, 2, result.Checked)
	assert.Empty(t, result.Archived)

	open, ok := f.cache.Get(repoID().StateKey("open"))
	require.True(t, ok)
	assert.Equal(t, 3, open.PRNumber)
	assert.Equal(t, "✓ CI passing", open.CIStatus)

	nopr, ok := f.cache.Get(repoID().StateKey("nopr"))
	require.True(t, ok, "branches without a PR are cached so list knows they were checked")
	assert.Zero(t, nopr.PRNumber)
	assert.Zero(t, f.ciCalls["nopr"])
}

func TestSync_SkipsDirtyWorktree(t *testing.T) {
	f := newSyncFixture(t, porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/dirty", "ddd", "dirty"),
	), map[string]*github.PRInfo{"dirty": {Number: 10, State: github.StateMerged}})
	maintenance.SetIsWorktreeDirty(func(_, _ string) (bool, error) { return true, nil })

	result, out := f.run(t)

	assert.Empty(t, result.Archived)
	assert.Contains(t, out, "Skipped auto-archive of dirty: worktree has uncommitted changes")
	entry, ok := f.cache.Get(repoID().StateKey("dirty"))
	require.True(t, ok, "a merged PR that was not archived still shows in list")
	assert.Equal(t, github.StateMerged, entry.PRState)
}

func TestSync_SkipsWorktreeInUse(t *testing.T) {
	f := newSyncFixture(t, porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/busy", "eee", "busy"),
		wtEntry("/wt/busy-sibling", "fff", "busy-sibling"),
	), map[string]*github.PRInfo{
		"busy":         {Number: 1, State: github.StateMerged},
		"busy-sibling": {Number: 2, State: github.StateMerged},
	})
	maintenance.SetBusyPaths([]string{"/", "/wt/busy/src"})

	result, out := f.run(t)

	require.Len(t, result.Archived, 1, "a path prefix match must not catch a sibling directory")
	assert.Equal(t, "busy-sibling", result.Archived[0].Branch)
	assert.Contains(t, out, "Skipped auto-archive of busy: worktree is in use by a running process")
}

func TestSync_SkipsSuppressedAndArchivedBranches(t *testing.T) {
	f := newSyncFixture(t, porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/suppressed", "eee", "suppressed"),
		wtEntry("/wt/detached", "fff", ""),
	), nil)
	require.NoError(t, f.store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{repoID().StateKey("suppressed"): {SuppressAutoArchive: true}},
	}))

	result, _ := f.run(t)

	assert.Zero(t, result.Checked)
	assert.Empty(t, f.prCalls)
}

func TestSync_ReportsPerBranchFailures(t *testing.T) {
	f := newSyncFixture(t, porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/broken", "bbb", "broken"),
	), nil)
	maintenance.SetGetPRForBranch(func(context.Context, string) (*github.PRInfo, error) {
		return nil, errors.New("rate limited")
	})

	result, out := f.run(t)

	assert.Equal(t, 1, result.Failed)
	assert.Contains(t, out, "failed to check PR for broken: rate limited")
	_, ok := f.cache.Get(repoID().StateKey("broken"))
	assert.False(t, ok, "failed fetches must not poison the cache")
}

func TestParseLsofNames(t *testing.T) {
	out := []byte("p613\nfcwd\nn/\np967\nfcwd\nn/Users/me/wt/feat\n")
	assert.Equal(t, []string{"/", "/Users/me/wt/feat"}, maintenance.ParseLsofNames(out))
}
