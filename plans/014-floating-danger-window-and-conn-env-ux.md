# Plan 014: Floating danger window + conn@env statusline UX

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md`.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: Plan 005 (Neovim MVP client). This plan only changes presentation.
- **Category**: direction
- **Planned at**: commit `38ab8b1`, 2026-07-21
- **Shipped at**: this PR (`feat/close-original-loop`)
- **Issue**: <none>
- **Status**: DONE

## Why this matters

The README promised two presentation pieces that never landed:

1. "Los warnings peligrosos pueden mostrarse en un floating window" —
   today `:DbDanger` always opens a result split, and preflight on
   `:DbRun` only `vim.notify`s. A floating window is the natural
   "look at this, then q" surface for a warning you did not ask to
   keep around.
2. ROADMAP P3 #7 "statusline / echo de conn@env" — the active
   connection is easy to forget after a few `:DbConn` hops, and the
   env label (dev / staging / prod / readonly) is the thing that
   actually changes danger behaviour. Surfacing `conn@env` next to
   the buffer is the cheapest reminder.

Both flags are **opt-in, default OFF**, so existing muscle memory
(`:DbDanger` result buffer, `:DbConn` notify `Conexión activa: name`,
preflight notify-only) stays byte-compatible.

This slice also closes the original product loop by wiring CLI
`--max-rows` into `:DbRun` (Plan 011 already shipped the CLI) and
adding repository CI so `go test` / `go vet` / `go build` run on
every push.

## Current state

- `danger_preflight = true` (default ON). `preflight_danger` runs
  `dbx danger` before `:DbRun`.
- `decide_danger`: safe silent; warning notify WARN proceed; critical
  notify ERROR; block only for `restricted_environment_write`.
- `:DbDanger` opens a JSON result buffer via `result_buffer`.
- `:DbRun` had no `--max-rows`. CLI already has `dbx query --max-rows N`.
  Default 0 keeps the bare pretty JSON array.
- `:DbConn` notifies `Conexión activa: name`. No `vim.g.dbx_status`.
- `dbx status --json` envelope has an `env` field.
- No `.github/workflows` yet.

## Product decisions

1. `setup({ max_rows = N })` default 0. When N>0, `:DbRun` and
   history rerun pass `--max-rows N`. Do not steal `:DbRun` nargs
   for N. Envelope stays valid JSON. WARN if truncated is true.
2. `setup({ danger_float = true })` default false. When ON, show the
   danger envelope in a floating window (`nvim_open_win`
   relative=editor) for `:DbDanger` instead of the result buffer,
   and for preflight warning/critical (float plus notify). Default
   off stays notify-only / result-buffer. Preflight proceed/block
   is unchanged. `q` closes the float.
3. `setup({ statusline = true })` default false. Always export
   `M.statusline()` returning `conn@env` or just `conn`. Env comes
   from `dbx status --json` (cached, async refresh on `:DbConn` /
   setup; on failure show just conn). When ON, write `vim.g.dbx_status`.
   Do not clobber `vim.o.statusline`. Users put
   `%{get(g:,'dbx_status','')}` in their own statusline. Default
   `:DbConn` notify stays `Conexión activa: name`.
4. GitHub Actions on push/PR to main: `go test ./...`, `go vet ./...`,
   `go build -o /tmp/dbx ./cmd/dbx`, plus a separate Neovim smoke job.

## Scope

In scope: `lua/dbx/init.lua`, `tests/nvim_smoke.lua`,
`cmd/dbx/explain_test.go` (temp `--config` so CI HOME is green),
this plan file, `plans/README.md`, `README.md`,
`.github/workflows/ci.yml`.

Out of scope: new schema commands, reverting 010-013, changing the
CLI `--max-rows` contract, Postgres, writes, git config, clobbering
`vim.o.statusline`, changing default preflight proceed/block.

## Steps

### Step 1: Wire `--max-rows` into `:DbRun`

Helper `query_argv(conn)` adds `--max-rows N` when config.max_rows > 0.
Used by `:DbRun` and `rerun_history_entry`. Optional WARN on truncated.

### Step 2: `danger_float`

Float for `:DbDanger` instead of result_buffer. Preflight warning/critical
gets float plus notify. Default off unchanged.

### Step 3: `statusline` / `M.statusline()`

Always export `M.statusline()`. Cache env from `dbx status --json`.
Never assign `vim.o.statusline`.

### Step 4: GitHub Actions

`.github/workflows/ci.yml` with `go-version-file: go.mod`. Separate nvim job.
Explain tests must pass `--config` so a clean HOME is green.

### Step 5: Docs

README Neovim section (Spanish) for `max_rows`, `danger_float`,
`statusline`, and that CI exists. Mark 014 DONE.

## Test plan

- Default DbRun log does NOT contain `--max-rows`.
- After `setup({ max_rows = 50 })`: DbRun and history rerun CONTAIN `--max-rows 50`.
- Existing `query --conn local_wms` assertions still pass.
- Default `:DbDanger` still uses the result buffer.
- `danger_float=true`: floating win relative == editor.
- Default DbConn notify unchanged.
- `statusline=true` + DbConn: `vim.g.dbx_status` set; `M.statusline()` has conn name.

## Done criteria

- [x] `go test ./...` exits 0 (including HOME=/tmp/empty-home)
- [x] `go vet ./...` exits 0
- [x] `go build -o /tmp/dbx ./cmd/dbx` exits 0
- [x] `nvim --headless -u NONE -l tests/nvim_smoke.lua` exits 0
- [x] flags default OFF / max_rows 0
- [x] `.github/workflows/ci.yml` exists
- [x] `plans/README.md` status row DONE
- [x] README documents the three setup keys and CI

## STOP conditions

Stop if default preflight proceed/block would change, if wiring
`--max-rows` requires stealing `:DbRun`'s connection arg, if the CLI
envelope contract would change, or if fixing explain tests requires
production code rather than a temp `--config`.

## Maintenance notes

- `danger_float` is presentation only. Severity policy lives in `decide_danger`.
- `M.statusline()` is safe to call from a statusline expression.
  The async env refresh must never notify on failure.
- `max_rows` is a Neovim-side default for the CLI flag.
