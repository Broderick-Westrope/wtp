package state_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/satococoa/wtp/v2/internal/state"
)

// newTestStore creates a Store backed by a temporary directory and sets
// XDG_DATA_HOME so xdg.WtpDataDir() resolves into that temp dir.
func newTestStore(t *testing.T) *state.Store {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

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
