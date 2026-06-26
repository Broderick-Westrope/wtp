package maintenance_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/config"
	"github.com/Broderick-Westrope/wtp/v3/internal/github"
	"github.com/Broderick-Westrope/wtp/v3/internal/maintenance"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

func setupTestEnv(t *testing.T) *state.Store {
	t.Helper()

	dir := t.TempDir()
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", dir)
	axdg.Reload()

	return state.NewStore()
}

func defaultCfg() config.GlobalConfig {
	return config.GlobalConfig{
		CacheTTL:            60 * time.Second,
		ArchiveRetention:    240 * time.Hour,
		MaintenanceInterval: 10 * time.Minute,
	}
}

func repoID() *remote.RepoIdentifier {
	return &remote.RepoIdentifier{Owner: "owner", Repo: "repo"}
}

// ─── RunCheap tests ─────────────────────────────────────────────────────────

func TestRunCheap_ReapsExpiredEntries(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	expired := time.Now().Add(-cfg.ArchiveRetention - time.Hour)

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("old-branch"): {
				Archived:   true,
				ArchivedAt: expired,
				CommitSHA:  "abc123",
				Branch:     "old-branch",
			},
		},
	}))

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunCheap())

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
}

func TestRunCheap_KeepsFreshEntries(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	fresh := time.Now().Add(-time.Hour)

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("fresh-branch"): {
				Archived:   true,
				ArchivedAt: fresh,
				CommitSHA:  "abc123",
				Branch:     "fresh-branch",
			},
		},
	}))

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunCheap())

	st, err := store.Load()
	require.NoError(t, err)
	assert.Len(t, st.Worktrees, 1)
}

func TestRunCheap_ReapsLegacyEntries(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("legacy"): {
				Archived: true,
				// No CommitSHA, ArchivedAt, etc. — legacy entry
			},
		},
	}))

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunCheap())

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
	assert.Contains(t, buf.String(), "Cleaned up 1 legacy archive entries")
}

func TestRunCheap_UsesCorrectExpirationPrecedence(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	now := time.Now()
	// PRClosedAt is expired, ArchivedAt is fresh — should use PRClosedAt (expired)
	expiredPR := now.Add(-cfg.ArchiveRetention - time.Hour)
	freshArchive := now.Add(-time.Hour)

	// ArchivedAt is expired, no PRClosedAt — should use ArchivedAt
	expiredArchive := now.Add(-cfg.ArchiveRetention - 2*time.Hour)

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("pr-expired"): {
				Archived:   true,
				ArchivedAt: freshArchive,
				PRClosedAt: expiredPR,
				CommitSHA:  "abc",
				Branch:     "pr-expired",
			},
			id.StateKey("archive-expired"): {
				Archived:   true,
				ArchivedAt: expiredArchive,
				CommitSHA:  "def",
				Branch:     "archive-expired",
			},
		},
	}))

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunCheap())

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
}

func TestRunCheap_OnlyAffectsCurrentRepo(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	otherID := remote.RepoIdentifier{Owner: "other", Repo: "other-repo"}
	expired := time.Now().Add(-cfg.ArchiveRetention - time.Hour)

	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("my-branch"): {
				Archived:   true,
				ArchivedAt: expired,
				CommitSHA:  "abc",
				Branch:     "my-branch",
			},
			otherID.StateKey("other-branch"): {
				Archived:   true,
				ArchivedAt: expired,
				CommitSHA:  "def",
				Branch:     "other-branch",
			},
		},
	}))

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunCheap())

	st, err := store.Load()
	require.NoError(t, err)
	assert.Len(t, st.Worktrees, 1)
	_, ok := st.Worktrees[otherID.StateKey("other-branch")]
	assert.True(t, ok, "other repo's entry should be untouched")
}

// ─── RunExpensive tests ─────────────────────────────────────────────────────

// mockExecutor implements command.Executor for testing.
type mockExecutor struct {
	output string
	err    error
}

