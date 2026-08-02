# Plan 013: `:DbHistoryRun <idx>` and history picker UX

> **Executor instructions**: Follow this plan step by step. Run every
> verification command and confirm the expected result before moving to the
> next step. If anything in the "STOP conditions" section occurs, stop and
> report — do not improvise. When done, update the status row for this plan
> in `plans/README.md`.
>
> **Drift check (run first)**: `git diff --stat 38ab8b1..HEAD -- lua/dbx/init.lua lua/dbx/complete.lua internal/history/history.go tests/nvim_smoke.lua plans/README.md`
> If any of those paths changed, compare the "Current state" excerpts against
> the live code before proceeding; on a mismatch, treat it as a STOP
> condition.

## Status

- **Priority**: P2
- **Effort**: M
- **Risk**: LOW
- **Depends on**: none — `dbx history` already exists and `:DbHistory`
  / `:DbHistoryLast` are shipped. This plan closes the loop between
  them.
- **Category**: direction
- **Planned at**: commit `38ab8b1`, 2026-07-21
- **Issue**: <none>

## Why this matters

The history path has a gap: README documents
`dbx history show <index>` and `dbx history run <index>` at the CLI
level, and `:DbHistory` shows a tabular list of recent successful
queries. But Neovim only exposes `:DbHistoryLast`, which always
re-runs the **newest** entry. To re-run an arbitrary entry the user
must: read the `:DbHistory` buffer, count the row index, switch to a
shell, type `dbx history show <N>`, copy the SQL, paste it into a
buffer, then `:DbRun`. Plan 005 explicitly deferred this UX with the
note "A required interaction needs an unspecified picker/UI
framework". That framework is now available: `vim.ui.select` is
built into Neovim 0.10+ (the minimum version enforced by
`plugin/dbx.lua:1-7`), so no extra dependency is required.

The product win is the "I just ran this 30 seconds ago, run it again"
loop — without scrolling, copying, or context-switching.

## Current state

- `internal/history/history.go:18-39` defines `Entry` (carries
  `Connection`, `SQL`, `Timestamp`, `Rows`, `Bytes`, `DurationMs`).
- `internal/history/history.go:115-127` `ShowByIndex(cwd, index)`
  returns one entry by 1-based index (1 = newest).
- `cmd/dbx/history.go` exposes the CLI subcommands
  (`list` / `show` / `run` / `clear`); `run` already exists and pipes
  the SQL into `dbx query --conn <conn>`.
- `lua/dbx/init.lua:1031-1053` defines `:DbHistory` (calls
  `dbx history list --json`, renders tabular via `render_history`).
