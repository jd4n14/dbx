# Design: `dbx ddl` (MySQL)

**Date:** 2026-07-08  
**Status:** Approved (user)  
**Approach:** B — dedicated `internal/ddl` package  
**Scope:** CLI Go only (no Neovim)

## Problem

Backend debugging needs table DDL without opening DataGrip. After `dbx query`, the next CLI slice is `dbx ddl`: fetch `SHOW CREATE TABLE` for a named table and print it for inspection (and later, a Neovim buffer with `filetype=sql`).

## Goals

- Implement `dbx ddl --conn <name> --table <table> [--config <path>] [--json]`.
- Default stdout: raw MySQL `CREATE TABLE …` SQL text.
- Optional `--json`: structured envelope for tooling.
- Fail closed on invalid table names (no SQL injection via identifiers).
- Reuse existing config discovery, force-safe MySQL open, and offline-test patterns from `query`.
- Keep other commands (`snapshot`, `diff`, `path`, `danger`) as stubs.

## Non-goals

- Neovim / Lua plugin
- Views, procedures, triggers, indexes-as-objects
- Schema-qualified names (`db.table`)
- PostgreSQL or multi-engine abstraction beyond `dialect: "mysql"`
- Re-formatting or normalizing MySQL DDL beyond what the server returns
- Full SQL parser or `danger` integration

## Decisions (confirmed)

| Topic | Decision |
|-------|----------|
| Package layout | Dedicated `internal/ddl` (not thin wrapper over `query.Run` → JSON) |
| Default stdout | Pure SQL (`Create Table` column text) + trailing newline if missing |
| `--json` envelope | `{ type, connection, dialect, table, ddl }` pretty JSON |
| `--table` | Simple identifier only |
| Objects | `SHOW CREATE TABLE` only |
| Identifier rules | ASCII `^[A-Za-z_][A-Za-z0-9_]*$`, max 64 chars |
| SQL construction | Validate + backtick-quote; never bind identifiers as string params |
| Policy | Do not route free-form user SQL through `query.ValidateQuery`; statement is fully built by `ddl` |
| Driver | MySQL only (same as current `mysql.Open`) |

## CLI contract

```bash
dbx ddl --conn <name> --table <table> [--config <path>] [--json]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--conn` | yes | Named connection from YAML config |
| `--table` | yes | Simple table identifier |
| `--config` | no | Same discovery as `query`: flag → `DBX_CONFIG` → `./.dbx/config.yaml` → XDG/user config |
| `--json` | no | Emit JSON envelope instead of raw SQL |

### Success: SQL mode (default)

- stdout: exact `Create Table` string from MySQL
- Ensure a single trailing newline on the document written to stdout
- stderr: empty (aside from unrelated runtime noise; product errors only on failure)
- exit 0

### Success: JSON mode (`--json`)

```json
{
  "type": "ddl",
  "connection": "docker_mysql",
  "dialect": "mysql",
  "table": "orders",
  "ddl": "CREATE TABLE `orders` (\n  `id` bigint NOT NULL AUTO_INCREMENT,\n  ...\n)"
}
```

- Pretty-print: 2-space indent + trailing newline (same style as `query`)
- `type` is always `"ddl"`
- `dialect` is always `"mysql"` in this MVP
- `connection` is the `--conn` value
- `table` is the validated name as requested (no backticks)
- `ddl` is the same string as SQL mode (without forcing an extra newline into the JSON string solely for document formatting)

### Failure

- exit ≠ 0
- `error: …` on stderr via existing `main` wrapper
- **stdout purity:** no partial SQL or JSON on failure

## Architecture

```
cmd/dbx/main.go       case "ddl" → runDDL
cmd/dbx/ddl.go        flags, config, format stdout, injectable fetch
cmd/dbx/ddl_test.go   CLI tests

internal/ddl/
  table.go            ValidateTableName, QuoteIdentifier
  table_test.go
  fetch.go            Fetch(ctx, db, table) string
  fetch_test.go       fake db.DB
  run.go              FetchConnection(ctx, conn, table)
  result.go           Result struct + EncodeJSON (optional small file or in ddl.go)
```

### Data flow

```
CLI flags
  → config.FindConfigPath / Load / Connection
  → ddl.ValidateTableName(table)     // before any network
  → ddl.FetchConnection(ctx, conn, table)
       → mysql.Open(ctx, conn)       // force-safe DSN
       → Fetch: SHOW CREATE TABLE `quoted`
       → extract Create Table text
       → Close
  → write SQL or JSON envelope to stdout
```

### Why not reuse `query.Run`

`query.Run` is optimized for arbitrary read SQL → row matrix → pretty JSON array. DDL needs a single DDL string (or a metadata envelope). Routing through JSON and back is fragile and couples DDL to `jsonutil`. Building a fixed `SHOW CREATE TABLE` after identifier validation is simpler and safer.

### Dependency rules

