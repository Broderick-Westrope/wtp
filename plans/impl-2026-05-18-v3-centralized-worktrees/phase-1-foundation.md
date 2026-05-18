# Phase 1: Foundation

> **Status:** PENDING
> **Depends on:** Nothing
> **Enables:** Phases 2, 3

## Specification

**Problem:** v3 needs new internal infrastructure before any commands can be updated: XDG path resolution, remote URL parsing for `<owner>/<repo>` derivation, state/cache file management with file locking, and a global config layer.

**Goal:** New internal packages that all command updates in Phases 2 and 3 can import. No existing commands are modified in this phase.

**Success Criteria:**
- [ ] XDG path resolution for DATA_HOME, CONFIG_HOME, CACHE_HOME with env var overrides and defaults
- [ ] Remote URL parser handles all 5 specified formats, strips `.git`, includes hostname for non-github.com
- [ ] State file (archive metadata) with flock-based locking and atomic writes
- [ ] Cache file (PR/CI data) with flock-based locking and atomic writes
- [ ] Global config struct with cache TTL, loaded from `$XDG_CONFIG_HOME/wtp/config.yml`
- [ ] All packages have thorough unit tests

## Context Loading

```bash
read internal/config/config.go
read internal/git/repository.go
read go.mod
```

## Tasks

### Task 1: XDG path resolution + remote URL parser

**Context:** `internal/config/config.go` (existing config patterns), `go.mod`

**Files:**
- Create: `internal/xdg/xdg.go`
- Create: `internal/xdg/xdg_test.go`
- Create: `internal/remote/parse.go`
- Create: `internal/remote/parse_test.go`

**Steps:**

1. [ ] Create `internal/xdg/xdg.go` with functions:
   - `DataHome() string` — returns `$XDG_DATA_HOME` or `~/.local/share`
   - `ConfigHome() string` — returns `$XDG_CONFIG_HOME` or `~/.config`
   - `CacheHome() string` — returns `$XDG_CACHE_HOME` or `~/.cache`
   - `WtpDataDir() string` — returns `DataHome()/wtp`
   - `WtpConfigDir() string` — returns `ConfigHome()/wtp`
   - `WtpCacheDir() string` — returns `CacheHome()/wtp`
   - `WorktreeStorageRoot() string` — returns `WtpDataDir()/worktrees`
   - `EnsureDir(path string) error` — creates directory with `0755` if it doesn't exist

2. [ ] Create `internal/xdg/xdg_test.go` with tests:
   - Each function respects env var override
   - Each function falls back to default when env var unset
   - `EnsureDir` creates nested directories
   - `EnsureDir` is idempotent (no error if dir exists)

3. [ ] Create `internal/remote/parse.go` with:
   - `type RepoIdentifier struct { Host string; Owner string; Repo string }` — Host is empty for github.com
   - `func Parse(remoteURL string) (RepoIdentifier, error)` — parses all 5 URL formats
   - `func (r RepoIdentifier) StoragePath() string` — returns `owner/repo` or `host/owner/repo` for non-github.com
   - `func (r RepoIdentifier) StateKey(branch string) string` — returns `owner/repo::branch` or `host/owner/repo::branch`
   - `func ParseStateKey(key string) (repoPath, branch string)` — splits on first `::` via `strings.SplitN(key, "::", 2)`
   - Parsing logic: try `url.Parse` first (handles `https://`, `ssh://`). If scheme is empty, try SCP-style (`user@host:owner/repo.git`). Strip trailing `.git`. Extract last two path segments as `owner/repo`. For `ssh://` URLs, strip port from host. For non-`github.com` hosts, set `Host` field.

4. [ ] Create `internal/remote/parse_test.go` with table-driven tests for:
   - `https://github.com/owner/repo.git` → `{Host:"", Owner:"owner", Repo:"repo"}`
   - `https://github.com/owner/repo` → same
   - `git@github.com:owner/repo.git` → same
   - `ssh://git@github.com/owner/repo.git` → same
   - `ssh://git@github.com:2222/owner/repo.git` → same (port ignored)
   - `https://corp.example.com/org/repo.git` → `{Host:"corp.example.com", Owner:"org", Repo:"repo"}`
   - `git@corp.example.com:org/repo.git` → same
   - Invalid URL → error
   - URL with only one path segment → error
   - `StoragePath()` returns `owner/repo` for github.com, `corp.example.com/org/repo` for GHE
   - `StateKey("feature/auth")` returns `owner/repo::feature/auth`
   - `ParseStateKey("owner/repo::fix::hotfix")` → `("owner/repo", "fix::hotfix")` (split on first `::`)

**Verify:**
```bash
go test ./internal/xdg/ ./internal/remote/ -v
```

---

### Task 2: State and cache file management with locking

**Context:** `internal/xdg/xdg.go` (from Task 1)

**Files:**
- Create: `internal/state/state.go`
- Create: `internal/state/state_test.go`
- Create: `internal/cache/cache.go`
- Create: `internal/cache/cache_test.go`
- Modify: `go.mod` (add `gofrs/flock` direct dependency)

**Steps:**

1. [ ] Promote `github.com/gofrs/flock` from indirect to direct dependency: `go get github.com/gofrs/flock` (already in go.mod as indirect via golangci-lint)

