package cache_test

import (
	"testing"
	"time"

	axdg "github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/satococoa/wtp/v3/internal/cache"
)

// newTestStore creates a Store backed by a temporary directory and sets
// XDG_CACHE_HOME so xdg.WtpCacheDir() resolves into that temp dir.
func newTestStore(t *testing.T) *cache.Store {
	t.Helper()

	dir := t.TempDir()
	t.Cleanup(axdg.Reload)
	t.Setenv("XDG_CACHE_HOME", dir)
	axdg.Reload()

	return cache.NewStore()
}

func TestLoad_EmptyWhenFileMissing(t *testing.T) {
	s := newTestStore(t)

	c, err := s.Load()
	require.NoError(t, err)
	assert.NotNil(t, c.Worktrees)
	assert.Empty(t, c.Worktrees)
}

func TestSetGet_RoundTrip(t *testing.T) {
	s := newTestStore(t)

	const key = "owner/repo::feature"

	entry := cache.WorktreeCache{
		PRNumber: 42,
		PRState:  "open",
		PRTitle:  "My PR",
		CIStatus: "success",
	}

	require.NoError(t, s.Set(key, &entry))

	got, ok := s.Get(key)
	require.True(t, ok)
	assert.Equal(t, entry.PRNumber, got.PRNumber)
	assert.Equal(t, entry.PRState, got.PRState)
	assert.Equal(t, entry.PRTitle, got.PRTitle)
	assert.Equal(t, entry.CIStatus, got.CIStatus)
	assert.False(t, got.UpdatedAt.IsZero(), "UpdatedAt should be set by Set()")
}

func TestIsExpired(t *testing.T) {
	s := newTestStore(t)

	ttl := 5 * time.Minute

	fresh := cache.WorktreeCache{UpdatedAt: time.Now()}
	assert.False(t, s.IsExpired(&fresh, ttl), "brand-new entry should not be expired")

	stale := cache.WorktreeCache{UpdatedAt: time.Now().Add(-10 * time.Minute)}
	assert.True(t, s.IsExpired(&stale, ttl), "entry older than TTL should be expired")
}

func TestDelete_RemovesEntry(t *testing.T) {
	s := newTestStore(t)

	const key = "owner/repo::main"

	require.NoError(t, s.Set(key, &cache.WorktreeCache{PRNumber: 1}))

	_, ok := s.Get(key)
	require.True(t, ok, "entry should exist after Set")

	require.NoError(t, s.Delete(key))

	_, ok = s.Get(key)
	assert.False(t, ok, "entry should be gone after Delete")
}

func TestSetBatch_WritesMultipleEntries(t *testing.T) {
	s := newTestStore(t)

	batch := map[string]cache.WorktreeCache{
		"owner/repo::main":    {PRNumber: 1, PRState: "merged"},
		"owner/repo::feature": {PRNumber: 2, PRState: "open"},
		"owner/repo::hotfix":  {PRNumber: 3, PRState: "closed"},
	}

	require.NoError(t, s.SetBatch(batch))

	for key, want := range batch {
		got, ok := s.Get(key)
		require.True(t, ok, "key %q should exist after SetBatch", key)
		assert.Equal(t, want.PRNumber, got.PRNumber)
		assert.Equal(t, want.PRState, got.PRState)
		assert.False(t, got.UpdatedAt.IsZero(), "UpdatedAt should be set for key %q", key)
	}
}

func TestSetBatch_PreservesExplicitUpdatedAt(t *testing.T) {
	s := newTestStore(t)

	explicit := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	batch := map[string]cache.WorktreeCache{
		"owner/repo::main": {PRNumber: 1, UpdatedAt: explicit},
	}

	require.NoError(t, s.SetBatch(batch))

	got, ok := s.Get("owner/repo::main")
	require.True(t, ok)
	assert.Equal(t, explicit, got.UpdatedAt, "explicit UpdatedAt should not be overwritten")
}
