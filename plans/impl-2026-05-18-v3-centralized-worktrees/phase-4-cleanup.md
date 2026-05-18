# Phase 4: Module Path, Cleanup, Final Integration

> **Status:** PENDING
> **Depends on:** Phase 3

## Specification

**Problem:** After Phases 2 and 3, the codebase has v3 functionality but still uses the v2 module path. Dead code from v2 patterns (managed/unmanaged checks, old config fields) may remain. E2E tests need updating for the new storage model and new commands.

**Goal:** Clean v3 module path, no dead code, E2E tests covering the full v3 workflow, all checks passing.

**Success Criteria:**
- [ ] Module path updated to `github.com/satococoa/wtp/v3`
- [ ] No dead code from v2 patterns
- [ ] E2E tests cover: centralized storage, archive/unarchive, PR/CI display, doctor
- [ ] `go tool task dev` passes (build + test + lint + fmt)
- [ ] Shell hooks and completion still work

## Context Loading

```bash
read go.mod
read cmd/wtp/app.go
read test/e2e/framework/framework.go
```

## Tasks

### Task 1: Module path update to v3

**Context:** `go.mod`, all `.go` files with imports

**Files:**
- Modify: `go.mod` — change module path
- Modify: all files with `github.com/satococoa/wtp/v2` imports

**Steps:**

1. [ ] Update `go.mod` module path from `github.com/satococoa/wtp/v2` to `github.com/satococoa/wtp/v3`
2. [ ] Find and replace all import paths across the entire codebase using `sed` or AST-aware tooling:
   - `github.com/satococoa/wtp/v2` → `github.com/satococoa/wtp/v3` (covers all subpackage imports)
   - Verify no string literals or comments contain the old path
3. [ ] Run `go mod tidy` to clean up
4. [ ] Update `.goreleaser.yml` if it references the module path

**Verify:**
```bash
go build ./...
go test ./... -count=1
```

---

### Task 2: Dead code removal and cleanup

**Context:** Entire `cmd/wtp/` and `internal/` directories

**Files:**
- Remove: `cmd/wtp/worktree_managed.go` (if not already deleted in Phase 2)
- Remove: `cmd/wtp/worktree_managed_test.go` (if exists)
- Modify: any files with leftover v2 patterns

**Steps:**

1. [ ] Verify `cmd/wtp/worktree_managed.go` is deleted
2. [ ] Search for any remaining references to:
   - `isWorktreeManagedCommon`
   - `isWorktreeManaged` (the remove.go variant)
   - `isWorktreeManagedList`
   - `ResolveWorktreePath` (old config method)
   - `DefaultBaseDir`
   - `Defaults` struct
   - `Version` field on `Config`
   - `base_dir` in YAML tags
   - `detach` flag references
3. [ ] Remove any found dead code
4. [ ] Run `go vet ./...` to catch unused imports/variables
5. [ ] Run `go tool task lint` to catch style issues
6. [ ] Run `go tool task fmt` to fix formatting

**Verify:**
```bash
go vet ./...
go tool task lint
go tool task fmt
```

---

### Task 3: E2E test updates

**Context:** `test/e2e/`, `test/e2e/framework/`

**Files:**
- Modify: `test/e2e/framework/framework.go` — update for XDG env vars
- Modify: `test/e2e/worktree_test.go` — update for centralized storage paths
- Modify: `test/e2e/worktree_creation_test.go` — update path expectations
- Modify: `test/e2e/integration_test.go` — remove/update `TestConfigBaseDirIntegration`
- Modify: `test/e2e/basic_test.go` — verify commands work with centralized storage
- Modify: `test/e2e/remote_test.go` — update for new origin-based path derivation
- Modify: `test/e2e/error_test.go` — update error messages for new error cases
- Modify: `test/e2e/shell_test.go` — verify completion outputs branch names
- Modify: `test/e2e/hook_streaming_test.go` — verify hooks work with centralized paths
- Create: `test/e2e/archive_test.go` — archive/unarchive E2E tests
- Create: `test/e2e/doctor_test.go` — doctor E2E tests

**Steps:**

1. [ ] Update `test/e2e/framework/framework.go`:
   - Set `XDG_DATA_HOME`, `XDG_CONFIG_HOME`, `XDG_CACHE_HOME` to temp directories in test environment
   - Add assertion helpers: `AssertWorktreeArchived`, `AssertWorktreeNotArchived`
   - Update `AssertWorktreeCreated` to check centralized storage path
   - Add `SetupGitRemote(url string)` helper to configure origin remote in test repos

2. [ ] Update existing E2E tests:
   - `worktree_test.go`: verify worktrees created under `$XDG_DATA_HOME/wtp/worktrees/<owner>/<repo>/`
   - `worktree_creation_test.go`: update path expectations for centralized storage
   - `integration_test.go`: remove `TestConfigBaseDirIntegration` (no more `base_dir`), add test for hooks-only config
   - All tests that create worktrees: ensure test repos have an `origin` remote configured

3. [ ] Create `test/e2e/archive_test.go`:
   - `TestArchiveWorkflow`: create worktree → archive → verify hidden from `wtp list` → verify shown with `--all` → unarchive → verify shown in `wtp list`
   - `TestArchiveMainWorktree`: attempt to archive `@` → error
   - `TestArchiveNonexistent`: attempt to archive unknown branch → error

4. [ ] Create `test/e2e/doctor_test.go`:
   - `TestDoctorCleanState`: no issues reported
   - `TestDoctorGhStatus`: verify `gh` availability check output

5. [ ] Run full E2E suite

**Verify:**
```bash
go tool task test-e2e
```

---

### Task 4: Final integration check

**Context:** Entire project

**Files:** None (verification only)

**Steps:**

1. [ ] Run `go tool task dev` (build + test + lint + fmt)
2. [ ] Manually verify shell hooks still work:
   - `wtp shell-init bash` outputs valid script
   - `wtp shell-init zsh` outputs valid script
   - `wtp shell-init fish` outputs valid script
3. [ ] Verify `wtp --help` shows all commands including `archive`, `unarchive`, `doctor`
4. [ ] Verify `wtp --version` works

**Verify:**
```bash
go tool task dev
./wtp --help
./wtp shell-init bash | head -5
./wtp shell-init zsh | head -5
```
