# Phase 4: List Cleanup

> **Spec:** `plans/design-2026-06-22-archive-overhaul.md`
> **Depends on:** Phase 3 (maintenance system must be operational before removing auto-archive from list)

## Specification

**Problem:** `wtp list` currently owns auto-archive logic (detecting merged PRs and archiving branches). This responsibility now belongs to the maintenance system. Additionally, `--all` synthesizes archived entries from `git worktree list`, which no longer contains them.

**Goal:** `wtp list` focuses purely on display. Auto-archive code is removed. `--all` synthesizes archived entries from state.json.

**Scope:** `cmd/wtp/list.go`, `cmd/wtp/list_test.go`, `test/e2e/archive_test.go`.

**Success Criteria:**

- [ ] `wtp list` no longer auto-archives branches (no calls to `autoArchiveBranch` or `stateStore.SetArchived`)
- [ ] `wtp list` still fetches and displays PR/CI data
- [ ] `wtp list --all` shows archived entries from state.json with "(archived)" label
- [ ] `wtp list --quiet --all` includes archived branch names as bare names
- [ ] `--no-sync` still skips gh calls for display
- [ ] All existing list tests updated and passing

## Context Loading

_Run before starting:_

```bash
read cmd/wtp/list.go
read cmd/wtp/list_test.go
read internal/state/state.go
read internal/remote/parse.go
read test/e2e/archive_test.go
```

## List Cleanup Tasks

### Task 1: Remove auto-archive from list

**Context:** `cmd/wtp/list.go`

**Files:**
- Modify: `cmd/wtp/list.go` (remove auto-archive logic)
- Modify: `cmd/wtp/list_test.go` (update tests)

**Steps:**

1. [ ] In `fetchPRCIForBranch`: remove the `toArchive` logic and the call to `autoArchiveBranch`. The function should still fetch PR/CI data and update `prciData` and `newEntries`, but never set `archivedBranches[branch] = true` or call `autoArchiveBranch`.

2. [ ] In `handleCachedPRCI`: remove the auto-archive block that checks `cached.PRState == github.StateMerged` and sets `archivedBranches`. Keep the prciData update.

3. [ ] In `updateSharedWithFreshData`: remove the auto-archive block that checks `pr.State == github.StateMerged`. Keep the cache entry write and prciData update. Remove the `archiveRequest` return — function should return nothing or just the prciData update.

4. [ ] Remove the `autoArchiveBranch` function entirely.

5. [ ] Remove the `archiveRequest` struct.

6. [ ] In `listCommandWithCommandExecutor`: remove the "Rebuild displayWorktrees after auto-archiving" block (lines 210-219 in current code). The `fetchPRCIData` no longer modifies `archivedBranches` during execution.

7. [ ] Clean up the initial `archivedBranches` build block (current `list.go:179-190`). Post-overhaul, archived branches no longer appear in `git worktree list`, so cross-referencing worktrees against state to find archived branches is dead code. Remove this block. The `archivedBranches` map is now only populated by the `--all` synthesis in Task 2 and used purely for display labeling.

8. [ ] Update function signatures as needed after removing the archive-related parameters.

9. [ ] Update tests: remove any test assertions that check for auto-archive behavior in list. Keep tests for PR/CI display, `--no-sync`, `--quiet`, etc.

**Verify:**
```bash
go tool task test
# Expected: all list tests pass, no auto-archive behavior in list
```

### Task 2: Synthesize archived entries for --all from state.json

**Context:** `cmd/wtp/list.go`

**Files:**
- Modify: `cmd/wtp/list.go` (add state-based synthesis for `--all`)
- Modify: `cmd/wtp/list_test.go` (add tests for new `--all` behavior)

**Steps:**

1. [ ] In `listCommandWithCommandExecutor`, after building `displayWorktrees` and loading state:
   - If `opts.ShowAll` and `repoID != nil`:
     - Load state: `st, _ := stateStore.Load()`
     - For each entry in `st.Worktrees` where `entry.Archived == true`:
       - Extract repo path and branch from the key using `remote.ParseStateKey(key)`
       - If repo path matches `repoID.StoragePath()`:
         - Create a synthetic `git.Worktree{Branch: entry.Branch, HEAD: entry.CommitSHA}` and append to `displayWorktrees`
         - Add branch to `archivedBranches` map for display labeling

2. [ ] In `displayWorktreesQuiet`: when `--all` is active (pass through opts or use archivedBranches), include archived branches as bare names — they are valid arguments to `wtp unarchive`.

3. [ ] In `buildListRows`: the existing `archivedBranches` check already adds "(archived)" label. Synthetic entries from state will have their branch in `archivedBranches`, so the label is applied automatically. For synthetic entries, HEAD will be the SHA from state (may be empty for legacy entries — display as "-" or empty).

4. [ ] Add tests:
   - `TestListAll_ShowsArchivedFromState` — state has archived entries for current repo, `--all` includes them with "(archived)" label
   - `TestListAll_QuietIncludesArchivedBareNames` — `--quiet --all` outputs archived branch names without suffix
   - `TestListAll_IgnoresOtherRepos` — state has entries for different repos, only current repo entries shown
   - `TestListAll_NoState` — no archived entries in state, `--all` behaves like regular list (no crash)

**Verify:**
```bash
go tool task test
# Expected: all list tests pass
```

### Task 3: Update e2e tests for list + archive integration

**Context:** `test/e2e/`

**Files:**
- Modify: `test/e2e/archive_test.go` (update archive workflow test)

**Steps:**

1. [ ] Update `TestArchiveWorkflow` (if not already updated in Phase 2):
   - After archiving, verify `wtp list --all --no-sync` shows the branch with "(archived)"
   - After unarchiving, verify `wtp list --no-sync` shows the branch without "(archived)"

2. [ ] Add `TestListAllShowsArchivedEntries` — archive a branch, verify `wtp list --all --no-sync` includes it, verify `wtp list --no-sync` (without `--all`) does NOT include it.

3. [ ] Add `TestListQuietAllShowsArchivedBranches` — archive a branch, verify `wtp list --quiet --all --no-sync` includes the bare branch name.

**Verify:**
```bash
go tool task test-e2e
# Expected: all e2e tests pass
```

<!-- Review notes: devils-advocate review confirmed auto-archive removal is safe since maintenance handles it, and --all synthesis from state.json is the correct approach. -->
