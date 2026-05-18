# Phase 2: Core Commands

> **Status:** PENDING
> **Depends on:** Phase 1
> **Blocks:** Phase 3

## Specification

**Problem:** The existing `wtp add`, `remove`, `cd`, `exec`, and `init` commands use repo-relative storage (`base_dir`) and the managed/unmanaged worktree filter. They need to be updated for centralized XDG storage, branch-name-based worktree resolution, and state file integration.

**Goal:** All core commands work with centralized storage. `wtp add` creates worktrees under `$XDG_DATA_HOME/wtp/worktrees/<owner>/<repo>/<branch>`. All commands resolve worktrees by branch name. `wtp remove` cleans up state/cache entries. Detached HEAD support is removed from `wtp add`.

**Success Criteria:**
- [ ] `wtp add <branch>` creates worktrees at `$XDG_DATA_HOME/wtp/worktrees/<owner>/<repo>/<branch>`
- [ ] Detached HEAD worktrees are skipped in discovery (shown as `(detached)` in list but not addressable)
- [ ] `wtp cd <branch>` navigates to any discovered worktree by branch name
- [ ] `wtp exec <branch> -- <cmd>` runs commands in any discovered worktree
- [ ] `wtp remove <branch>` removes any discovered worktree + state/cache cleanup
- [ ] `wtp init` scaffolds hooks-only `.wtp.yml` (no `version`, no `base_dir`)
- [ ] Per-repo config loading simplified: hooks only, no `version`/`base_dir` fields
- [ ] Tab completion uses branch names for all commands
- [ ] `isWorktreeManagedCommon` filter eliminated — all worktrees are first-class

## Context Loading

```bash
read cmd/wtp/add.go
read cmd/wtp/remove.go
read cmd/wtp/cd.go
read cmd/wtp/exec.go
read cmd/wtp/init.go
read cmd/wtp/worktree_resolver.go
read cmd/wtp/worktree_managed.go
read internal/config/config.go
read internal/git/worktree.go
```

## Tasks

### Task 1: Simplify per-repo config and eliminate managed/unmanaged filter

**Context:** `internal/config/config.go`, `cmd/wtp/worktree_managed.go`, `cmd/wtp/worktree_resolver.go`

**Files:**
- Modify: `internal/config/config.go` — remove `Version`, `Defaults`, `BaseDir`, `ResolveWorktreePath`; keep `Config` with hooks only
- Delete: `cmd/wtp/worktree_managed.go` — `isWorktreeManagedCommon` no longer needed
- Modify: `cmd/wtp/worktree_resolver.go` — rewrite resolution to use branch names, remove managed filter
- Modify: `internal/git/worktree.go` — update `Worktree` struct if needed for branch-based naming
- Update: corresponding test files

**Steps:**

1. [ ] Simplify `internal/config/config.go`:
   - Remove `Version string` field from `Config`
   - Remove `Defaults` struct and `Defaults` field from `Config`
   - Remove `ResolveWorktreePath` method
   - Remove `DefaultBaseDir` constant
   - Keep: `Config{Hooks}`, `Hooks{PostCreate}`, `Hook{Type,From,To,Command,Env,WorkDir}`
   - Keep: `LoadConfig`, `SaveConfig`, `Validate`, `HasHooks`, `ApplyDefaults` (simplified — only sets hook defaults if needed)
   - Update `LoadConfig` to handle both old format (gracefully ignore `version`/`defaults` if present) and new format

2. [ ] Delete `cmd/wtp/worktree_managed.go` entirely. Note: this will cause `list.go` to not compile (it references `isWorktreeManagedList`). This is intentional — Phase 3 rewrites `list.go` entirely. Phase 2 verify steps use `-run` flags to skip list tests.

3. [ ] Rewrite `cmd/wtp/worktree_resolver.go` — all call sites must be updated for the new signature:
   - `cd.go` (current call at ~line 95)
   - `exec.go` (current call at ~line 75)
   - `cd_test.go` (3 call sites)
   - `remove.go` — `findTargetWorktreeFromList` replaced by this function (Phase 2 Task 3)
   New signature and logic:
   - `resolveWorktreePathByName(name string, worktrees []git.Worktree) (string, error)`:
     - If `name` is `@` or `root` → return main worktree path (where `wt.IsMain == true`)
     - Otherwise → find worktree where `wt.Branch == name`
     - No managed/unmanaged filtering — all worktrees participate
     - Error if no match found (with available branch names listed)
   - `availableWorktreeNames(worktrees []git.Worktree) []string` — returns all branch names + `@` for main
   - Remove `tryDirectWorktreeMatches`, `tryMainWorktreeMatches`, `findMainWorktreePath` (the logic is now inline and simpler)

4. [ ] Update `internal/git/worktree.go`:
   - Ensure `Name()` method returns branch name (currently returns `filepath.Base(Path)` — change to return `Branch`)
   - Keep `CompletionName` but simplify: return branch name, or `@` for main worktree

5. [ ] Update all tests in `cmd/wtp/worktree_resolver_test.go` to test branch-name resolution
6. [ ] Update `internal/config/config_test.go` for simplified config struct
7. [ ] Delete `cmd/wtp/worktree_managed_test.go` if it exists

**Verify:**
```bash
go test ./internal/config/ ./cmd/wtp/ -v -run "TestResolver|TestConfig"
```

---

### Task 2: Update `wtp add` for centralized storage

