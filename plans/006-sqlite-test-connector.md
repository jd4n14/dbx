# Plan 006: Add a portable SQLite connector for integration tests

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before continuing. Stop
> on any STOP condition; do not silently turn this testing connector into
> SQLite product support. When complete, update Plan 006 in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat 89dea03..HEAD -- go.mod go.sum internal/config internal/mysql internal/query internal/db cmd/dbx/query.go cmd/dbx/ddl.go cmd/dbx/ddl_test.go README.md`
> This repository has uncommitted snapshot work at plan time. Preserve it; this
> plan does not alter snapshot semantics.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: MED — driver dispatch must not accidentally change MySQL's
  force-safe DSN policy or make SQLite appear supported by `ddl`.
- **Depends on**: none (recommended after Plan 001)
- **Category**: tests, dx, dependencies
- **Planned at**: commit `89dea03`, 2026-07-09

## Why this matters

The only live query integration test currently requires a manually supplied
MySQL DSN and skips by default (`internal/query/integration_test.go:15-24`).
That leaves the real `database/sql` path, scan behavior, and driver dispatch
uncovered in ordinary local and CI runs. Add an embedded SQLite connector only
for deterministic test fixtures, so `go test ./...` executes a genuine query
round trip without Docker, credentials, or network access.

Use `modernc.org/sqlite` as the SQLite dependency: its official package
documentation describes it as a CGo-free `database/sql` driver, registered via
blank import and opened with driver name `sqlite`.

## Current state

- `internal/config/load.go:140-179` defaults an empty driver to `mysql`,
  rejects any non-MySQL driver, and assumes either a raw MySQL DSN or
  host/user/database fields.
- `internal/query/run.go:51-66` validates SQL and then always calls
  `mysql.Open`; this prevents any second connector from reaching `query.Run`.
- `internal/mysql/open.go:24-42` owns MySQL-specific DSN construction,
  connection pool limits, ping, and adapter implementation; those safeguards
  must remain intact.
- `internal/db/db.go:9-23` provides the driver-neutral `DB` and `Rows`
  interfaces already used by the query package.
- `internal/query/integration_test.go:20-70` is optional because it creates a
  `config.Connection{Driver: "mysql", DSN: ...}` and relies on a live server.
- `cmd/dbx/ddl.go:44-69` resolves a connection but `ddl.FetchConnection`
  currently assumes MySQL. Once config accepts SQLite, `ddl` needs a clear
  pre-network driver rejection.
- Existing conventions are pure Go tests with `t.TempDir`, no test framework,
  no credentials committed, and `go test ./...` / `go vet ./...` as the
  verified project checks.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Resolve dependency | `go get modernc.org/sqlite@latest` | `go.mod` and `go.sum` contain a resolved direct version |
| SQLite tests | `go test ./internal/sqlite ./internal/query -count=1 -v` | all pass; no skip required |
| Config/CLI tests | `go test ./internal/config ./cmd/dbx -count=1` | all pass |
| Full verification | `go test ./... -count=1` | all packages pass without MySQL env vars |
| Static checks | `go vet ./...` | exit 0, no findings |
| Build | `go build -o /tmp/dbx ./cmd/dbx` | exit 0 with CGO disabled or unavailable |

The executor must commit the resolved module versions produced by `go get`; do
not hand-edit transitive `modernc.org/*` entries. Before selecting the version,
review the upstream package's current `go.mod`, because its documentation
requires a matching `modernc.org/libc` version.

## Scope

**In scope**

- `go.mod`, `go.sum`
- New `internal/sqlite/open.go` and `internal/sqlite/open_test.go`
- `internal/config/config.go`, `internal/config/load.go`, and their tests
- `internal/query/run.go`, its unit/integration tests, and only the smallest
  new driver-dispatch seam required
- `cmd/dbx/ddl.go`, `cmd/dbx/ddl_test.go`, `README.md`
- `plans/README.md` status row

**Out of scope**

- SQLite DDL (`SHOW CREATE TABLE` is MySQL-specific), migrations, write access,
  or exposing SQLite as a documented production database target
- Replacing the MySQL driver, changing `internal/mysql/dsn.go`, or weakening
  `query.ValidateQuery`
- Docker, an external service, seed files, benchmarks, or a test framework
- Changes to snapshots, `diff`, `path`, `danger`, and Neovim code

## Git workflow

- Branch: `codex/sqlite-test-connector`
- Match current commit style, e.g. `test(query): add sqlite integration path`.
- Do not push or open a pull request unless the operator asks.

## Steps

### Step 1: Add the pure-Go SQLite dependency and narrow connector contract

Run the dependency command above and add `modernc.org/sqlite` as a direct
module requirement. Create `internal/sqlite/open.go` that blank-imports the
driver and exposes:

```go
func Open(ctx context.Context, conn config.Connection) (db.DB, error)
```

For SQLite, `conn.DSN` is required and is the complete driver DSN. Do not reuse
MySQL host, port, user, password, or database fields. Open with
`sql.Open("sqlite", conn.DSN)`, set `MaxOpenConns(1)` and `MaxIdleConns(1)`,
ping with the supplied context, close on ping error, and return an adapter that
implements the existing `internal/db.DB` interface. Keep the adapter private;
duplicating its small implementation is preferable to leaking MySQL internals
or prematurely redesigning all connection code.

Test with a URI-shaped shared in-memory DSN such as
`file:dbx_sqlite_open?mode=memory&cache=shared`; verify open, ping, query, and
close. Ensure no CGo compiler or SQLite system library is needed.

**Verify**: `CGO_ENABLED=0 go test ./internal/sqlite -count=1 -v` → PASS.

### Step 2: Make configuration explicitly recognize test SQLite connections

Update `validateAndNormalize` to accept only `mysql` and `sqlite`, retaining an
empty driver default of `mysql`. For `sqlite`, require a non-empty trimmed DSN
and reject any non-empty host, port, user, password, `password_env`, or
database field with a field-specific error. Do not resolve `password_env` for a
SQLite connection. Retain current MySQL validation exactly, including default
port and password truth table.

Add `config` tests for a valid SQLite DSN; missing DSN; forbidden field values;
and a regression proving a MySQL connection is unchanged. Continue using
existing `dev`/`staging`/`prod`/`readonly` environment labels; do not add a
`test` label merely for this connector.

**Verify**: `go test ./internal/config -count=1` → PASS.

### Step 3: Dispatch query connections without weakening policy

In `internal/query/run.go`, preserve the current order: first
`ValidateQuery(sqlText)`, then select the opener by normalized
`conn.Driver` (`mysql.Open` or `sqlite.Open`), then call the existing `Run`.
Return an explicit unsupported-driver error for any impossible value; config
normally prevents it, but `RunConnection` is public and can be called directly
by tests. Do not put a generic `sql.Open` call in `query`; each connector owns
its DSN rules and driver registration.

Replace the optional MySQL-only integration coverage with an always-on SQLite
round-trip test in `internal/query` that:

1. opens a fixture connection to a unique shared in-memory URI and keeps its
   seed `*sql.DB` alive;
2. creates and seeds a simple table through that fixture only;
3. calls `query.RunConnection` with `Driver: "sqlite"` and a `SELECT`;
4. asserts pretty JSON, nested JSON string handling if SQLite returns text, and
   the same read-only denial behavior for `DELETE` before any SQLite open.

Retain the optional MySQL integration test as MySQL-specific coverage. The
SQLite test is not a dialect parity claim: limit its SQL to portable `SELECT`
and basic literals.

**Verify**: `CGO_ENABLED=0 go test ./internal/query -count=1 -v` → PASS with
the SQLite round trip executed, not skipped.

### Step 4: Guard non-query commands and document the test-only boundary

Before `dbx ddl` reaches `ddl.FetchConnection`, reject a resolved connection
whose `Driver` is not `mysql` with an error such as `ddl only supports mysql`;
assert the fetch function is never invoked. This avoids turning SQLite config
acceptance into a confusing MySQL DSN error or accidental SQLite DDL promise.

In the Spanish README, add a concise “SQLite para pruebas” section with a
config example using only `driver: sqlite`, a local/test-only `dsn`, and
`env: dev`; document that it supports `dbx query` integration tests and is not
a production engine or DDL target. Do not document it beside the main MySQL
connection as an equivalent user-facing runtime option.

**Verify**: `go test ./cmd/dbx -count=1` → PASS; add the sqlite `ddl` rejection
test with empty stdout and no fetch call.

### Step 5: Run the complete portable verification set

Run all commands from “Commands you will need”, with `CGO_ENABLED=0` for the
full suite and build. Confirm `git diff --check` is clean and only Scope files
changed. Update Plan 006's index row to DONE.

**Verify**: `CGO_ENABLED=0 go test ./... -count=1 && go vet ./... && go build -o /tmp/dbx ./cmd/dbx && git diff --check` → all commands exit 0.

## Test plan

- `internal/sqlite/open_test.go`: valid shared-memory URI, ping/query,
  `MaxOpenConns` behavior if observable through fixture use, and malformed DSN
  error without leaking the DSN contents.
- `internal/config/config_test.go`: valid/invalid SQLite field matrix plus
  unchanged MySQL normalization.
- `internal/query` integration test: actual SQLite seed/query JSON output;
  `DELETE` remains rejected before driver open; unsupported direct driver fails
  clearly.
- `cmd/dbx/ddl_test.go`: sqlite connection rejects before fetch and writes no
  stdout.

## Done criteria

- [ ] `driver: sqlite` requires only a DSN and rejects MySQL credential fields.
- [ ] A CGo-free, shared in-memory SQLite test exercises `query.RunConnection`
  during ordinary `go test ./...` without MySQL credentials.
- [ ] MySQL's existing DSN safety, query allowlist, and optional live test
  remain unchanged and passing.
- [ ] `dbx ddl` explicitly rejects SQLite before any connection/fetch.
- [ ] `CGO_ENABLED=0 go test ./... -count=1`, `go vet ./...`, build, and
  `git diff --check` pass.
- [ ] README documents the testing-only restriction and Plan 006 is DONE.

## STOP conditions

- The selected `modernc.org/sqlite` release cannot build with the repository's
  declared Go version or with `CGO_ENABLED=0` on the supported CI platforms.
- The driver requires manually pinning a `modernc.org/libc` version contrary to
  its own resolved module graph; stop and report the dependency conflict.
- Supporting SQLite would require changing MySQL query behavior, MySQL DSN
  construction, or allowing mutations.
- The test fixture loses its in-memory schema between seed and query despite a
  kept-open shared-memory connection; report the observed DSN/pool behavior
  instead of switching to a disk-backed fixture without approval.

## Maintenance notes

SQLite exists here to make tests portable, not to promise dialect parity.
Keep its test SQL portable and its connection semantics isolated in
`internal/sqlite`. If the product later needs real SQLite support, create a new
design plan covering DDL, dialect-specific policy, type mapping, documentation,
and release-size impact rather than expanding this test connector ad hoc.
