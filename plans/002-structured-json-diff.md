# Plan 002: Compare snapshots structurally

> **Executor instructions**: Execute each step and verification gate in order.
> Do not modify files outside Scope. Update Plan 002 in `plans/README.md` when
> complete.
>
> **Drift check (run first)**: `git diff --stat 89dea03..HEAD -- cmd/dbx/main.go cmd/dbx/query_test.go internal/snapshot README.md`
> Also read completed Plan 001; this plan requires its validated, lossless
> snapshot contract.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — comparison semantics become an interface users will rely on.
- **Depends on**: `plans/001-secure-snapshot-persistence.md`
- **Category**: direction, tests
- **Planned at**: commit `89dea03`, 2026-07-09

## Why this matters

The primary debugging workflow is to capture before/after state and understand
what changed. `README.md:77-95` defines path-oriented structured differences,
but `cmd/dbx/main.go:28-30` still returns “not implemented” for `diff`.
A deterministic value-level diff is more useful than textual JSON comparison:
formatting and object-key order cannot hide or invent changes.

## Current state

- `internal/snapshot/store.go:61-84` loads a named snapshot; comparison must
  use only its `Data` field, not timestamp, SQL, connection, or envelope name.
- `internal/snapshot/envelope.go:21-28` keeps `Data` as `json.RawMessage`.
- `cmd/dbx/main.go:23-30` owns command dispatch and currently stubs `diff`.
- CLI tests use injected writers and assert stdout purity in
  `cmd/dbx/ddl_test.go:15-106` and `cmd/dbx/snapshot_test.go`.
- The repository uses stdlib plus small internal packages; do not add a JSON
  diff dependency for this MVP.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Diff unit tests | `go test ./internal/diff -count=1 -v` | all pass |
| CLI tests | `go test ./cmd/dbx -count=1` | all pass |
| Full suite | `go test ./... -count=1` | all packages pass |
| Static checks | `go vet ./...` | exit 0 |
| Build | `go build -o /tmp/dbx ./cmd/dbx` | exit 0 |

## Scope

**In scope**

- New `internal/diff/` package and tests
- New `cmd/dbx/diff.go` and tests
- `cmd/dbx/main.go`, `cmd/dbx/query_test.go`, `README.md`

**Out of scope**

- Snapshot storage behavior (Plan 001 owns it)
- SQL execution policy or database connections
- LCS/move-aware array matching, colors, pager integration, or Neovim code

## Git workflow

- Branch: `codex/structured-json-diff`
- Commit message style: `feat(diff): add structured snapshot comparison`.
- Do not push unless instructed.

## Steps

### Step 1: Define and test the stable comparison model

Create `internal/diff` with a typed change record containing `path`, `kind`
(`added`, `removed`, `changed`), `before`, and `after` as raw JSON. Decode
snapshot data with `json.Decoder.UseNumber`; recursively compare:

- objects by the sorted union of keys;
- arrays by index from `0` through the largest length minus one;
- scalars by semantic JSON equality, preserving `json.Number` text;
- root path as `$`, object keys as `.key` when identifier-safe and `['key']`
  otherwise, array offsets as `[n]`.

Changes must be sorted by path. This deliberately does not try to identify row
moves: arrays are positional, which is predictable for snapshots and avoids a
heuristic identity API. Add tests for nested objects, addition/removal, arrays,
changed type, equal documents, key-order independence, and large integers.

**Verify**: `go test ./internal/diff -count=1 -v` → PASS.

### Step 2: Provide text and JSON renderers

Add two renderers to `internal/diff`: default human-readable output and a
machine-readable JSON envelope. The default must produce the README style, for
example `$.rows[0].status`, then `- <pretty JSON>` and `+ <pretty JSON>` for a
changed value; use only `+` for additions and `-` for removals. Separate
changes with one blank line and end output with exactly one newline. Equal
snapshots print `no differences\n` and succeed.

The `--json` envelope must be pretty JSON with `type: "diff"`, `before`,
`after`, and ordered `changes`; raw `before`/`after` values must stay valid
JSON rather than become strings. Test both output forms byte-for-byte for a
small fixture.

**Verify**: `go test ./internal/diff -count=1` → PASS.

### Step 3: Wire `dbx diff`

Implement `dbx diff [--dir <path>] [--json] <before> <after>` in
`cmd/dbx/diff.go`. Parse flags with `flag.NewFlagSet`, validate exactly two
snapshot names using `snapshot.ValidateName`, load both through
`snapshot.Load`, compare `Data`, and buffer all output before one stdout write.
Wire it in `cmd/dbx/main.go` and remove only `diff` from the stub test. On a
bad flag, missing name, missing/corrupt snapshot, or encoding failure, write no
partial stdout and return an error for `main` to print on stderr.

Document usage, output semantics, positional-array limitation, and `--json`
in the Spanish README. Do not change snapshot envelopes.

**Verify**: `go test ./cmd/dbx -count=1` → PASS; add cases for no differences,
nested change, `--json`, missing snapshot, and stdout purity on errors.

## Test plan

- Model internal test fakes and naming after `internal/snapshot/store_test.go`.
- Test comparison independently of disk, then use temp snapshot directories in
  CLI tests as `cmd/dbx/snapshot_test.go` does.
- Include the exact large-ID regression from Plan 001 so a future decoder does
  not reintroduce precision loss.

## Done criteria

- [ ] `dbx diff before after` compares snapshot `data` structurally and
  deterministically.
- [ ] `--json` is valid pretty JSON with ordered changes.
- [ ] Equal snapshots return `no differences\n` with exit 0.
- [ ] `go test ./... -count=1`, `go vet ./...`, and build pass.
- [ ] README documents the command and Plan 002 is DONE in the index.

## STOP conditions

- Plan 001 is not DONE or snapshots still decode numbers as `float64`.
- A requested behavior requires semantic row matching instead of positional
  arrays; that needs an explicit key-selection product decision.
- A change would require an external library merely to parse JSON.

## Maintenance notes

Keep the comparison package independent of disk and CLI concerns. If a future
feature adds ignore paths or identity-based row matching, make it an explicit
option; never silently alter the default positional semantics.
