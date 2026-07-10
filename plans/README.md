# Implementation Plans

Generated on 2026-07-09 against commit `89dea03`. These plans complete the
remaining MVP described in `README.md`: a safe SQL/JSON inspection workflow
from Neovim, with persisted snapshots, structured comparison and filtering.
Each executor must read its plan in full, honor its STOP conditions, and update
the status after completion.

## Project status

- **Implemented and verified locally:** MySQL connection/config discovery,
  read-only `query`, pretty JSON conversion, `ddl`, snapshots (`save`, `list`,
  `show`, private last-result cache, and `--from-last`), structured snapshot
  diff, bounded JSON paths, and offline SQL danger analysis.
- **Not implemented:** the Neovim Lua plugin.
- **Verification baseline:** `go test ./...`, `go vet ./...`, and
  `go build -o /tmp/dbx ./cmd/dbx` pass on the reviewed workspace. There is no
  repository CI configuration yet; it is deferred until the MVP commands land.

## Execution order & status

| Plan | Title | Priority | Effort | Depends on | Status |
|---|---|---:|---:|---|---|
| [001](001-secure-snapshot-persistence.md) | Preserve and secure snapshot data | P1 | M | — | DONE |
| [002](002-structured-json-diff.md) | Compare snapshots structurally | P1 | M | 001 | DONE |
| [003](003-json-path-filter.md) | Filter result data by a bounded path syntax | P1 | M | 001 | DONE |
| [004](004-danger-analysis.md) | Report dangerous SQL without executing it | P1 | M | — | DONE |
| [005](005-neovim-mvp-client.md) | Expose the complete MVP through Neovim | P1 | M | 001, 002, 003, 004 | TODO |
| [006](006-sqlite-test-connector.md) | Add a portable SQLite connector for integration tests | P2 | M | — | TODO |

## Dependency notes

- Plan 001 is first because the current snapshot implementation is the shared
  persistence contract for `diff`, `path`, and the Neovim client. It also fixes
  precision loss and local-file permissions before database results are kept on
  disk.
- Plans 002 and 003 may be implemented in either order after Plan 001.
- Plan 004 is independent at the Go package level, but Plan 005 consumes its
  CLI command to expose warnings in the editor.
- Plan 006 can be executed after Plan 001 and before the remaining plans to
  give their executors an offline database integration-test option. It is not
  required by the user-facing command implementations.
- Add GitHub Actions only after these plans are merged: its initial workflow
  should run the already-verified `go test ./...`, `go vet ./...`, and build
  commands. It is intentionally not a separate MVP blocker.

## Findings considered and rejected

- **PostgreSQL support:** deferred. The README explicitly declares MySQL as the
  first engine, and a driver abstraction now would delay the MVP.
- **A general SQL parser:** deferred. The current read-only policy is
  intentionally fail-closed; Plan 004 adds advisory analysis without weakening
  the execution barrier.
- **Query row limits:** worth scheduling after the MVP. `README.md` documents
  the unbounded-result limitation, but a limit changes the output contract and
  needs a separate product decision (default, override, truncation metadata).
