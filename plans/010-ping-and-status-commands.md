# Plan 010: `dbx ping` / `dbx status` — connection health checks

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat 38ab8b1..HEAD -- cmd/dbx/main.go internal/mysql/open.go internal/db/db.go plans/README.md`
> If any of those paths changed, compare the "Current state" excerpts against
> the live code before proceeding; on a mismatch, treat it as a STOP
> condition.

## Status

- **Priority**: P1
- **Effort**: S
- **Risk**: LOW
- **Depends on**: none
- **Category**: direction
- **Planned at**: commit `38ab8b1`, 2026-07-21
- **Issue**: <none>

## Why this matters

Today the only way to discover a misconfigured connection is to run a query
and watch it fail at ping time (`internal/mysql/open.go:36` runs
`PingContext` immediately after `sql.Open`). The user has no cheap "is
this connection actually alive?" command — they have to pipe a `SELECT 1`
through `dbx query` to find out. A standalone ping is the kind of low-cost
DX primitive every ops tool ships, and dbx is missing it. This is a pure
additive slice: one new command, no behavior change to existing commands.

## Current state

The pieces needed already exist:

- `internal/db/db.go:11` declares `DB` with `PingContext(ctx) error`.
- `internal/mysql/open.go:36-39` calls `PingContext` on every open and
  returns a wrapped error on failure.
- `internal/mysql/open.go:55-61` exposes `PingContext` on the adapter.
- `cmd/dbx/main.go:23-43` is the command dispatcher; new commands are added
  by a `case "X":` line and a `runX` function in `cmd/dbx/<x>.go`.
- `cmd/dbx/version.go:8` already exposes a build identifier
  (`Version = "dbx 0.0.1"`) — `status` reuses it.
- `internal/config/config.go:30-43` defines the `Connection` struct, which
  carries `Env` (dev/staging/prod/readonly). `status` returns it.

There is no `dbx ping` or `dbx status` anywhere in `cmd/dbx/main.go`
today (the help text at `cmd/dbx/main.go:48-90` enumerates every command).
`dbx version` exists; that is the only no-DB-needed CLI command.

The repo's CLI test pattern uses injectable fetch functions (see
`cmd/dbx/ddl.go:17` `ddlFetchFunc`) so ping can be tested without MySQL.
`db.DB` is an interface, so the existing fake-DB pattern from
`cmd/dbx/ddl_test.go` and `cmd/dbx/query_test.go` applies directly.

## Commands you will need

| Purpose   | Command                          | Expected on success                |
|-----------|----------------------------------|------------------------------------|
| Tests     | `go test ./...`                  | exit 0, no failures                |
| Vet       | `go vet ./...`                   | exit 0, no warnings                |
| Build     | `go build -o /tmp/dbx ./cmd/dbx` | exit 0, binary at `/tmp/dbx`       |
| Smoke     | `/tmp/dbx ping --help`           | usage line, exit 0                 |
| Smoke     | `/tmp/dbx status --help`         | usage line, exit 0                 |
| Smoke     | `/tmp/dbx version`               | prints `dbx 0.0.1`, exit 0         |

## Scope

**In scope** (the only files you should modify):
- `cmd/dbx/main.go` — add `case "ping":` and `case "status":`, plus help lines
- `cmd/dbx/ping.go` (create) — flag parsing, runner, injectable fake
- `cmd/dbx/ping_test.go` (create) — table-driven CLI tests
- `cmd/dbx/status.go` (create) — flag parsing, runner, injectable fake
- `cmd/dbx/status_test.go` (create) — table-driven CLI tests
- `README.md` — append a "CLI: `dbx ping` / `dbx status`" subsection that
  matches the style of existing `dbx <cmd>` sections

**Out of scope** (do NOT touch, even though they look related):
- `internal/db/db.go` — interface stays unchanged
- `internal/mysql/open.go` — DSN/pool/ping behavior is reused as-is
- `internal/config/config.go` — no new fields
- `lua/dbx/init.lua` — no Neovim command in this plan (the user can call
  `:!dbx ping --conn local_wms` if they want it from the editor)
- `plans/008-*` / `plans/009-*` — already DONE
- Any change to existing `dbx query` / `dbx danger` exit semantics

## Git workflow

- Branch: `feat/dbx-ping-status` from `origin/main` (which is at
  commit `38ab8b1` after PR #12)
- Commit per step or per logical unit; message style:
  `feat(cli): add dbx ping and dbx status` (matches repo log style)
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add `dbx ping` CLI command

Create `cmd/dbx/ping.go` with:

- `func runPing(args []string) error` — top-level entry, mirrors `runDDL`
  pattern at `cmd/dbx/ddl.go`
- `func runPingCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) error` —
  the testable variant (input injection point). Parse `--conn`, `--config`.
- Resolve the connection via the same `config.Load(...)` + `conn, ok := cfg.Connection(*connName)`
  pattern used by `cmd/dbx/ddl.go:60-70`. Connection lookup is invalid → exit
  with `error: connection %q not found` on stderr, exit 1.
- Open the connection through an injectable seam `openDB func(ctx context.Context, conn config.Connection) (db.DB, error)`.
  Default production value: `mysql.Open`. Tests pass a fake.
- Call `database.PingContext(ctx)` with the same `DefaultQueryTimeout` shape
  used by `internal/query/run.go:25`. On success: print `ok` + newline to
  stdout, exit 0. On failure: print `error: ping <conn>: <err>` to stderr,
  exit 1. No JSON envelope.
- Mirror `--config` discovery (flag → `DBX_CONFIG` → `./.dbx/config.yaml`
  → XDG/user). Reuse the helper from `cmd/dbx/ddl.go` if it's already
  factored, otherwise duplicate the four-line lookup.

**Verify**: `go build -o /tmp/dbx ./cmd/dbx` exits 0; `/tmp/dbx ping --help`
prints usage (no `connection` flag listed means it's required).

### Step 2: Add `dbx ping` tests

Create `cmd/dbx/ping_test.go`. Use a fake `db.DB` whose `PingContext`
returns nil for `local_wms` and an error for `prod_ro` (mirrors the
`fakeDB` pattern in `cmd/dbx/ddl_test.go:30-50`). Table-driven cases:

- `--conn local_wms` → exit 0, stdout `ok\n`, empty stderr.
- `--conn prod_ro` (ping returns error) → exit 1, stderr contains
  `error: ping prod_ro:`, stdout empty.
- Missing `--conn` → exit 1, stderr mentions `--conn is required`.
- Unknown `--conn nope` → exit 1, stderr mentions `connection "nope" not found`.

**Verify**: `go test ./cmd/dbx -count=1 -run Ping` exits 0.

### Step 3: Add `dbx status` CLI command

Create `cmd/dbx/status.go`. Same injectable pattern as ping. Differences:

- Default output (no flag): single-line text
  `<conn> <env> <driver> <server_version>` (e.g.
  `local_wms dev mysql 8.0.36`). Exit 0.
- `--json`: pretty JSON envelope
  `{"type":"status","connection":"<name>","driver":"<mysql|sqlite>",
   "env":"<dev|staging|prod|readonly|empty>","server_version":"<x.y.z>",
   "sql_mode":"<comma-separated or empty>","dbx_version":"<Version>"}`.
- Server version comes from `SELECT VERSION()` on the same open
  connection. `sql_mode` comes from `SELECT @@SESSION.sql_mode`.
- Both queries are routed through `query.ValidateQuery` (already accepts
  `SELECT`). No new SQL surface.
- The driver field comes from `conn.Driver` (normalized to `mysql` or
  `sqlite`); never infer it from the server reply.

**Verify**: `go build -o /tmp/dbx ./cmd/dbx` exits 0; `/tmp/dbx status --help`
prints usage listing `--conn`, `--config`, `--json`.

### Step 4: Add `dbx status` tests

`cmd/dbx/status_test.go`. Fake `db.DB` with `QueryContext` returning a
canned `[]string{"VERSION()"}` / `[]string{"@@SESSION.sql_mode"}` and
matching row scans:

- Text mode → exit 0, stdout is one line containing the connection name,
  the env, the driver, the version, and (when `sql_mode` is non-empty)
  the sql mode.
- `--json` mode → exit 0, stdout is valid JSON matching the envelope shape
  above. Round-trip with `json.Unmarshal` and assert each field.
- DB error on `QueryContext` → exit 1, stderr mentions `error: status:`.
- Unknown connection → exit 1.

**Verify**: `go test ./cmd/dbx -count=1 -run Status` exits 0.

### Step 5: Wire into `cmd/dbx/main.go` and update help text

In `cmd/dbx/main.go`:

- Add `case "ping": return runPing(args[1:])` and
  `case "status": return runStatus(args[1:])` next to the existing
  `case "version"` block.
- Update `printUsage()` to list `ping` ("Verify a connection is reachable")
  and `status` ("Print connection metadata (text or JSON)") between
  `explain` and `version`.
- Add two example lines to the help text.

**Verify**: `/tmp/dbx --help` exits 0 and lists `ping` and `status`;
`/tmp/dbx version` still exits 0 and prints `dbx 0.0.1`.

### Step 6: Document in README

In `README.md`, append a new section after the existing `dbx explain`
section. Match the structure of nearby CLI sections (`### Uso`,
`### Flags`, `### Salida`). Include a one-line description, the text/JSON
examples, the flag table, and the policy "Solo conexiones
mysql/sqlite (sqlite solo documentado en tests)". Do NOT document
`sqlite` driver as supported for ping/status (it's a test connector per
Plan 006; users running ping/status against a sqlite connection should
get an error or fall through silently — pick the friendly option).

