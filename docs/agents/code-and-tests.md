# Code and Tests

- Follow `.golangci.yml` and run `go tool task fmt` + `go tool task lint` for touched changes.
- Add or update tests when behavior changes.
- Prefer unit tests for logic changes; add E2E tests for git workflow behavior.
- The CLI lives in the importable `cli` package and may run embedded and concurrently; never read cwd, stdio, env or `os.Args` from the process directly — use `internal/procenv` (see [architecture](../architecture.md#embedding)).
