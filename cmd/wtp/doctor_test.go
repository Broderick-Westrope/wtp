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

	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}

	// Registered paths — no state entries exist
	registeredPaths := map[string]bool{
		"/repo": true,
	}

	var buf bytes.Buffer
	count := checkOrphanedStateEntries(&buf, &repoID, registeredPaths)

	assert.Equal(t, 0, count)
}

func TestDoctor_CheckOrphanedStateEntries_DetectsOrphaned(t *testing.T) {
	dataDir := t.TempDir()
	cacheDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("XDG_CACHE_HOME", cacheDir)

	wtpDir := filepath.Join(dataDir, "wtp")
	require.NoError(t, os.MkdirAll(wtpDir, 0o755))

	repoID := remote.RepoIdentifier{Owner: "owner", Repo: "repo"}

	// Create a state entry for a branch that doesn't exist on disk
	stateStore := state.NewStore()
	require.NoError(t, stateStore.SetArchived(repoID.StateKey("feature/orphaned"), true))

	// No worktree on disk for feature/orphaned
	registeredPaths := map[string]bool{
		"/repo": true,
	}

	var buf bytes.Buffer
	count := checkOrphanedStateEntries(&buf, &repoID, registeredPaths)

	assert.Greater(t, count, 0, "should detect orphaned state entry")
	assert.Contains(t, buf.String(), "⚠")
}

func TestDoctor_CheckOrphanedStateEntries_NilRepoID(t *testing.T) {
	var buf bytes.Buffer
	count := checkOrphanedStateEntries(&buf, nil, map[string]bool{})

	assert.Equal(t, 0, count, "should skip check when repoID is nil")
}

// ===== checkGHStatus Tests =====

func TestDoctor_CheckGHStatus_Available(t *testing.T) {
	if !listIsGHAvailable() {
		t.Skip("gh CLI not available in this environment")
	}

	var buf bytes.Buffer
	// checkGHStatus uses github.IsAvailable() directly, not the mockable var
	count := checkGHStatus(&buf)

	output := buf.String()
	assert.Contains(t, output, "gh CLI found")
	// count depends on authentication state; just ensure it ran without panic
	_ = count
}

func TestDoctor_CheckGHStatus_NotAvailable(t *testing.T) {
	// Test the output format by simulating not-available output
	var buf bytes.Buffer

	// We can't mock github.IsAvailable in doctor.go directly, but we can
	// test that the output format is correct by calling checkGHStatus
	// in an environment where gh is not in PATH.
	// If gh IS available, just verify the output contains known strings.
	if listIsGHAvailable() {
		count := checkGHStatus(&buf)
		output := buf.String()
		assert.Contains(t, output, "gh CLI")
		_ = count
	} else {
		count := checkGHStatus(&buf)
		output := buf.String()
		assert.Contains(t, output, "✗ gh CLI not found")
		assert.Greater(t, count, 0)
	}
}

// ===== checkOrphanedCentralizedDirs Tests =====

func TestDoctor_CheckOrphanedCentralizedDirs_Empty(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)

	var buf bytes.Buffer
	count := checkOrphanedCentralizedDirs(&buf, map[string]bool{})

	// No storage root → no issues
	assert.Equal(t, 0, count)
}

func TestDoctor_CheckOrphanedCentralizedDirs_DetectsOrphaned(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)

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
			absPath:  "/home/user/worktrees/feature",
			relPath:  "../worktrees/feature",
			expected: true,
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