**Context:** `cmd/wtp/add.go`, `internal/remote/parse.go` (from Phase 1), `internal/xdg/xdg.go` (from Phase 1)

**Files:**
- Modify: `cmd/wtp/add.go` — new path resolution using XDG + remote URL
- Update: `cmd/wtp/add_test.go`

**Steps:**

1. [ ] Rewrite `resolveWorktreePath` in `add.go`:
   - Get remote origin URL via `git remote get-url origin` (add method to `Repository` or use `ExecuteGitCommand`)
   - Parse with `remote.Parse(url)` to get `RepoIdentifier`
   - Compute worktree path: `filepath.Join(xdg.WorktreeStorageRoot(), repoID.StoragePath(), branchName)`
   - Call `xdg.EnsureDir` on the parent directory
   - Error if `origin` remote doesn't exist: `"no 'origin' remote found; set one with 'git remote add origin <url>'"`
   - Error if URL can't be parsed: include the raw URL and suggest filing a bug

2. [ ] Ensure `buildWorktreeCommand` does not support detached mode. Note: no `--detach` flag exists in current production code. This step is a verification — check that `GitWorktreeAddOptions` doesn't have a `Detach` field being used, and that test fixtures don't rely on detach behavior.

3. [ ] Update `setupRepoAndConfig`:
   - Config loading should use the simplified config (hooks only)
   - Remove `base_dir` resolution logic
   - Keep hook loading from per-repo `.wtp.yml`
   - Call `config.EnsureGlobalConfig()` during setup (creates global config on first `wtp add` if it doesn't exist, per spec)

4. [ ] Update `displaySuccessMessage` to show branch name instead of path-based name

5. [ ] Add `GetRemoteURL(remoteName string) (string, error)` method to `internal/git/repository.go`:
   - Runs `git remote get-url <remoteName>`
   - Returns URL string or error

6. [ ] Update tests in `cmd/wtp/add_test.go`:
   - Mock executor to capture `git worktree add` calls and verify paths are under XDG storage
   - Test error case: no `origin` remote
   - Test error case: unparseable URL
   - All tests should set `XDG_DATA_HOME` to `t.TempDir()`
   - **Hook path resolution test:** verify that copy/symlink hooks work when worktree is under centralized XDG path (source resolves from main worktree at `repoRoot`, destination is under `$XDG_DATA_HOME/wtp/worktrees/owner/repo/branch`). This is critical — the worktree is now in a completely different directory tree from the source repo.

**Verify:**
```bash
go test ./cmd/wtp/ -v -run TestAdd
go test ./internal/git/ -v -run TestGetRemoteURL
```

---

### Task 3: Update `wtp remove`, `cd`, `exec` for branch-name resolution

**Context:** `cmd/wtp/remove.go`, `cmd/wtp/cd.go`, `cmd/wtp/exec.go`, `cmd/wtp/worktree_resolver.go` (updated in Task 1)

**Files:**
- Modify: `cmd/wtp/remove.go` — use branch-name resolution, add state/cache cleanup
- Modify: `cmd/wtp/cd.go` — use branch-name resolution, remove managed filter
- Modify: `cmd/wtp/exec.go` — use branch-name resolution
- Update: corresponding test files

**Steps:**

1. [ ] Update `cmd/wtp/remove.go`:
   - Replace `findTargetWorktreeFromList` to use `resolveWorktreePathByName` (branch name lookup)
   - Remove `isWorktreeManaged` check — all discovered worktrees can be removed
   - After `git worktree remove` succeeds:
     - Get `RepoIdentifier` from remote origin
     - Compute state key via `repoID.StateKey(worktree.Branch)`
     - Call `state.Store.WithLock` to remove the archive entry (best-effort, don't error if missing)
     - Call `cache.Store.Delete(key)` (best-effort)
   - Update `getWorktreeNameFromPath` → simplify to return `worktree.Branch`
   - Update `completeWorktrees` → return branch names for all non-main worktrees

2. [ ] Update `cmd/wtp/cd.go`:
   - Replace path-based resolution with `resolveWorktreePathByName`
   - Remove `isWorktreeManagedCommon` call in `writeManagedWorktreesForCd`
   - `completeWorktreesForCd` → return `@` for main, branch names for all others
   - Remove `getWorktreeNameFromPathCd` — no longer needed

3. [ ] Update `cmd/wtp/exec.go`:
   - Replace path-based resolution with `resolveWorktreePathByName`
   - `completeWorktreesForExec` → delegate to updated cd completion

4. [ ] Update all test files to use branch names instead of path-based names

**Verify:**
```bash
go test ./cmd/wtp/ -v -run "TestRemove|TestCd|TestExec"
```

---

### Task 4: Update `wtp init` and per-repo config template

**Context:** `cmd/wtp/init.go`

**Files:**
- Modify: `cmd/wtp/init.go` — new template without `version`/`base_dir`
- Update: `cmd/wtp/init_test.go`

**Steps:**

1. [ ] Update the `.wtp.yml` template in `init.go` to:
   ```yaml
   hooks:
     post_create:
       # - type: copy
       #   from: .env
       #   to: .env
       # - type: symlink
       #   from: .bin
       #   to: .bin
       # - type: command
       #   command: npm install
   ```
   Remove all lines related to `version` and `defaults`/`base_dir`.

2. [ ] Update tests to verify new template format

**Verify:**
```bash
go test ./cmd/wtp/ -v -run TestInit
```