- `lua/dbx/init.lua:1054-1075` defines `:DbHistoryLast` (calls
  `newest_history_entry()` and re-runs it through `:DbRun`'s logic).
- `lua/dbx/init.lua:710-717` `newest_history_entry()` and
  `lua/dbx/init.lua:693-707` `history_entries()` already exist and
  are testable (pure Lua calls into the CLI).
- `lua/dbx/complete.lua` has `parse_history_list` (already
  extracts `{index, ts, connection, sql}` rows from the JSON).
- `tests/nvim_smoke.lua:11-15` enumerates user commands — extend
  the list with `:DbHistoryRun`.

## Product decisions (resolved by this plan)

1. **Index-based re-run is the primary UX.** `:DbHistoryRun <idx>`
   re-runs the entry at the given 1-based index (matching the CLI).
   Range validation: `<idx>` must be a positive integer. Out-of-range
   indices produce a friendly notification.
2. **The picker is opt-in.** Add
   `setup({ history_picker = true })` (default OFF to preserve
   `:DbHistoryLast`'s zero-keypress behavior). When ON, both
   `:DbHistory` (the tabular buffer) and a new keymap provide an
   interactive re-run.
3. **The picker uses `vim.ui.select`**, not a custom float. Built-in
   since Neovim 0.6+, no dependency.
4. **`:DbHistoryRun <idx>` works without the picker.** Even with
   `history_picker = false`, the index-based command is available.
5. **No new CLI commands.** The CLI already has `dbx history run`
   (mirrored by `:DbHistoryLast`). We just expose more of it through
   Neovim.

## Commands you will need

| Purpose   | Command                          | Expected on success                |
|-----------|----------------------------------|------------------------------------|
| Tests     | `go test ./...`                  | exit 0, no failures                |
| Vet       | `go vet ./...`                   | exit 0, no warnings                |
| Build     | `go build -o /tmp/dbx ./cmd/dbx` | exit 0, binary at `/tmp/dbx`       |
| Smoke     | `nvim --headless -u NONE -l tests/nvim_smoke.lua` | exit 0         |
| Smoke     | `:DbHistoryRun 1` (in smoke)     | notification; exit 0                |

## Scope

**In scope** (the only files you should modify):
- `lua/dbx/init.lua` — add `:DbHistoryRun <idx>` command; add
  `setup({ history_picker = boolean })` config field; add
  `M.run_history_by_index(idx)` public function (for tests +
  picker); add an `on_lines` autocmd on the `dbx://history` buffer
  so `<CR>` on a row invokes `run_history_by_index` when the picker
  is enabled.
- `lua/dbx/complete.lua` — add `parse_history_index_arglead` helper
  (filter prefix on integer strings returned by
  `complete_history_indexes`).
- `tests/nvim_smoke.lua` — add `:DbHistoryRun` to the commands list;
  add at least one test that asserts the command exists and accepts
  an integer argument.
- `README.md` — append a short subsection documenting
  `:DbHistoryRun <idx>` and the `history_picker` flag.

**Out of scope** (do NOT touch, even though they look related):
- `internal/history/history.go` — already supports index lookup.
- `cmd/dbx/history.go` — `dbx history run <idx>` already exists.
- `lua/dbx/complete.lua`'s `parse_history_list` — unchanged.
- The default mappings table (`default_mappings` at
  `lua/dbx/init.lua:42-46`) — do not auto-add a `<leader>dh` keymap
  for the picker; the picker is opt-in and command-driven.
- `:DbHistoryLast` semantics — unchanged. Both commands can coexist.

## Git workflow

- Branch: `feat/nvim-history-rerun` from `origin/main`
  (commit `38ab8b1`).
- Commit per logical unit; message style:
  `feat(nvim): :DbHistoryRun <idx> and opt-in history picker`.
- Do NOT push or open a PR unless the operator instructed it.

## Steps

### Step 1: Add `M.run_history_by_index` to `lua/dbx/init.lua`

Pure helper: takes an integer index, calls
`internal.history.ShowByIndex` semantics via the CLI
(`dbx history show <idx> --json`), validates the entry has a
connection and non-empty SQL, then calls the existing
`:DbRun` rendering path (`run({ "query", "--conn", conn }, {
stdin = sql, kind = "query", filetype = "json" })` — same shape as
`newest_history_entry` at `lua/dbx/init.lua:1054-1075`).

Implementation: extract the inner "re-run this entry" logic from
`newest_history_entry`'s caller into a reusable function. Both
`:DbHistoryLast` and `:DbHistoryRun` call it.

Add input validation: non-integer or non-positive index → notify
"DbHistoryRun requires a positive integer index" (WARN).

**Verify**: `nvim --headless -u NONE -l tests/nvim_smoke.lua` exits 0.

### Step 2: Add `:DbHistoryRun` user command

In `register_commands` (`lua/dbx/init.lua:780+`), after the
`:DbHistoryLast` block, add:

```lua
vim.api.nvim_create_user_command("DbHistoryRun", function(opts)
  local arg = vim.trim(opts.args or "")
  local idx = tonumber(arg)
  if not idx or idx < 1 or math.floor(idx) ~= idx then
    notify("DbHistoryRun requires a positive integer index", vim.log.levels.WARN)
    return
  end
  M.run_history_by_index(idx)
end, {
  nargs = 1,
  complete = complete_history_indexes,
  desc = "Re-run the history entry at the given 1-based index",
})
```

`complete_history_indexes` already exists at
`lua/dbx/init.lua:685-696` and returns strings.

**Verify**: `tests/nvim_smoke.lua` exits 0 with the new command in
the registration list (line 11-15).

### Step 3: Add `history_picker` setup field

In the config table at the top of `lua/dbx/init.lua` (lines 1-30),
add `history_picker = false` (default OFF).

Add handling in `M.setup` (around `lua/dbx/init.lua:1198-1230`):
copy the `merge_table` style used for `result` and `danger_preflight`
above — simple `if opts.history_picker ~= nil then config.history_picker = opts.history_picker and true or false end`.

Add `function M.history_picker_active() return config.history_picker end`
near `M.sql_omnifunc_active` at `lua/dbx/init.lua:1237-1248` for
testability.

**Verify**: smoke test exits 0; `M.history_picker_active()` returns
`false` by default and `true` after `setup({ history_picker = true })`.

### Step 4: Wire the picker into the `dbx://history` buffer

After `render_history` populates the `dbx://history` buffer
(`lua/dbx/init.lua:735-752`), attach a one-shot `BufReadPost`
autocmd on that buffer (or a `CmdlineEnter` / mapping) so that
when the picker is enabled, `<CR>` on a row re-runs that row's
entry. Implementation sketch:

- Track the `dbx://history` bufnr via a module-local
  `history_bufnr` variable; set it inside `render_history` after
  `result_buffer` returns.
- Register a `vim.keymap.set("n", "<CR>", function()
    if not config.history_picker then return end
    local row = vim.api.nvim_win_get_cursor(0)[1]
    -- row 1 is the header; subtract 1 to get the entry index.
    M.run_history_by_index(row - 1)
  end, { buffer = history_bufnr, desc = "dbx: re-run history entry" })`
- Only install the mapping when `config.history_picker` is true.
  When the user disables the picker, the mapping is removed on the
  next setup call.

**Verify**: smoke test exits 0; manual test (out of scope for the
automated test) confirms `<CR>` invokes the helper when
`history_picker = true`.

### Step 5: Add `vim.ui.select`-based picker

Add a new helper `M.pick_history_entry()` that, when invoked
(typically via `:DbHistoryPick` user command), calls
`vim.ui.select(entries, format_item, on_choice)`. Implementation:

- `entries`: list of `{index, ts, conn, sql}` from
  `history_entries()`.
- `format_item`: `"#<idx>  <ts>  <conn>  <truncated sql>"`.
- `on_choice`: if user picks one, call `run_history_by_index(idx)`.

Wire `:DbHistoryPick` user command in `register_commands`
(same area as Step 2). Even when `history_picker = false`, the
command is available — it's a one-shot picker invocation that does
not change buffer mappings.

**Verify**: smoke test exits 0 with `:DbHistoryPick` in the
registration list.

### Step 6: Document in README

Append a short subsection under "Cliente mínimo para Neovim" (the
section listing `:DbRun`, `:DbHistory`, etc.):

- List `:DbHistoryRun <idx>` alongside `:DbHistoryLast`.
- Mention `:DbHistoryPick` (one-shot `vim.ui.select` over recent
  entries).
- Document `setup({ history_picker = true })` and its effect
  (adds `<CR>` mapping on the `dbx://history` buffer to re-run the
  row under the cursor).

**Verify**: `grep -n "DbHistoryRun\|history_picker" README.md`
returns at least 3 hits.

## Test plan

- Extend `tests/nvim_smoke.lua` with:
  - `:DbHistoryRun` and `:DbHistoryPick` registered (assertion on
    `vim.fn.exists(":DbHistoryRun") == 2`).
  - One pure helper test that `run_history_by_index` notifies
    on bad input (non-integer, zero, negative) without crashing.
  - One assertion that `M.history_picker_active()` is `false` by
    default and `true` after `setup({ history_picker = true })`.

- Do not add a full integration test of the picker (it requires a
  populated `.dbx/history.jsonl`, which the smoke harness does not
  set up). The visible-from-Neovim commands are covered by the
  registration assertions.

## Done criteria

Machine-checkable. ALL must hold:

- [ ] `go test ./...` exits 0
- [ ] `go vet ./...` exits 0
- [ ] `go build -o /tmp/dbx ./cmd/dbx` exits 0
- [ ] `nvim --headless -u NONE -l tests/nvim_smoke.lua` exits 0
- [ ] `:DbHistoryRun`, `:DbHistoryPick` appear in
      `tests/nvim_smoke.lua:11-15`
- [ ] `grep -n 'DbHistoryRun\|history_picker' README.md` returns
      at least 3 hits
- [ ] `M.history_picker_active()` returns `false` by default,
      `true` after `setup({ history_picker = true })` (smoke test)
- [ ] `plans/README.md` status row updated to DONE
- [ ] No files outside the in-scope list are modified (`git status`)

## STOP conditions

Stop and report back (do not improvise) if:

- The code at the locations in "Current state" doesn't match the
  excerpts (the codebase has drifted since this plan was written).
- A step's verification fails twice after a reasonable fix attempt.
- The fix appears to require touching an out-of-scope file
  (especially `internal/history/history.go` or `cmd/dbx/history.go`).
- `vim.ui.select` is not available in the executor's Neovim
  (it requires 0.6+; the plugin already enforces 0.10+ via
  `plugin/dbx.lua:1-7`, so this should not happen — verify).
- Refactoring `newest_history_entry`'s callers into a shared
  helper changes `:DbHistoryLast`'s behavior (it must stay
  byte-for-byte identical).

## Maintenance notes

- The picker is `vim.ui.select`-based. If Neovim's UI library
  changes shape in a future major version, the picker is the only
  call site that needs updating.
- The `history_picker` flag controls a per-buffer `<CR>` mapping.
  If `:DbHistory` is re-rendered (via another `:DbHistory` call),
  the mapping must be reinstalled on the new bufnr — the helper
  registered in Step 4 already handles this because it sets the
  mapping inside `render_history`.
- The picker does NOT auto-clear the history. If the user wants
  to prune, they shell out to `dbx history clear` (already shipped
  via Plan 005).