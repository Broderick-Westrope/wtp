// Package cache manages ephemeral PR/CI data cached to disk using JSON file
// storage with flock-based mutual exclusion and atomic writes.
package cache

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	"github.com/satococoa/wtp/v3/internal/xdg"
)

// Store holds the paths used to persist and lock the cache file.
type Store struct {
	path     string // path to cache.json
	lockPath string // path to cache.json.lock
}

// NewStore returns a Store rooted at xdg.WtpCacheDir()/cache.json.
func NewStore() *Store {
	dir := xdg.WtpCacheDir()
	return &Store{
		path:     filepath.Join(dir, "cache.json"),
		lockPath: filepath.Join(dir, "cache.json.lock"),
	}
}

// Cache is the top-level structure persisted to disk.
// Worktrees is keyed by "owner/repo::branch" (or "host/owner/repo::branch").
type Cache struct {
	Worktrees map[string]WorktreeCache `json:"worktrees"`
}

// WorktreeCache holds cached PR/CI metadata for a single worktree.
type WorktreeCache struct {
	PRNumber  int       `json:"pr_number,omitempty"`
	PRState   string    `json:"pr_state,omitempty"`
	PRTitle   string    `json:"pr_title,omitempty"`
	CIStatus  string    `json:"ci_status,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Load reads the cache file without acquiring a lock.
// If the file does not exist an empty Cache is returned without error.
func (s *Store) Load() (Cache, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Cache{Worktrees: map[string]WorktreeCache{}}, nil
		}

		return Cache{}, fmt.Errorf("read cache file: %w", err)
	}

	var c Cache
	if err := json.Unmarshal(data, &c); err != nil {
		return Cache{}, fmt.Errorf("unmarshal cache: %w", err)
	}

	if c.Worktrees == nil {
		c.Worktrees = map[string]WorktreeCache{}
	}

	return c, nil
}

// Save acquires a lock on the lock file, writes the cache to a temporary file
// adjacent to the cache file, renames it into place, then releases the lock.
func (s *Store) Save(c Cache) error {
	if err := xdg.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}

	fl := flock.New(s.lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire cache lock: %w", err)
	}

	defer fl.Unlock() //nolint:errcheck

	return s.writeAtomic(c)
}

// writeAtomic writes cache to a .tmp file then renames it over the target path.
// The caller is responsible for holding the lock before calling this method.
func (s *Store) writeAtomic(c Cache) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}

	tmpPath := s.path + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		return fmt.Errorf("write tmp cache file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename cache file: %w", err)
	}

	return nil
}

// withLock acquires the lock, loads the current cache, calls fn with that
// cache, and if fn returns without error saves the returned cache.
func (s *Store) withLock(fn func(Cache) (Cache, error)) error {
	if err := xdg.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}

	fl := flock.New(s.lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire cache lock: %w", err)
	}

	defer fl.Unlock() //nolint:errcheck

	current, err := s.Load()
	if err != nil {
		return err
	}

	updated, err := fn(current)
	if err != nil {
		return err
	}

	return s.writeAtomic(updated)
}

// Get returns the cached entry for key. The second return value is false when
// the key is not present in the cache.
//
// Note: Get does not check expiry — use IsExpired to determine staleness.
func (s *Store) Get(key string) (WorktreeCache, bool) {
	c, err := s.Load()
	if err != nil {
		return WorktreeCache{}, false
	}

	entry, ok := c.Worktrees[key]

	return entry, ok
}

// IsExpired reports whether entry is older than ttl.
func (s *Store) IsExpired(entry WorktreeCache, ttl time.Duration) bool {
	return time.Since(entry.UpdatedAt) > ttl
}

// Set stores entry for key, setting UpdatedAt to the current time.
func (s *Store) Set(key string, entry WorktreeCache) error {
	entry.UpdatedAt = time.Now()

	return s.withLock(func(c Cache) (Cache, error) {
		c.Worktrees[key] = entry

		return c, nil
	})
}

// Delete removes the entry for key from the cache.
func (s *Store) Delete(key string) error {
	return s.withLock(func(c Cache) (Cache, error) {
		delete(c.Worktrees, key)

		return c, nil
	})
}

// SetBatch atomically writes all entries in the provided map, setting UpdatedAt
// to the current time for each entry that does not already have one set.
func (s *Store) SetBatch(entries map[string]WorktreeCache) error {
	now := time.Now()

	return s.withLock(func(c Cache) (Cache, error) {
		for key, entry := range entries {
			if entry.UpdatedAt.IsZero() {
				entry.UpdatedAt = now
			}

			c.Worktrees[key] = entry
		}

		return c, nil
	})
}
