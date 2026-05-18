# Phase 3: GitHub Integration + Archive

> **Status:** PENDING
> **Depends on:** Phases 1 & 2

## Specification

**Problem:** `wtp list` shows no GitHub context (PR state, CI status). There's no way to hide finished worktrees from the list. Merged PRs leave stale worktrees cluttering the list.

**Goal:** `wtp list` shows compact single-line PR/CI status via `gh` CLI with TTL caching. `wtp archive`/`wtp unarchive` commands hide worktrees. Merged PRs are auto-archived during `wtp list` polling. `wtp doctor` helps diagnose issues.

**Success Criteria:**
- [ ] `wtp list` shows PR number, state, and CI status per worktree
- [ ] PR/CI data is cached with configurable TTL (default 60s)
- [ ] `wtp list --no-sync` skips `gh` calls and auto-archive
- [ ] `wtp list --all` shows archived worktrees
- [ ] `wtp list --quiet --no-sync` is side-effect-free
- [ ] `wtp archive <branch>` / `wtp unarchive <branch>` work
- [ ] Merged PRs auto-archived with printed notice
- [ ] Graceful degradation when `gh` is not installed
- [ ] `wtp doctor` detects v2 worktrees, orphaned state, `gh` status

## Context Loading

```bash
read cmd/wtp/list.go
read cmd/wtp/app.go
read internal/state/state.go
read internal/cache/cache.go
```

## Tasks

### Task 1: GitHub CLI wrapper

**Context:** None (new package)

**Files:**
- Create: `internal/github/github.go`
- Create: `internal/github/github_test.go`

**Steps:**

1. [ ] Create `internal/github/github.go` with:
   - `func IsAvailable() bool` — checks if `gh` is on PATH via `exec.LookPath("gh")`
   - `func IsAuthenticated() (bool, error)` — runs `gh auth status` and checks exit code
   - `type PRInfo struct { Number int; State string; Title string; HeadBranch string; IsDraft bool }`
   - `type CIStatus struct { State string; Total int; Passing int; Failing int; Pending int }`
   - `func GetPRForBranch(branch string) (*PRInfo, error)` — runs `gh pr view <branch> --json number,state,title,headRefName,isDraft` and parses JSON output. Returns `nil, nil` if no PR exists (exit code 1 with "no pull requests found").
   - `func GetCIStatus(branch string) (*CIStatus, error)` — runs `gh pr checks <branch> --json name,state` and aggregates. Returns `nil, nil` if no PR or no checks.
   - `func FormatPRState(pr *PRInfo) string` — returns e.g. `#42 Ready`, `#42 Draft`, `#42 Merged`, `#42 Closed`
   - `func FormatCIStatus(ci *CIStatus) string` — returns e.g. `✓ CI passing`, `✗ 2/5 failing`, `● 3 pending`, `-` if no checks
   - All functions that shell out use `exec.Command("gh", ...)` with a 10-second timeout context

2. [ ] Create `internal/github/github_test.go`:
   - Test `FormatPRState` and `FormatCIStatus` formatting (pure functions, no mocking needed)
   - Test JSON parsing logic with sample `gh` output fixtures
   - Test `GetPRForBranch` returns nil when "no pull requests found" in stderr

**Verify:**
```bash
go test ./internal/github/ -v
```

---

### Task 2: Rewrite `wtp list` with PR/CI status, archive filtering, auto-archive

**Context:** `cmd/wtp/list.go` (this task owns `list.go` exclusively — Phase 2 does not modify it), `internal/github/github.go` (from Task 1), `internal/state/state.go` (from Phase 1), `internal/cache/cache.go` (from Phase 1), `internal/remote/parse.go` (from Phase 1), `internal/config/global.go` (from Phase 1)

**Note:** Phase 2 deleted `worktree_managed.go` which `list.go` references via `isWorktreeManagedList`. This task resolves that compile error by rewriting the file.

