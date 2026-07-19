# Plan 008 — Snapshot export to CSV / JSON Lines

## Goal

Add `dbx export <snapshot-id>` and `:DbExport` so a saved snapshot can be
dumped to disk as CSV or JSON Lines, with an optional JSON sidecar carrying
audit metadata. This is the second deliverable of the post-MVP roadmap; the
explicit user goal is to be able to share snapshot results without opening
DataGrip.

## Status

- **Priority:** P2
- **Size:** M
- **Depends on:** Plan 001 (snapshot persistence), Plan 005 (Neovim MVP client)
- **Status:** TODO (set to DONE in the same commit that opens the PR)
- **Branch:** `feat/dbx-export-snapshots` (from `origin/main`)
- **PR target:** `jd4n14/dbx` from `g0d13:feat/dbx-export-snapshots`

## Confirmed decisions (Diego sign-off)

1. **`--json` sidecar is ON by default.** Honors `--no-json` as opt-out. The
   sidecar carries audit metadata: snapshot id, source connection alias,
   `exported_at` timestamp, row count, export format, and the dbx
   commit/version that produced it. **No query text, no connection secrets,
   no row data** — metadata only.
2. **Atomic writes only.** Write to a temp file in the same directory as the
   target, `fsync`, then `rename(2)`. Applies to both the data file and the
   sidecar — write sidecar first, then data, so a partial state never claims
   a row count that the data file doesn't yet contain.
3. **Offline behavior.** No network calls, no shell-out beyond what dbx
   already does. Format encoding uses stdlib only (`encoding/csv` for CSV;
   one-JSON-object-per-line for JSONL).

## Scope

### CLI surface

```
dbx export <snapshot-id> [--format csv|jsonl] [-o FILE] [--json|--no-json]
```

- `--format csv|jsonl` (default: `csv`)
- `-o FILE`: required unless defaults suffice. Default if omitted:
  `<snapshot-id>.<ext>` in the current directory.
- `--json` / `--no-json` (default: `--json`)
- Errors should be friendly, exit non-zero, single short line.

### Neovim UX

- `:DbExport [args]` mirrors the `:DbShow` / `:DbLoad` style. Default args:
  write the current snapshot (resolved via the same index mechanism) to the
  current buffer (or to a sibling file if a path is given).
- cmdline completion for `:DbExport` against the snapshot index (same `cmpl`
  mechanism as `:DbShow`).

### Tests (table-driven)

- CSV header row present; quoting behavior on commas, quotes, newlines
  (RFC 4180).
- JSONL one object per line; preserved row types (numbers, strings, nulls,
  booleans).
- `--json` sidecar ON and OFF paths.
- Malformed / missing snapshot id → friendly error.
- Atomic write failure path: inject a non-writable directory for the rename
  target.

### Quality gate

- `go test ./...`
- `go vet ./...`
- `go build -o /tmp/dbx ./cmd/dbx`
- `nvim --headless -u NONE -l tests/nvim_smoke.lua` (extend only if needed;
  keep passing).

## STOP conditions

- Tests red, `go vet` warnings, build fail — STOP, do not push.
- Plan or file-path conflicts with repo state — STOP, report to parent.
- Any deviation from this plan's SCOPE or DONE CRITERIA — STOP, report.
- Network calls introduced — STOP, this plan is offline-only.
- Connection secrets or query text leaked into the sidecar — STOP, fix and
  report.
- `--json` sidecar default switched to OFF — STOP, the decision is final.

## Done criteria

- `dbx export` works for CSV and JSONL with and without `--json` sidecar.
- `:DbExport` works from Neovim with cmdline completion against the snapshot
  index.
- All quality gate commands pass.
- `plans/README.md` updated: 008 → DONE, 009 → TODO; "Implemented and
  verified locally" mentions snapshot export.
- PR opened against `jd4n14/dbx` from `g0d13:feat/dbx-export-snapshots`,
  MERGEABLE.
- Branch pushed to fork.
- Any surfaced findings for 009 (EXPLAIN pretty-printer) flagged in PR
  description.

## Notes for Plan 009 (EXPLAIN pretty-printer)

- Executor fills in at PR time if any reusable helper or pattern emerges from
  export — atomic write helper, format scaffolding, sidecar generator, etc.
- `--json` sidecar default-ON decision likely extends to `dbx explain --json`
  for Plan 009.
