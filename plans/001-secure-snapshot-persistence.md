# Plan 001: Preserve and secure snapshot data

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving on. If a
> STOP condition occurs, stop and report; do not improvise. Update Plan 001 in
> `plans/README.md` only after all done criteria hold.
>
> **Drift check (run first)**: `git diff --stat 89dea03..HEAD -- cmd/dbx/query.go cmd/dbx/query_test.go cmd/dbx/snapshot.go cmd/dbx/snapshot_test.go internal/snapshot README.md .gitignore`
> The snapshot files are intentionally uncommitted work at planning time. Keep
> the current behavior unless this plan explicitly changes it.

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — it changes persisted JSON and file modes, so preserve the
  documented envelope and add regression coverage first.
- **Depends on**: none
- **Category**: correctness, security, tests
- **Planned at**: commit `89dea03`, 2026-07-09

## Why this matters

Snapshots are the shared data source for the remaining MVP commands. The
current normalization decodes JSON to `any` and re-encodes it, which converts
large JSON integers to `float64` and can silently change database identifiers.
The same code writes result data and SQL to files readable by other local users
(`0644` files under `0755` directories). Make the persistence contract
lossless, validate envelopes at the boundary, and give automated clients an
explicit way to use the last result.

## Current state

- `cmd/dbx/query.go:84-91` writes `.dbx/last.json` before returning query JSON
  on stdout.
- `internal/snapshot/envelope.go:45-61` uses `json.Unmarshal(raw, &v)` followed
  by `json.Marshal(v)` in `NormalizeData`; this loses the lexical precision of
  large numeric literals.
- `internal/snapshot/store.go:31-59` creates the snapshot directory with
  `0755`, writes data through `atomicWrite`, and returns its path.
- `internal/snapshot/last.go:24-26` writes `last.json` with `0644`; snapshots
  do the same at `internal/snapshot/store.go:151`.
- `cmd/dbx/snapshot.go:97-111` infers whether to read stdin from its file type.
  A Neovim job commonly has a non-TTY stdin, so it needs an explicit
  `--from-last` selection rather than TTY inference.
- Match the existing convention: exported package helpers plus table-driven
  Go tests in `internal/snapshot/*_test.go`; CLI helpers in
  `cmd/dbx/snapshot_test.go`; success data only on stdout.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| Package tests | `go test ./internal/snapshot ./cmd/dbx -count=1` | all tests pass |
| Full tests | `go test ./... -count=1` | all packages pass |
| Static checks | `go vet ./...` | exit 0, no findings |
| Build | `go build -o /tmp/dbx ./cmd/dbx` | exit 0 |

## Scope

**In scope**

- `internal/snapshot/envelope.go`, `last.go`, `store.go` and their tests
- `cmd/dbx/query.go`, `cmd/dbx/query_test.go`, `cmd/dbx/snapshot.go`,
  `cmd/dbx/snapshot_test.go`
- `README.md`

**Out of scope**

- `internal/query/` SQL policy and MySQL connection code
- `diff`, `path`, `danger`, and Lua code (later plans)
- changing `.dbx/` from local/gitignored storage to a shared or versioned store

## Git workflow

- Branch: `codex/secure-snapshots`
- Match existing Conventional Commit history, for example
  `feat(ddl): wire dbx ddl CLI with SQL and --json output`.
- Do not push or open a PR unless instructed.

## Steps

### Step 1: Make JSON validation lossless

Replace the `Unmarshal`/`Marshal` normalization in
`internal/snapshot/envelope.go` with validation plus byte-preserving compacting
(`json.Valid` and `json.Compact` into a buffer). The resulting
`json.RawMessage` must contain valid compact JSON without converting numbers,
while retaining the current empty/invalid error shape. Use raw JSON formatting
for `snapshot show --data` too (`json.Indent`), never unmarshal it into `any`.

Add regression tests using at least `9007199254740993` in an object and in an
array; after normalize, write/read, and `show --data`, the exact integer text
must remain present and valid JSON.

**Verify**: `go test ./internal/snapshot ./cmd/dbx -count=1` → PASS, including
the large-integer regressions.

### Step 2: Validate loaded envelopes and add an explicit last-result source

Make `ReadLast` require `type == "last_result"`, non-empty valid `data`, and a
valid JSON value. Make `Load` require `type == "snapshot"`, a name matching the
requested validated filename, a non-zero `created_at`, and valid non-empty
`data`; return contextual errors without printing stored SQL or data.

Add `--from-last` to the save form of `dbx snapshot`. It must force
`dataFromLast(cwd)` even when stdin is a pipe, and must be rejected when used
with actual JSON stdin to avoid ambiguity. Preserve current behavior when the
flag is absent: pipe/redirect uses JSON, interactive TTY uses last result.
Document `dbx snapshot --from-last --name before_split_order` as the stable
automation/Neovim invocation.

**Verify**: `go test ./cmd/dbx ./internal/snapshot -count=1` → PASS. Add CLI
tests for `--from-last`, malformed/wrong-type cache, malformed/wrong-name
snapshot, and no stdout on each error.

### Step 3: Restrict default on-disk permissions

Create default `.dbx` and `.dbx/snapshots` directories with `0700`; store
`last.json`, snapshots, and temporary files with `0600`. For existing default
directories, explicitly `Chmod` only the directories owned by dbx under
`cwd/.dbx`; do not recursively chmod a user-supplied `--dir`. A custom
directory still receives `0600` data files, but its parent mode is not dbx's
to change.

Keep same-directory temp-file plus rename atomicity. Test modes on Unix using
`os.Stat(...).Mode().Perm()`; skip only the permission assertion on platforms
where the mode cannot be represented. Update the README warning to explain
that snapshots may include sensitive rows and SQL and are intentionally
owner-readable only by default.

**Verify**: `go test ./internal/snapshot ./cmd/dbx -count=1` → PASS, and
`go build -o /tmp/dbx ./cmd/dbx` → exit 0.

## Test plan

- Extend `internal/snapshot/envelope_test.go`, `last_test.go`, and
  `store_test.go` rather than creating a second test style.
- Extend `cmd/dbx/snapshot_test.go` for source selection and stdout purity;
  extend `cmd/dbx/query_test.go` to prove query cache preserves a large ID.
- Cover valid arrays/objects, invalid data, wrong envelope types/names, a
  large integer, `--from-last`, and Unix file modes.

## Done criteria

- [ ] Snapshot and last-result data preserve `9007199254740993` exactly.
- [ ] Default dbx storage is private (`0700` directories, `0600` files).
- [ ] `dbx snapshot --from-last --name <name>` works in a non-TTY job.
- [ ] Invalid or mismatched envelopes fail before emitting stdout.
- [ ] `go test ./... -count=1`, `go vet ./...`, and the build command pass.
- [ ] No files outside Scope changed; Plan 001 is marked DONE in the index.

## STOP conditions

- The working snapshot implementation no longer matches the locations above.
- Supporting Windows requires semantics that cannot meet the stated mode tests.
- Preserving numeric lexemes conflicts with an already-released snapshot file
  format; report the compatibility evidence before adding migration logic.

## Maintenance notes

`Data` is intentionally raw JSON so later `diff` and `path` can use
`json.Decoder.UseNumber` and preserve database IDs. Review any future code that
unmarshals it into `any`: it must use `UseNumber` or operate on raw bytes.