**Files:**
- Modify: `cmd/wtp/list.go` — major rewrite of display and data flow
- Update: `cmd/wtp/list_test.go`

**Steps:**

1. [ ] Add new flags to `NewListCommand`:
   - `--all` (bool) — show archived worktrees
   - `--no-sync` (bool) — skip `gh` calls and auto-archive

2. [ ] Rewrite `listCommandWithCommandExecutor` data flow:
   - Discover worktrees via `git worktree list --porcelain` (unchanged)
   - Parse remote origin → `RepoIdentifier` (for state/cache key derivation)
   - Load state (archive metadata)
   - Filter out archived worktrees unless `--all` is set
   - If `gh` is available AND not `--no-sync` AND not `--quiet`:
     - Load cache
     - Load global config for TTL
     - For each non-archived worktree with a branch:
       - Check cache: if fresh (within TTL), use cached data
       - If stale or missing: call `github.GetPRForBranch`, `github.GetCIStatus`
       - Update cache entry
     - Auto-archive: if PR state is `MERGED`, mark worktree as archived in state, print notice: `Auto-archived <branch> (PR #N merged)`
     - Batch-write cache updates (single lock acquisition)
     - Batch-write state updates for auto-archived worktrees (single lock acquisition)
   - If `--quiet`: output branch names only (`@` for main), no mutations regardless of other flags

3. [ ] Update display functions:
   - `displayWorktreesRelative` → new column layout:
     ```
     BRANCH         | PR          | CI           | HEAD
     @ (main)       |             |              | a3f8b2c
     feature/auth   | #42 Ready   | ✓ CI passing | b4c9d3e
     fix/login      | #38 Draft   | ● 2 pending  | c5d0e4f
     old-feature    | (archived)  |              | d6e1f5g   ← only with --all
     ```
   - Archived worktrees shown dimmed/marked with `(archived)` when `--all` is used
   - Detached HEAD worktrees shown with `(detached)` marker in branch column
   - No PR/CI columns if `gh` is unavailable (columns simply absent)
   - `displayWorktreesQuiet` → output `@` for main, branch name for others, one per line

4. [ ] Handle graceful degradation:
   - If `gh` is not installed: print columns without PR/CI, print one-time hint to stderr: `hint: install 'gh' CLI for PR/CI status in wtp list`
   - Track hint display via a simple file flag at `xdg.WtpDataDir()/.gh-hint-shown`
   - If `gh` is installed but not authenticated: similar hint for `gh auth login`

