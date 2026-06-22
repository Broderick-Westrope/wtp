# Archive Overhaul Implementation Plan

> **Status:** DRAFT

## Overview

Rework wtp's archive system from a soft boolean flag to a destructive operation that removes worktrees and branches from git, hiding them from LazyGit and all git tooling. Add a two-tier maintenance system to replace the `wtp list`-only auto-archive. This is phased because it spans 4 independent domains: foundation types, archive/unarchive commands, a new maintenance subsystem, and list command changes.

## Phases

| # | File | Delivers | Depends on | Review focus |
|---|------|----------|------------|--------------|
| 1 | `phase-1-foundation.md` | State schema, config, github exports, git helpers | — | Schema design, backward compat, migration |
| 2 | `phase-2-archive-unarchive.md` | Destructive archive + SHA-based unarchive | Phase 1 | Data safety, idempotency, partial failure recovery |
| 3 | `phase-3-maintenance.md` (parallel) | Two-tier maintenance system | Phase 1 | Throttle logic, dirty-worktree guard, auto-archive |
| 4 | `phase-4-list-cleanup.md` | Remove auto-archive from list, --all synthesis | Phase 3 | Display correctness, no regressions |

> Phases 2 and 3 are parallel — they share no code dependencies beyond Phase 1's foundation types.

## Phase Boundaries

- **1 → 2:** Foundation provides the expanded `WorktreeState` schema and git helper methods that archive/unarchive depend on.
- **1 → 3:** Foundation provides config fields (`ArchiveRetention`, `MaintenanceInterval`) and exported PR state constants that maintenance depends on.
- **3 → 4:** List cleanup removes auto-archive code that maintenance replaces. Must land after maintenance is operational.
