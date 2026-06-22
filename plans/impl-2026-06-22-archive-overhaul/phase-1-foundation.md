# Phase 1: Foundation

> **Spec:** `plans/design-2026-06-22-archive-overhaul.md`

## Specification

**Problem:** The state schema only tracks `Archived bool`. The config lacks retention/interval fields. The github package doesn't export `StateClosed`. The git package lacks helpers for SHA verification and dirty-worktree detection.

**Goal:** All foundation types and helpers are in place for phases 2-4 to build on without touching shared packages.

**Scope:** `internal/state`, `internal/config`, `internal/github`, `internal/git`, `internal/command`. No command-level changes.

**Success Criteria:**

- [ ] `WorktreeState` has all new fields and round-trips through JSON correctly
- [ ] Legacy state entries (archived=true, no SHA) are detectable
- [ ] `GlobalConfig` has `ArchiveRetention` and `MaintenanceInterval` with defaults
- [ ] `StateClosed` is exported from the github package
- [ ] Git helpers exist for: SHA verification, dirty-worktree check, branch existence check, unpushed commits check
- [ ] All existing tests pass

## Context Loading

_Run before starting:_

```bash
read internal/state/state.go
read internal/state/state_test.go
read internal/config/global.go
read internal/config/global_test.go
read internal/github/github.go
read internal/git/repository.go
read internal/command/builders.go
```

## State Schema Tasks

### Task 1: Extend WorktreeState schema

**Context:** `internal/state/`

**Files:**
- Modify: `internal/state/state.go` (extend `WorktreeState` struct)
- Modify: `internal/state/state_test.go` (add round-trip tests for new fields)

**Steps:**

1. [ ] Add new fields to `WorktreeState`:
   ```go
   type WorktreeState struct {
       Archived            bool      `json:"archived"`
       ArchivedAt          time.Time `json:"archived_at,omitempty"`
       PRClosedAt          time.Time `json:"pr_closed_at,omitempty"`
       CommitSHA           string    `json:"commit_sha,omitempty"`
       Branch              string    `json:"branch,omitempty"`
       WorktreePath        string    `json:"worktree_path,omitempty"`
       SuppressAutoArchive bool      `json:"suppress_auto_archive,omitempty"`
   }
   ```
   Add `import "time"` to the import list.

2. [ ] Add helper method `IsLegacy() bool` on `WorktreeState` — returns `true` when `Archived` is true but `CommitSHA` is empty (old-format entry with no recovery metadata).

3. [ ] Add helper method `ExpirationTime() time.Time` on `WorktreeState` — returns `PRClosedAt` if non-zero, else `ArchivedAt`. Returns zero time if neither is set (legacy entries).

4. [ ] Add test `TestWorktreeState_JSONRoundTrip` — creates a `WorktreeState` with all fields populated, marshals to JSON, unmarshals back, asserts equality. Verify `omitempty` works: a struct with only `Archived: true` should produce minimal JSON.

5. [ ] Add test `TestWorktreeState_IsLegacy` — verify `IsLegacy()` returns true for `{Archived: true}` and false for `{Archived: true, CommitSHA: "abc123"}`.

6. [ ] Add test `TestWorktreeState_ExpirationTime` — verify precedence: `PRClosedAt` wins when set; falls back to `ArchivedAt`; returns zero when neither set.

**Verify:**
```bash
go tool task test
# Expected: all state tests pass
```

## Config Tasks

### Task 2: Extend GlobalConfig with retention and interval

**Context:** `internal/config/`

**Files:**
- Modify: `internal/config/global.go` (add fields, defaults, YAML handling)
- Modify: `internal/config/global_test.go` (add parsing tests)

**Steps:**

1. [ ] Add constants:
   ```go
   DefaultArchiveRetention    = 240 * time.Hour  // 10 days
   DefaultMaintenanceInterval = 10 * time.Minute
   ```

2. [ ] Add fields to `GlobalConfig`:
   ```go
   ArchiveRetention    time.Duration `yaml:"archive_retention"`
   MaintenanceInterval time.Duration `yaml:"maintenance_interval"`
   ```

3. [ ] Update `MarshalYAML` to include the two new fields as duration strings (same pattern as `CacheTTL`).

4. [ ] Update `UnmarshalYAML` to parse both new fields — same pattern as `CacheTTL`: try `time.ParseDuration` first, fall back to integer seconds. Use defaults when fields are absent.

5. [ ] Update `LoadGlobalConfig` default return to include the new defaults.

6. [ ] Update `EnsureGlobalConfig` default to include the new defaults.

