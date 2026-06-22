# Phase 3: Maintenance System

> **Spec:** `plans/design-2026-06-22-archive-overhaul.md`
> **Depends on:** Phase 1 (state schema, config, github exports)
> **Parallel with:** Phase 2

## Specification

**Problem:** Auto-archive only runs inside `wtp list`. Users who rarely run `list` accumulate stale worktrees visible in git tooling. Expired archive entries are never cleaned up.

**Goal:** A two-tier maintenance system runs before every wtp command. The cheap tier reaps expired state entries (pure file I/O). The expensive tier checks PR states via `gh` and auto-archives merged/closed PRs, throttled by a per-repo timestamp file.

**Scope:** New `internal/maintenance/` package, integration in `cmd/wtp/app.go`. No changes to archive, unarchive, or list commands.

**Success Criteria:**

- [ ] Cheap maintenance reaps expired entries every command
- [ ] Legacy entries (no timestamps) are reaped with a one-time warning
- [ ] Expensive maintenance checks PR states, throttled by configurable interval (default 10m)
- [ ] Per-repo timestamp files prevent cross-repo throttle suppression
- [ ] Auto-archive triggers on MERGED and CLOSED PR states
- [ ] Auto-archive skips dirty worktrees with a warning
- [ ] Auto-archive skips branches with `SuppressAutoArchive` set
- [ ] Auto-archive records correct metadata (SHA, path, timestamps) before removing worktree/branch
- [ ] Maintenance is wired into the root command's `Before` hook
- [ ] All existing tests pass; new unit tests for maintenance logic

## Context Loading

_Run before starting:_

```bash
read cmd/wtp/app.go
read cmd/wtp/list.go  # for fetchPRCIData, autoArchiveBranch patterns
read internal/state/state.go
read internal/config/global.go
read internal/cache/cache.go  # for Store pattern reference
read internal/github/github.go
read internal/git/repository.go
read internal/remote/parse.go
read internal/xdg/xdg.go
```

## Maintenance Package Tasks

### Task 1: Create cheap maintenance tier

**Context:** `internal/maintenance/` (new package)

**Files:**
- Create: `internal/maintenance/maintenance.go`
- Create: `internal/maintenance/maintenance_test.go`

**Steps:**

1. [ ] Create `internal/maintenance/maintenance.go` with package `maintenance`.

2. [ ] Define `Runner` struct:
   ```go
   type Runner struct {
       stateStore  *state.Store
       globalCfg   config.GlobalConfig
       repoID      *remote.RepoIdentifier
       mainRepoPath string
       stderr      io.Writer
   }
   ```
   Add constructor `NewRunner(stateStore, globalCfg, repoID, mainRepoPath, stderr)`.

3. [ ] Implement `RunCheap() error`:
   - Load state via `stateStore.Load()`
   - Track legacy entries found (archived=true, no SHA, no timestamps)
   - For each entry whose key starts with `repoID.StoragePath() + "::"`:
     - If `IsLegacy()`: mark for deletion, increment legacy counter
     - If `Archived` and `ExpirationTime()` is non-zero and older than `globalCfg.ArchiveRetention`: mark for deletion
   - Use `stateStore.WithLock` to delete all marked entries atomically
   - If legacy entries were reaped, print one-time warning to stderr: `"Cleaned up N legacy archive entries (no recovery metadata)"`
   - Return nil (errors during reap are best-effort)

4. [ ] Add tests:
   - `TestRunCheap_ReapsExpiredEntries` — state has entries with `ArchivedAt` older than retention, verify they're deleted
   - `TestRunCheap_KeepsFreshEntries` — entries within retention window are kept
   - `TestRunCheap_ReapsLegacyEntries` — entries with only `Archived: true` and no timestamps are reaped
   - `TestRunCheap_UsesCorrectExpirationPrecedence` — entry with `PRClosedAt` set uses that; entry with only `ArchivedAt` uses that
   - `TestRunCheap_OnlyAffectsCurrentRepo` — entries for other repos are untouched

**Verify:**
```bash
go tool task test
# Expected: all maintenance tests pass
```

### Task 2: Create expensive maintenance tier with throttle

**Context:** `internal/maintenance/`

**Files:**
- Modify: `internal/maintenance/maintenance.go` (add throttle + PR checking)
- Modify: `internal/maintenance/maintenance_test.go` (add tests)

**Steps:**

