# Auto-cd After `wtp add` Implementation Plan

> **Status:** DRAFT

## Specification

**Problem:** After `wtp add`, users must manually run `wtp cd <name>` to switch to the new worktree. This is friction — you create a worktree to work in it, not to admire it from afar.

**Goal:** `wtp add` auto-cds into the new worktree by default. Users who don't want this can pass `--stay`.

**Scope:**
- In scope: `wtp add` emitting a cd marker, `--stay` flag, shell hook updates (bash/zsh/fish), success message changes
- Out of scope: auto-cd for other commands (though the marker mechanism is general-purpose for future use)

**Success Criteria:**
- [ ] `wtp add my-feature` creates the worktree and cds into it (via shell hook)
- [ ] `wtp add my-feature --stay` creates the worktree without cd-ing, shows `wtp cd` hint
- [ ] `wtp add my-feature --exec "npm install"` runs exec in worktree, then cds there
- [ ] `wtp add my-feature --exec "npm install" --stay` runs exec but does not cd
- [ ] `--exec` failure still emits cd marker (worktree exists, user should land there)
- [ ] Shell hooks updated for bash, zsh, and fish
- [ ] Marker mechanism is general-purpose (not add-specific)
- [ ] Non-marker stderr lines still display normally
- [ ] Existing `wtp cd` behavior unchanged
- [ ] Marker only emitted when `__WTP_HOOKED=1` is set (no noise for direct callers)
- [ ] Paths with spaces/special characters handled correctly (double-quoted cd)
- [ ] Marker emission is testable via injected `io.Writer`

## Context Loading

_Run before starting:_

```bash
read cmd/wtp/add.go
read cmd/wtp/hook.go
read cmd/wtp/hook_test.go
read cmd/wtp/add_test.go
read internal/command/builders.go
read plans/design-2026-05-27-auto-cd-after-add.md
```

## Go-side Tasks

### Task 1: Create the marker package

**Context:** `internal/command/` (for package structure reference), `plans/design-2026-05-27-auto-cd-after-add.md`

**Files:**
- Create: `internal/marker/marker.go`
- Create: `internal/marker/marker_test.go`

**Steps:**

1. [ ] Create `internal/marker/marker.go` with:
   ```go
   package marker

   import (
       "fmt"
       "io"
       "os"
   )

   const Prefix = "__wtp_cd:"

   // Emit writes the cd marker to the given writer.
   // It only emits when the __WTP_HOOKED env var is set to "1",
   // preventing confusing output when wtp is called directly (without the shell hook).
   func Emit(w io.Writer, path string) error {
       if os.Getenv("__WTP_HOOKED") != "1" {
           return nil
       }
       _, err := fmt.Fprintf(w, "%s%s\n", Prefix, path)
       return err
   }
   ```

2. [ ] Create `internal/marker/marker_test.go` with tests:
   - `TestEmit_WritesMarkerWhenHooked`: set `__WTP_HOOKED=1` via `t.Setenv`, call `Emit` with a `bytes.Buffer`, assert output is `__wtp_cd:/some/path\n`
   - `TestEmit_NoOutputWhenNotHooked`: don't set env var, call `Emit`, assert buffer is empty
   - `TestEmit_NoOutputWhenHookedIsWrongValue`: set `__WTP_HOOKED=0`, call `Emit`, assert buffer is empty

**Verify:**
```bash
go test ./internal/marker/ -v
# Expected: 3 tests passing
```

### Task 2: Add `--stay` flag and marker emission to `wtp add`

**Context:** `cmd/wtp/add.go`, `cmd/wtp/add_test.go`, `internal/marker/marker.go`

**Files:**
- Modify: `cmd/wtp/add.go`
- Modify: `cmd/wtp/add_test.go`

**Steps:**

1. [ ] In `NewAddCommand()` (line 42-52 of `cmd/wtp/add.go`), add a `--stay` flag to the `Flags` slice:
   ```go
   &cli.BoolFlag{
       Name:  "stay",
       Usage: "Don't change to the new worktree directory after creation",
   },
   ```

2. [ ] In `addCommand()` (line 57), pass `os.Stderr` as the new `errW` parameter when calling `addCommandWithCommandExecutor`:
   ```go
   return addCommandWithCommandExecutor(cmd, fw, os.Stderr, executor, cfg, mainRepoPath, repo.GetRemoteURL)
   ```

3. [ ] Update `addCommandWithCommandExecutor` signature (line 84) to add `errW io.Writer` after `w io.Writer`:
   ```go
   func addCommandWithCommandExecutor(
       cmd *cli.Command,
       w io.Writer,
       errW io.Writer,
       cmdExec command.Executor,
       cfg *config.Config,
       mainRepoPath string,
       getRemoteURL func(string) (string, error),
   ) error {
   ```

