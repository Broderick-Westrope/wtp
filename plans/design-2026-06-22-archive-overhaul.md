# Archive Overhaul Design Spec

**Problem:** Archived worktrees still appear in LazyGit and other git tooling because archiving only flips a boolean in wtp's state file — the local branch and worktree directory remain intact. Additionally, archived worktrees are never auto-deleted, auto-archive only triggers on `wtp list`, and there is no periodic maintenance system.

**Goal:** Archiving fully removes the worktree and branch from git's perspective, hiding them from all git tooling. A two-tier maintenance system handles auto-archiving and expired entry cleanup across all wtp commands. Unarchiving restores the worktree to its pre-archive state.

**Scope:**

In scope:
- Rework `wtp archive` to remove worktree + branch and record recovery metadata
- Rework `wtp unarchive` to recreate worktree + branch from recorded SHA and re-run hooks
- Add two-tier maintenance system (cheap + expensive) as a pre-command hook
- Auto-archive on both MERGED and CLOSED PR states
- Move auto-archive responsibility from `wtp list` to the maintenance system
- Extend state schema with recovery metadata and timestamps
- Extend global config with `archive_retention` and `maintenance_interval`
- Update `wtp unarchive` shell completion to read from state.json (archived branches no longer appear in `git worktree list`)
- Update `wtp list --all` to synthesize archived entries from state.json

Out of scope:
- Cross-repo maintenance (only current repo is checked)
- Changes to `wtp add` or `wtp remove` behavior
- Changes to PR/CI display in `wtp list` (still fetches for display)

**Constraints:**
- Cheap maintenance must be fast (pure file I/O, no network)
- Expensive maintenance must not block commands when throttled (file-based timestamp check only)
- Commit SHA recovery relies on git's gc not having pruned the object (default 90-day gc window vs 10-day retention = large safety margin)
- `gh` CLI must be available for expensive maintenance; silently skip if not
- Repos without an `origin` remote cannot use archive (unchanged from current behavior)
- Maintenance does not run during shell completion callbacks (urfave/cli `ShellComplete` callbacks bypass `Before` hooks)

**Success Criteria:**
- [ ] Archived branches do not appear in `git branch`, `git worktree list`, or LazyGit
- [ ] `wtp unarchive` restores a worktree to its pre-archive local state (directory, branch, hooks re-run)
- [ ] Expired archive entries (>10 days default) are automatically reaped
- [ ] Auto-archive triggers on merged and closed PRs via maintenance, not `wtp list`
- [ ] Auto-archive skips worktrees with uncommitted changes (prints warning instead)
- [ ] Maintenance runs before every wtp command with throttling for expensive operations
- [ ] `archive_retention` and `maintenance_interval` are configurable in `config.yml`
- [ ] Unarchiving a branch that was auto-archived prevents immediate re-archiving
- [ ] Partial archive failures leave state recoverable (state written before git operations)
- [ ] `wtp list --all` shows archived entries synthesized from state.json
- [ ] All existing tests pass; new behavior has unit and e2e coverage

**Design Decisions:**

### Archive Behavior

`wtp archive <branch>` now performs a destructive operation:

1. Validate: not main worktree, not current worktree
2. **Idempotency check:** if state already says `archived: true` for this branch, skip to step 5 (re-attempt git cleanup for partial failures). This makes `wtp archive <branch>` the natural retry path if a previous archive partially failed.
3. Validate branch exists as a worktree. Check for dirty working tree — refuse unless `--force` is passed. Check for unpushed commits (only if an upstream tracking ref exists; skip the check if no upstream) — warn and refuse unless `--force` is passed.
4. **Record archive metadata in state.json first** (commit SHA, branch, path, timestamps) — this ensures recovery is possible even if subsequent git operations fail.
5. Run `git worktree remove <path>` to delete the worktree directory (skip if worktree no longer exists).
6. Run `git branch -D <branch>` to delete the local branch ref (skip if branch no longer exists). Uses `-D` not `-d` because `-d` fails when HEAD isn't the merge target, which is common when working from a non-main worktree.
7. If step 5 or 6 fails on a non-"already gone" error: print a warning suggesting `wtp archive <branch>` to retry. The state entry is preserved for recovery.
8. Clear `SuppressAutoArchive` for this branch if previously set (a manual archive is an explicit signal that auto-archive should resume if the branch is later unarchived and a new PR is created).

The `--force` flag overrides both the dirty-worktree and unpushed-commits checks.

*Alternative considered:* Writing state last (after git operations). Rejected because a failure between worktree removal and state write would lose the SHA with no recovery path. Write-first means partial failures are always recoverable via `wtp unarchive`.

*Alternative considered:* Keeping the worktree directory as a plain (non-git) folder. Rejected because it leaves the directory in an inconsistent state that other tools wouldn't understand, and the branch ref (which LazyGit shows) still needs to be deleted regardless.