5. [ ] Remove `isWorktreeManagedList` — no longer needed (was left as dead code by Phase 2's `worktree_managed.go` deletion)
6. [ ] Remove `GitRepository` interface — use `git.Repository` directly or simplify the abstraction
7. [ ] **Breaking change note:** `wtp list --quiet` now outputs branch names instead of directory-based names. This is a v3 breaking change. `@` for main worktree, branch name for others (e.g. `feature/auth`), one per line. Detached HEAD worktrees are omitted from quiet output.

7. [ ] Update `cmd/wtp/list_test.go`:
   - Test list without `gh` → no PR/CI columns
   - Test list with cached PR data → displays from cache
   - Test `--all` shows archived worktrees
   - Test `--no-sync` skips GitHub calls
   - Test `--quiet` outputs branch names only
   - Test `--quiet --no-sync` has no side effects
   - Test auto-archive: mock `gh` returning MERGED state, verify state file updated and notice printed
   - Test detached HEAD shown with `(detached)` marker

**Verify:**
```bash
go test ./cmd/wtp/ -v -run TestList
```

---

### Task 3: `wtp archive`, `wtp unarchive` commands

**Context:** `cmd/wtp/app.go`, `internal/state/state.go` (from Phase 1), `internal/remote/parse.go` (from Phase 1), `cmd/wtp/worktree_resolver.go` (from Phase 2)

**Files:**
- Create: `cmd/wtp/archive.go`
- Create: `cmd/wtp/archive_test.go`
- Modify: `cmd/wtp/app.go` — register new commands

**Steps:**

1. [ ] Create `cmd/wtp/archive.go` with:
   - `func NewArchiveCommand() *cli.Command` — `wtp archive <branch>`
     - Discovers worktrees via `git worktree list --porcelain`
     - Resolves `<branch>` to a worktree via `resolveWorktreePathByName`
     - Parses remote origin → `RepoIdentifier`
     - Computes state key: `repoID.StateKey(worktree.Branch)`
     - Calls `state.Store.SetArchived(key, true)`
     - Prints: `Archived <branch>`
     - Error if worktree is already archived
     - Error if trying to archive main worktree (`@`)
     - Tab completion: non-archived, non-main branch names
   - `func NewUnarchiveCommand() *cli.Command` — `wtp unarchive <branch>`
     - Loads state, finds archived entry matching `<branch>`
     - Calls `state.Store.SetArchived(key, false)`
     - Prints: `Unarchived <branch>`
     - Error if worktree is not archived
     - Tab completion: archived branch names only (load state, cross-reference with discovered worktrees)

2. [ ] Register `archive`, `unarchive`, AND `doctor` (from Task 4) commands in `cmd/wtp/app.go` `newApp()` — batched to avoid multiple modifications to this small file

3. [ ] Create `cmd/wtp/archive_test.go`:
   - Test archive sets state flag
   - Test unarchive clears state flag
   - Test archive already-archived → error
   - Test unarchive not-archived → error
   - Test archive main worktree → error
   - Test archive with invalid branch name → helpful error

**Verify:**
```bash
go test ./cmd/wtp/ -v -run "TestArchive|TestUnarchive"
```

---

### Task 4: `wtp doctor` command

**Context:** `cmd/wtp/app.go`, `internal/state/state.go`, `internal/xdg/xdg.go`, `internal/github/github.go`

**Files:**
- Create: `cmd/wtp/doctor.go`
- Create: `cmd/wtp/doctor_test.go`
- Modify: `cmd/wtp/app.go` — register command

**Steps:**

1. [ ] Create `cmd/wtp/doctor.go` with `NewDoctorCommand`:
   - **v2 worktree detection:**
     - Run `git worktree list --porcelain` to get all registered worktrees
     - Check if any worktree paths match common v2 patterns: path contains `/worktrees/` as a segment relative to the repo root, or is a sibling `../worktrees/` directory
     - For each detected v2 worktree, print: `⚠ Possible v2 worktree: <path>` and `  Run: git worktree remove <path>`
   - **Orphaned state entries:**
     - Load `state.json`, for each key check if the corresponding worktree exists (either in `git worktree list` output or on disk at the centralized path)
     - Print: `⚠ Orphaned state entry: <key> (worktree not found on disk)`
     - Suggest: `  Run: wtp doctor --fix to clean up` (future enhancement — for now just report)
   - **Orphaned centralized directories:**
     - Walk `$XDG_DATA_HOME/wtp/worktrees/` filesystem
     - For each leaf directory, check if it's registered in `git worktree list`
     - Print: `⚠ Orphaned directory: <path> (not registered as a git worktree)`
   - **`gh` CLI status:**
     - Check `github.IsAvailable()` → print `✓ gh CLI found` or `✗ gh CLI not found`
     - If available, check `github.IsAuthenticated()` → print `✓ gh authenticated` or `✗ gh not authenticated (run: gh auth login)`
   - **Summary:** print total issues found, or `✓ No issues found`

2. [ ] `app.go` registration is handled in Task 3 (batched). No separate registration step here.

3. [ ] Create `cmd/wtp/doctor_test.go`:
   - Test with clean state → no issues
   - Test with orphaned state entry → detected
   - Test with `gh` not available → reported
   - Tests use `t.TempDir()` for XDG paths

**Verify:**
```bash
go test ./cmd/wtp/ -v -run TestDoctor
```