func (m *mockExecutor) Execute(_ []command.Command) (*command.ExecutionResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &command.ExecutionResult{
		Results: []command.Result{
			{Output: m.output},
		},
	}, nil
}

// porcelain builds a git worktree list --porcelain output for testing.
func porcelain(entries ...string) string {
	return strings.Join(entries, "\n")
}

// wtEntry builds a single worktree porcelain block.
func wtEntry(path, head, branch string) string {
	s := "worktree " + path + "\nHEAD " + head + "\n"
	if branch != "" {
		s += "branch refs/heads/" + branch + "\n"
	} else {
		s += "detached\n"
	}
	return s
}

func TestRunExpensive_SkipsWhenThrottled(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	// Create fresh throttle file
	tDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "wtp", "maintenance")
	require.NoError(t, os.MkdirAll(tDir, 0o755))
	tFile := filepath.Join(tDir, "owner--repo")
	require.NoError(t, os.WriteFile(tFile, nil, 0o600))

	prCalled := false
	maintenance.SetGetPRForBranch(func(_ context.Context, _ string) (*github.PRInfo, error) {
		prCalled = true
		return nil, nil
	})
	t.Cleanup(maintenance.RestoreGetPRForBranch)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))
	assert.False(t, prCalled, "should not call gh when throttled")
}

func TestRunExpensive_SkipsWhenGHUnavailable(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	maintenance.SetIsGHAvailable(func() bool { return false })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))
}

func TestRunExpensive_AutoArchivesMergedPR(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	output := porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/feat", "bbb111", "feat"),
	)

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	maintenance.SetNewExecutor(func() command.Executor {
		return &mockExecutor{output: output}
	})
	t.Cleanup(maintenance.RestoreNewExecutor)

	maintenance.SetGetPRForBranch(func(_ context.Context, branch string) (*github.PRInfo, error) {
		if branch == "feat" {
			return &github.PRInfo{Number: 42, State: github.StateMerged}, nil
		}
		return nil, nil
	})
	t.Cleanup(maintenance.RestoreGetPRForBranch)

	maintenance.SetIsWorktreeDirty(func(_, _ string) (bool, error) { return false, nil })
	t.Cleanup(maintenance.RestoreIsWorktreeDirty)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))

	st, err := store.Load()
	require.NoError(t, err)

	ws, ok := st.Worktrees[id.StateKey("feat")]
	require.True(t, ok)
	assert.True(t, ws.Archived)
	assert.Equal(t, "bbb111", ws.CommitSHA)
	assert.Contains(t, buf.String(), "Auto-archived feat (PR #42 MERGED)")
}

func TestRunExpensive_AutoArchivesClosedPR(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	output := porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/fix", "ccc222", "fix"),
	)

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	maintenance.SetNewExecutor(func() command.Executor {
		return &mockExecutor{output: output}
	})
	t.Cleanup(maintenance.RestoreNewExecutor)

	maintenance.SetGetPRForBranch(func(_ context.Context, branch string) (*github.PRInfo, error) {
		if branch == "fix" {
			return &github.PRInfo{Number: 7, State: github.StateClosed}, nil
		}
		return nil, nil
	})
	t.Cleanup(maintenance.RestoreGetPRForBranch)

	maintenance.SetIsWorktreeDirty(func(_, _ string) (bool, error) { return false, nil })
	t.Cleanup(maintenance.RestoreIsWorktreeDirty)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))

	st, err := store.Load()
	require.NoError(t, err)

	ws, ok := st.Worktrees[id.StateKey("fix")]
	require.True(t, ok)
	assert.True(t, ws.Archived)
	assert.Contains(t, buf.String(), "Auto-archived fix (PR #7 CLOSED)")
}

