# Plan 004: Report dangerous SQL without executing it

> **Executor instructions**: Follow this plan literally. This plan must not
> weaken the read-only execution policy. Run each verification gate and update
> the index only when all done criteria pass.
>
> **Drift check (run first)**: `git diff --stat 89dea03..HEAD -- cmd/dbx/main.go internal/query cmd/dbx/query_test.go README.md`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: HIGH — SQL safety logic is security-sensitive; conservative false
  positives are preferable to allowing an unsafe execution path.
- **Depends on**: none
- **Category**: security, direction, tests
- **Planned at**: commit `89dea03`, 2026-07-09

## Why this matters

The stated MVP needs to flag dangerous SQL before a user acts on it
(`README.md:109-115`). There is currently no `danger` command even though it
is advertised; `cmd/dbx/main.go:28-30` returns “not implemented”. The existing
`query` command correctly denies all writes, but an analysis command gives the
Neovim UI actionable explanations and preserves the safety boundary.

## Current state

- `internal/query/policy.go:64-110` is the authoritative write barrier:
  `query.RunConnection` invokes it before opening a MySQL connection
  (`internal/query/run.go:51-66`).
- The policy already recognizes leading comments, multi-statements, `WITH`
  plus top-level DML/DDL, but its helpers are private and are not a general SQL
  parser (`internal/query/policy.go:113-283`).
- Connection labels are validated as `dev`, `staging`, `prod`, or `readonly`
  in `internal/config/config.go:48-54`; config resolution happens without a DB
  connection in `internal/config/load.go`.
- Existing command tests use stdout/stderr injection and no live MySQL.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Analysis tests | `go test ./internal/danger -count=1 -v` | all pass |
| Query policy tests | `go test ./internal/query -count=1` | all pass |
| CLI tests | `go test ./cmd/dbx -count=1` | all pass |
| Full suite | `go test ./... -count=1` | all packages pass |
| Static checks | `go vet ./...` | exit 0 |

## Scope

**In scope**

- New `internal/danger/` package and tests
- New `cmd/dbx/danger.go` and tests
- Minimal shared lexical helpers extracted from `internal/query/policy.go`, if
  needed to avoid duplicate scanners
- `cmd/dbx/main.go`, `cmd/dbx/query_test.go`, `README.md`

**Out of scope**

- Allowing `dbx query` to execute writes, confirmations, or transactions
- Replacing the heuristic with a full SQL parser
- Connecting to MySQL from `dbx danger`
- Neovim UI (Plan 005)

## Git workflow

- Branch: `codex/danger-analysis`
- Commit message: `feat(danger): analyze unsafe SQL without execution`.

## Steps

### Step 1: Specify and test an advisory result contract

Create `internal/danger` with a result envelope:

```json
{"type":"danger","safe":false,"severity":"critical","findings":[{"code":"delete_without_where","message":"..."}]}
```

Use severities `safe`, `warning`, and `critical`; findings must be stable,
machine-readable codes plus Spanish human messages. Analyze SQL without a
database call and detect at minimum:

- multiple statements;
- `DROP`, `TRUNCATE`, `ALTER`, and `CREATE INDEX`;
- `UPDATE` or `DELETE` with no top-level `WHERE`;
- any other write statement (`INSERT`, `REPLACE`, `LOAD`, `CALL`, etc.);
- `SELECT ... INTO OUTFILE`/`DUMPFILE` and `SELECT ... FOR UPDATE` as warning;
- a non-read statement on `prod` or `readonly` as critical when an environment
  label is supplied.

Use a conservative lexer that honors quoted strings and comments. Reuse or
extract the existing policy scanner; do not copy an independently drifting
comment/string implementation. `safe` must be true only if there are no
findings, while analysis itself returns a valid result (exit 0) for both safe
and unsafe SQL. Add tests that prove no DB open is possible because the package
has no DB dependency.

**Verify**: `go test ./internal/danger -count=1 -v` → PASS.

### Step 2: Preserve the query write barrier

If lexical helpers move, retain every existing `internal/query/policy_test.go`
case and add regressions for comments and quoted keywords used by the danger
tests. `query.ValidateQuery` must still deny every write before `mysql.Open`,
including CTE+DML and multi-statements. It must not start accepting `UPDATE
... WHERE` merely because danger reports it as lower severity.

**Verify**: `go test ./internal/query -count=1` → PASS with no changed policy
allowlist expectations.

### Step 3: Wire `dbx danger` as a read-only CLI command

Implement `dbx danger [--conn <name> --config <path>]` reading SQL from stdin.
When `--conn` is omitted, analyze SQL with no environment context. When it is
present, resolve config through existing discovery, validate the named
connection, and pass only `Connection.Env` to the analyzer; never construct a
DSN or open a connection. Buffer a pretty JSON result before writing stdout.
Malformed flags/config/connection/empty SQL are errors with empty stdout.
Wire the command in `cmd/dbx/main.go`, remove only `danger` from stub tests,
and document output and exit behavior in README.

**Verify**: `go test ./cmd/dbx -count=1` → PASS. Add tests for safe SELECT,
critical DELETE without WHERE, UPDATE with WHERE warning, multi-statement,
prod/readonly escalation, missing config, and stdout purity.

## Test plan

- Test SQL analysis with literal `"DELETE"`, block and line comments, nested
  CTEs, multiple statements, and non-ASCII identifiers where relevant.
- Use query's existing fake/offline policy tests as the model for proving that
  all rejection paths occur before any connection attempt.
- Assert JSON decoding of each CLI result rather than relying only on strings.

## Done criteria

- [ ] `dbx danger` returns a documented JSON envelope and never contacts MySQL.
- [ ] All minimum dangerous forms have deterministic finding codes.
- [ ] Production/readonly context escalates non-read statements to critical.
- [ ] `dbx query` remains read-only and passes all pre-existing policy tests.
- [ ] `go test ./... -count=1` and `go vet ./...` pass; README and index update.

## STOP conditions

- Implementing a rule would require parsing arbitrary SQL expressions beyond
  the conservative tokenizer.
- Refactoring shared helpers changes an existing `ValidateQuery` outcome.
- A requirement asks this command to execute, confirm, or rewrite SQL.

## Maintenance notes

This command is advisory; `query.ValidateQuery` remains the enforcement point.
Every new finding code needs a test and a documented severity so the Neovim
client can render it without scraping human text.
