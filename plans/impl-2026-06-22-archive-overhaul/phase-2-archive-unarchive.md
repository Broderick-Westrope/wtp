# Phase 2: Archive/Unarchive Rework

> **Spec:** `plans/design-2026-06-22-archive-overhaul.md`
> **Depends on:** Phase 1 (state schema, git helpers)

## Specification

**Problem:** `wtp archive` only sets a boolean flag — worktrees and branches remain visible in git tooling. `wtp unarchive` only clears the flag. Neither performs git operations.

**Goal:** `wtp archive` destructively removes the worktree directory and local branch, recording recovery metadata. `wtp unarchive` recreates both from the recorded SHA and re-runs hooks. Shell completion for unarchive reads from state.json.

**Scope:** `cmd/wtp/archive.go`, `cmd/wtp/archive_test.go`, `test/e2e/archive_test.go`. No maintenance or list changes.

**Success Criteria:**

- [ ] `wtp archive` removes worktree and branch from git; branch no longer appears in `git branch`
- [ ] `wtp archive` records SHA, path, branch, timestamps in state.json before git operations
- [ ] `wtp archive` is idempotent — re-running retries git cleanup for partial failures
- [ ] `wtp archive` refuses dirty worktrees and unpushed commits without `--force`
- [ ] `wtp archive` blocks from current worktree
- [ ] `wtp unarchive` recreates worktree and branch from SHA, re-runs hooks
- [ ] `wtp unarchive` sets `SuppressAutoArchive`, fails clearly if SHA gc'd or branch exists
- [ ] `wtp unarchive` shell completion reads from state.json
- [ ] All existing tests updated and passing; new test coverage for all paths

## Context Loading

_Run before starting:_

```bash
read cmd/wtp/archive.go
read cmd/wtp/archive_test.go
read cmd/wtp/add.go  # for executePostCreateHooks pattern, resolveWorktreePath
read cmd/wtp/remove.go  # for git worktree remove pattern, cleanup patterns
read cmd/wtp/worktree_resolver.go
read internal/state/state.go
read internal/git/repository.go
read internal/command/builders.go
read internal/hooks/executor.go
read internal/config/config.go  # for LoadConfig, Hook types
read internal/remote/parse.go  # for RepoIdentifier, StateKey, StoragePath
read test/e2e/archive_test.go
read test/e2e/framework/framework.go
```

## Archive Command Tasks

### Task 1: Rewrite archive command with destructive behavior

**Context:** `cmd/wtp/archive.go`, `cmd/wtp/remove.go`

**Files:**
- Modify: `cmd/wtp/archive.go` (rewrite `archiveCommand` and `archiveCommandCore`)
- Modify: `cmd/wtp/archive_test.go` (rewrite tests for new behavior)

**Steps:**

1. [ ] Add `--force` flag to `NewArchiveCommand()`:
   ```go
   &cli.BoolFlag{
       Name:    "force",
       Aliases: []string{"f"},
       Usage:   "Force archive even if worktree is dirty or has unpushed commits",
   },
   ```

2. [ ] Rewrite `archiveCommand` to:
   - Get cwd, check not in target worktree (same pattern as `remove.go:129-131` using `isPathWithin`)
   - Pass `force` flag to core

3. [ ] Rewrite `archiveCommandCore` with the new flow:
   - Validate: not main worktree, not current worktree
   - **Idempotency check:** if `stateStore.IsArchived(key)` is true, load the existing state entry and skip to git cleanup (step 5 of spec). This handles partial failure retries.
   - For fresh archives: validate branch exists as a worktree, check dirty (`repo.IsWorktreeDirty`), check unpushed (`repo.HasUnpushedCommits`) — refuse unless force. Skip unpushed check if `HasUnpushedCommits` returns error (no upstream).
   - Record metadata in state first: call `state.PerformArchive(executor, stateStore, key, ws)` where `ws` is populated with `CommitSHA` from `targetWt.HEAD`, `Branch`, `WorktreePath` from `targetWt.Path`, `ArchivedAt = time.Now()`. This shared function handles: write state → remove worktree → delete branch.
   - Clear `SuppressAutoArchive` if set
   - Print success message

4. [ ] The `state.Store` methods `SetArchivedFull` and `ClearArchived` are already implemented in Phase 1. Use them directly.

