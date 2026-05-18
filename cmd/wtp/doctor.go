package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/satococoa/wtp/v3/internal/command"
	"github.com/satococoa/wtp/v3/internal/errors"
	"github.com/satococoa/wtp/v3/internal/git"
	"github.com/satococoa/wtp/v3/internal/github"
	"github.com/satococoa/wtp/v3/internal/remote"
	"github.com/satococoa/wtp/v3/internal/state"
	"github.com/satococoa/wtp/v3/internal/xdg"
)

var doctorGetwd = os.Getwd

// NewDoctorCommand creates the doctor command.
func NewDoctorCommand() *cli.Command {
	return &cli.Command{
		Name:        "doctor",
		Usage:       "Diagnose common wtp issues",
		Description: "Checks for v2 worktrees, orphaned state entries, orphaned directories, and gh CLI status.",
		Action:      doctorCommand,
	}
}

func doctorCommand(_ context.Context, cmd *cli.Command) error {
	w := cmd.Root().Writer
	if w == nil {
		w = os.Stdout
	}

	cwd, err := doctorGetwd()
	if err != nil {
		return errors.DirectoryAccessFailed("access current", ".", err)
	}

	repo, err := git.NewRepository(cwd)
	if err != nil {
		return errors.NotInGitRepository()
	}

	// Get all registered worktrees
	executor := command.NewRealExecutor()
	result, err := executor.Execute([]command.Command{command.GitWorktreeList()})
	if err != nil {
		return errors.GitCommandFailed("git worktree list", err.Error())
	}
	worktrees := parseWorktreesFromOutput(result.Results[0].Output)

	// Determine repo root (main worktree path)
	var repoRoot string
	for _, wt := range worktrees {
		if wt.IsMain {
			repoRoot = wt.Path
			break
		}
	}

	// Build set of registered worktree paths
	registeredPaths := make(map[string]bool)
	for _, wt := range worktrees {
		registeredPaths[wt.Path] = true
	}

	issueCount := 0

	// 1. v2 worktree detection
	issueCount += checkV2Worktrees(w, worktrees, repoRoot)

	// 2. Orphaned state entries
	var repoID *remote.RepoIdentifier
	if remoteURL, err := repo.GetRemoteURL("origin"); err == nil {
		if id, err := remote.Parse(remoteURL); err == nil {
			repoID = &id
		}
	}
	issueCount += checkOrphanedStateEntries(w, repoID, registeredPaths)

	// 3. Orphaned centralized directories
	issueCount += checkOrphanedCentralizedDirs(w, registeredPaths)

	// 4. gh CLI status
	issueCount += checkGHStatus(w)

	// Summary
	if issueCount == 0 {
		if _, err := fmt.Fprintln(w, "✓ No issues found"); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "\n%d issue(s) found\n", issueCount); err != nil {
			return err
		}
	}

	return nil
}

// checkV2Worktrees detects possible v2-style worktrees (paths containing /worktrees/ segment).
// Returns number of issues found.
func checkV2Worktrees(w io.Writer, worktrees []git.Worktree, repoRoot string) int {
	count := 0
	for _, wt := range worktrees {
		if wt.IsMain {
			continue
		}
		// Check if path contains /worktrees/ as a path segment (v2 pattern)
		rel := wt.Path
		if repoRoot != "" {
			if r, err := filepath.Rel(repoRoot, wt.Path); err == nil {
				rel = r
			}
		}
		// v2 stored worktrees in <repo>/.worktrees/ or a sibling worktrees/ dir
		if isV2WorktreePath(wt.Path, rel) {
			_, _ = fmt.Fprintf(w, "⚠ Possible v2 worktree: %s\n", wt.Path)
			_, _ = fmt.Fprintf(w, "  Run: git worktree remove %s\n", wt.Path)
			count++
		}
	}
	return count
}

// isV2WorktreePath returns true if the path looks like a v2-managed worktree.
// v2 stored worktrees in <repo>/.worktrees/ (with dot prefix) or a sibling worktrees/
// directory. Centralized wtp storage paths (under XDG_DATA_HOME) are excluded.
func isV2WorktreePath(absPath, relPath string) bool {
	// Skip paths under centralized wtp storage (XDG_DATA_HOME/wtp/worktrees)
	storageRoot := xdg.WorktreeStorageRoot()
	if storageRoot != "" && strings.HasPrefix(filepath.ToSlash(absPath), filepath.ToSlash(storageRoot)) {
		return false
	}

	for _, p := range []string{absPath, relPath} {
		parts := strings.Split(filepath.ToSlash(p), "/")
		for _, part := range parts {
			if part == ".worktrees" || part == "worktrees" {
				return true
			}
		}
	}
	return false
}