### Unarchive Behavior

`wtp unarchive <branch>` recreates the worktree from recorded metadata:

1. Look up branch in state.json archived entries
2. Verify commit SHA still exists in object store (`git cat-file -t <sha>`) — fail with clear error if gc'd
3. Check if branch name already exists locally (`git rev-parse --verify <branch>`) — fail with clear error if someone manually recreated it
4. Determine worktree path: try original recorded path first; if the parent directory doesn't exist or the target path is already occupied, fall back to the standard centralized storage path (`WorktreeStorageRoot()/repoID.StoragePath()/branch`)
5. Run `git worktree add -b <branch> <path> <sha>` to recreate worktree and branch
6. Re-run post-create hooks from `.wtp.yml` (same as `wtp add`)
7. Remove the archived entry from state.json
8. Set `SuppressAutoArchive` flag in state for this branch (prevents maintenance from immediately re-archiving a merged/closed PR branch)

*Alternative considered:* Skipping hook re-execution since hooks already ran once. Rejected because the directory is recreated from scratch — symlinks, copied files, and command hook outputs are all gone.

*Shell completion:* `completeArchivedBranches` must be updated to read branch names from state.json archived entries instead of cross-referencing `git worktree list` with state (since archived branches no longer appear in worktree list).

### Auto-Archive Triggers

Auto-archive triggers on both `MERGED` and `CLOSED` PR states. Previously only `MERGED` triggered auto-archive. Both `StateMerged` and `StateClosed` must be exported from the `github` package for the maintenance system to use.

For auto-archived branches, the unpushed-commits check is skipped — merged PRs have their work on the remote by definition. Note: closed (not merged) PRs may have had their remote branch deleted, but the SHA is still recorded locally for recovery.

**Dirty-worktree guard for auto-archive:** Before destructive removal, auto-archive must check if the worktree has uncommitted changes. If dirty, skip with a warning to stderr: "Skipped auto-archive of \<branch\>: worktree has uncommitted changes." This prevents silent data loss — the user can manually commit or `wtp archive --force` when ready.

Branches with `SuppressAutoArchive` set are skipped by maintenance auto-archive. This flag is set when a user explicitly unarchives a branch, preventing the re-archive loop where maintenance detects a merged/closed PR and immediately re-archives what the user just restored. The flag is cleared when the user manually runs `wtp archive` on the branch (an explicit archive is a signal that auto-archive should resume for future cycles).

### Maintenance System

Two-tier pre-command maintenance replaces the current `wtp list`-only auto-archive:

**Cheap tier (every command):**
- Check state.json for expired entries (past `archive_retention` from `PRClosedAt`, or from `ArchivedAt` if `PRClosedAt` is zero)
- Delete expired entries
- Pure file I/O, no network calls
- Expiration precedence: use `PRClosedAt` when set (auto-archives from merged or closed PRs), fall back to `ArchivedAt` (manual archives with no PR)

**Expensive tier (throttled):**
- Check per-repo timestamp file at `$XDG_DATA_HOME/wtp/maintenance/<owner>--<repo>` (slashes in `StoragePath()` replaced with `--` to produce a flat filename; e.g., `Broderick-Westrope--wtp`)
- If older than `maintenance_interval` (default 10 minutes, configurable): proceed
- If fresh: skip entirely
- For each non-archived worktree in the current repo: check PR state via `gh pr view`
- Check dirty-worktree status before auto-archiving (skip with warning if dirty)
- Auto-archive any with MERGED or CLOSED state (unless `SuppressAutoArchive` is set)
- Update per-repo timestamp file
- Requires `gh` CLI; silently skips if unavailable

**Integration point:** Maintenance runs via urfave/cli's `Before` hook on the root command, which executes before any subcommand's `Action`. Shell completion callbacks (`ShellComplete`) bypass `Before` hooks, so maintenance does not run during tab completion — this is acceptable since completion should be fast.

*Alternative considered:* Running maintenance only on `wtp list`. Rejected because users may not run `list` frequently, leaving stale worktrees visible in git tooling for extended periods.

*Alternative considered:* A single global timestamp file for the expensive tier. Rejected because working across multiple repos would cause repo A's maintenance to suppress repo B's checks for the throttle duration.

### wtp list Changes

`wtp list` retains its PR/CI data fetching for display purposes (the table still shows PR and CI columns). However, auto-archive responsibility moves to the maintenance system. The existing `fetchPRCIData` function no longer calls `autoArchiveBranch`. The `--no-sync` flag continues to skip `gh` calls for display.

