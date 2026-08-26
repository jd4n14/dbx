# Plan 012: Schema inspection siblings — `dbx indexes`, `dbx fk`, `dbx table-size`

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat 38ab8b1..HEAD -- internal/introspect internal/config internal/db lua/dbx/init.lua plans/README.md`
> If any of those paths changed, compare the "Current state" excerpts against
> the live code before proceeding; on a mismatch, treat it as a STOP
> condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: Plan 007 (schema browser, already DONE — this plan
  extends `internal/introspect` and reuses its patterns).
- **Category**: direction
- **Planned at**: commit `38ab8b1`, 2026-07-21
- **Issue**: <none>

## Why this matters

The schema browser (Plan 007) shipped `dbx tables` and `dbx columns`.
The README notes these are "las dos preguntas diarias de DataGrip
(`¿qué tablas hay?` y `¿qué columnas tiene esta tabla?`)". The third
question a backend engineer asks during debugging — "what indexes does
this table have, what's its foreign-key graph, how big is it?" — has no
dbx equivalent. Today Diego has to hand-write
`SELECT * FROM information_schema.STATISTICS WHERE table_name = '...'`
or shell into MySQL CLI. Three small commands close the loop and put
dbx on par with DataGrip's table-properties dialog.

## Current state

The schema browser pieces are in place to extend cleanly:

- `internal/introspect/` contains three packages-of-files in one
  package: `tables.go`, `columns.go`, and `run.go`. Each pair of
  (list, result) files mirrors the others. Add three more siblings
  following the exact same shape.
- `internal/introspect/columns.go:24-52` defines the injectable DB
  pattern: `ListColumns(ctx context.Context, database db.DB, table, like string)`.
  Every function signature starts with `ctx, database db.DB` and the
  table name is validated via `ddl.ValidateTableName` before any DB
  call.
- `internal/ddl/table.go` exposes `ValidateTableName` (ASCII,
  max 64) and `QuoteIdentifier`. Reuse them.
- `internal/introspect/run.go` (review before editing — ~70 LOC)
  wires `dbx tables` and `dbx columns` from `cmd/dbx/`. It dispatches
  by subcommand and applies the `--json` / `--like` / `--schema` flags.
  The new commands must integrate there.
- `cmd/dbx/main.go:23-43` lists every command; three new cases are
  added (`indexes`, `fk`, `table-size`).
- `internal/config/config.go:30-43` defines `Connection` with `Driver`
  (mysql / sqlite). sqlite is test-only per Plan 006 and `dbx ddl`
  rejects non-mysql (`cmd/dbx/ddl.go:70-77`); the new commands
  follow the same rule.

`information_schema` is **not** used anywhere in the repo today —
`dbx columns` and `dbx tables` use `SHOW TABLES` and
`SHOW COLUMNS`. The new commands use `information_schema` because
that's where MySQL stores the relational metadata (indexes, FK
constraints, table size). This is the first repo consumer of
`information_schema`; treat it as a careful addition.

## Commands you will need

| Purpose   | Command                          | Expected on success                |
|-----------|----------------------------------|------------------------------------|
| Tests     | `go test ./...`                  | exit 0, no failures                |
| Vet       | `go vet ./...`                   | exit 0, no warnings                |
| Build     | `go build -o /tmp/dbx ./cmd/dbx` | exit 0, binary at `/tmp/dbx`       |
| Smoke     | `/tmp/dbx indexes --help`        | usage line, exit 0                 |
| Smoke     | `/tmp/dbx fk --help`             | usage line, exit 0                 |
| Smoke     | `/tmp/dbx table-size --help`     | usage line, exit 0                 |

## Scope

**In scope** (the only files you should modify):
- `internal/introspect/indexes.go` (create) — `Index` struct +
  `ListIndexes(ctx, db.DB, table) ([]Index, error)` + SQL builder.
- `internal/introspect/indexes_test.go` (create) — table-driven tests
  with fake DB.
- `internal/introspect/fk.go` (create) — `ForeignKey` struct +
  `ListForeignKeys(ctx, db.DB, table) ([]ForeignKey, error)` + SQL
  builder.
- `internal/introspect/fk_test.go` (create).
- `internal/introspect/table_size.go` (create) — `TableSize` struct +
  `TableSize(ctx, db.DB, table) (TableSize, error)` + SQL builder.
- `internal/introspect/table_size_test.go` (create).
- `internal/introspect/run.go` — dispatch the three new subcommands.
- `cmd/dbx/indexes.go` (create) — CLI flags + injectable fetch.
- `cmd/dbx/indexes_test.go` (create).
- `cmd/dbx/fk.go` (create).
- `cmd/dbx/fk_test.go` (create).
- `cmd/dbx/table_size.go` (create).
- `cmd/dbx/table_size_test.go` (create).
- `cmd/dbx/main.go` — three new `case` lines + three help-text rows.
- `lua/dbx/init.lua` — three new user commands (`:DbIndexes`,
  `:DbFk`, `:DbTableSize`) following the `:DbColumns` shape
  (`lua/dbx/init.lua:858-878`); three new completion functions
  reusing `complete.parse_columns_list` shape if needed (actually
  reuse `complete.parse_tables_list` for the fk-table).
- `tests/nvim_smoke.lua` — extend the `commands` list at lines 11-15
  to include `:DbIndexes`, `:DbFk`, `:DbTableSize`.
- `README.md` — three new CLI subsections matching the `dbx columns`
  style.

**Out of scope** (do NOT touch, even though they look related):
- `internal/introspect/columns.go` and `tables.go` — leave untouched.
- `internal/introspect/fake_test.go` (if it exists) — extend if
  needed but do not refactor.
- `internal/config/config.go` — no new fields.
- `lua/dbx/complete.lua` — the existing `parse_tables_list` and
  `parse_columns_list` cover both `indexes` (column-list-like) and
  `fk` (table-list-like) consumers; no new parser needed.
- `internal/ddl/*` — no schema-DDL surface changes.
- `internal/danger/danger.go` — `information_schema` queries are
  reads; danger policy already allows `SELECT` and
  `SHOW … FROM information_schema` (the `SHOW` keyword covers it).
  If you hit a false negative, STOP and report; do not add new
  allowlist keywords.

## Git workflow

- Branch: `feat/dbx-schema-inspection-siblings` from `origin/main`
  (commit `38ab8b1`).
- Commit per command or per logical unit; message style:
  `feat(introspect): add dbx indexes / fk / table-size`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: `internal/introspect/indexes.go`

SQL: `SELECT INDEX_NAME, NON_UNIQUE, SEQ_IN_INDEX, COLUMN_NAME,
COLLATION, CARDINALITY, INDEX_TYPE FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? ORDER BY
INDEX_NAME, SEQ_IN_INDEX`. The seven columns are stable across MySQL
5.7+.

Define:

```go
type Index struct {
    Name       string `json:"name"`
    NonUnique  bool   `json:"non_unique"`
    SeqInIndex int    `json:"seq_in_index"`
    ColumnName string `json:"column_name"`
    Collation  string `json:"collation"`    // "A", "D", or NULL
    Cardinality int64 `json:"cardinality"`  // approximate; -1 if unset
    IndexType  string `json:"index_type"`   // "BTREE", "FULLTEXT", "HASH"
}
```

`ListIndexes(ctx, db.DB, table) ([]Index, error)` — validate table
name, build SQL (use a parameter for the table name; `?` works
because it's a value, not an identifier), scan rows, map column index
by header (same pattern as `internal/introspect/columns.go:64-78`),
return slice.

Reuse `ddl.ValidateTableName` and `ddl.QuoteIdentifier` for the
qualifier in case the table name needs backticks (do not — the
parameter binding handles quoting, but log a comment that
`information_schema.TABLES.TABLE_NAME` is compared as a string
column, not an identifier).

**Verify**: `go test ./internal/introspect -count=1 -run Indexes`
exits 0; covers empty result, one-row, multi-row indexes (composite),
multi-indexes on one table.

### Step 2: `internal/introspect/fk.go`

SQL: `SELECT kcu.COLUMN_NAME, kcu.REFERENCED_TABLE_SCHEMA,
kcu.REFERENCED_TABLE_NAME, kcu.REFERENCED_COLUMN_NAME,
rc.CONSTRAINT_NAME, rc.UPDATE_RULE, rc.DELETE_RULE FROM
information_schema.KEY_COLUMN_USAGE kcu JOIN
information_schema.REFERENTIAL_CONSTRAINTS rc ON
kcu.CONSTRAINT_SCHEMA = rc.CONSTRAINT_SCHEMA AND
kcu.CONSTRAINT_NAME = rc.CONSTRAINT_NAME WHERE
kcu.TABLE_SCHEMA = DATABASE() AND kcu.TABLE_NAME = ? AND
kcu.REFERENCED_TABLE_NAME IS NOT NULL ORDER BY rc.CONSTRAINT_NAME,
kcu.ORDINAL_POSITION`.

Define:

```go
type ForeignKey struct {
    Name                  string `json:"name"`
    Column                string `json:"column"`
    ReferencedSchema      string `json:"referenced_schema"`
    ReferencedTable       string `json:"referenced_table"`
    ReferencedColumn      string `json:"referenced_column"`
    UpdateRule            string `json:"update_rule"`   // "CASCADE", "RESTRICT", ...
    DeleteRule            string `json:"delete_rule"`
}
```

Same validate-table-first, scan-by-header pattern.

**Verify**: `go test ./internal/introspect -count=1 -run ForeignKey`
exits 0; covers no FKs, single FK, multi-column FK, self-reference.

### Step 3: `internal/introspect/table_size.go`

SQL: `SELECT TABLE_ROWS, DATA_LENGTH, INDEX_LENGTH,
DATA_FREE, AUTO_INCREMENT, TABLE_COLLATION, CREATE_TIME,
UPDATE_TIME, ENGINE FROM information_schema.TABLES WHERE
TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`.

Define:

```go
type TableSize struct {
    Rows          int64  `json:"rows"`           // approximate; -1 if unset
    DataBytes     int64  `json:"data_bytes"`
    IndexBytes    int64  `json:"index_bytes"`
    DataFreeBytes int64  `json:"data_free_bytes"`
    AutoIncrement int64  `json:"auto_increment"` // -1 if unset
    Collation     string `json:"collation"`
    CreateTime    string `json:"create_time"`    // RFC3339 (nullable)
    UpdateTime    string `json:"update_time"`
    Engine        string `json:"engine"`
}
```

Document that `Rows` is an **estimate** from
`information_schema.TABLES.TABLE_ROWS`; for InnoDB this can drift from
`COUNT(*)`. The README must say so.

**Verify**: `go test ./internal/introspect -count=1 -run TableSize`
exits 0; covers null `TABLE_ROWS` (-1 default), populated values.

### Step 4: CLI surface

For each of the three new commands, follow the `dbx columns` shape
(`cmd/dbx/columns.go:18-150`):

```bash
dbx indexes --conn <name> --table <table> [--config <path>] [--json]
dbx fk --conn <name> --table <table> [--config <path>] [--json]
dbx table-size --conn <name> --table <table> [--config <path>] [--json]
```

- Default output is JSON (since these are inherently structured;
  TSV is misleading for `Indexes` rows). Print pretty JSON to
  stdout. (`dbx columns` defaults to TSV because columns map cleanly
  to a table; indexes/FKs are too nested.)
- `--json` is opt-in to confirm the shape; when both default and
  `--json` produce the same JSON, omit the flag from the help text
  (mention the shape in the README).
- Reject non-mysql driver with the same
  `"<cmd> only supports mysql"` error used by `cmd/dbx/ddl.go:72`.
- Injectable fetch function pattern from
  `cmd/dbx/columns.go:18-25`.

**Verify**: `go test ./cmd/dbx -count=1 -run "Indexes|FK|TableSize"`
exits 0.

### Step 5: Wire into `cmd/dbx/main.go`

Add three new `case` lines (`indexes`, `fk`, `table-size`) in the
same shape as `tables` and `columns`. Add three rows to `printUsage()`
between `columns` and `explain`.

**Verify**: `/tmp/dbx --help` exits 0 and lists the three new
commands.

### Step 6: Neovim commands

Add `:DbIndexes`, `:DbFk`, `:DbTableSize` to `lua/dbx/init.lua`,
each following `:DbColumns` (`lua/dbx/init.lua:858-878`). Each:
- accepts optional table name arg (falls back to `<cword>`)
- uses current connection
- calls the matching CLI command
- opens result in a `tsv` buffer tagged `dbx_result = "indexes"` /
  `"fk"` / `"table_size"`

Use `kind` from `lua/dbx/init.lua:93-114` (`result_buffer`) so the
buffer reuses the existing split UX.

Extend `tests/nvim_smoke.lua:11-15` `commands` list with the three
new command names. No new parser helpers are needed (existing
`complete.parse_tables_list` covers the fk scenario where the user
might tab-complete `--table` against the table-list).

**Verify**: `nvim --headless -u NONE -l tests/nvim_smoke.lua` exits 0.

### Step 7: Document in README

Append three CLI subsections after the existing `dbx columns` section
in `README.md`. Match the `dbx columns` structure (`### Uso`, `### Flags`,
`### Salida`). For `table-size`, include the explicit
"Rows is an estimate" caveat so users do not confuse it with
`COUNT(*)`. For `indexes` / `fk`, include one realistic
information_schema example showing composite indexes / multi-column
FKs.

**Verify**: `grep -n "dbx indexes\|dbx fk\|dbx table-size" README.md`
returns at least 3 hits per command.

## Test plan

- All new tests follow the table-driven pattern in
  `internal/introspect/columns_test.go` and `cmd/dbx/columns_test.go`.
- Test cases per introspect function: empty result, single row, many
  rows, NULL fields, weird collation values (e.g. `NULL` for
  `COLLATION`).
- Test cases per CLI command: success JSON shape, DB error path,
  unknown table (rejected by `ValidateTableName`), non-mysql driver
  rejected.
- Smoke test: `tests/nvim_smoke.lua` exits 0 with the new
  `commands` list.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `go test ./...` exits 0
- [ ] `go vet ./...` exits 0
- [ ] `go build -o /tmp/dbx ./cmd/dbx` exits 0
- [ ] `nvim --headless -u NONE -l tests/nvim_smoke.lua` exits 0
- [ ] `/tmp/dbx indexes --help`, `/tmp/dbx fk --help`,
      `/tmp/dbx table-size --help` print usage, exit 0
- [ ] `/tmp/dbx --help` lists all three commands
- [ ] `grep -n 'dbx indexes\|dbx fk\|dbx table-size' README.md`
      returns at least 3 hits per command
- [ ] `plans/README.md` status row updated to DONE
- [ ] No files outside the in-scope list are modified (`git status`)

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the
  excerpts (the codebase has drifted since this plan was written).
- A step's verification fails twice after a reasonable fix attempt.
- The fix appears to require touching an out-of-scope file.
- You discover the assumption "`information_schema` queries are
  permitted by `query.ValidateQuery`" is false — that would block
  the new commands. (They use `SELECT … FROM information_schema.`
  which is allowed today; verify before coding.)
- The `dbx ddl` non-mysql driver error message shape needs to change
  to be reused three times — STOP and propose a helper instead.
- The Neovim smoke test starts failing for unrelated reasons.

## Maintenance notes

- This plan introduces `information_schema` as a new SQL surface in
  the repo. Future plans that need more relational metadata
  (`information_schema.COLUMNS` for full type info, `VIEWS`,
  `TRIGGERS`, `ROUTINES`) should follow the same pattern: one new
  file in `internal/introspect/`, one new CLI command, one new
  `:Db*` Lua command.
- `TABLE_ROWS` is an InnoDB estimate that can be off by an order of
  magnitude on busy tables. If a future plan adds `dbx count
  --exact` (which runs `SELECT COUNT(*)`), it must reuse the same
  row-limit machinery from Plan 011 to avoid runaway memory.
- The three commands are MySQL-only. If Postgres lands, they need
  their own driver-specific SQL — the structs above are generic
  enough that only the SQL builder changes.