7. [ ] Add test cases to the existing config tests:
   - Parse `archive_retention: "240h"` → 240h
   - Parse `archive_retention: 864000` → 240h (integer seconds)
   - Parse `maintenance_interval: "10m"` → 10m
   - Absent fields → defaults (240h, 10m)

**Verify:**
```bash
go tool task test
# Expected: all config tests pass
```

## GitHub Package Tasks

### Task 3: Export StateClosed constant

**Context:** `internal/github/`

**Files:**
- Modify: `internal/github/github.go` (export constant)
- Modify: `internal/github/github_test.go` (add assertion if relevant)

**Steps:**

1. [ ] Change `stateClosed = "CLOSED"` to `StateClosed = "CLOSED"` (export it, same pattern as `StateMerged`).

2. [ ] Update any internal references from `stateClosed` to `StateClosed`.

3. [ ] Verify no compilation errors.

**Verify:**
```bash
go tool task test
# Expected: all github tests pass
```

## Git Helper Tasks

### Task 4: Add git helper methods for archive operations

**Context:** `internal/git/`, `internal/command/`

**Files:**
- Modify: `internal/git/repository.go` (add new methods)
- Modify: `internal/git/repository_test.go` (add unit tests)
- Modify: `internal/command/builders.go` (add new command builders)

**Steps:**

1. [ ] Add `CommitExists(sha string) (bool, error)` method to `Repository` — runs `git cat-file -t <sha>`, returns true if exit code 0, false if exit code 1 (not found), error otherwise. Validate sha input (no newlines, no `..`).

2. [ ] Add `IsWorktreeDirty(worktreePath string) (bool, error)` method to `Repository` — runs `git -C <worktreePath> status --porcelain` and returns true if output is non-empty. This checks for both staged and unstaged changes.

3. [ ] Add `HasUnpushedCommits(branch string) (bool, error)` method to `Repository` — runs `git log <branch>@{u}..<branch> --oneline` (the `@{u}` syntax resolves the upstream from git config regardless of which worktree is checked out). Returns false (no unpushed) if the command succeeds with empty output. Returns false if the command fails due to no upstream (look for "no upstream configured" or "no such branch" in stderr). Returns true if output is non-empty.

4. [ ] Add command builder `GitBranchForceDelete(branchName string) Command` in `builders.go` — convenience wrapper that calls `GitBranchDelete(branchName, true)`. This makes the archive code's intent clearer.

5. [ ] Add state helper methods that Phase 2 and Phase 3 will use:
   - `SetArchivedFull(key string, ws WorktreeState) error` on `state.Store` — uses `WithLock` to write the complete `WorktreeState` (not just the `Archived` bool)
   - `ClearArchived(key string) error` on `state.Store` — uses `WithLock` to set `Archived = false`, clear `CommitSHA`, `ArchivedAt`, `PRClosedAt`, `WorktreePath`, set `SuppressAutoArchive = true`, keep `Branch`
   - Add tests for both: round-trip verify fields are set/cleared correctly

6. [ ] Extract `parseWorktreesFromOutput` from `cmd/wtp/list.go` to `internal/git/` as `ParseWorktreeListOutput(output string) []Worktree`. Update all callers in `cmd/wtp/` (`list.go`, `archive.go`, `remove.go`, `cd.go`, `doctor.go`, `exec.go` and their tests). This avoids an import cycle when the maintenance package needs to parse worktree output.

7. [ ] Create shared `PerformArchive` function in `internal/state/archive.go`:
   ```go
   // PerformArchive executes the write-first archive sequence: record metadata in state,
   // then remove worktree, then delete branch. Used by both manual archive and auto-archive.
   func PerformArchive(executor command.Executor, stateStore *Store, key string, ws WorktreeState) error
   ```
   This function: calls `stateStore.SetArchivedFull`, runs `git worktree remove` (skip if already gone), runs `git branch -D` (skip if already gone). Returns nil on success, warns on partial failure. Both Phase 2's `archiveCommandCore` and Phase 3's `RunExpensive` will call this instead of duplicating the logic.

8. [ ] Add tests for `CommitExists`: test with a real commit SHA (true), test with a fake SHA (false).

9. [ ] Add tests for `IsWorktreeDirty`: test with a clean worktree (false), test after creating an untracked file (true).

10. [ ] Add tests for `HasUnpushedCommits`: test with no upstream (false — skip check), test with upstream and no unpushed (false), test with unpushed commits (true).

**Verify:**
```bash
go tool task test
# Expected: all git tests pass
```

<!-- Review notes: devils-advocate review verified schema migration story, config defaults, and git helper completeness. -->