2. [ ] Create `internal/state/state.go` with:
   - `type Store struct` — holds path to `state.json` and lock file path (`state.json.lock`)
   - `func NewStore() *Store` — uses `xdg.WtpDataDir()/state.json`
   - `type State struct { Worktrees map[string]WorktreeState }` — keyed by `owner/repo::branch`
   - `type WorktreeState struct { Archived bool \`json:"archived"\` }`
   - `func (s *Store) Load() (State, error)` — reads without locking; returns empty state if file doesn't exist
   - `func (s *Store) Save(state State) error` — acquires flock on lock file, writes to `.tmp`, renames, releases lock
   - `func (s *Store) WithLock(fn func(State) (State, error)) error` — acquires lock, loads, calls fn, saves if fn returns modified state, releases lock. This is the primary mutation API.
   - `func (s *Store) IsArchived(key string) bool` — reads state, checks key
   - `func (s *Store) SetArchived(key string, archived bool) error` — uses `WithLock`

3. [ ] Create `internal/state/state_test.go` with tests:
   - `Load` returns empty state when file doesn't exist
   - `Save` + `Load` round-trips correctly
   - `SetArchived` creates entry, `IsArchived` reads it back
   - `SetArchived(key, false)` clears the archived flag
   - `WithLock` prevents concurrent modification (use goroutines with small sleep to verify serialization)
   - Atomic write: if process dies mid-write, `.tmp` file doesn't corrupt `state.json`
   - All tests use `t.TempDir()` via env var override for `XDG_DATA_HOME`

4. [ ] Create `internal/cache/cache.go` with:
   - `type Store struct` — holds path to `cache.json` under `xdg.WtpCacheDir()` and lock file
   - `func NewStore() *Store`
   - `type Cache struct { Worktrees map[string]WorktreeCache \`json:"worktrees"\` }`
   - `type WorktreeCache struct { PRNumber int \`json:"pr_number,omitempty"\`; PRState string \`json:"pr_state,omitempty"\`; PRTitle string \`json:"pr_title,omitempty"\`; CIStatus string \`json:"ci_status,omitempty"\`; UpdatedAt time.Time \`json:"updated_at"\` }`
   - `func (s *Store) Load() (Cache, error)` — reads without locking
   - `func (s *Store) Save(cache Cache) error` — flock + atomic write
   - `func (s *Store) Get(key string) (WorktreeCache, bool)` — reads cache, returns entry if exists and not expired
   - `func (s *Store) IsExpired(entry WorktreeCache, ttl time.Duration) bool` — checks `time.Since(entry.UpdatedAt) > ttl`
   - `func (s *Store) Set(key string, entry WorktreeCache) error` — sets `UpdatedAt = time.Now()`, saves with lock
   - `func (s *Store) Delete(key string) error` — removes entry, saves with lock
   - `func (s *Store) SetBatch(entries map[string]WorktreeCache) error` — atomic batch update with single lock acquisition

5. [ ] Create `internal/cache/cache_test.go` with tests:
   - `Load` returns empty cache when file doesn't exist
   - `Set` + `Get` round-trips
   - `IsExpired` returns false for fresh entries, true for stale entries
   - `Delete` removes entry
   - `SetBatch` writes multiple entries atomically
   - All tests use `t.TempDir()` via env var override for `XDG_CACHE_HOME`

**Verify:**
```bash
go test ./internal/state/ ./internal/cache/ -v -race
```

---

### Task 3: Global config

**Context:** `internal/config/config.go` (existing config), `internal/xdg/xdg.go` (from Task 1)

**Files:**
- Create: `internal/config/global.go`
- Create: `internal/config/global_test.go`

**Steps:**

1. [ ] Create `internal/config/global.go` with:
   - `type GlobalConfig struct { CacheTTL time.Duration \`yaml:"cache_ttl"\` }`
   - `const DefaultCacheTTL = 60 * time.Second`
   - `func LoadGlobalConfig() (GlobalConfig, error)` — reads from `xdg.WtpConfigDir()/config.yml`. Returns default config if file doesn't exist. Parses YAML.
   - `func SaveGlobalConfig(cfg GlobalConfig) error` — writes to `xdg.WtpConfigDir()/config.yml`, creating directory if needed. Uses atomic write (temp + rename).
   - `func EnsureGlobalConfig() (GlobalConfig, error)` — loads if exists, creates with defaults if not. Returns the config either way.
   - Custom YAML marshaling for `time.Duration` (marshal as string like `"60s"`, unmarshal from string or integer seconds). Use `go.yaml.in/yaml/v3` — same library as existing `internal/config/config.go`.

2. [ ] Create `internal/config/global_test.go` with tests:
   - `LoadGlobalConfig` returns defaults when file doesn't exist
   - `SaveGlobalConfig` + `LoadGlobalConfig` round-trips
   - `EnsureGlobalConfig` creates file on first call, reads on subsequent
   - Custom duration parsing: `"60s"`, `"5m"`, `"1h"` all parse correctly
   - Invalid YAML → error
   - All tests use `t.TempDir()` via env var override for `XDG_CONFIG_HOME`

**Verify:**
```bash
go test ./internal/config/ -v -run TestGlobal
```
