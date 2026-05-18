// Package state manages persistent worktree state (e.g. archive flags) using
// JSON file storage with flock-based mutual exclusion and atomic writes.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"

	"github.com/satococoa/wtp/v3/internal/xdg"
)

const stateFileMode = 0o600 // owner read/write only

// Store holds the paths used to persist and lock the state file.
type Store struct {
	path     string // path to state.json
	lockPath string // path to state.json.lock
}

// NewStore returns a Store rooted at xdg.WtpDataDir()/state.json.
func NewStore() *Store {
	dir := xdg.WtpDataDir()
	return &Store{
		path:     filepath.Join(dir, "state.json"),
		lockPath: filepath.Join(dir, "state.json.lock"),
	}
}

// State is the top-level structure persisted to disk.
// Worktrees is keyed by "owner/repo::branch" (or "host/owner/repo::branch" for non-github.com).
type State struct {
	Worktrees map[string]WorktreeState `json:"worktrees"`
}

// WorktreeState holds per-worktree metadata.
type WorktreeState struct {
	Archived bool `json:"archived"`
}

// Load reads the state file without acquiring a lock.
// If the file does not exist an empty State is returned without error.
func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Worktrees: map[string]WorktreeState{}}, nil
		}

		return State{}, fmt.Errorf("read state file: %w", err)
	}

	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("unmarshal state: %w", err)
	}

	if st.Worktrees == nil {
		st.Worktrees = map[string]WorktreeState{}
	}

	return st, nil
}

// Save acquires a lock on the lock file, writes the state to a temporary file
// adjacent to the state file, renames it into place, then releases the lock.
func (s *Store) Save(state State) error {
	if err := xdg.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}

	fl := flock.New(s.lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}

	defer fl.Unlock() //nolint:errcheck

	return s.writeAtomic(state)
}

// writeAtomic writes state to a .tmp file then renames it over the target path.
// The caller is responsible for holding the lock before calling this method.
func (s *Store) writeAtomic(state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpPath := s.path + ".tmp"

	if err := os.WriteFile(tmpPath, data, stateFileMode); err != nil {
		return fmt.Errorf("write tmp state file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("rename state file: %w", err)
	}

	return nil
}

// WithLock is the primary mutation API.
// It acquires the lock, loads the current state, calls fn with that state, and
// if fn returns without error saves the returned state before releasing the lock.
func (s *Store) WithLock(fn func(State) (State, error)) error {
	if err := xdg.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}

	fl := flock.New(s.lockPath)
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
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

// IsArchived reports whether the worktree identified by key is archived.
func (s *Store) IsArchived(key string) bool {
	st, err := s.Load()
	if err != nil {
		return false
	}

	return st.Worktrees[key].Archived
}

// SetArchived sets or clears the archived flag for the worktree identified by key.
func (s *Store) SetArchived(key string, archived bool) error {
	return s.WithLock(func(st State) (State, error) {
		entry := st.Worktrees[key]
		entry.Archived = archived
		st.Worktrees[key] = entry

		return st, nil
	})
}