// checkOrphanedStateEntries finds state entries with no matching on-disk worktree.
// Returns number of issues found.
func checkOrphanedStateEntries(w io.Writer, repoID *remote.RepoIdentifier, registeredPaths map[string]bool) int {
	if repoID == nil {
		return 0
	}

	stateStore := state.NewStore()
	st, err := stateStore.Load()
	if err != nil {
		return 0
	}

	count := 0
	for key := range st.Worktrees {
		_, branch := remote.ParseStateKey(key)
		if branch == "" {
			continue
		}

		// Check if any registered worktree has this branch
		found := false
		for path := range registeredPaths {
			// Check if path exists on disk
			if _, err := os.Stat(path); err == nil {
				found = true
				break
			}
		}
		// Also check worktrees with this branch (via key matching)
		_ = found // We check via the key prefix matching repoID

		// More precise: check if repoID matches and branch exists in registered paths
		if !strings.HasPrefix(key, repoID.StoragePath()) {
			continue // key belongs to a different repo
		}

		// Check if any registered path is a worktree with this branch
		_, _ = w, branch
		// Since we can't map key→path directly without worktrees list,
		// just check if there's a disk path for the centralized store
		centralPath := filepath.Join(xdg.WorktreeStorageRoot(), repoID.StoragePath(), branch)
		if _, err := os.Stat(centralPath); os.IsNotExist(err) {
			_, _ = fmt.Fprintf(w, "⚠ Orphaned state entry: %s (worktree not found on disk)\n", key)
			_, _ = fmt.Fprintln(w, "  Run: wtp doctor --fix to clean up (future enhancement)")
			count++
		}
	}
	return count
}

// checkOrphanedCentralizedDirs finds directories in centralized storage not registered as worktrees.
// Returns number of issues found.
func checkOrphanedCentralizedDirs(w io.Writer, registeredPaths map[string]bool) int {
	storageRoot := xdg.WorktreeStorageRoot()
	if _, err := os.Stat(storageRoot); os.IsNotExist(err) {
		return 0 // no centralized storage yet
	}

	count := 0
	// Walk up to 3 levels deep (host/owner/repo or owner/repo/branch)
	walkCentralizedDirs(storageRoot, registeredPaths, w, &count, 0)
	return count
}

const maxCentralizedDirDepth = 3

func walkCentralizedDirs(
	dir string,
	registeredPaths map[string]bool,
	w io.Writer,
	count *int,
	depth int,
) {
	if depth > maxCentralizedDirDepth {
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		// Check if this directory is a registered worktree
		if !registeredPaths[fullPath] {
			// Check if it looks like a leaf worktree dir (has .git file)
			gitFile := filepath.Join(fullPath, ".git")
			if _, err := os.Stat(gitFile); err == nil {
				// This is a worktree dir not registered with git
				_, _ = fmt.Fprintf(w, "⚠ Orphaned directory: %s (not registered as a git worktree)\n", fullPath)
				(*count)++
				continue
			}
			// Recurse deeper
			walkCentralizedDirs(fullPath, registeredPaths, w, count, depth+1)
		}
	}
}

// checkGHStatus checks gh CLI availability and authentication.
// Returns number of issues found.
func checkGHStatus(w io.Writer) int {
	count := 0
	if !github.IsAvailable() {
		_, _ = fmt.Fprintln(w, "✗ gh CLI not found")
		_, _ = fmt.Fprintln(w, "  Install from: https://cli.github.com")
		count++
		return count
	}

	_, _ = fmt.Fprintln(w, "✓ gh CLI found")

	auth, err := github.IsAuthenticated()
	if err != nil {
		_, _ = fmt.Fprintf(w, "⚠ gh authentication check failed: %v\n", err)
		count++
	} else if !auth {
		_, _ = fmt.Fprintln(w, "✗ gh not authenticated (run: gh auth login)")
		count++
	} else {
		_, _ = fmt.Fprintln(w, "✓ gh authenticated")
	}
	return count
}
