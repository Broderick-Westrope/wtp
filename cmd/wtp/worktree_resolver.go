package main

import (
	"strings"

	"github.com/satococoa/wtp/v2/internal/errors"
	"github.com/satococoa/wtp/v2/internal/git"
)

// resolveWorktreePathByName resolves a worktree path by name.
//
// Special names:
//   - "@" or "root" → returns the main worktree path (IsMain == true)
//
// For all other names, the worktree whose Branch matches name is returned.
// An asterisk suffix (used by shell completion) is stripped before matching.
//
// Returns an error if no matching worktree is found.
func resolveWorktreePathByName(name string, worktrees []git.Worktree) (string, error) {
	name = strings.TrimSuffix(name, "*")

	for _, wt := range worktrees {
		if name == "@" || name == "root" {
			if wt.IsMain {
				return wt.Path, nil
			}
		} else if wt.Branch == name {
			return wt.Path, nil
		}
	}

	return "", errors.WorktreeNotFound(name, availableWorktreeNames(worktrees))
}

// availableWorktreeNames returns the addressable names for all worktrees:
// "@" for the main worktree and the branch name for all others.
func availableWorktreeNames(worktrees []git.Worktree) []string {
	names := make([]string, 0, len(worktrees))
	for _, wt := range worktrees {
		if wt.IsMain {
			names = append(names, "@")
		} else if wt.Branch != "" {
			names = append(names, wt.Branch)
		}
	}
	return names
}
