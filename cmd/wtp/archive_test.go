package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/command"
	"github.com/Broderick-Westrope/wtp/v3/internal/git"
	"github.com/Broderick-Westrope/wtp/v3/internal/remote"
	"github.com/Broderick-Westrope/wtp/v3/internal/state"
	"github.com/Broderick-Westrope/wtp/v3/internal/xdg"
)

// testRepoID is a shared repo identifier for archive tests.
var testRepoID = remote.RepoIdentifier{Owner: "owner", Repo: "repo"}

// testWorktrees returns a standard set of worktrees for archive tests.
func testWorktrees() []git.Worktree {
	return []git.Worktree{
		{Path: "/repo", Branch: "main", IsMain: true, HEAD: "abc123"},
		{Path: "/repo/.wt/feature/auth", Branch: "feature/auth", HEAD: "def456"},
		{Path: "/repo/.wt/fix/login", Branch: "fix/login", HEAD: "ghi789"},
	}
}

// setupStateStore creates a state store backed by a temp dir.
func setupStateStore(t *testing.T) *state.Store {
	t.Helper()
	dataDir := t.TempDir()
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()
	wtpDir := filepath.Join(dataDir, "wtp")
	require.NoError(t, os.MkdirAll(wtpDir, 0o755))
	return state.NewStore()
}

// mockExecutor records commands and returns configurable results.
type mockExecutor struct {
	commands []command.Command
	results  *command.ExecutionResult
	err      error
}

func (m *mockExecutor) Execute(
	commands []command.Command,
) (*command.ExecutionResult, error) {
	m.commands = append(m.commands, commands...)
	if m.err != nil {
		return nil, m.err
	}
	if m.results != nil {
		return m.results, nil
	}
	results := make([]command.Result, len(commands))
	for i := range commands {
		results[i] = command.Result{Command: commands[i]}
	}
	return &command.ExecutionResult{Results: results}, nil
}

// mockGitQuerier provides configurable git query responses.
type mockGitQuerier struct {
	isDirty         bool
	isDirtyErr      error
	hasUnpushed     bool
	hasUnpushedErr  error
	commitExists    bool
	commitExistsErr error
	branchExists    bool
	branchExistsErr error
	mainPath        string
	mainPathErr     error
}

func (m *mockGitQuerier) IsWorktreeDirty(string) (bool, error) {
	return m.isDirty, m.isDirtyErr
}

func (m *mockGitQuerier) HasUnpushedCommits(string) (bool, error) {
	return m.hasUnpushed, m.hasUnpushedErr
}

func (m *mockGitQuerier) CommitExists(string) (bool, error) {
	return m.commitExists, m.commitExistsErr
}

func (m *mockGitQuerier) BranchExists(string) (bool, error) {
	return m.branchExists, m.branchExistsErr
}

func (m *mockGitQuerier) GetMainWorktreePath() (string, error) {
	return m.mainPath, m.mainPathErr
}

// defaultMockRepo returns a mock that passes all safety checks.
func defaultMockRepo() *mockGitQuerier {
	return &mockGitQuerier{mainPath: "/repo"}
}

// callArchive is a test helper for calling archiveCommandCore
// with commonly-used defaults.
func callArchive(
	t *testing.T,
	buf *bytes.Buffer,
	branch, cwd string,
	force bool,
	worktrees []git.Worktree,
	stateStore *state.Store,
	exec *mockExecutor,
	repo *mockGitQuerier,
) error {
	t.Helper()
	return archiveCommandCore(
		buf, branch, cwd, force,
		worktrees, testRepoID, stateStore, exec, repo,
	)
}

// callUnarchive is a test helper for calling unarchiveCommandCore.
func callUnarchive(
	t *testing.T,
	buf *bytes.Buffer,
	branch string, //nolint:unparam // consistent test-helper API
	stateStore *state.Store,
	exec *mockExecutor,
	repo *mockGitQuerier,
) error {
	t.Helper()
	return unarchiveCommandCore(
		buf, branch, testRepoID, stateStore, exec, repo,
	)
}

// ===== Archive Command Tests =====

