# Plan 005: Expose the complete MVP through Neovim

> **Executor instructions**: Read Plans 001–004 before changing code. Follow
> every step and verification command. Do not add a table browser, completion
> engine, or database driver to Lua. Update the index when complete.
>
> **Drift check (run first)**: `git diff --stat 89dea03..HEAD -- cmd/dbx README.md lua plugin plans`

## Status

- **Priority**: P1
- **Effort**: M
- **Risk**: MED — CLI process I/O and buffer lifecycle need an explicit,
  minimal interface.
- **Depends on**: Plans 001, 002, 003, and 004
- **Category**: direction, dx, docs
- **Planned at**: commit `89dea03`, 2026-07-09

## Why this matters

The project's principal promise is inspecting an order from Neovim without an
IDE (`README.md:25-35`), but the repository contains no Lua files and therefore
no editor entry point. Once the CLI commands exist, a thin plugin can make the
workflow real while keeping database access and data logic in Go as the README
requires.

## Current state

- `cmd/dbx/query.go` reads SQL from stdin and prints pretty JSON only on
  success; it caches the result for snapshots.
- `cmd/dbx/ddl.go` prints SQL or `--json`; `cmd/dbx/main.go` exposes all command
  names but Plans 002–004 implement the remaining commands.
- Plan 001 adds explicit `dbx snapshot --from-last`, which the plugin must use
  instead of relying on TTY detection in a job process.
- No `lua/`, `plugin/`, test harness, or Neovim version requirement exists.
- The desired commands are listed at `README.md:130-142`: `:DbRun`, `:DbDDL`,
  `:DbSnapshot`, `:DbDiff`, and `:DbPath`.

## Commands you will need

| Purpose | Command | Expected on success |
|---|---|---|
| CLI baseline | `go test ./... -count=1 && go vet ./...` | exit 0 |
| Build CLI | `go build -o /tmp/dbx ./cmd/dbx` | exit 0 |
| Neovim version | `nvim --version` | version 0.10 or newer |
| Lua smoke test | `nvim --headless -u NONE -l tests/nvim_smoke.lua` | exit 0, no output |

## Scope

**In scope**

- New `lua/dbx/init.lua`, `plugin/dbx.lua`, and `tests/nvim_smoke.lua`
- `README.md`
- Minimal changes needed to make existing CLI command invocation testable

**Out of scope**

- Any Lua database driver, tree/table explorer, schema browser, completion,
  history UI, PostgreSQL support, or custom floating-window framework
- Changing CLI JSON shapes from Plans 001–004
- Package-manager-specific installation files (leave integration to runtimepath)

## Git workflow

- Branch: `codex/neovim-mvp-client`
- Commit message: `feat(nvim): add minimal dbx client`.

## Steps

### Step 1: Establish a small Lua public API and plugin entry point

Create `lua/dbx/init.lua` with `setup(opts)` and create `plugin/dbx.lua` that
registers user commands. Require Neovim 0.10+ and use `vim.system` for async
process execution; fail with a clear `vim.notify` message if the executable or
configured connection is absent. Configuration must be only:

```lua
require("dbx").setup({ executable = "dbx", connection = "local_wms" })
```

Use a supplied `:DbRun [connection]` argument as a one-call override. Keep all
state in buffers or in dbx's `.dbx` cache; do not retain credentials in Lua.
Add a headless smoke test that prepends the repository to `runtimepath`, loads
the plugin, calls `setup`, and asserts all commands are registered.

**Verify**: `nvim --headless -u NONE -l tests/nvim_smoke.lua` → exit 0.

### Step 2: Implement one safe asynchronous command runner

In `lua/dbx/init.lua`, implement a private wrapper around `vim.system` that
accepts argv and optional stdin, captures stdout/stderr, schedules all UI work
with `vim.schedule`, and returns errors through `vim.notify`. On process
success, open or reuse a scratch buffer (`buftype=nofile`, `bufhidden=wipe`,
`swapfile=false`) in a split, assign requested filetype, set lines from stdout,
and never execute stdout as Vimscript/Lua.

Use this wrapper for:

- `:DbRun [connection]`: visual selection if present, otherwise the full
  buffer; run `dbx query --conn <connection>` with that text as stdin; show a
  `json` result buffer.
- `:DbDDL [table]`: argument or `<cword>`; run `dbx ddl --conn <connection>
  --table <table>`; show an `sql` result buffer.
- `:DbSnapshot <name>`: require a name; run `dbx snapshot --from-last --name
  <name>` with no query stdin; show the returned path via notification.

For each invocation, stderr must be visible only on error; never overwrite the
source SQL buffer.

**Verify**: extend `tests/nvim_smoke.lua` with a fake executable script in a
temporary directory. It must assert argv/stdin capture and scratch-buffer
filetypes without using a real database.

### Step 3: Add the remaining result commands

Add:

- `:DbDiff <before> <after>` → `dbx diff <before> <after>`, rendered in a
  `diff` buffer;
- `:DbPath [snapshot] <path>` → with a snapshot call
  `dbx path --snapshot <snapshot> <path>`, otherwise `dbx path <path>`,
  rendered as `json`;
- `:DbDanger` → analyze visual selection/full buffer with `dbx danger --conn
  <connection>` and render its JSON result in a `json` buffer.

Do not call `DbDanger` automatically before `DbRun`: `query` intentionally
allows only inspection SQL, and automatic duplicate process calls would slow
the common safe path. Users can invoke it explicitly before considering a
write elsewhere.

**Verify**: the headless test invokes each command against the fake executable
and asserts command registration, argv, stdin, result buffer options, and an
error notification path.

### Step 4: Document installation and the end-to-end workflow

Add a short Spanish README section: minimum Neovim version, runtimepath/plugin
installation expectation, `setup` example, required dbx config connection,
all six commands, input source rules, and that database credentials remain in
dbx configuration. Include an end-to-end example: select query → `:DbRun` →
`:DbSnapshot before` → application change → `:DbRun` → `:DbSnapshot after` →
`:DbDiff before after` → `:DbPath before metadata.fulfillment.status`.

**Verify**: `go test ./... -count=1 && go vet ./... && go build -o /tmp/dbx ./cmd/dbx && nvim --headless -u NONE -l tests/nvim_smoke.lua` → all exit 0.

## Test plan

- Test Lua using a fake executable that records arguments/stdin and emits known
  JSON, SQL, diff, and error outputs; never use an actual MySQL instance.
- Cover missing configuration/name, process error, visual/full-buffer source
  selection, and no mutation of source buffer.
- Keep Go tests unchanged except any narrow seam required by the plugin's
  documented invocation.

## Done criteria

- [ ] The five requested Neovim commands plus `:DbDanger` are registered.
- [ ] `DbRun`, `DbDDL`, `DbPath`, and `DbDanger` render output in correctly
  typed scratch splits; `DbSnapshot` uses `--from-last`; `DbDiff` uses diff.
- [ ] Process failures show stderr through notification and do not alter source
  buffers.
- [ ] The headless Lua smoke test and all Go verification commands pass.
- [ ] README provides configuration and end-to-end usage; Plan 005 is DONE.

## STOP conditions

- Neovim available to the executor is below 0.10 or lacks `vim.system`.
- A required interaction needs an unspecifed picker/UI framework.
- The CLI contracts from Plans 001–004 differ from those listed here.

## Maintenance notes

Keep Lua an adapter only. New data operations belong in the Go CLI first with
stdout-purity tests; then the plugin may add a single command that calls that
contract. Review buffer options and subprocess argv carefully because they are
the plugin's security boundary.