1. [ ] Add throttle helpers:
   - `maintenanceDir() string` — returns `filepath.Join(xdg.WtpDataDir(), "maintenance")`
   - `throttleFile(repoID) string` — returns `filepath.Join(maintenanceDir(), sanitizeRepoKey(repoID.StoragePath()))` where `sanitizeRepoKey` replaces `/` with `--`
   - `isThrottled(throttlePath string, interval time.Duration) bool` — checks if file modification time is within `interval` of `time.Now()`
   - `touchThrottleFile(throttlePath string) error` — creates/updates the file

2. [ ] Implement `RunExpensive(ctx context.Context) error`:
   - Check throttle: if `isThrottled`, return nil immediately
   - Check `gh` available: if `!github.IsAvailable()`, return nil
   - Get worktrees via `git worktree list --porcelain` (use `command.NewRealExecutor` + `command.GitWorktreeList()`)
   - Parse worktrees using `git.ParseWorktreeListOutput` (extracted to `internal/git/` in Phase 1)
   - For each non-main, non-detached worktree with a branch:
     - Compute state key
     - Skip if `SuppressAutoArchive` is set in state
     - Skip if already archived in state
     - Check PR state via `github.GetPRForBranch(ctx, wt.Branch)`
     - If PR state is `github.StateMerged` or `github.StateClosed`:
       - Check dirty: `repo.IsWorktreeDirty(wt.Path)` — if dirty, print warning to stderr and skip
       - Call `state.PerformArchive(executor, stateStore, key, ws)` — the shared function (from Phase 1) handles write-state-first → remove worktree → delete branch
       - Print notice to stderr: `"Auto-archived %s (PR #%d %s)\n"`
   - Touch throttle file
   - Return nil (individual branch errors are non-fatal, print to stderr)

3. [ ] `parseWorktreesFromOutput` was extracted to `internal/git/` in Phase 1. Use `git.ParseWorktreeListOutput` directly.

4. [ ] Add tests (use mocked executor, gh functions, and state store):
   - `TestRunExpensive_SkipsWhenThrottled` — throttle file is fresh, verify no gh calls
   - `TestRunExpensive_SkipsWhenGHUnavailable` — gh not available, verify no errors
   - `TestRunExpensive_AutoArchivesMergedPR` — mock PR as MERGED, verify state written + git operations
   - `TestRunExpensive_AutoArchivesClosedPR` — mock PR as CLOSED, verify same behavior
   - `TestRunExpensive_SkipsDirtyWorktree` — mock dirty check true, verify skipped with warning
   - `TestRunExpensive_SkipsSuppressedBranches` — SuppressAutoArchive set, verify skipped
   - `TestRunExpensive_TouchesThrottleFile` — verify file is updated after run
   - `TestRunExpensive_PerRepoThrottleIsolation` — two repo keys, verify separate throttle files

**Verify:**
```bash
go tool task test
# Expected: all maintenance tests pass
```

### Task 3: Wire maintenance into the root command

**Context:** `cmd/wtp/app.go`

**Files:**
- Modify: `cmd/wtp/app.go` (add `Before` hook)
- Create: `cmd/wtp/maintenance.go` (maintenance entry point for the command layer)
- Create: `cmd/wtp/maintenance_test.go` (test the wiring)

**Steps:**

1. [ ] Create `cmd/wtp/maintenance.go` with a `runMaintenance(ctx context.Context, w io.Writer) error` function:
   - Get cwd, create `git.Repository`, get main worktree path
   - Get remote URL, parse repo ID — if any of these fail (not in a git repo, no remote), return nil silently
   - Load global config
   - Create `maintenance.NewRunner(stateStore, cfg, repoID, mainRepoPath, w)`
   - Run `runner.RunCheap()` — always
   - Run `runner.RunExpensive(ctx)` — always (throttle check is inside)
   - Return nil (maintenance errors are non-fatal)

2. [ ] Add `Before` hook to `newApp()` in `app.go`. Maintenance errors must be **non-fatal** — the user's command must always proceed:
   ```go
   Before: func(ctx context.Context, cmd *cli.Command) error {
       // Maintenance is best-effort; errors are logged to stderr but never block the user's command.
       _ = runMaintenance(ctx, os.Stderr)
       return nil
   },
   ```

3. [ ] Add tests:
   - `TestRunMaintenance_SilentlySkipsNonGitDir` — cwd is not a git repo, verify no error
   - `TestRunMaintenance_SilentlySkipsNoRemote` — git repo but no origin remote, verify no error

**Verify:**
```bash
go tool task test
go tool task test-e2e
# Expected: all tests pass, existing e2e tests unaffected
```

<!-- Review notes: devils-advocate review confirmed throttle isolation, dirty-worktree guard for auto-archive, and parseWorktreesFromOutput extraction need. -->
