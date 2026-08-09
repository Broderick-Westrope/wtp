package state_test

import (
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

// mockExecutor records commands and returns pre-configured results.
type mockExecutor struct {
	calls   [][]command.Command
	results []*command.ExecutionResult
	errs    []error
	callIdx int
}

func (m *mockExecutor) Execute(commands []command.Command) (*command.ExecutionResult, error) {
	m.calls = append(m.calls, commands)
	idx := m.callIdx
	m.callIdx++

	if idx < len(m.errs) && m.errs[idx] != nil {
		return nil, m.errs[idx]
	}
	if idx < len(m.results) && m.results[idx] != nil {
		return m.results[idx], nil
	}
	// Default: success with no output
	results := make([]command.Result, len(commands))
	for i, cmd := range commands {
		results[i] = command.Result{Command: cmd}
	}
	return &command.ExecutionResult{Results: results}, nil
}

func newArchiveTestStore(t *testing.T) *state.Store {
	t.Helper()

	dir := t.TempDir()
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", dir)
	axdg.Reload()

	return state.NewStore()
}

func TestPerformArchive_Success(t *testing.T) {
	store := newArchiveTestStore(t)
	executor := &mockExecutor{}

	now := time.Now().Truncate(time.Second)
	ws := state.WorktreeState{
		Archived:     true,
		ArchivedAt:   now,
		CommitSHA:    "abc123",
		Branch:       "feature/foo",
		WorktreePath: "/tmp/wt/feature-foo",
	}

	_, err := state.PerformArchive(executor, store, "owner/repo::feature/foo", &ws)
	require.NoError(t, err)

	// Verify state was written
	st, err := store.Load()
	require.NoError(t, err)
	got := st.Worktrees["owner/repo::feature/foo"]
	assert.True(t, got.Archived)
	assert.Equal(t, "abc123", got.CommitSHA)

	// Verify two executor calls: worktree remove + branch delete
	assert.Len(t, executor.calls, 2)
	assert.Equal(t, "git", executor.calls[0][0].Name)
	assert.Contains(t, executor.calls[0][0].Args, "remove")
	assert.Equal(t, "git", executor.calls[1][0].Name)
	assert.Contains(t, executor.calls[1][0].Args, "-D")
}

func TestPerformArchive_SkipsWhenFieldsEmpty(t *testing.T) {
	store := newArchiveTestStore(t)
	executor := &mockExecutor{}

	ws := state.WorktreeState{
		Archived: true,
		// No WorktreePath or Branch — should skip both remove and delete
	}

	_, err := state.PerformArchive(executor, store, "owner/repo::empty", &ws)
	require.NoError(t, err)

	// No executor calls should be made
	assert.Empty(t, executor.calls)
}

func TestPerformArchive_WorktreeAlreadyGone(t *testing.T) {
	store := newArchiveTestStore(t)

	// Return error containing "not a valid working tree" for worktree remove
	executor := &mockExecutor{
		results: []*command.ExecutionResult{
			{Results: []command.Result{{
				Error:  assert.AnError,
				Output: "fatal: not a valid working tree",
			}}},
			nil, // branch delete success
		},
	}

	ws := state.WorktreeState{
		Archived:     true,
		Branch:       "feature/bar",
		WorktreePath: "/tmp/wt/gone",
	}

	_, err := state.PerformArchive(executor, store, "owner/repo::feature/bar", &ws)
	require.NoError(t, err, "should succeed even when worktree is already gone")
}

func TestPerformArchive_BranchAlreadyGone(t *testing.T) {
	store := newArchiveTestStore(t)

	executor := &mockExecutor{
		results: []*command.ExecutionResult{
			nil, // worktree remove success
			{Results: []command.Result{{
				Error:  assert.AnError,
				Output: "error: branch 'feature/gone' not found",
			}}},
		},
	}

	ws := state.WorktreeState{
		Archived:     true,
		Branch:       "feature/gone",
		WorktreePath: "/tmp/wt/feature-gone",
	}

	_, err := state.PerformArchive(executor, store, "owner/repo::feature/gone", &ws)
	require.NoError(t, err, "should succeed even when branch is already gone")
}