**Verify**: `grep -n "dbx ping" README.md` returns at least 3 matches
(section header, two example lines).

## Test plan

- All new tests in `cmd/dbx/ping_test.go` and `cmd/dbx/status_test.go`
  follow the existing `cmd/dbx/<cmd>_test.go` table-driven pattern
  (`cmd/dbx/ddl_test.go:35-100` is the closest exemplar).
- Cover: success text, success JSON, DB error path, unknown connection,
  missing flag.
- Reuse the `fakeDB` shape used elsewhere — do not introduce a new
  mocking library.
- Run the full suite (`go test ./...`) and the Lua smoke
  (`nvim --headless -u NONE -l tests/nvim_smoke.lua`) to confirm no
  regressions. The smoke test enumerates user commands at
  `tests/nvim_smoke.lua:11-15`; do not add new Neovim commands in this
  plan.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `go test ./...` exits 0
- [ ] `go vet ./...` exits 0
- [ ] `go build -o /tmp/dbx ./cmd/dbx` exits 0
- [ ] `/tmp/dbx ping --help` and `/tmp/dbx status --help` print usage, exit 0
- [ ] `/tmp/dx --help` lists `ping` and `status`
- [ ] `grep -rn 'dbx ping\|dbx status' README.md` returns at least 3 hits
- [ ] No files outside the in-scope list are modified (`git status`)
- [ ] `plans/README.md` status row for this plan updated to DONE

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the
  excerpts (the codebase has drifted since this plan was written).
- A step's verification fails twice after a reasonable fix attempt.
- The fix appears to require touching an out-of-scope file.
- You discover the assumption "`internal/db/db.go` exposes
  `PingContext(ctx) error` and `internal/mysql/open.go` already pings
  on open" is false.

## Maintenance notes

- `dbx ping` reuses `mysql.Open`, so it inherits the force-safe DSN
  policy (`internal/mysql/dsn.go`) and pool settings
  (`SetMaxOpenConns(1)`). Future changes to those affect ping too.
- `dbx status` issues two trivial SELECTs (`VERSION()`, `@@sql_mode`)
  on every call. If a no-network mode is added later, status will need
  to skip them and only return local metadata.
- Both commands are MySQL-only in practice (sqlite is test-only per
  Plan 006 and rejected by `ddl`). If a future Postgres driver is added,
  ping/status should accept it without code changes (the injectable
  seam is driver-agnostic); only the help text needs an update.