func TestRunExpensive_SkipsDirtyWorktree(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	output := porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/dirty", "ddd333", "dirty"),
	)

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	maintenance.SetNewExecutor(func() command.Executor {
		return &mockExecutor{output: output}
	})
	t.Cleanup(maintenance.RestoreNewExecutor)

	maintenance.SetGetPRForBranch(func(_ context.Context, _ string) (*github.PRInfo, error) {
		return &github.PRInfo{Number: 10, State: github.StateMerged}, nil
	})
	t.Cleanup(maintenance.RestoreGetPRForBranch)

	maintenance.SetIsWorktreeDirty(func(_, _ string) (bool, error) { return true, nil })
	t.Cleanup(maintenance.RestoreIsWorktreeDirty)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))

	st, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, st.Worktrees)
	assert.Contains(t, buf.String(), "Skipped auto-archive of dirty: worktree has uncommitted changes")
}

func TestRunExpensive_SkipsSuppressedBranches(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	// Pre-populate state with SuppressAutoArchive
	require.NoError(t, store.Save(state.State{
		Worktrees: map[string]state.WorktreeState{
			id.StateKey("suppressed"): {SuppressAutoArchive: true},
		},
	}))

	output := porcelain(
		wtEntry("/main", "aaa", "main"),
		wtEntry("/wt/suppressed", "eee444", "suppressed"),
	)

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	maintenance.SetNewExecutor(func() command.Executor {
		return &mockExecutor{output: output}
	})
	t.Cleanup(maintenance.RestoreNewExecutor)

	prCalled := false
	maintenance.SetGetPRForBranch(func(_ context.Context, _ string) (*github.PRInfo, error) {
		prCalled = true
		return &github.PRInfo{Number: 1, State: github.StateMerged}, nil
	})
	t.Cleanup(maintenance.RestoreGetPRForBranch)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))

	assert.False(t, prCalled, "should not check PR for suppressed branch")
}

func TestRunExpensive_TouchesThrottleFile(t *testing.T) {
	store := setupTestEnv(t)
	id := repoID()
	cfg := defaultCfg()

	output := porcelain(wtEntry("/main", "aaa", "main"))

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	maintenance.SetNewExecutor(func() command.Executor {
		return &mockExecutor{output: output}
	})
	t.Cleanup(maintenance.RestoreNewExecutor)

	var buf bytes.Buffer
	runner := maintenance.NewRunner(store, cfg, id, "/tmp/repo", &buf)
	require.NoError(t, runner.RunExpensive(context.Background()))

	tFile := filepath.Join(os.Getenv("XDG_DATA_HOME"), "wtp", "maintenance", "owner--repo")
	_, err := os.Stat(tFile)
	assert.NoError(t, err, "throttle file should exist after RunExpensive")
}

func TestRunExpensive_PerRepoThrottleIsolation(t *testing.T) {
	store := setupTestEnv(t)
	cfg := defaultCfg()

	id1 := &remote.RepoIdentifier{Owner: "owner", Repo: "repo1"}
	id2 := &remote.RepoIdentifier{Owner: "owner", Repo: "repo2"}

	output := porcelain(wtEntry("/main", "aaa", "main"))

	maintenance.SetIsGHAvailable(func() bool { return true })
	t.Cleanup(maintenance.RestoreIsGHAvailable)

	maintenance.SetNewExecutor(func() command.Executor {
		return &mockExecutor{output: output}
	})
	t.Cleanup(maintenance.RestoreNewExecutor)

	// Run for repo1
	var buf1 bytes.Buffer
	r1 := maintenance.NewRunner(store, cfg, id1, "/tmp/repo1", &buf1)
	require.NoError(t, r1.RunExpensive(context.Background()))

	tDir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "wtp", "maintenance")
	_, err := os.Stat(filepath.Join(tDir, "owner--repo1"))
	assert.NoError(t, err, "repo1 throttle file should exist")

	_, err = os.Stat(filepath.Join(tDir, "owner--repo2"))
	assert.True(t, os.IsNotExist(err), "repo2 throttle file should not exist")

	// Run for repo2
	var buf2 bytes.Buffer
	r2 := maintenance.NewRunner(store, cfg, id2, "/tmp/repo2", &buf2)
	require.NoError(t, r2.RunExpensive(context.Background()))

	_, err = os.Stat(filepath.Join(tDir, "owner--repo2"))
	assert.NoError(t, err, "repo2 throttle file should exist after run")
}
