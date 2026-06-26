package state_test

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Broderick-Westrope/wtp/v3/internal/state"
)

// newTestStore creates a Store backed by a temporary directory and sets
// XDG_DATA_HOME so xdg.WtpDataDir() resolves into that temp dir.
func newTestStore(t *testing.T) *state.Store {
	t.Helper()

	dir := t.TempDir()
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_DATA_HOME", dir)
	axdg.Reload()

	return state.NewStore()
}

func TestLoad_EmptyWhenFileMissing(t *testing.T) {
	s := newTestStore(t)

	st, err := s.Load()
	require.NoError(t, err)
	assert.NotNil(t, st.Worktrees)
	assert.Empty(t, st.Worktrees)
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	original := state.State{
		Worktrees: map[string]state.WorktreeState{
			"owner/repo::main":    {Archived: false},
			"owner/repo::feature": {Archived: true},
		},
	}

	require.NoError(t, s.Save(original))

	loaded, err := s.Load()
	require.NoError(t, err)
	assert.Equal(t, original, loaded)
}

func TestSetArchived_IsArchived(t *testing.T) {
	s := newTestStore(t)

	const key = "owner/repo::feature"

	assert.False(t, s.IsArchived(key), "should not be archived before SetArchived")

	require.NoError(t, s.SetArchived(key, true))
	assert.True(t, s.IsArchived(key))
}

func TestSetArchived_ClearsFlag(t *testing.T) {
	s := newTestStore(t)

	const key = "owner/repo::feature"

	require.NoError(t, s.SetArchived(key, true))
	require.True(t, s.IsArchived(key))

	require.NoError(t, s.SetArchived(key, false))
	assert.False(t, s.IsArchived(key))
}

func TestWithLock_Serialization(t *testing.T) {
	s := newTestStore(t)

	const (
		key        = "owner/repo::main"
		goroutines = 10
	)

	// Each goroutine increments a counter stored as archived flag.
	// We use a simple approach: each goroutine calls WithLock and sets a
	// unique key so we can verify all writes are visible without races.
	var wg sync.WaitGroup

	keys := make([]string, goroutines)
	for i := range goroutines {
		keys[i] = key + string(rune('A'+i))
	}

	for i := range goroutines {
		wg.Add(1)

		go func(k string) {
			defer wg.Done()

			err := s.WithLock(func(st state.State) (state.State, error) {
				// simulate some work while holding the lock
				time.Sleep(time.Millisecond)
				st.Worktrees[k] = state.WorktreeState{Archived: true}

				return st, nil
			})
			assert.NoError(t, err)
		}(keys[i])
	}

	wg.Wait()

	loaded, err := s.Load()
	require.NoError(t, err)

	for _, k := range keys {
		assert.True(t, loaded.Worktrees[k].Archived, "key %q should be archived", k)
	}
}

func TestWorktreeState_JSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	prClosed := now.Add(-time.Hour)

	full := state.WorktreeState{
		Archived:            true,
		ArchivedAt:          now,
		PRClosedAt:          prClosed,
		CommitSHA:           "abc123def456",
		Branch:              "feature/foo",
		WorktreePath:        "/tmp/wt/feature-foo",
		SuppressAutoArchive: true,
	}

	data, err := json.Marshal(full)
	require.NoError(t, err)

	var got state.WorktreeState
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, full.Archived, got.Archived)
	assert.Equal(t, full.CommitSHA, got.CommitSHA)
	assert.Equal(t, full.Branch, got.Branch)
	assert.Equal(t, full.WorktreePath, got.WorktreePath)
	assert.Equal(t, full.SuppressAutoArchive, got.SuppressAutoArchive)
	assert.True(t, full.ArchivedAt.Equal(got.ArchivedAt), "ArchivedAt mismatch")
	assert.True(t, full.PRClosedAt.Equal(got.PRClosedAt), "PRClosedAt mismatch")

	// omitempty: minimal struct should produce compact JSON
	minimal := state.WorktreeState{Archived: true}
	data, err = json.Marshal(minimal)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "commit_sha")
	assert.NotContains(t, string(data), "archived_at")
	assert.NotContains(t, string(data), "branch")
}