4. [ ] After `executePostCreateCommand` (line 138) and before `displaySuccessMessage` (line 142), add marker emission. The marker should be emitted even if `--exec` fails — restructure to:
   ```go
   execErr := executePostCreateCommand(w, cmdExec, cmd.String("exec"), workTreePath)

   // Emit cd marker before handling exec error — the worktree was created successfully,
   // so the user should land there even if --exec failed (to debug it).
   stay := cmd.Bool("stay")
   if !stay {
       if err := marker.Emit(errW, workTreePath); err != nil {
           return err
       }
   }

   if execErr != nil {
       return fmt.Errorf("worktree was created at '%s', but --exec command failed: %w", workTreePath, execErr)
   }
   ```

5. [ ] Update `displaySuccessMessage` and `displaySuccessMessageWithCommitish` to accept a `stay bool` parameter. When `stay` is false, replace the `💡 To switch...` / `wtp cd` hint with `📍 Changed to worktree directory.`. When `stay` is true, keep the existing hint. Update the call at line 142:
   ```go
   if err := displaySuccessMessage(w, branchName, workTreePath, mainRepoPath, stay); err != nil {
       return err
   }
   ```
   The `displaySuccessMessageWithCommitish` function (line 542-585) should change the tail section. When `!stay`:
   ```go
   if _, err := fmt.Fprintln(w, "📍 Changed to worktree directory."); err != nil {
       return err
   }
   ```
   When `stay`, keep the existing lines 568-582.

6. [ ] Add `&cli.BoolFlag{Name: "stay"}` to the `Flags` slice in the `createTestCLICommand` helper (at `add_test.go:617-621`), so test commands can use the `--stay` flag:
   ```go
   Flags: []cli.Flag{
       &cli.BoolFlag{Name: "force"},
       &cli.StringFlag{Name: "branch"},
       &cli.StringFlag{Name: "track"},
       &cli.StringFlag{Name: "exec"},
       &cli.BoolFlag{Name: "stay"},
   },
   ```

7. [ ] Update all 10 test callsites of `addCommandWithCommandExecutor` in `cmd/wtp/add_test.go` to pass a `&bytes.Buffer{}` (or shared `errBuf`) as the new `errW` parameter. The callsites are at lines: 412, 457, 502, 520, 536, 560, 599, 659, 676, 696. Each call like:
   ```go
   err := addCommandWithCommandExecutor(cmd, &buf, mockExec, cfg, "/test/repo", mockGetRemoteURL(...))
   ```
   becomes:
   ```go
   var errBuf bytes.Buffer
   err := addCommandWithCommandExecutor(cmd, &buf, &errBuf, mockExec, cfg, "/test/repo", mockGetRemoteURL(...))
   ```

8. [ ] Update the 3 direct calls to `displaySuccessMessage` in `TestDisplaySuccessMessage_Integration` (`add_test.go:863, 879, 895`) to pass the new `stay` parameter. These existing tests should pass `false` for `stay` (the default behavior), and update assertions accordingly — replace `assert.Contains(t, output, "💡 To switch to the new worktree, run:")` with `assert.Contains(t, output, "📍 Changed to worktree directory.")`, and remove `assert.Contains` for the `wtp cd <name>` line. Also add a new sub-test with `stay=true` that asserts the old `💡 To switch` hint and `wtp cd` line are present.

9. [ ] Update `TestNewAddCommand` (`add_test.go:56`) to include `"stay"` in the `flagNames` slice:
   ```go
   flagNames := []string{"branch", "exec", "stay"}
   ```

10. [ ] Add new tests:
   - `TestAddCommand_EmitsMarkerWhenHooked`: set `t.Setenv("__WTP_HOOKED", "1")`, run add without `--stay`, assert `errBuf` contains `__wtp_cd:<expected_path>`
   - `TestAddCommand_NoMarkerWhenStay`: set `t.Setenv("__WTP_HOOKED", "1")`, set `--stay` flag via `createTestCLICommand(map[string]any{"stay": true, "branch": "test"}, ...)`, run add, assert `errBuf` is empty
   - `TestAddCommand_NoMarkerWhenNotHooked`: set `t.Setenv("__WTP_HOOKED", "")` explicitly (to prevent leakage), run add without `--stay`, assert `errBuf` is empty
   - `TestAddCommand_SuccessMessageShowsCdHint_WhenStay`: set `--stay`, assert stdout contains `wtp cd` and `💡`
   - `TestAddCommand_SuccessMessageShowsChanged_WhenNotStay`: don't set `--stay`, assert stdout contains `📍 Changed to worktree directory.`
   - `TestAddCommand_MarkerEmittedBeforeExecError`: set `t.Setenv("__WTP_HOOKED", "1")`, configure `--exec` to fail, assert `errBuf` contains the marker AND the function returns an error

**Verify:**
```bash
go test ./cmd/wtp/ -run TestAddCommand -v
# Expected: all existing tests still pass + new tests pass
```

## Shell Hook Tasks

