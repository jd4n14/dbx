# Plan 003: Filter result data by a bounded path syntax

> **Executor instructions**: Follow the steps and run all listed checks. Stop
> rather than broadening the path language beyond this plan. Mark Plan 003
> DONE in `plans/README.md` only after every criterion passes.
>
> **Drift check (run first)**: `git diff --stat 89dea03..HEAD -- cmd/dbx/main.go internal/snapshot README.md`
> Read completed Plan 001 before editing: its raw-JSON contract is required.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — an ambiguous selector language will be hard to change later.
- **Depends on**: `plans/001-secure-snapshot-persistence.md`
- **Category**: direction, tests
- **Planned at**: commit `89dea03`, 2026-07-09

## Why this matters

Large JSON rows are only useful if a user can focus on a nested field without
manually reading the entire document. The MVP explicitly accepts “JSONPath or
a similar path” (`README.md:97-105`), while `path` is currently a stub in
`cmd/dbx/main.go:28-30`. A small, documented subset is safer and easier to
test than claiming complete JSONPath compatibility.

## Current state

- `snapshot.LastResult.Data` and `snapshot.Snapshot.Data` are raw JSON data,
  not envelopes (`internal/snapshot/envelope.go:21-40`).
- `snapshot.ReadLast` and `snapshot.Load` are the only approved source loaders
  after Plan 001.
- `cmd/dbx/snapshot.go:202-224` buffers JSON before writing it to stdout; use
  the same stdout-purity pattern.
- No third-party dependencies currently implement JSONPath.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Selector tests | `go test ./internal/path -count=1 -v` | all pass |
| CLI tests | `go test ./cmd/dbx -count=1` | all pass |
| Full suite | `go test ./... -count=1` | all packages pass |
| Static checks | `go vet ./...` | exit 0 |

## Scope

**In scope**

- New `internal/path/` package and tests
- New `cmd/dbx/path.go` and tests
- `cmd/dbx/main.go`, `cmd/dbx/query_test.go`, `README.md`

**Out of scope**

- Full JSONPath features (filters, scripts, recursive descent, unions)
- Reading arbitrary host files or accepting a source file path
- Snapshot writes, diff, danger, and Neovim UI

## Git workflow

- Branch: `codex/json-path-filter`
- Commit message: `feat(path): add snapshot JSON path filtering`.

## Steps

### Step 1: Implement a strict, small selector grammar

Create `internal/path` with a parser and evaluator. Support only:

- dotted object fields: `metadata.fulfillment.status`;
- array index: `items[0]`;
- array wildcard: `items[*].id`;
- implicit mapping over the root snapshot row array, so
  `metadata.status` evaluated against `[ {"metadata":{"status":"x"}} ]`
  yields `["x"]`.

The output is always a JSON array of matches in encounter order, even for a
single scalar. Missing fields, type mismatches, and out-of-range indexes yield
no match for that branch; malformed syntax is an error. Reject empty paths,
leading/trailing dots, negative indexes, quoted key syntax, `..`, `$`, filters,
and script expressions. Decode with `UseNumber` and marshal raw selected values
without losing large identifiers. Add parser/evaluator tests for every allowed
form and rejection listed above.

**Verify**: `go test ./internal/path -count=1 -v` → PASS.

### Step 2: Wire a single-source `dbx path` command

Implement:

`dbx path [--snapshot <name>] [--dir <path>] <path>`

With `--snapshot`, load that named snapshot. Without it, load
`.dbx/last.json`; this makes the command work immediately after `dbx query`.
Do not accept stdin in this command. Reject unexpected positional arguments,
validate snapshot names, evaluate against the `Data` field only, pretty-print
the result array with two-space indentation and a newline, then write once to
stdout. Wire the command and remove only `path` from the old stub list.

**Verify**: `go test ./cmd/dbx -count=1` → PASS. Add temp-directory CLI tests
for last-result source, named snapshot source, wildcard, no match (`[]`), bad
syntax, missing snapshot, and zero stdout on failure.

### Step 3: Document the intentional non-JSONPath contract

Update the Spanish README command examples to include both source forms and a
table of the three supported selectors. State plainly that this is a bounded
path syntax, not a full JSONPath implementation, and that root rows are mapped
implicitly. Keep command output as JSON so it can feed Neovim buffers and Unix
tools.

**Verify**: `go test ./... -count=1 && go vet ./...` → exit 0.

## Test plan

- Use standalone raw-JSON fixtures in `internal/path` and `json.Decoder`
  configured with `UseNumber`.
- Test nested maps, an array wildcard, implicit root mapping, branch failures,
  syntax rejection, and a value larger than JavaScript's safe integer range.
- Mirror snapshot command tests for CLI flag and stdout-purity behavior.

## Done criteria

- [ ] The three documented selector forms work and all output is valid pretty
  JSON arrays.
- [ ] `dbx path` reads last result by default and a named snapshot with
  `--snapshot`.
- [ ] Unsupported JSONPath syntax is rejected, never silently misinterpreted.
- [ ] `go test ./... -count=1` and `go vet ./...` pass.
- [ ] README is updated and Plan 003 is DONE.

## STOP conditions

- Plan 001's validated source loaders are unavailable.
- Supporting a requested selector would need code evaluation, SQL, or an
  unbounded full-JSONPath grammar.
- Output semantics need scalar-or-array variability rather than the stable
  array contract; request a product decision.

## Maintenance notes

The grammar is a user-facing API. Add new syntax only with parser tests and
documentation; do not reinterpret a currently-invalid expression later.
