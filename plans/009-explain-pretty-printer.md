# Plan 009 — EXPLAIN pretty-printer

## Goal

Add `dbx explain <SQL>`, `:DbExplain`, and `:DbExplainJson` so the
`EXPLAIN` plan for a query can be surfaced as a tabular tree (human) or
JSON (machine), with the same `--json` sidecar metadata convention as
Plan 008. The goal is to replace DataGrip's "explain plan" tab with
something offline and scriptable.

## Status

- **Priority:** P2
- **Size:** S
- **Depends on:** Plan 005 (Neovim MVP client), Plan 008 (sidecar +
  atomic write helpers)
- **Status:** DONE
- **Branch:** `feat/dbx-explain-pretty-printer` (from `origin/main`)
- **PR target:** `jd4n14/dbx` from
  `g0d13:feat/dbx-explain-pretty-printer`

## Confirmed decisions (Diego sign-off)

1. **Default output is tabular (human-readable).** Verb remap:
   `dbx explain <SQL>` runs `EXPLAIN <SQL>` and prints a table with the
   canonical columns: `id, select_type, table, type, possible_keys,
   key, key_len, ref, rows, Extra`. Long `Extra` values truncate with
   `…`.
2. **`dbx explain --json` / `:DbExplainJson` produces JSON output** —
   the raw `EXPLAIN FORMAT=JSON` response as a single JSON blob on
   stdout, AND writes a sidecar `<data>.meta.json` with audit metadata
   (same schema as Plan 008). Honors `--no-json-sidecar` as opt-out.
3. **Reuse the Plan 008 helpers.** `internal/export.Sidecar` (sidecar
   generator) and `internal/export.AtomicWrite` (atomic write helper).
   Confirm helper signatures before adding new ones; copy/adapt rather
   than duplicate.
4. **Offline behavior.** No network. Reuse the existing `internal/mysql`
   driver for `EXPLAIN`. No shell-out beyond what dbx already does.

## Scope

### CLI surface

```
dbx explain [--json] [-o FILE] [--json-sidecar|--no-json-sidecar] <SQL>
dbx explain --help
```

- Default: tabular to stdout. No file written.
- `--json`: switch to JSON output (raw `EXPLAIN FORMAT=JSON`).
- `-o FILE`: when provided, write output to file using atomic write
  from Plan 008 helper. Required for JSON output if sidecar is desired.
- `--json-sidecar` / `--no-json-sidecar`: default ON (matches Plan 008).

### Neovim UX

- `:DbExplain [args]` — runs `EXPLAIN <args>` and renders result in a
  scratch buffer (tabular mode) or current buffer (JSON mode).
- `:DbExplainJson [args]` — same as `:DbExplain` but forces JSON
  output; sidecar written next to the buffer's file.
- cmdline completion for both against the connection alias + last
  statement (mirrors `:DbRun` UX completion).

### Tests (table-driven)

- Tabular: column ordering, NULL handling, `Extra` truncation with `…`.
- JSON: round-trip the sidecar meta fields; verify schema matches Plan
  008.
- Sidecar default ON / `--no-json-sidecar` paths.
- Error path: invalid SQL → friendly error (do NOT auto-route through
  Plan 004 danger preflight unless zero-cost).
- Atomic write: reuse Plan 008's failure test paths.

### Quality gate

- `go test ./...`
- `go vet ./...`
- `go build -o /tmp/dbx ./cmd/dbx`
- `nvim --headless -u NONE -l tests/nvim_smoke.lua` (extend only if
  needed; keep passing)

## STOP conditions

- Tests red, `go vet` warnings, build fail — STOP, do not push.
- Plan / file-path conflicts with repo state — STOP, report to parent.
- Any deviation from this plan's SCOPE or DONE CRITERIA — STOP, report.
- Network calls introduced — STOP, this plan is offline-only.
- Connection secrets or query text leaked into the sidecar — STOP, fix
  and report.
- `--json-sidecar` default switched to OFF — STOP, decision carries
  over from Plan 008.

## Done criteria

- `dbx explain <SQL>` renders tabular (default).
- `dbx explain --json <SQL>` produces JSON output with sidecar.
- `:DbExplain` and `:DbExplainJson` work from Neovim with cmdline
  completion.
- All quality gate commands pass.
- `plans/README.md` updated: 009 → DONE.
- PR opened against `jd4n14/dbx` from
  `g0d13:feat/dbx-explain-pretty-printer`, MERGEABLE.
- Branch pushed to fork.
- Any surfaced findings (e.g. on reusing Plan 008 helpers or
  extensions needed for later plans) flagged in the PR description.
