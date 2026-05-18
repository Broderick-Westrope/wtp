# wtp v3: Centralized Worktrees with PR/CI Status and Archive

**Problem:** wtp stores worktrees inside the repo directory, which pollutes the project tree and lacks visibility into GitHub PR/CI state. Users coming from tools like supacode want the ease of managed worktree lifecycle (create, track, archive, auto-cleanup) without leaving their terminal.

**Goal:** A clean v3 of wtp that centralizes worktree storage under XDG directories, adds GitHub PR/CI status to `wtp list`, and introduces archive/auto-archive workflows — all as a CLI-native experience.

**Scope:**

In scope:
- Centralized worktree storage at `$XDG_DATA_HOME/wtp/worktrees/<owner>/<repo>/<branch>` (branch slashes create nested dirs on filesystem, see design decisions)
- Global config at `$XDG_CONFIG_HOME/wtp/config.yml` (storage defaults, cache TTL)
- Per-repo `.wtp.yml` retains hooks only (post_create copy/symlink/command)
- State file at `$XDG_DATA_HOME/wtp/state.json` for archive metadata; separate cache file at `$XDG_CACHE_HOME/wtp/cache.json` for PR/CI data
- PR/CI status in `wtp list` via `gh` CLI (compact single-line, cached with TTL)
- `wtp archive <name>` / `wtp unarchive <name>` (metadata-only hide)
- `wtp list` hides archived by default, `--all` shows them
- Auto-archive on merge detected during `wtp list` polling
- All discovered worktrees (managed and unmanaged) get PR/CI status and archive capability
- `wtp remove` cleans up state file entries and centralized directory
- `wtp doctor` detects v2 worktrees and prints cleanup commands
- Clean break: v3 module path, no backward compatibility with v2 storage

Out of scope:
- Multi-forge support (GitLab, Bitbucket) — GitHub only via `gh` CLI
- Background daemons or watchers
- Terminal session management (that's the terminal emulator's job)
- Run/archive lifecycle scripts (existing hooks cover setup; run/archive scripts are not needed)
- GUI or TUI — this remains a CLI tool
- Automated migration from v2 (but `wtp doctor` helps manual cleanup)
- Windows support (not re-evaluated for v3)
- Cross-repo worktree discovery (future feature; v3 is repo-scoped)

**Constraints:**
- Go 1.24 toolchain
- `gh` CLI required for PR/CI features (graceful degradation if not installed)
- XDG Base Directory Specification compliance (`XDG_DATA_HOME` defaults to `~/.local/share`, `XDG_CONFIG_HOME` defaults to `~/.config`)
- Existing CLI framework: urfave/cli/v3
- Must work on macOS and Linux (current shell hook support: bash, zsh, fish)

**Success Criteria:**
- [ ] `wtp add <branch>` creates worktrees under `$XDG_DATA_HOME/wtp/worktrees/<owner>/<repo>/<branch>`
- [ ] `<owner>/<repo>` derived from `git remote get-url origin` with robust parsing (see design decisions)
- [ ] Global config at `$XDG_CONFIG_HOME/wtp/config.yml` controls defaults
- [ ] Per-repo `.wtp.yml` scoped to hooks only (no `base_dir`, no `version`)
- [ ] `wtp list` shows compact single-line with PR state and CI status when `gh` is available
- [ ] PR/CI data cached in a separate cache file with configurable TTL (default 60s)
- [ ] `wtp list --no-sync` skips `gh` calls and suppresses auto-archive
- [ ] `wtp list --all` includes archived worktrees
- [ ] `wtp list --quiet --no-sync` is safe for scripting (no mutations, no network calls)
- [ ] `wtp archive <name>` marks worktree as archived in state file (works for any discovered worktree)
- [ ] `wtp unarchive <name>` reverses archive
- [ ] Merged PRs auto-archived during `wtp list` polling, with printed notice
- [ ] All discovered worktrees (managed and unmanaged) get PR/CI status and archive capability
- [ ] Graceful degradation: `gh` not installed -> list works without PR/CI columns
- [ ] `wtp remove` cleans up state file and cache entries for the removed worktree
- [ ] `wtp doctor` warns about v2 worktrees and prints cleanup commands
- [ ] Module path updated to v3

**Design Decisions:**

- **XDG over home directory dotfiles** — follows the XDG Base Directory Specification rather than `~/.wtp/`. Rationale: respects user preferences for data/config separation, plays well with backup and dotfile management strategies.

