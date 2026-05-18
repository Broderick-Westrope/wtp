package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/satococoa/wtp/v3/internal/git"
	"github.com/satococoa/wtp/v3/internal/remote"
	"github.com/satococoa/wtp/v3/internal/state"
)

// ===== Command Structure Tests =====

func TestNewDoctorCommand(t *testing.T) {
	cmd := NewDoctorCommand()
	assert.NotNil(t, cmd)
	assert.Equal(t, "doctor", cmd.Name)
	assert.NotEmpty(t, cmd.Usage)
	assert.NotNil(t, cmd.Action)
}

// ===== checkV2Worktrees Tests =====

func TestDoctor_CheckV2Worktrees_Clean(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	worktrees := []git.Worktree{
		// Path under centralized wtp storage — should NOT be flagged as v2
		{Path: filepath.Join(dataDir, "wtp", "owner", "repo", "feature"), Branch: "feature"},
	}

	var buf bytes.Buffer
	count := checkV2Worktrees(&buf, worktrees, "/repo")

	assert.Equal(t, 0, count)
	assert.Empty(t, buf.String())
}

func TestDoctor_CheckV2Worktrees_DetectsV2(t *testing.T) {
	worktrees := []git.Worktree{
		{Path: "/repo", Branch: "main", IsMain: true},
		{Path: "/repo/.worktrees/feature/auth", Branch: "feature/auth"},
	}

	var buf bytes.Buffer
	count := checkV2Worktrees(&buf, worktrees, "/repo")

	assert.Greater(t, count, 0, "should detect v2 worktree")
	assert.Contains(t, buf.String(), "⚠")
	assert.Contains(t, buf.String(), "/repo/.worktrees/feature/auth")
}

// ===== checkOrphanedStateEntries Tests =====

func TestDoctor_CheckOrphanedStateEntries_Clean(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}

	var buf bytes.Buffer
	count := checkOrphanedStateEntries(&buf, &repoID)

	assert.Equal(t, 0, count)
}

func TestDoctor_CheckOrphanedStateEntries_DetectsOrphaned(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)

	t.Setenv("XDG_CACHE_HOME", cacheDir)
	axdg.Reload()

	wtpDir := filepath.Join(dataDir, "wtp")
	require.NoError(t, os.MkdirAll(wtpDir, 0o755))

	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}

	// Create a state entry for a branch that doesn't exist on disk
	stateStore := state.NewStore()
	require.NoError(t, stateStore.SetArchived(repoID.StateKey("feature/orphaned"), true))

	// No worktree on disk for feature/orphaned
	var buf bytes.Buffer
	count := checkOrphanedStateEntries(&buf, &repoID)

	assert.Greater(t, count, 0, "should detect orphaned state entry")
	assert.Contains(t, buf.String(), "⚠")
}

func TestDoctor_CheckOrphanedStateEntries_NilRepoID(t *testing.T) {
	var buf bytes.Buffer
	count := checkOrphanedStateEntries(&buf, nil)

	assert.Equal(t, 0, count, "should skip check when repoID is nil")
}

// ===== checkGHStatus Tests =====

func TestDoctor_CheckGHStatus_Available(t *testing.T) {
	old := doctorIsGHAvailable
	doctorIsGHAvailable = func() bool { return true }
	t.Cleanup(func() { doctorIsGHAvailable = old })

	var buf bytes.Buffer
	count := checkGHStatus(&buf)

	output := buf.String()
	assert.Contains(t, output, "gh CLI found")
	// count depends on authentication state; just ensure it ran without panic
	_ = count
}

func TestDoctor_CheckGHStatus_NotAvailable(t *testing.T) {
	old := doctorIsGHAvailable
	doctorIsGHAvailable = func() bool { return false }
	t.Cleanup(func() { doctorIsGHAvailable = old })

	var buf bytes.Buffer
	count := checkGHStatus(&buf)
	output := buf.String()
	assert.Contains(t, output, "✗ gh CLI not found")
	assert.Greater(t, count, 0)
}

// ===== checkOrphanedCentralizedDirs Tests =====

func TestDoctor_CheckOrphanedCentralizedDirs_Empty(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	var buf bytes.Buffer
	count := checkOrphanedCentralizedDirs(&buf, map[string]bool{})

	// No storage root → no issues
	assert.Equal(t, 0, count)
}

func TestDoctor_CheckOrphanedCentralizedDirs_DetectsOrphaned(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	// Create a fake worktree directory in centralized storage with a .git file
	wtDir := filepath.Join(dataDir, "wtp", "worktrees", "owner", "repo", "feature-orphaned")
	require.NoError(t, os.MkdirAll(wtDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(wtDir, ".git"), []byte("gitdir: ..."), 0o644))

	// No registered paths include this directory
	registeredPaths := map[string]bool{}

	var buf bytes.Buffer
	count := checkOrphanedCentralizedDirs(&buf, registeredPaths)

	assert.Greater(t, count, 0, "should detect orphaned centralized directory")
	assert.Contains(t, buf.String(), "⚠")
	assert.Contains(t, buf.String(), "feature-orphaned")
}

// ===== isV2WorktreePath Tests =====

func TestIsV2WorktreePath(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	axdg.Reload()

	tests := []struct {
		absPath  string
		relPath  string
		expected bool
	}{
		{
			absPath:  "/repo/.worktrees/feature",
			relPath:  ".worktrees/feature",
			expected: true,
		},
		{
			// relPath starts with ".." not "worktrees", so NOT flagged as v2
			absPath:  "/home/user/worktrees/feature",
			relPath:  "../worktrees/feature",
			expected: false,
		},
		{
			// Centralized wtp storage path — should NOT be flagged
			absPath:  filepath.Join(dataDir, "wtp", "worktrees", "owner", "repo", "feature"),
			relPath:  "feature",
			expected: false,
		},
		{
			absPath:  "/home/user/projects/feature",
			relPath:  "../projects/feature",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.absPath, func(t *testing.T) {
			result := isV2WorktreePath(tt.absPath, tt.relPath)
			assert.Equal(t, tt.expected, result)
		})
	}
}
