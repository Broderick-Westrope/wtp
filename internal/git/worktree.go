// Package git provides helpers for interacting with git repositories and worktrees.
package git

import (
	"fmt"
	"path/filepath"
)

const (
	// MainBranch is the default branch name used for primary repositories.
	MainBranch = "main"
	// MasterBranch is kept for backward compatibility with repositories using master.
	MasterBranch = "master"
)

// Worktree represents a git worktree with basic metadata.
type Worktree struct {
	Path   string
	Branch string
	HEAD   string
	IsMain bool // True if this is the main/root worktree
}

// Name returns the branch name of the worktree.
// Falls back to the directory name when Branch is empty (e.g. detached HEAD).
func (w *Worktree) Name() string {
	if w.Branch != "" {
		return w.Branch
	}
	return filepath.Base(w.Path)
}

func (w *Worktree) String() string {
	if w.Branch != "" {
		return fmt.Sprintf("%s [%s]", w.Path, w.Branch)
	}
	return fmt.Sprintf("%s [%s]", w.Path, w.HEAD)
}

// CompletionName returns the name to display for shell completion.
//
// The repoName parameter is accepted for API compatibility but is no longer used.
// Returns "@" for the main worktree and the branch name for all others.
func (w *Worktree) CompletionName(_ string) string {
	if w.IsMain {
		return "@"
	}
	return w.Branch
}

// IsMainWorktree returns true if this is the main/root worktree
func (w *Worktree) IsMainWorktree(mainWorktreePath string) bool {
	// If IsMain flag is set, use it (set by GetWorktrees)
	if w.IsMain {
		return true
	}

	// If mainWorktreePath is provided, compare paths
	if mainWorktreePath != "" {
		return w.Path == mainWorktreePath
	}

	// This shouldn't happen in normal usage since we always provide mainWorktreePath
	// But if it does, we can't determine without more context
	return false
}