### Task 3: Update shell hooks to handle cd marker

**Context:** `cmd/wtp/hook.go`, `cmd/wtp/hook_test.go`

**Files:**
- Modify: `cmd/wtp/hook.go`
- Modify: `cmd/wtp/hook_test.go`

**Steps:**

1. [ ] Update `printBashHook` in `cmd/wtp/hook.go` (line 70-101). Replace the `else` branch (line 95-97) — currently `command wtp "$@"` — with the marker-aware logic. Also update the comment at line 71 from `# wtp cd command hook for bash` to `# wtp shell hook for bash`. The full new `else` block:
   ```bash
   else
       local __wtp_stderr_file
       __wtp_stderr_file=$(mktemp)
       __WTP_HOOKED=1 command wtp "$@" 2>"$__wtp_stderr_file"
       local __wtp_exit=$?
       local __wtp_target=""
       while IFS= read -r __wtp_line; do
           if [[ "$__wtp_line" == __wtp_cd:* ]]; then
               __wtp_target="${__wtp_line#__wtp_cd:}"
           else
               printf '%s\n' "$__wtp_line" >&2
           fi
       done < "$__wtp_stderr_file"
       rm -f "$__wtp_stderr_file"
       if [[ -n "$__wtp_target" && -d "$__wtp_target" ]]; then
           cd "$__wtp_target" || true
       fi
       return $__wtp_exit
   fi
   ```

2. [ ] Update `printZshHook` (line 103-134). Same changes as bash — identical shell code. Update the comment too.

3. [ ] Update `printFishHook` (line 136-167). Replace the `else` branch (line 161-163) with:
   ```fish
   else
       set -l __wtp_stderr_file (mktemp)
       __WTP_HOOKED=1 command wtp $argv 2>"$__wtp_stderr_file"
       set -l __wtp_exit $status
       set -l __wtp_target ""
       while read -l __wtp_line
           if string match -q '__wtp_cd:*' -- $__wtp_line
               set __wtp_target (string replace '__wtp_cd:' '' -- $__wtp_line)
           else
               echo "$__wtp_line" >&2
           end
       end < $__wtp_stderr_file
       rm -f $__wtp_stderr_file
       if test -n "$__wtp_target" -a -d "$__wtp_target"
           cd "$__wtp_target"
       end
       return $__wtp_exit
   end
   ```
   Also update the comment from `# wtp cd command hook for fish` to `# wtp shell hook for fish`.

4. [ ] Update `cmd/wtp/hook_test.go`:
   - In `TestHookCommand_GeneratesValidShellScripts`, add new `contains` entries for all three shells:
     - `"__WTP_HOOKED=1"` (all shells)
     - `"__wtp_cd:"` (all shells)
     - `"mktemp"` (all shells)
   - In `TestHookScripts_HandleEdgeCases`, add `requiredLogic` entries:
     - Bash/Zsh: `"${__wtp_line#__wtp_cd:}"` (parameter expansion for path extraction)
     - Fish: `"string replace '__wtp_cd:' '' -- $__wtp_line"` (fish path extraction)
   - Add new test `TestHookScripts_PreserveExitCode`:
     - Bash/Zsh hooks contain `local __wtp_exit=$?` and `return $__wtp_exit`
     - Fish hook contains `set -l __wtp_exit $status` and `return $__wtp_exit`
   - Add new test `TestHookScripts_ValidateDirectoryBeforeCd`:
     - Bash/Zsh: contains `-d "$__wtp_target"`
     - Fish: contains `-d "$__wtp_target"`
   - Update `TestFishHook_VariableScoping` if needed — the new code uses `set -l __wtp_stderr_file`, `set -l __wtp_exit`, `set -l __wtp_target` which are all properly scoped with `-l`

5. [ ] Update the `Description` in `NewHookCommand()` (line 17 of `hook.go`) — change "Generate shell hook for cd functionality" to "Generate shell hook for cd and auto-cd functionality" (or similar). The inline description at line 17-22 should mention that the hook enables both `wtp cd` directory switching and auto-cd after `wtp add`.

**Verify:**
```bash
go test ./cmd/wtp/ -run TestHook -v
# Expected: all hook tests pass
```

## Final Verification

```bash
go tool task test
go tool task lint
```

<!-- Review notes:
- Devils advocate caught: missing `stay` flag in `createTestCLICommand` helper (would silently make --stay tests pass for wrong reasons) — fixed in Step 6
- Devils advocate caught: `displaySuccessMessage` signature change breaks 3 direct test callsites — fixed in Step 8
- Devils advocate caught: `TestNewAddCommand` flag list needed updating — fixed in Step 9
- Devils advocate caught: fish hook `echo $__wtp_line` needs quoting to preserve whitespace — fixed
- Devils advocate caught: `t.Setenv("__WTP_HOOKED", "")` should be explicit in "not hooked" tests — incorporated
-->
