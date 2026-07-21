# Implementation Plans

Generated on 2026-07-09 against commit `89dea03` (MVP slice), and refreshed
on 2026-07-21 against commit `38ab8b1` (post-MVP direction slice). These
plans complete the MVP described in `README.md` (plans 001–009, all DONE)
and propose the next direction slice (plans 010–014). Each executor must
read its plan in full, honor its STOP conditions, and update the status
after completion.

## Project status

- **Implemented and verified locally:** MySQL connection/config discovery,
  read-only `query`, pretty JSON conversion, `ddl`, snapshots (`save`, `list`,
  `show`, private last-result cache, and `--from-last`), structured snapshot
  diff, bounded JSON paths, offline SQL danger analysis, the portable SQLite
  test connector (Plan 006, offline-only), the schema browser (`dbx tables` /
  `dbx columns`, Plan 007), the Neovim omnifunc SQL completion, snapshot
  export to CSV / JSON Lines with `--json` sidecar default ON (Plan 008),
  the `EXPLAIN` pretty-printer (`dbx explain`, `:DbExplain`, Plan 009), and
  the minimal Neovim client.
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
| [005](005-neovim-mvp-client.md) | Expose the complete MVP through Neovim | P1 | M | 001, 002, 003, 004 | DONE |
| [006](006-sqlite-test-connector.md) | Add a portable SQLite connector for integration tests | P2 | M | — | DONE |
| [007](007-schema-browser-with-sql-completion.md) | Schema browser + SQL completion (`dbx tables` / `dbx columns`, `:DbTables` / `:DbColumns`, omnifunc) | P1 | M | — | DONE |
| [008](008-export-snapshots-csv-jsonl.md) | Snapshot CSV / JSONL export with `--json` sidecar (`dbx export`, `:DbExport`) | P2 | M | 001 | DONE |
| [009](009-explain-pretty-printer.md) | Pretty-print `EXPLAIN` output (`dbx explain`, `:DbExplain`) | P2 | M | 001 | DONE |
| [010](010-ping-and-status-commands.md) | `dbx ping` / `dbx status` — connection health and metadata | P1 | S | — | DONE ([PR #13](https://github.com/jd4n14/dbx/pull/13)) |
| [011](011-query-row-limit-with-truncation-metadata.md) | `--max-rows N` for `dbx query` with truncation envelope | P1 | M | 005 | TODO |
| [012](012-schema-inspection-siblings.md) | `dbx indexes` / `dbx fk` / `dbx table-size` + `:Db*` mirrors | P2 | M | 007 | TODO |
| [013](013-history-rerun-and-picker-ux.md) | `:DbHistoryRun <idx>` + opt-in `history_picker` UX | P2 | M | — | TODO |
| [014](014-floating-danger-window-and-conn-env-ux.md) | Floating danger window + conn@env statusline UX | P2 | M | 005 | TODO |

## Direction proposals (post-MVP, plans 010–014)

The MVP is feature-complete against the README; the next slice closes
specific inspection and UX gaps that surfaced while shipping the MVP.
Ordered roughly by leverage, lowest-risk first:

- **010 — `dbx ping` / `dbx status`** — cheapest slice, makes the
  implicit connection ping (`internal/mysql/open.go:36`) a standalone
  CLI command, plus server-version and `sql_mode` discovery. No new
  internal package; pure additive surface.
- **011 — `--max-rows N` + truncation metadata** — the row-limit
  decision that `plans/README.md` explicitly deferred after the MVP.
  Lands the LIMIT-via-SQL approach with a stable envelope so existing
  consumers (`dbx export`, Neovim rendering, snapshot diff) keep
  working.
- **012 — schema inspection siblings (`indexes` / `fk` / `table-size`)** —
  closes the daily DataGrip gap Plan 007 left open (table-level
  metadata beyond columns and DDL). First repo consumer of
  `information_schema`; MySQL-only.
- **013 — `:DbHistoryRun <idx>` + opt-in history picker** — closes the
  history-loop gap Plan 005 deferred ("A required interaction needs
  an unspecified picker/UI framework"). Uses built-in `vim.ui.select`
  — no extra dependency. Index-based re-run is always available; the
  picker is opt-in via `setup({ history_picker = true })`.
- **014 — floating danger window + conn@env statusline UX** — delivers
  the README's "Los warnings peligrosos pueden mostrarse en un
  floating window" presentation and the `.ai/ROADMAP-nvim-usability.md`
  P3 #7 "statusline / echo de conn@env" item. Both flags are
  opt-in (default OFF) to preserve existing muscle memory.

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
- Plan 007 ships independently of every prior plan; the daily DataGrip gap
  ("what tables exist") is the most common interruption when reviewing SQL
  from Neovim. Its omnifunc benefits downstream plans (column-aware INSERT
  export, EXPLAIN columns) without entangling them.
- Plan 011 (row limits) depends on Plan 005 because the Neovim client
  renders the bare query output; the truncation envelope is the only
  shape change, and Plan 005's `result_buffer` path stays compatible.
- Plan 012 (schema siblings) depends on Plan 007 because it extends the
  `internal/introspect` package and reuses `ValidateTableName` /
  `QuoteIdentifier` patterns from Plan 007's siblings.
- Plan 014 (floating danger + statusline) depends on Plan 005 because
  it changes how the danger envelope is presented (float vs buffer).
  Plan 004 produced the envelope; Plan 005 wired it through Lua. Plan
  014 only changes presentation, so the dependency on 005 (not 004)
  is correct.
- Plans 010 and 013 are independent — they reuse existing primitives
  (mysql.Open + db.DB.PingContext for 010; history + vim.ui.select for
  013) and add no new internal package.
- Add GitHub Actions only after the post-MVP plans are merged: its initial
  workflow should run the already-verified `go test ./...`, `go vet ./...`,
  and build commands. It is intentionally not a separate MVP blocker.

## Findings considered and rejected

- **PostgreSQL support:** deferred. The README explicitly declares MySQL as the
  first engine, and a driver abstraction now would delay the MVP.
- **A general SQL parser:** deferred. The current read-only policy is
  intentionally fail-closed; Plan 004 adds advisory analysis without weakening
  the execution barrier.
- **Query row limits:** worth scheduling after the MVP. `README.md` documents
  the unbounded-result limitation, but a limit changes the output contract and
  needs a separate product decision (default, override, truncation metadata).
  Plan 011 implements the chosen contract.
- **`--like` wildcards for `dbx tables`/`dbx columns`:** deferred. Accepting
  identifier-shape LIKE literals keeps the SQL builder safe and side-steps
  full LIKE-pattern parsing; users who want prefix filtering run
  `complete.filter_prefix` on the Neovim side. Wildcard support can ship later
  via a `like_pattern` flag without breaking the current contract.
- **`EXPLAIN ANALYZE`:** considered as a Plan 009 extension. It would execute
  the query (including DML), which conflicts with the read-only write barrier;
  safe only for `SELECT` against a non-prod connection. Deferred until a
  product decision is made about scope (SELECT-only? against staging only?
  opt-in flag?). Not proposed in plans 010–014.
- **A `dbx tui` / interactive TUI frontend:** out of scope. The README and
  Diego's intent docs establish Neovim as the editor-side UI; a separate TUI
  would duplicate work and split maintenance.
- **Auto-starting a watcher for live `EXPLAIN` re-evaluation:** out of scope.
  EXPLAIN is a snapshot of the optimizer's plan; live updates would require
  infrastructure (file watcher + cache invalidation) that has no clear
  product win.