- `ddl` may import: `config`, `db`, `mysql`, stdlib
- `ddl` must **not** import `query` or `jsonutil`
- CLI may import `ddl` and `config` (same pattern as `query`)

## Identifier validation and quoting

### ValidateTableName

After `strings.TrimSpace`:

1. Non-empty
2. Length ≤ 64 (MySQL identifier max)
3. Match `^[A-Za-z_][A-Za-z0-9_]*$` only
4. Reject dots, spaces, quotes, backticks, unicode, leading digits, etc.

Error message (stable enough for tests to substring-match):

```text
invalid table name: must be a simple identifier (letters, digits, underscore; max 64)
```

### QuoteIdentifier

- Wrap in backticks: `` `name` ``
- Escape internal backticks by doubling (defensive; current charset forbids them)

### Executed SQL

```sql
SHOW CREATE TABLE `orders`
```

No multi-statement, no user-supplied SQL fragments.

## Fetch behavior

1. Call `QueryContext` with the constructed `SHOW CREATE TABLE …`.
2. Expect **exactly one** row.
3. Resolve DDL column:
   - Prefer column named `Create Table` (case-insensitive match on `Columns()`).
   - Else use column index **1** (second column) — MySQL’s stable shape is `(Table, Create Table)`.
4. Coerce cell to string (`string` or `[]byte`; reject unexpected types / nil / empty).
5. Errors:
   - 0 rows → `ddl: table not found or empty SHOW CREATE result` (or wrap server error if Query fails first)
   - >1 rows → `ddl: unexpected row count from SHOW CREATE TABLE`
   - empty DDL string → `ddl: empty CREATE TABLE text`
   - query/connect errors → wrap with `ddl:` / `connect:` prefixes consistent with `query`

### Timeouts

- `internal/ddl`: `DefaultTimeout = 30 * time.Second` applied inside `Fetch` / `FetchConnection` (same magnitude as `query.DefaultQueryTimeout`, defined locally — no import of `query`).
- CLI: overall context = `ddl.DefaultTimeout + 15s` connect budget (mirror `query` CLI).

## JSON encoding

```go
type Result struct {
    Type       string `json:"type"`
    Connection string `json:"connection"`
    Dialect    string `json:"dialect"`
    Table      string `json:"table"`
    DDL        string `json:"ddl"`
}
```

Built only in the CLI (or a tiny helper in `ddl`) after a successful fetch. Library core returns the DDL string; envelope fields that are CLI-only (`connection` name) are known at the command layer.

## Testing strategy

| Layer | Coverage |
|-------|----------|
| `ValidateTableName` / `QuoteIdentifier` | valid names; empty; `a.b`; injection-ish `orders;drop`; leading digit; too long; backtick |
| `Fetch` + fake `db.DB` | happy path; 0 rows; empty DDL; query error; column name match; fallback to col[1] |
| CLI `runDDLCmd` | missing flags; SQL stdout; `--json` shape; error leaves stdout empty |
| Optional integration | `DBX_MYSQL_TEST_DSN` skippable test: create temp table or use existing, `SHOW CREATE` round-trip |
| Regression | update stub tests that currently assert `ddl` is not implemented |

Offline `go test ./...` must pass without MySQL.

## Documentation

Extend root `README.md` (Spanish, same style as the `query` section):

- Usage examples (SQL and `--json`)
- Flags table
- Identifier rules and limitations
- Note that output is MySQL’s native DDL (may include current `AUTO_INCREMENT=N`, etc.)

## File change map

| Path | Action |
|------|--------|
| `internal/ddl/table.go` | create |
| `internal/ddl/table_test.go` | create |
| `internal/ddl/fetch.go` | create |
| `internal/ddl/fetch_test.go` | create |
| `internal/ddl/run.go` | create |
| `cmd/dbx/ddl.go` | create |
| `cmd/dbx/ddl_test.go` | create |
| `cmd/dbx/main.go` | wire `ddl` command |
| `cmd/dbx/query_test.go` | stop treating `ddl` as stub-only if needed |
| `internal/ddl/integration_test.go` | optional create |
| `README.md` | document `ddl` |

## Risks (accepted for MVP)

- Unicode / quoted MySQL identifiers not supported by design.
- No cross-database table names.
- DDL text is server-as-is (includes runtime auto-increment counters, etc.).
- Live MySQL not required for CI offline green.

## Alternatives considered

| Approach | Why rejected |
|----------|----------------|
| A: wrap `query.Run` and parse JSON rows | Extra hop; couples to jsonutil; weaker typing |
| C: all logic in `cmd/dbx` only | Harder to unit-test; domain logic in CLI |

## Open questions

None remaining for MVP implementation.

## PR Plan

Single focused PR is enough for this slice:

1. **`feat(ddl): add dbx ddl for MySQL SHOW CREATE TABLE`**
   - `internal/ddl` + CLI + tests + README
   - Depends on existing `query`/config/mysql stack on `main`
   - No dependency on future snapshot/diff/danger work