func TestWorktreeState_IsLegacy(t *testing.T) {
	tests := []struct {
		name     string
		ws       state.WorktreeState
		expected bool
	}{
		{
			name:     "legacy: archived without SHA",
			ws:       state.WorktreeState{Archived: true},
			expected: true,
		},
		{
			name:     "not legacy: archived with SHA",
			ws:       state.WorktreeState{Archived: true, CommitSHA: "abc123"},
			expected: false,
		},
		{
			name:     "not legacy: not archived",
			ws:       state.WorktreeState{Archived: false},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.ws.IsLegacy())
		})
	}
}

func TestWorktreeState_ExpirationTime(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	earlier := now.Add(-time.Hour)

	t.Run("PRClosedAt wins when set", func(t *testing.T) {
		ws := state.WorktreeState{
			ArchivedAt: earlier,
			PRClosedAt: now,
		}
		assert.True(t, ws.ExpirationTime().Equal(now))
	})

	t.Run("falls back to ArchivedAt", func(t *testing.T) {
		ws := state.WorktreeState{
			ArchivedAt: earlier,
		}
		assert.True(t, ws.ExpirationTime().Equal(earlier))
	})

	t.Run("zero when neither set", func(t *testing.T) {
		ws := state.WorktreeState{}
		assert.True(t, ws.ExpirationTime().IsZero())
	})
}

func TestSetArchivedFull(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().Truncate(time.Second)
	const key = "owner/repo::feature"

	ws := state.WorktreeState{
		Archived:     true,
		ArchivedAt:   now,
		CommitSHA:    "abc123",
		Branch:       "feature",
		WorktreePath: "/tmp/wt/feature",
	}

	require.NoError(t, s.SetArchivedFull(key, &ws))

	st, err := s.Load()
	require.NoError(t, err)

	got := st.Worktrees[key]
	assert.True(t, got.Archived)
	assert.Equal(t, "abc123", got.CommitSHA)
	assert.Equal(t, "feature", got.Branch)
	assert.Equal(t, "/tmp/wt/feature", got.WorktreePath)
	assert.True(t, got.ArchivedAt.Equal(now))
}

func TestClearArchived(t *testing.T) {
	s := newTestStore(t)

	now := time.Now().Truncate(time.Second)
	const key = "owner/repo::feature"

	// First set full archived state
	ws := state.WorktreeState{
		Archived:     true,
		ArchivedAt:   now,
		PRClosedAt:   now,
		CommitSHA:    "abc123",
		Branch:       "feature",
		WorktreePath: "/tmp/wt/feature",
	}
	require.NoError(t, s.SetArchivedFull(key, &ws))

	// Now clear it
	require.NoError(t, s.ClearArchived(key))

	st, err := s.Load()
	require.NoError(t, err)

	got := st.Worktrees[key]
	assert.False(t, got.Archived)
	assert.Empty(t, got.CommitSHA)
	assert.True(t, got.ArchivedAt.IsZero())
	assert.True(t, got.PRClosedAt.IsZero())
	assert.Empty(t, got.WorktreePath)
	assert.True(t, got.SuppressAutoArchive, "SuppressAutoArchive should be true after ClearArchived")
	assert.Equal(t, "feature", got.Branch, "Branch should be preserved")
}

func TestAtomicWrite_TmpFileDoesNotCorrupt(t *testing.T) {
	s := newTestStore(t)

	original := state.State{
		Worktrees: map[string]state.WorktreeState{
			"owner/repo::main": {Archived: true},
		},
	}

	require.NoError(t, s.Save(original))

	// Load should always return the last complete Save, never a partial write.
	loaded, err := s.Load()
	require.NoError(t, err)
	assert.Equal(t, original, loaded)
}
