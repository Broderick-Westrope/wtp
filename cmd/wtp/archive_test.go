package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/satococoa/wtp/v3/internal/git"
	"github.com/satococoa/wtp/v3/internal/remote"
	"github.com/satococoa/wtp/v3/internal/state"
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

// setupStateStore creates a state store backed by a temp dir and sets XDG_DATA_HOME.
func setupStateStore(t *testing.T) *state.Store {
	t.Helper()
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	// Ensure the wtp subdirectory exists
	wtpDir := filepath.Join(dataDir, "wtp")
	require.NoError(t, os.MkdirAll(wtpDir, 0o755))
	return state.NewStore()
}

// ===== Archive Command Tests =====

func TestArchiveCommand_SetsFlag(t *testing.T) {
	stateStore := setupStateStore(t)

	var buf bytes.Buffer
	err := archiveCommandCore(&buf, "feature/auth", testWorktrees(), testRepoID, stateStore)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "Archived feature/auth")

	key := testRepoID.StateKey("feature/auth")
	assert.True(t, stateStore.IsArchived(key), "feature/auth should be archived")
}

func TestArchiveCommand_AlreadyArchived(t *testing.T) {
	stateStore := setupStateStore(t)

	// Pre-archive
	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchived(key, true))

	var buf bytes.Buffer
	err := archiveCommandCore(&buf, "feature/auth", testWorktrees(), testRepoID, stateStore)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already archived")
}

func TestArchiveCommand_MainWorktreeError(t *testing.T) {
	stateStore := setupStateStore(t)

	var buf bytes.Buffer

	// Attempt to archive by "@" special name
	err := archiveCommandCore(&buf, "@", testWorktrees(), testRepoID, stateStore)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot archive the main worktree")

	// Attempt to archive by "root" special name
	err = archiveCommandCore(&buf, "root", testWorktrees(), testRepoID, stateStore)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot archive the main worktree")
}

func TestArchiveCommand_MainWorktreeByBranchError(t *testing.T) {
	stateStore := setupStateStore(t)

	// Worktree where main has a branch name "main"
	worktrees := []git.Worktree{
		{Path: "/repo", Branch: "main", IsMain: true},
		{Path: "/repo/.wt/feature", Branch: "feature"},
	}

	// resolveWorktreePathByName won't find "main" by branch (it skips main)
	// so this should return a not-found error
	var buf bytes.Buffer
	err := archiveCommandCore(&buf, "main", worktrees, testRepoID, stateStore)
	assert.Error(t, err)
	// Either "not found" or "cannot archive main" — either is acceptable
}

func TestArchiveCommand_InvalidBranch(t *testing.T) {
	stateStore := setupStateStore(t)

	var buf bytes.Buffer
	err := archiveCommandCore(&buf, "nonexistent-branch", testWorktrees(), testRepoID, stateStore)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-branch")
}

func TestArchiveCommand_DoesNotAffectOthers(t *testing.T) {
	stateStore := setupStateStore(t)

	var buf bytes.Buffer
	err := archiveCommandCore(&buf, "feature/auth", testWorktrees(), testRepoID, stateStore)
	require.NoError(t, err)

	// fix/login should not be archived
	assert.False(t, stateStore.IsArchived(testRepoID.StateKey("fix/login")))
}

// ===== Unarchive Command Tests =====

func TestUnarchiveCommand_ClearsFlag(t *testing.T) {
	stateStore := setupStateStore(t)

	// Pre-archive
	key := testRepoID.StateKey("feature/auth")
	require.NoError(t, stateStore.SetArchived(key, true))

	var buf bytes.Buffer
	err := unarchiveCommandCore(&buf, "feature/auth", testRepoID, stateStore)

	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "Unarchived feature/auth")
	assert.False(t, stateStore.IsArchived(key), "feature/auth should no longer be archived")
}

func TestUnarchiveCommand_NotArchived(t *testing.T) {
	stateStore := setupStateStore(t)

	var buf bytes.Buffer
	err := unarchiveCommandCore(&buf, "feature/auth", testRepoID, stateStore)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not archived")
}

func TestUnarchiveCommand_DoesNotAffectOthers(t *testing.T) {
	stateStore := setupStateStore(t)

	// Archive both branches
	require.NoError(t, stateStore.SetArchived(testRepoID.StateKey("feature/auth"), true))
	require.NoError(t, stateStore.SetArchived(testRepoID.StateKey("fix/login"), true))

	// Unarchive only feature/auth
	var buf bytes.Buffer
	err := unarchiveCommandCore(&buf, "feature/auth", testRepoID, stateStore)
	require.NoError(t, err)

	// fix/login should still be archived
	assert.True(t, stateStore.IsArchived(testRepoID.StateKey("fix/login")))
}

// ===== Command Structure Tests =====

func TestNewArchiveCommand(t *testing.T) {
	cmd := NewArchiveCommand()
	assert.NotNil(t, cmd)
	assert.Equal(t, "archive", cmd.Name)
	assert.NotEmpty(t, cmd.Usage)
	assert.NotNil(t, cmd.Action)
	assert.NotNil(t, cmd.ShellComplete)
}

func TestNewUnarchiveCommand(t *testing.T) {
	cmd := NewUnarchiveCommand()
	assert.NotNil(t, cmd)
	assert.Equal(t, "unarchive", cmd.Name)
	assert.NotEmpty(t, cmd.Usage)
	assert.NotNil(t, cmd.Action)
	assert.NotNil(t, cmd.ShellComplete)
}