**`--all` flag:** Currently `--all` includes archived worktrees from `git worktree list`. Post-overhaul, archived branches are removed from git and won't appear in `git worktree list`. The `--all` flag must synthesize archived entries from state.json: for each archived entry matching the current repo, add a synthetic row to the display with the branch name and "(archived)" label. These rows show the branch name and archive date but no PR/CI/HEAD data (since the worktree no longer exists in git). In `--quiet` mode, `--all` includes archived branch names as bare names (no suffix) — they are valid arguments to `wtp unarchive`.

### wtp remove Interaction

`wtp remove` on a branch that exists in state.json as archived (but whose worktree/branch are already gone) will return "worktree not found" since the worktree doesn't appear in `git worktree list`. This is acceptable — users should use `wtp unarchive` to restore or wait for the retention period to expire.

### State Schema Changes

```go
type WorktreeState struct {
    Archived             bool      `json:"archived"`
    ArchivedAt           time.Time `json:"archived_at,omitempty"`
    PRClosedAt           time.Time `json:"pr_closed_at,omitempty"`
    CommitSHA            string    `json:"commit_sha,omitempty"`
    Branch               string    `json:"branch,omitempty"`
    WorktreePath         string    `json:"worktree_path,omitempty"`
    SuppressAutoArchive  bool      `json:"suppress_auto_archive,omitempty"`
}
```

`PRClosedAt` is set for both MERGED and CLOSED PRs — both are forms of PR closure. This replaces the earlier `MergedAt` name which was semantically incorrect for closed-without-merge PRs.

**Migration:** Existing state.json entries with only `"archived": true` (no SHA/path metadata) are from the old system. These entries cannot be unarchived under the new system since there's no SHA to restore from. The cheap maintenance tier treats them as expired and reaps them (they have no `PRClosedAt` or `ArchivedAt`, so they're considered immediately expired). A one-time warning is printed to stderr: "Cleaned up N legacy archive entries (no recovery metadata)."

### Global Config Changes

```yaml
cache_ttl: 60s                # existing
archive_retention: 240h       # 10 days, new
maintenance_interval: 10m     # new
```

```go
type GlobalConfig struct {
    CacheTTL            time.Duration `yaml:"cache_ttl"`
    ArchiveRetention    time.Duration `yaml:"archive_retention"`
    MaintenanceInterval time.Duration `yaml:"maintenance_interval"`
}
```

Defaults: `ArchiveRetention` = 240h (10 days), `MaintenanceInterval` = 10m.

### Safeguards

- **Dirty worktree (manual):** `wtp archive` refuses unless `--force` is passed
- **Dirty worktree (auto):** maintenance skips auto-archive with a warning to stderr
- **Unpushed commits:** `wtp archive` warns and refuses unless `--force` is passed
- **Current worktree:** `wtp archive` blocks with "cannot archive the current worktree"
- **Main worktree:** `wtp archive` blocks with "cannot archive the main worktree" (unchanged)
- **GC'd commit:** `wtp unarchive` fails with "commit \<sha\> no longer exists in the object store"
- **Branch name conflict:** `wtp unarchive` fails if the branch name already exists locally
- **Missing original path:** `wtp unarchive` falls back to centralized storage path (`WorktreeStorageRoot()`)
- **Partial archive failure:** State is written first; partial git failures leave a recoverable state entry. Re-running `wtp archive <branch>` retries the git cleanup (idempotent).
- **No upstream tracking ref:** unpushed-commits check is skipped if the branch has no upstream, since there's nothing to compare against
- **Re-archive loop:** `SuppressAutoArchive` flag prevents maintenance from re-archiving a deliberately unarchived branch; cleared on manual `wtp archive`

**Context Files:**
- `cmd/wtp/archive.go` — current archive/unarchive commands
- `cmd/wtp/list.go` — current auto-archive logic in `fetchPRCIData` and `autoArchiveBranch`
- `cmd/wtp/remove.go` — worktree removal and cleanup patterns
- `cmd/wtp/add.go` — worktree path resolution (`resolveWorktreePath`), hook execution (`executePostCreateHooks`)
- `cmd/wtp/worktree_resolver.go` — branch-to-path resolution
- `cmd/wtp/app.go` — command registration, `Before` hook integration point
- `internal/state/state.go` — state persistence with flock
- `internal/cache/cache.go` — cache persistence patterns
- `internal/config/global.go` — global config schema and loading
- `internal/command/builders.go` — git command builders
- `internal/hooks/executor.go` — post-create hook execution
- `internal/git/worktree.go` — worktree types
- `internal/xdg/xdg.go` — `WorktreeStorageRoot()` for centralized storage paths
- `internal/remote/parse.go` — `RepoIdentifier`, `StoragePath()`, `StateKey()`, `ParseStateKey()`
- `internal/github/github.go` — PR state constants (`StateMerged`, needs `StateClosed` exported)