- **`<owner>/<repo>` from remote origin, no domain** — e.g. `satococoa/wtp` not `github.com/satococoa/wtp`. Parsed from `git remote get-url origin`. Supported URL formats:
  - `https://github.com/owner/repo.git` (HTTPS with `.git`)
  - `https://github.com/owner/repo` (HTTPS without `.git`)
  - `git@github.com:owner/repo.git` (SCP-style SSH)
  - `ssh://git@github.com/owner/repo.git` (SSH protocol URL)
  - `ssh://git@github.com:2222/owner/repo.git` (SSH with port — port is ignored)
  - Trailing `.git` is always stripped.
  - If `origin` remote does not exist, `wtp add` errors with: `no 'origin' remote found; set one with 'git remote add origin <url>'`.
  - If the URL cannot be parsed to `<owner>/<repo>`, `wtp add` errors with the unparseable URL and a suggestion to file a bug.
  - GHE (GitHub Enterprise) with non-github.com domains: the hostname is included in the path when it is not `github.com`. E.g. `corp.example.com/org/repo` becomes `$XDG_DATA_HOME/wtp/worktrees/corp.example.com/org/repo/<branch>`. This prevents collisions between GHE and github.com repos with the same `org/repo`.

- **Branch slash handling** — branches like `feature/auth` create nested directories on the filesystem (e.g. `.../satococoa/wtp/feature/auth/`). This is fine for the filesystem. The **state file key** uses `::` as the separator between repo identifier and branch: `satococoa/wtp::feature/auth`. Key parsing always uses `SplitN(key, "::", 2)` — split on the first `::` only. This is safe because repo identifiers (`owner/repo` or `host/owner/repo`) never contain `::`, while branch names technically can contain `:` characters. For GHE repos the key includes the hostname: `corp.example.com/org/repo::feature/auth`.

- **Discovery model** — `wtp list` uses `git worktree list --porcelain` as its primary discovery mechanism, scoped to the current repository. This is the same as v2. The centralized storage changes *where* worktrees are created, not *how* they are discovered. State and cache files enrich discovered worktrees with archive flags and PR/CI data. Cross-repo discovery (e.g. `wtp list --all-repos`) is a future feature, not in v3 scope.

- **No managed/unmanaged distinction** — all worktrees discovered via `git worktree list` get the same capabilities: PR/CI status, archive, auto-archive, `cd`, `remove`, `exec`. The v2 `isWorktreeManagedCommon` filter is eliminated entirely — all discovered worktrees are first-class. The only difference is visual: worktrees under centralized storage show the branch name as their user-facing name (e.g. `feature/auth`), while worktrees elsewhere show their absolute path. There is no `wtp adopt` command — it's unnecessary since all worktrees get full features regardless of location.

- **User-facing worktree names** — when users run `wtp cd <name>`, `wtp remove <name>`, or `wtp exec <name>`, the `<name>` is the branch name (e.g. `feature/auth`). This is derived from the worktree's checked-out branch, not the filesystem path. For centralized worktrees, the branch name maps directly to the path under `<owner>/<repo>/`. For non-centralized worktrees, the branch name is still the handle — the underlying path is resolved from git's worktree metadata. Tab completion uses branch names. The main worktree is addressed as `@` (unchanged from v2). `wtp list --quiet` outputs one branch name per line (`@` for main), suitable for scripting.

- **Detached HEAD worktrees unsupported** — v3 requires every worktree to have a branch. `wtp add --detach` is removed. Detached HEAD worktrees discovered via `git worktree list` are displayed in `wtp list` with a `(detached)` marker but cannot be addressed by `wtp cd`, `wtp remove`, `wtp archive`, or `wtp exec`. Users needing detached worktrees can use `git worktree add --detach` directly and manage them with raw git commands.

- **Split global + per-repo config** — global config owns cache TTL and default behaviour. Per-repo `.wtp.yml` owns hooks only. The `version` and `defaults.base_dir` fields are removed from `.wtp.yml`. `wtp init` scaffolds per-repo `.wtp.yml` for hooks. Global config is created on first `wtp add` if it doesn't exist (no separate global init command). Storage is always centralized under `$XDG_DATA_HOME/wtp/worktrees/` — there is no config option to change this in v3. The old `base_dir`-relative behavior is removed entirely.

