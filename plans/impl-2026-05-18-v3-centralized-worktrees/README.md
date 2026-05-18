# wtp v3 Implementation Plan

> **Status:** DRAFT
> **Spec:** [design-2026-05-18-v3-centralized-worktrees.md](../design-2026-05-18-v3-centralized-worktrees.md)

## Overview

Implements wtp v3: centralized worktree storage under XDG directories, GitHub PR/CI status in `wtp list`, and archive/auto-archive workflows.

## Phases

| Phase | Description | Dependencies | Status |
|-------|-------------|--------------|--------|
| 1 | Foundation: XDG paths, remote URL parser, state/cache, global config | None | PENDING |
| 2 | Core commands: `add`, `remove`, `cd`, `exec`, `init` updated for centralized storage | Phase 1 | PENDING |
| 3 | GitHub integration + list + archive: `list` with PR/CI, `archive`/`unarchive`, `doctor` | Phases 1 & 2 | PENDING |
| 4 | Module path, cleanup, final integration | Phase 3 | PENDING |

## Execution Strategy

- Phase 1 builds new internal packages with no impact on existing commands.
- Phase 2 updates core commands for centralized storage and rewrites the worktree resolver.
- Phase 3 depends on Phase 2 (needs the rewritten resolver for archive commands, and owns `list.go` exclusively).
- Phase 4 is a cleanup/integration pass after Phase 3 is done.

## Review Notes

- Phases 2 and 3 are sequential (not parallel) because: (a) `archive`/`unarchive` depend on the rewritten resolver from Phase 2, (b) `list.go` is modified by both the managed-filter removal and the PR/CI rewrite, so Phase 3 owns it exclusively.
- `app.go` command registration is batched into Phase 3 (archive, unarchive, doctor all registered together).
- Hook path resolution with centralized storage is explicitly tested in Phase 2 — copy/symlink hooks must work when worktree is under XDG path.
- No `--detach` flag exists in current production code; the plan just ensures detached HEAD worktrees are skipped in discovery.
- `list.go` is not touched in Phase 2. The `isWorktreeManagedList` removal happens in Phase 3's list rewrite. Phase 2 deletes `worktree_managed.go` but leaves `list.go` with a compile error that Phase 3 resolves. Phase 2's verify steps use `-run` flags to skip list tests.
- Global config is wired into `wtp add` in Phase 2 via `EnsureGlobalConfig()` call.
- Uses `go.yaml.in/yaml/v3` (same as existing config code) for global config YAML.
