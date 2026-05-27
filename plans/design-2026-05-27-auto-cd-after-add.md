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
- Backward compatible — `--stay` is opt-in, auto-cd is the new default

## Design Decisions

### Marker-based cd signaling (general-purpose)

The shell hook will scan stderr of **any** `wtp` subcommand for a marker line:

```
__wtp_cd:/absolute/path/to/worktree
```

- **Why stderr:** stdout stays clean for humans and piping. No stripping logic needed. stderr is the conventional sideband channel.
- **Why a general marker:** any future command can opt into auto-cd by emitting this marker. No per-command hook logic needed.
- **Alternative considered:** extending the hook to only wrap `wtp add` (like it does for `wtp cd`). Rejected — less extensible, duplicates wrapping logic.

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

## Implementation Details

### Shell hook changes

The `else` branch (non-cd commands) currently does `command wtp "$@"`. It needs to:
1. Capture stderr into a variable while still displaying non-marker stderr lines
2. Check captured stderr for `__wtp_cd:<path>`
3. If found, `cd` to the path

The `wtp cd` branch remains unchanged — it uses stdout, not the marker.

### Go-side changes

- `cmd/wtp/add.go`: add `--stay` bool flag
- `cmd/wtp/add.go`: after hooks and exec, if `!stay`, write `__wtp_cd:<worktreePath>` to stderr
- `cmd/wtp/add.go`: update `displaySuccessMessage` — show "Changed to worktree directory." when not staying, show `wtp cd` hint when staying
- Consider a shared helper (e.g. `internal/cd/marker.go`) for emitting the marker, so future commands can reuse it

**Success Criteria:**
- [ ] `wtp add my-feature` creates the worktree and cds into it (via shell hook)
- [ ] `wtp add my-feature --stay` creates the worktree without cd-ing, shows `wtp cd` hint
- [ ] `wtp add my-feature --exec "npm install"` runs exec in worktree, then cds there
- [ ] `wtp add my-feature --exec "npm install" --stay` runs exec but does not cd
- [ ] Shell hooks updated for bash, zsh, and fish
- [ ] Marker mechanism is general-purpose (not add-specific)
- [ ] Non-marker stderr lines still display normally
- [ ] Existing `wtp cd` behavior unchanged

**Context Files:**
- `cmd/wtp/add.go` — add command implementation
- `cmd/wtp/hook.go:70-167` — shell hook output (bash, zsh, fish)
- `cmd/wtp/cd.go` — existing cd command (stdout-based, unaffected)
- `internal/command/builders.go` — git worktree add builder
- `cmd/wtp/app.go` — command registration
