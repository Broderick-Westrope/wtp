# Auto-cd After `wtp add` Design Spec

**Problem:** After `wtp add`, users must manually run `wtp cd <name>` to switch to the new worktree. This is friction — you create a worktree to work in it, not to admire it from afar.

**Goal:** `wtp add` auto-cds into the new worktree by default. Users who don't want this can pass `--stay`.

**Scope:**
- In scope: `wtp add` emitting a cd marker, `--stay` flag, shell hook updates (bash/zsh/fish), success message changes
- Out of scope: auto-cd for other commands (though the marker mechanism is general-purpose for future use)

**Constraints:**
- The Go binary cannot `chdir` the parent shell — cd must happen via the existing shell hook pattern
- Must work for bash, zsh, and fish
- No user action required to adopt (hook is `eval`'d fresh each shell session)
- This is a **behavior change**: `wtp add` previously did not cd. Users calling `wtp add` through the shell hook will now auto-cd. Scripts calling `command wtp add` directly (bypassing the hook) are unaffected since the marker goes harmlessly to stderr. Document in release notes.

## Design Decisions

### Marker-based cd signaling (general-purpose)

The shell hook will scan stderr of **any** `wtp` subcommand for a marker line:

```
__wtp_cd:/absolute/path/to/worktree
```

- **Why stderr:** stdout stays clean for humans and piping. No stripping logic needed. stderr is the conventional sideband channel.
- **Why a general marker:** any future command can opt into auto-cd by emitting this marker. No per-command hook logic needed.
- **Alternative considered:** extending the hook to only wrap `wtp add` (like it does for `wtp cd`). Rejected — less extensible, duplicates wrapping logic.

### Environment variable gating (`__WTP_HOOKED=1`)

The shell hook sets `__WTP_HOOKED=1` before calling `command wtp`. The Go binary only emits the `__wtp_cd:` marker when this env var is set. This prevents confusing marker output when users run `command wtp add` directly without the hook, or in CI/non-interactive contexts.

### `--stay` flag

- `--stay` suppresses the cd marker on stderr
- When `--stay` is used, the existing "To switch to the new worktree, run: `wtp cd <name>`" hint is shown (it's useful context here)
- When `--stay` is NOT used (default), the hint is replaced with "Changed to worktree directory."

### `--exec` interaction

`--exec` and auto-cd are orthogonal:
1. Create worktree
2. Run post-create hooks (in worktree dir)
3. Run `--exec` command (in worktree dir)
4. Emit cd marker to stderr
5. Shell hook cds to worktree

`--stay` suppresses step 4 regardless of `--exec`.

**`--exec` failure:** If `--exec` fails, the cd marker is still emitted (before the error is returned). The worktree was created successfully — the user should land in it to debug the exec failure. The error message is still displayed.

## Implementation Details

### Shell hook changes

The `else` branch (non-cd commands) currently does `command wtp "$@"`. The new approach uses a temp file to avoid the complexity of real-time stderr capture/filtering:

All stderr is redirected to a temp file. After the command exits, the temp file is post-processed: marker lines are extracted, non-marker lines are forwarded to stderr. This approach is simpler and avoids process substitution race conditions and unreliable `$?` capture.

**Bash/Zsh:**
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

**Fish:**
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
            echo $__wtp_line >&2
        end
    end < $__wtp_stderr_file
    rm -f $__wtp_stderr_file
    if test -n "$__wtp_target" -a -d "$__wtp_target"
        cd "$__wtp_target"
    end
    return $__wtp_exit
end
```

Key details:
- All stderr goes to temp file, post-processed after command exits — no race conditions
- `$?` / `$status` unambiguously captures `wtp`'s exit code (no process substitution interference)
- Path extracted via parameter expansion (`${line#__wtp_cd:}` / `string replace`) — no hardcoded character offsets
- Path is always double-quoted in `cd` call to handle spaces/special chars
- `-d` check validates the directory exists before cd-ing
- Non-marker stderr lines are forwarded to the terminal after command completes
- `__WTP_HOOKED=1` is set on the command invocation
- Tradeoff: stderr output is buffered until after command completes (acceptable — `wtp add` runs in < 1s)

The `wtp cd` branch remains unchanged — it uses stdout, not the marker.

### Go-side changes

**Stderr writer architecture:** The existing `addCommandWithCommandExecutor` takes a single `io.Writer w` for stdout. Rather than changing this signature, the marker is written directly to `os.Stderr` — this is intentional since the marker is a shell-integration signal, not user-facing output. For testability, introduce a package-level helper that accepts an `io.Writer` for the stderr target, defaulting to `os.Stderr` in production.

Specifically:

- **`internal/marker/marker.go`** — shared helper:
  ```go
  const Prefix = "__wtp_cd:"

  // Emit writes the cd marker to the given writer (os.Stderr in production).
  // It only emits if __WTP_HOOKED=1 is set.
  func Emit(w io.Writer, path string) error {
      if os.Getenv("__WTP_HOOKED") != "1" {
          return nil
      }
      _, err := fmt.Fprintf(w, "%s%s\n", Prefix, path)
      return err
  }
  ```

- **`cmd/wtp/add.go`** changes:
  - Add `--stay` bool flag to `NewAddCommand()`
  - In `addCommandWithCommandExecutor`: add an `errW io.Writer` parameter (defaults to `os.Stderr` in `addCommand()`; tests pass a buffer)
  - After hooks and exec (but before returning any exec error), if `!stay`: call `marker.Emit(errW, workTreePath)`
  - Update `displaySuccessMessage` to accept a `stay bool` parameter — show "Changed to worktree directory." when not staying, show `wtp cd` hint when staying

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

**Context Files:**
- `cmd/wtp/add.go` — add command implementation (single `io.Writer w` for stdout at line 86)
- `cmd/wtp/hook.go:70-167` — shell hook output (bash, zsh, fish)
- `cmd/wtp/cd.go` — existing cd command (stdout-based, unaffected)
- `internal/command/builders.go` — git worktree add builder
- `cmd/wtp/app.go` — command registration