func TestArchiveCommand_RemovesWorktreeAndBranch(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "Archived feature/auth")

	// Verify state has metadata
	key := testRepoID.StateKey("feature/auth")
	st, err := stateStore.Load()
	require.NoError(t, err)
	entry := st.Worktrees[key]
	assert.True(t, entry.Archived)
	assert.Equal(t, "def456", entry.CommitSHA)
	assert.Equal(t, "feature/auth", entry.Branch)
	assert.Equal(t, "/repo/.wt/feature/auth", entry.WorktreePath)
	assert.False(t, entry.ArchivedAt.IsZero())

	// Verify executor was called with worktree remove and branch delete
	assert.GreaterOrEqual(t, len(mockExec.commands), 2)
}

func TestArchiveCommand_IdempotentRetry(t *testing.T) {
	stateStore := setupStateStore(t)

	// Pre-populate state with archived entry
	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:     true,
		ArchivedAt:   time.Now().Add(-time.Hour),
		CommitSHA:    "def456",
		Branch:       "feature/auth",
		WorktreePath: "/repo/.wt/feature/auth",
	}))

	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	var buf bytes.Buffer
	// Pass nil worktrees — the archived worktree is no longer in git
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", false,
		nil, stateStore, mockExec, mockRepo,
	)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "Archived feature/auth")
	assert.NotEmpty(t, mockExec.commands) // retry of git cleanup
}

func TestArchiveCommand_RefusesDirtyWorktree(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{isDirty: true}

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "uncommitted changes")
	assert.Contains(t, err.Error(), "--force")
}

func TestArchiveCommand_RefusesUnpushedCommits(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{hasUnpushed: true}

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unpushed commits")
	assert.Contains(t, err.Error(), "--force")
}

func TestArchiveCommand_ForceOverridesDirtyAndUnpushed(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{isDirty: true, hasUnpushed: true}

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", true,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "Archived feature/auth")
}

func TestArchiveCommand_BlocksCurrentWorktree(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/repo/.wt/feature/auth", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot archive the current worktree")
}

func TestArchiveCommand_MainWorktreeError(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	var buf bytes.Buffer

	err := callArchive(t, &buf,
		"@", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot archive the main worktree")

	err = callArchive(t, &buf,
		"root", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot archive the main worktree")
}

func TestArchiveCommand_MainWorktreeByBranchError(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	worktrees := []git.Worktree{
		{Path: "/repo", Branch: "main", IsMain: true},
		{Path: "/repo/.wt/feature", Branch: "feature"},
	}

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"main", "/somewhere/else", false,
		worktrees, stateStore, mockExec, mockRepo,
	)
	assert.Error(t, err)
}

func TestArchiveCommand_InvalidBranch(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"nonexistent-branch", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-branch")
}

func TestArchiveCommand_DoesNotAffectOthers(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)
	require.NoError(t, err)

	assert.False(t, stateStore.IsArchived(testRepoID.StateKey("fix/login")))
}

func TestArchiveCommand_ClearsSuppressAutoArchive(t *testing.T) {
	stateStore := setupStateStore(t)
	mockExec := &mockExecutor{}
	mockRepo := defaultMockRepo()

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.WithLock(func(st state.State) (state.State, error) {
		st.Worktrees[key] = state.WorktreeState{
			SuppressAutoArchive: true,
		}
		return st, nil
	}))

	var buf bytes.Buffer
	err := callArchive(t, &buf,
		"feature/auth", "/somewhere/else", false,
		testWorktrees(), stateStore, mockExec, mockRepo,
	)
	require.NoError(t, err)

	st, err := stateStore.Load()
	require.NoError(t, err)
	assert.False(t, st.Worktrees[key].SuppressAutoArchive)
}

// ===== Unarchive Command Tests =====

func TestUnarchiveCommand_RestoresWorktree(t *testing.T) {
	stateStore := setupStateStore(t)

	parentDir := t.TempDir()
	originalPath := filepath.Join(parentDir, "feature-auth")

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:     true,
		ArchivedAt:   time.Now(),
		CommitSHA:    "def456",
		Branch:       "feature/auth",
		WorktreePath: originalPath,
	}))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{
		commitExists: true,
		mainPath:     "/repo",
	}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)
	require.NoError(t, err)

	assert.Contains(t, buf.String(), "Unarchived feature/auth")
	assert.Contains(t, buf.String(), originalPath)

	// Verify git worktree add command was built correctly
	require.NotEmpty(t, mockExec.commands)
	addCmd := mockExec.commands[0]
	assert.Equal(t, "git", addCmd.Name)
	assert.Contains(t, addCmd.Args, "-b")
	assert.Contains(t, addCmd.Args, "feature/auth")
	assert.Contains(t, addCmd.Args, "def456")
	assert.Contains(t, addCmd.Args, originalPath)
}