5. [ ] Add helper function `isCurrentWorktree(targetPath, cwd string) bool` — reuse `isPathWithin` from `remove.go` (it's already in the same package).

6. [ ] Update tests in `archive_test.go`:
   - `TestArchiveCommand_SetsFlag` → rename to `TestArchiveCommand_RemovesWorktreeAndBranch`, test that state has SHA/path/timestamps, and that git operations (worktree remove + branch delete) are attempted. Since `archiveCommandCore` uses an executor, mock the executor to verify correct commands are built.
   - Add `TestArchiveCommand_IdempotentRetry` — state already says archived, re-running retries git cleanup
   - Add `TestArchiveCommand_RefusesDirtyWorktree` — mock `IsWorktreeDirty` to return true, verify error without `--force`
   - Add `TestArchiveCommand_RefusesUnpushedCommits` — mock `HasUnpushedCommits` to return true, verify error without `--force`
   - Add `TestArchiveCommand_ForceOverridesDirtyAndUnpushed` — verify `--force` bypasses both checks
   - Add `TestArchiveCommand_BlocksCurrentWorktree` — cwd inside target worktree, verify error
   - Keep existing tests for main worktree rejection, already-archived rejection (update to expect idempotent behavior)

**Verify:**
```bash
go tool task test
# Expected: all archive unit tests pass
```

### Task 2: Rewrite unarchive command with SHA-based restore

**Context:** `cmd/wtp/archive.go` (unarchive section), `cmd/wtp/add.go` (hook execution pattern)

**Files:**
- Modify: `cmd/wtp/archive.go` (rewrite `unarchiveCommand` and `unarchiveCommandCore`)
- Modify: `cmd/wtp/archive_test.go` (rewrite unarchive tests)

**Steps:**

1. [ ] Rewrite `unarchiveCommandCore` with the new flow:
   - Look up branch in state — must be archived with `CommitSHA` present. If archived but no SHA (legacy entry), return error: "cannot unarchive legacy entry — no recovery metadata"
   - Get a `git.Repository` instance for the main worktree path
   - Verify SHA exists: `repo.CommitExists(ws.CommitSHA)` — fail with clear error if not
   - Check branch doesn't already exist: `repo.BranchExists(branch)` — fail if it does
   - Determine worktree path: try `ws.WorktreePath` first. If parent dir doesn't exist or path is already occupied (`os.Stat` succeeds), fall back to `WorktreeStorageRoot()/repoID.StoragePath()/branch`
   - Ensure parent dir exists: `xdg.EnsureDir(filepath.Dir(worktreePath))`
   - Execute `git worktree add -b <branch> <path> <sha>` via executor (use `command.GitWorktreeAdd` with `Branch` set and commitish = SHA)
   - Load `.wtp.yml` config and run post-create hooks (same pattern as `add.go:378-394`, use `executePostCreateHooks`)
   - Remove archived entry and set suppress flag: call `stateStore.ClearArchived(key)` (implemented in Phase 1 — clears archive metadata, sets `SuppressAutoArchive = true`)
   - Print success message with the worktree path

2. [ ] The `ClearArchived` method is already implemented in Phase 1. Verify it sets `SuppressAutoArchive = true` and clears all archive metadata.

3. [ ] Update unarchive tests:
   - `TestUnarchiveCommand_RestoresWorktree` — state has archived entry with SHA/path, verify git worktree add command is built correctly
   - `TestUnarchiveCommand_FailsWhenSHAGarbageCollected` — mock `CommitExists` to return false, verify error message
   - `TestUnarchiveCommand_FailsWhenBranchExists` — mock `BranchExists` to return true, verify error
   - `TestUnarchiveCommand_FallsBackToStoragePath` — original path parent doesn't exist, verify fallback path used
   - `TestUnarchiveCommand_FallsBackWhenPathOccupied` — original path exists as directory, verify fallback
   - `TestUnarchiveCommand_SetsSuppressAutoArchive` — verify state has flag set after unarchive
   - `TestUnarchiveCommand_RejectsLegacyEntry` — archived=true but no SHA, verify error

**Verify:**
```bash
go tool task test
# Expected: all unarchive unit tests pass
```

### Task 3: Update shell completion for unarchive

**Context:** `cmd/wtp/archive.go` (completion functions)

**Files:**
- Modify: `cmd/wtp/archive.go` (rewrite `completeArchivedBranches`)
- Modify: `cmd/wtp/archive_test.go` (update completion tests)

**Steps:**

1. [ ] Rewrite `completeArchivedBranches` to read from state.json instead of cross-referencing git worktree list:
   - Load state via `state.NewStore().Load()`
   - Get repo identifier (same pattern as current: getwd → NewRepository → GetRemoteURL → remote.Parse)
   - Iterate state entries: for each entry where key starts with `repoID.StoragePath() + "::"` and `Archived == true`, extract the branch name using `remote.ParseStateKey(key)` and print it
   - No git worktree list needed — archived branches are no longer in git

2. [ ] `completeNonArchivedBranches` (for `wtp archive`) remains mostly the same — it reads from `git worktree list` which only shows non-archived worktrees. But update it to also exclude branches that are in state as archived (for the idempotent retry case — an already-archived branch should still appear in completion since retry is valid). Actually, keep it as-is: it shows worktrees from `git worktree list`, and archived worktrees won't be there.

3. [ ] Add/update completion tests to verify the new behavior.

**Verify:**
```bash
go tool task test
# Expected: all completion tests pass
```

### Task 4: Update e2e tests for archive/unarchive

**Context:** `test/e2e/`

**Files:**
- Modify: `test/e2e/archive_test.go` (update existing, add new scenarios)

**Steps:**

1. [ ] Update `TestArchiveWorkflow` — after archiving, verify:
   - Branch does NOT appear in `git branch` output (run `git branch` via the test repo)
   - Worktree directory does NOT exist on disk
   - `wtp list --no-sync` does NOT show the branch
   - `wtp unarchive` restores the worktree — branch appears in `git branch`, directory exists, `wtp list` shows it

2. [ ] Add `TestArchiveForce` — create a worktree, add an untracked file (dirty), attempt `wtp archive` (should fail), then `wtp archive --force` (should succeed).

3. [ ] Add `TestArchiveIdempotent` — archive a worktree, manually recreate the worktree via `git worktree add`, then re-run `wtp archive` — should succeed by re-removing.

4. [ ] Add `TestArchiveCurrentWorktree` — attempt to archive while cwd is inside the worktree, verify error.

5. [ ] Add `TestUnarchiveRestoredWorktreeHasContent` — create a worktree, commit a file in it, archive, unarchive, verify the committed file exists in the restored worktree.

**Verify:**
```bash
go tool task test-e2e
# Expected: all e2e archive tests pass
```

<!-- Review notes: devils-advocate review confirmed idempotency path, partial failure recovery via write-first, and completion rewrite necessity. -->