- **Separate state and cache files** — archive metadata lives in `$XDG_DATA_HOME/wtp/state.json` (small, important, rarely written). PR/CI cache lives in `$XDG_CACHE_HOME/wtp/cache.json` (frequently written, loss is recoverable — follows XDG spec for non-essential cached data, defaults to `~/.cache`). This separation means cache corruption never risks archive metadata. Both files use atomic writes (write to temp file in same directory + rename).

- **File locking for state writes** — state file writes use `flock`-based advisory locking via a separate lock file (`state.json.lock`) to prevent concurrent `wtp list` / `wtp archive` calls from clobbering each other. The locking protocol is: acquire lock on `state.json.lock` -> read `state.json` -> modify in memory -> write to `state.json.tmp` -> rename to `state.json` -> release lock. Cache file writes use the same pattern with `cache.json.lock` under `$XDG_CACHE_HOME/wtp/`. Read-only operations don't acquire locks (stale reads are acceptable). Atomic rename ensures readers on local filesystems never see partial writes. `gofrs/flock` is added as a direct dependency (currently indirect via golangci-lint).

- **Cached PR/CI polling on `wtp list`** — avoids background daemons while keeping data fresh. TTL-based cache means rapid successive `wtp list` calls are instant. `--no-sync` flag for offline/speed use — also suppresses auto-archive to keep the command side-effect-free. `--quiet` mode never mutates state regardless of other flags, making `wtp list --quiet --no-sync` safe for scripting.

- **Auto-archive detection** — only detects merges via PR state from `gh`. Branches merged without a PR (direct push) are not auto-archived — use `wtp archive` manually. If `gh` is unavailable, auto-archive is disabled. Worktrees whose PRs were merged while `gh` was unavailable will be caught on the next `wtp list` when `gh` is available again (since the PR state is persistent on GitHub). Auto-archive writes are batched into a single state file write per `wtp list` invocation.

- **`gh` CLI as the only GitHub integration** — no direct API calls, no token management. `gh` handles auth. Graceful degradation: if `gh` is not installed or not authenticated, PR/CI columns simply don't appear, auto-archive is disabled, and a one-time hint is printed suggesting `gh` installation.

- **Archive is metadata-only** — `wtp archive` toggles a flag in `state.json`. The worktree directory, git worktree registration, and branch all remain. The value proposition: when managing many worktrees (10+), archive declutters `wtp list` output to show only active work. It's a workflow organisation tool, not a resource management tool. Users who want to free disk/branch locks should use `wtp remove`. Archived worktrees are visually distinct in `wtp list --all` output.

- **`wtp remove` state cleanup** — in addition to `git worktree remove` and optional branch deletion (existing behaviour), v3 `wtp remove` also: removes the entry from `state.json` (archive metadata), removes the entry from `cache.json` (PR/CI cache), and deletes the centralized worktree directory if applicable. If the state/cache entry doesn't exist, removal still proceeds — state cleanup is best-effort.

- **`wtp init` in v3** — scaffolds per-repo `.wtp.yml` for hooks only. The template no longer includes `version` or `defaults.base_dir`. Example output:
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

- **`wtp doctor`** — a new command that detects potential issues:
  - v2 worktrees: checks git-registered worktrees whose paths match common v2 default patterns (`<repo>/worktrees/`). Best-effort — custom `base_dir` configurations from v2 may not be detected.
  - Orphaned state entries: state file entries with no corresponding worktree on disk.
  - Orphaned worktree directories: directories in centralized storage with no git worktree registration.
  - `gh` CLI availability and auth status.

- **Clean break v3** — the storage model change is fundamental enough to warrant a major version. No automated migration. `wtp doctor` helps users identify and clean up v2 worktrees manually. This keeps the codebase simple and avoids indefinite dual-path support.

**Context Files:**
- `cmd/wtp/app.go` — CLI app entrypoint, all commands wired here
- `cmd/wtp/add.go` — worktree creation command
- `cmd/wtp/list.go` — worktree listing command
- `cmd/wtp/remove.go` — worktree removal command
- `cmd/wtp/cd.go` — worktree navigation
- `cmd/wtp/init.go` — config scaffolding
- `internal/config/config.go` — config schema, loader, validation
- `internal/git/repository.go` — git operations (worktree, branch)
- `internal/git/worktree.go` — worktree parsing
- `internal/hooks/hooks.go` — post-create hook execution
- `internal/command/builders.go` — typed git command builders
- `docs/architecture.md` — architecture overview