func TestUnarchiveCommand_FailsWhenSHAGarbageCollected(t *testing.T) {
	stateStore := setupStateStore(t)

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:  true,
		CommitSHA: "deadbeef",
		Branch:    "feature/auth",
	}))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{commitExists: false}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no longer exists in the object store")
}

func TestUnarchiveCommand_FailsWhenBranchExists(t *testing.T) {
	stateStore := setupStateStore(t)

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:  true,
		CommitSHA: "def456",
		Branch:    "feature/auth",
	}))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{
		commitExists: true,
		branchExists: true,
	}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists locally")
}

func TestUnarchiveCommand_FallsBackToStoragePath(t *testing.T) {
	stateStore := setupStateStore(t)

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:     true,
		CommitSHA:    "def456",
		Branch:       "feature/auth",
		WorktreePath: "/nonexistent/parent/dir/feature-auth",
	}))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{commitExists: true, mainPath: "/repo"}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)
	require.NoError(t, err)

	expectedPath := filepath.Join(
		xdg.WorktreeStorageRoot(),
		testRepoID.StoragePath(), "feature", "auth",
	)
	assert.Contains(t, buf.String(), expectedPath)
}

func TestUnarchiveCommand_FallsBackWhenPathOccupied(t *testing.T) {
	stateStore := setupStateStore(t)

	originalPath := filepath.Join(t.TempDir(), "occupied")
	require.NoError(t, os.MkdirAll(originalPath, 0o755))

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:     true,
		CommitSHA:    "def456",
		Branch:       "feature/auth",
		WorktreePath: originalPath,
	}))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{commitExists: true, mainPath: "/repo"}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)
	require.NoError(t, err)

	expectedPath := filepath.Join(
		xdg.WorktreeStorageRoot(),
		testRepoID.StoragePath(), "feature", "auth",
	)
	assert.Contains(t, buf.String(), expectedPath)
}

func TestUnarchiveCommand_SetsSuppressAutoArchive(t *testing.T) {
	stateStore := setupStateStore(t)

	parentDir := t.TempDir()
	originalPath := filepath.Join(parentDir, "feature-auth")

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchivedFull(key, &state.WorktreeState{
		Archived:     true,
		CommitSHA:    "def456",
		Branch:       "feature/auth",
		WorktreePath: originalPath,
	}))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{commitExists: true, mainPath: "/repo"}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)
	require.NoError(t, err)

	st, err := stateStore.Load()
	require.NoError(t, err)
	entry := st.Worktrees[key]
	assert.True(t, entry.SuppressAutoArchive)
	assert.False(t, entry.Archived)
	assert.Empty(t, entry.CommitSHA)
}

func TestUnarchiveCommand_RejectsLegacyEntry(t *testing.T) {
	stateStore := setupStateStore(t)

	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchived(key, true))

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{commitExists: true}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "legacy entry")
	assert.Contains(t, err.Error(), "no recovery metadata")
}

func TestUnarchiveCommand_NotArchived(t *testing.T) {
	stateStore := setupStateStore(t)

	mockExec := &mockExecutor{}
	mockRepo := &mockGitQuerier{commitExists: true}

	var buf bytes.Buffer
	err := callUnarchive(t, &buf,
		"feature/auth", stateStore, mockExec, mockRepo,
	)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not archived")
}

// ===== Command Structure Tests =====

func TestNewArchiveCommand(t *testing.T) {
	cmd := NewArchiveCommand()
	assert.NotNil(t, cmd)
	assert.Equal(t, "archive", cmd.Name)
	assert.NotEmpty(t, cmd.Usage)
	assert.NotNil(t, cmd.Action)
	assert.NotNil(t, cmd.ShellComplete)
	assert.NotEmpty(t, cmd.Flags)
}

func TestNewUnarchiveCommand(t *testing.T) {
	cmd := NewUnarchiveCommand()
	assert.NotNil(t, cmd)
	assert.Equal(t, "unarchive", cmd.Name)
	assert.NotEmpty(t, cmd.Usage)
	assert.NotNil(t, cmd.Action)
	assert.NotNil(t, cmd.ShellComplete)
}
