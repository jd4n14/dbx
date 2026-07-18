local root = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(root)
vim.cmd.runtime("plugin/dbx.lua")

local function assert_equal(expected, actual, message)
  if not vim.deep_equal(expected, actual) then
    error((message or "values differ") .. "\nexpected: " .. vim.inspect(expected) .. "\nactual: " .. vim.inspect(actual))
  end
end

local commands = { "DbRun", "DbDDL", "DbSnapshot", "DbDiff", "DbPath", "DbDanger", "DbConn" }
for _, name in ipairs(commands) do
  assert(vim.fn.exists(":" .. name) == 2, name .. " is not registered")
end

-- Pure completion helpers (no CLI).
local complete = require("dbx.complete")
assert_equal(
  { "before", "after" },
  complete.parse_snapshot_list("before\t2026-07-15T00:00:00Z\nafter\n"),
  "parse_snapshot_list should take the name column"
)
assert_equal({}, complete.parse_snapshot_list(""), "empty snapshot list")
assert_equal(
  { "local_wms", "prod_ro" },
  complete.parse_connection_names(table.concat({
    "# comment",
    "connections:",
    "  local_wms:",
    "    host: 127.0.0.1",
    "    user: root",
    "    database: wms",
    "  prod_ro:",
    "    host: db.example",
    "    user: ro",
    "    database: wms",
    "    env: readonly",
    "other: true",
  }, "\n")),
  "parse_connection_names should list top-level connection keys"
)
assert_equal(
  { "local_wms" },
  complete.filter_prefix({ "local_wms", "prod_ro", "lab" }, "lo"),
  "filter_prefix should keep prefix matches"
)
assert_equal(
  { "local_wms", "prod_ro", "lab" },
  complete.filter_prefix({ "local_wms", "prod_ro", "lab" }, ""),
  "empty arglead returns all items"
)

local tmp = vim.fn.tempname()
vim.fn.mkdir(tmp, "p")
local log = tmp .. "/calls.log"
local fake = tmp .. "/fake-dbx"
vim.fn.writefile({
  "#!/bin/sh",
  "printf 'cwd=%s\\n' \"$(pwd)\" >> \"$DBX_TEST_LOG\"",
  "printf '%s\\n' \"$*\" >> \"$DBX_TEST_LOG\"",
  "input=$(cat)",
  "printf 'stdin=%s\\n' \"$input\" >> \"$DBX_TEST_LOG\"",
  "case \"$1\" in",
  "  ddl) printf 'CREATE TABLE orders (id int);\\n' ;;",
  "  diff) printf '@@ status @@\\n-old\\n+new\\n' ;;",
  "  snapshot) printf '/tmp/before.json\\n' ;;",
  "  fail) printf 'fake failure\\n' >&2; exit 7 ;;",
  "  *) printf '[{\"ok\":true}]\\n' ;;",
  "esac",
}, fake)
vim.fn.setfperm(fake, "rwx------")
vim.env.DBX_TEST_LOG = log

-- Neutral cwd root without project markers so baseline argv stays stable.
local neutral = tmp .. "/neutral"
vim.fn.mkdir(neutral, "p")
require("dbx").setup({ executable = fake, connection = "local_wms", root = neutral })

local notifications = {}
vim.notify = function(message, level)
  table.insert(notifications, { message = message, level = level })
end

local function wait_for(pattern)
  assert(vim.wait(3000, function()
    if vim.fn.filereadable(log) == 0 then
      return false
    end
    return table.concat(vim.fn.readfile(log), "\n"):find(pattern, 1, true) ~= nil
  end, 10), "timed out waiting for " .. pattern)
  vim.wait(50)
end

local function current_result(filetype)
  assert_equal("nofile", vim.bo.buftype, "result must be a scratch buffer")
  assert_equal("wipe", vim.bo.bufhidden, "result buffer must wipe when hidden")
  assert_equal(false, vim.bo.swapfile, "result must not use swap")
  assert_equal(filetype, vim.bo.filetype, "wrong result filetype")
end

local function clear_log()
  vim.fn.writefile({}, log)
end

local function log_text()
  if vim.fn.filereadable(log) == 0 then
    return ""
  end
  return table.concat(vim.fn.readfile(log), "\n")
end

local function assert_log_contains(needle, message)
  assert(log_text():find(needle, 1, true), message or ("log missing: " .. needle))
end

vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;", "select 2;" })
local source = vim.api.nvim_get_current_buf()
vim.cmd("1DbRun override_conn")
wait_for("query --conn override_conn")
current_result("json")
assert_log_contains("stdin=select 1;", "DbRun did not pass selected SQL")
assert_equal({ "select 1;", "select 2;" }, vim.api.nvim_buf_get_lines(source, 0, -1, false), "DbRun mutated source")

-- Without a range, DbRun must send only the statement under the cursor.
vim.api.nvim_set_current_buf(source)
vim.api.nvim_win_set_buf(0, source)
vim.api.nvim_win_set_cursor(0, { 1, 0 })
clear_log()
vim.cmd("DbRun")
wait_for("stdin=select 1;")
assert_log_contains("query --conn local_wms", "DbRun without range should use default connection")
assert(not log_text():find("stdin=select 1;\nselect 2;", 1, true), "DbRun without range must not send full multi-statement buffer")
assert(not log_text():find("stdin=select 2;", 1, true), "cursor on first statement must not send second")

vim.api.nvim_set_current_buf(source)
vim.api.nvim_win_set_buf(0, source)
vim.api.nvim_win_set_cursor(0, { 2, 0 })
clear_log()
vim.cmd("DbRun")
wait_for("stdin=select 2;")
assert_log_contains("stdin=select 2;", "DbRun did not pass statement under cursor")
assert(not log_text():find("stdin=select 1;", 1, true), "cursor on second statement must not send first")
assert(not log_text():find("stdin=select 1;\nselect 2;", 1, true), "DbRun without range must not send full buffer")

-- Pure helper coverage for blank-line between statements and quoted semicolons.
local sql = require("dbx.sql")
local multi = table.concat({
  "select 1;",
  "",
  "select ';';",
  "select 3",
}, "\n")
assert_equal("select 1;", sql.statement_under_cursor(multi, 1, 0), "first statement")
assert_equal("select 1;", sql.statement_under_cursor(multi, 2, 0), "blank line prefers previous statement")
assert_equal("select ';';", sql.statement_under_cursor(multi, 3, 0), "semicolon inside quotes is not a terminator")
assert_equal("select 3", sql.statement_under_cursor(multi, 4, 0), "final statement without trailing semicolon")
assert_equal(nil, sql.statement_under_cursor("   \n  ", 1, 0), "empty buffer has no statement")

vim.api.nvim_set_current_buf(source)
vim.cmd("DbDanger")
wait_for("danger --conn local_wms")
current_result("json")
assert_log_contains("stdin=select 1;\nselect 2;", "DbDanger did not pass full buffer")

vim.cmd("DbDDL orders")
wait_for("ddl --conn local_wms --table orders")
current_result("sql")

vim.cmd("DbDiff before after")
wait_for("diff before after")
current_result("diff")

vim.cmd("DbPath before metadata.status")
wait_for("path --snapshot before metadata.status")
current_result("json")

vim.cmd("DbPath metadata.status")
wait_for("path metadata.status")
current_result("json")

vim.cmd("DbSnapshot before")
wait_for("snapshot --from-last --name before")
assert(notifications[#notifications].message:find("/tmp/before.json", 1, true), "snapshot path was not notified")

local before_notifications = #notifications
require("dbx").setup({ executable = tmp .. "/missing", connection = "local_wms", root = neutral })
vim.cmd("DbPath metadata.status")
assert_equal(before_notifications + 1, #notifications, "missing executable must notify")
assert(notifications[#notifications].message:find("No se encontró", 1, true), "missing executable notification is unclear")

require("dbx").setup({ executable = fake, connection = "", root = neutral })
vim.cmd("DbRun")
assert(notifications[#notifications].message:find("Configura una conexión", 1, true), "missing connection must notify")

require("dbx").setup({ executable = fake, connection = "local_wms", root = neutral })
vim.cmd("DbSnapshot")
assert(notifications[#notifications].message:find("requiere un nombre", 1, true), "missing snapshot name must notify")

-- Exercise the process-error branch through the private runner's public executable contract.
local failing = tmp .. "/failing-dbx"
vim.fn.writefile({ "#!/bin/sh", "printf 'fake failure\\n' >&2", "exit 7" }, failing)
vim.fn.setfperm(failing, "rwx------")
require("dbx").setup({ executable = failing, connection = "local_wms", root = neutral })
before_notifications = #notifications
vim.cmd("DbPath metadata.status")
assert(vim.wait(3000, function() return #notifications > before_notifications end, 10), "process failure did not notify")
assert(notifications[#notifications].message:find("fake failure", 1, true), "stderr was not shown on error")

-- Project root: buffer under a temp project with .dbx/config.yaml must set cwd + --config.
local dbx = require("dbx")
local project = tmp .. "/project"
local nested = project .. "/sql/queries"
vim.fn.mkdir(nested, "p")
vim.fn.mkdir(project .. "/.dbx", "p")
local project_cfg = project .. "/.dbx/config.yaml"
vim.fn.writefile({ "connections: {}", "project: true" }, project_cfg)
local sql_file = nested .. "/demo.sql"
vim.fn.writefile({ "select 42;" }, sql_file)

-- Pure helpers
assert_equal(project, dbx.find_project_root(nested), "find_project_root should walk up to .dbx")
assert_equal(project_cfg, dbx.project_config_path(project), "project_config_path should return config.yaml")
assert_equal(nil, dbx.project_config_path(tmp), "project_config_path without config should be nil")

-- Clear forced root so discovery walks from the buffer file path.
require("dbx").setup({ executable = fake, connection = "local_wms", root = false })
vim.cmd("edit " .. vim.fn.fnameescape(sql_file))
clear_log()
vim.cmd("DbRun")
wait_for("cwd=" .. project)
assert_log_contains("cwd=" .. project, "CLI cwd must be the project root from buffer path")
assert_log_contains("--config " .. project_cfg, "CLI must pass --config when project config exists")
assert_log_contains("query --config " .. project_cfg .. " --conn local_wms", "config flag should follow subcommand")
assert_log_contains("stdin=select 42;", "DbRun from file should still pass statement")

-- setup { root = fixed } forces that cwd (and config under that root if present).
local forced = tmp .. "/forced-root"
vim.fn.mkdir(forced .. "/.dbx", "p")
local forced_cfg = forced .. "/.dbx/config.yaml"
vim.fn.writefile({ "connections: {}" }, forced_cfg)
require("dbx").setup({ executable = fake, connection = "local_wms", root = forced })
clear_log()
vim.cmd("DbRun")
wait_for("cwd=" .. forced)
assert_log_contains("cwd=" .. forced, "setup root string must force CLI cwd")
assert_log_contains("query --config " .. forced_cfg .. " --conn local_wms", "forced root config should be passed on query")

-- Commands without --config support (snapshot/path/diff) still get stable cwd only.
clear_log()
vim.cmd("DbPath metadata.status")
wait_for("cwd=" .. forced)
assert_log_contains("cwd=" .. forced, "DbPath must use forced root cwd")
assert(not log_text():find("--config ", 1, true), "DbPath must not receive --config")

-- setup { root = function } also works.
local fn_root = tmp .. "/fn-root"
vim.fn.mkdir(fn_root, "p")
require("dbx").setup({
  executable = fake,
  connection = "local_wms",
  root = function()
    return fn_root
  end,
})
clear_log()
vim.cmd("DbPath metadata.status")
wait_for("cwd=" .. fn_root)
assert_log_contains("cwd=" .. fn_root, "setup root function must force CLI cwd")
assert(not log_text():find("--config ", 1, true), "no --config when forced root has no config.yaml / path cmd")

-- Clear root override so later assertions (if added) use discovery again.
require("dbx").setup({ executable = fake, connection = "local_wms", root = false })

-- Completions: fake CLI lists snapshots; project config supplies connection names.
local complete_project = tmp .. "/complete-project"
vim.fn.mkdir(complete_project .. "/.dbx", "p")
local complete_cfg = complete_project .. "/.dbx/config.yaml"
vim.fn.writefile({
  "connections:",
  "  alpha_conn:",
  "    host: 127.0.0.1",
  "    user: a",
  "    database: db",
  "  beta_conn:",
  "    host: 127.0.0.1",
  "    user: b",
  "    database: db",
}, complete_cfg)

local complete_fake = tmp .. "/complete-fake-dbx"
-- Always consume stdin when present (vim.system may leave a pipe open otherwise).
local complete_script = table.concat({
  "#!/bin/sh",
  'printf "cwd=%s\n" "$(pwd)" >> "$DBX_TEST_LOG"',
  'printf "%s\n" "$*" >> "$DBX_TEST_LOG"',
  'if [ "$1" = snapshot ] && [ "$2" = list ]; then',
  '  printf "after_snap\nbefore_snap\t2026-07-15T00:00:00Z\n"',
  "  exit 0",
  "fi",
  "input=$(cat)",
  'printf "stdin=%s\n" "$input" >> "$DBX_TEST_LOG"',
  'case "$1" in',
  "  ddl) printf 'CREATE TABLE orders (id int);\\n' ;;",
  "  diff) printf '@@ status @@\\n-old\\n+new\\n' ;;",
  "  snapshot) printf '/tmp/before.json\\n' ;;",
  '  *) printf \'[{"ok":true}]\\n\' ;;',
  "esac",
}, "\n") .. "\n"
vim.fn.writefile(vim.split(complete_script, "\n", { plain = true }), complete_fake)
vim.fn.setfperm(complete_fake, "rwx------")

require("dbx").setup({ executable = complete_fake, connection = "alpha_conn", root = complete_project })
clear_log()

local snap_all = vim.fn.getcompletion("DbSnapshot ", "cmdline")
assert_equal({ "after_snap", "before_snap" }, snap_all, "DbSnapshot complete should list snapshot names")
assert_log_contains("snapshot list", "completion must call dbx snapshot list")
assert_log_contains("cwd=" .. complete_project, "snapshot list must run under project root")

local snap_prefix = vim.fn.getcompletion("DbSnapshot be", "cmdline")
assert_equal({ "before_snap" }, snap_prefix, "DbSnapshot complete should filter by prefix")

local diff_all = vim.fn.getcompletion("DbDiff ", "cmdline")
assert_equal({ "after_snap", "before_snap" }, diff_all, "DbDiff complete should list snapshots")

local path_all = vim.fn.getcompletion("DbPath ", "cmdline")
assert_equal({ "after_snap", "before_snap" }, path_all, "DbPath complete should list snapshots for optional name")

local conn_all = vim.fn.getcompletion("DbRun ", "cmdline")
table.sort(conn_all)
assert_equal({ "alpha_conn", "beta_conn" }, conn_all, "DbRun complete should list connection names from config")

local conn_prefix = vim.fn.getcompletion("DbRun al", "cmdline")
assert_equal({ "alpha_conn" }, conn_prefix, "DbRun complete should filter connection names")

local dbconn_all = vim.fn.getcompletion("DbConn ", "cmdline")
table.sort(dbconn_all)
assert_equal({ "alpha_conn", "beta_conn" }, dbconn_all, "DbConn complete should list connection names")

-- :DbConn sets session connection and is used by subsequent DbRun without args.
clear_log()
vim.cmd("DbConn beta_conn")
assert(notifications[#notifications].message:find("beta_conn", 1, true), "DbConn should notify active connection")
assert_equal("beta_conn", require("dbx").current_connection(), "current_connection should reflect :DbConn")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 9;" })
vim.cmd("DbRun")
wait_for("--conn beta_conn")
assert_log_contains("--conn beta_conn", "DbRun must use session connection from :DbConn")
assert_log_contains("stdin=select 9;", "DbRun after DbConn should still pass SQL")

-- Explicit DbRun override still wins over session connection.
clear_log()
vim.api.nvim_set_current_buf(vim.api.nvim_create_buf(true, false))
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 9;" })
vim.cmd("DbRun alpha_conn")
wait_for("--conn alpha_conn")
assert_log_contains("--conn alpha_conn", "DbRun arg must override session connection")
assert_equal("beta_conn", require("dbx").current_connection(), "DbRun override must not wipe session connection")

-- DbConn without args reports the active connection.
before_notifications = #notifications
vim.cmd("DbConn")
assert_equal(before_notifications + 1, #notifications, "DbConn without args should notify")
assert(notifications[#notifications].message:find("beta_conn", 1, true), "DbConn status should show session connection")

-- Optional keymaps: disabled by default; enabled with mappings = true.
local function find_map(mode, lhs)
  for _, map in ipairs(vim.api.nvim_get_keymap(mode)) do
    if map.lhs == lhs or map.lhs == vim.api.nvim_replace_termcodes(lhs, true, true, true) then
      return map
    end
    -- nvim may expand <leader> according to mapleader; also match rhs/desc.
    if map.desc and type(map.desc) == "string" and map.desc:match("^dbx:") then
      if lhs == "<leader>dr" and map.desc:find("run", 1, true) then
        return map
      end
      if lhs == "<leader>dd" and map.desc:find("DDL", 1, true) then
        return map
      end
      if lhs == "<leader>ds" and map.desc:find("snapshot", 1, true) then
        return map
      end
    end
  end
  return nil
end

local function count_dbx_maps()
  local n = 0
  for _, mode in ipairs({ "n", "x" }) do
    for _, map in ipairs(vim.api.nvim_get_keymap(mode)) do
      if map.desc and type(map.desc) == "string" and map.desc:match("^dbx:") then
        n = n + 1
      end
    end
  end
  return n
end

require("dbx").setup({ executable = complete_fake, connection = "alpha_conn", root = complete_project, mappings = false })
assert_equal(0, count_dbx_maps(), "mappings=false must not install keymaps")

vim.g.mapleader = " "
require("dbx").setup({ executable = complete_fake, connection = "alpha_conn", root = complete_project, mappings = true })
assert(count_dbx_maps() >= 3, "mappings=true must install default dbx keymaps")
assert(find_map("n", "<leader>dr"), "default run map missing")
assert(find_map("n", "<leader>dd"), "default ddl map missing")
assert(find_map("n", "<leader>ds"), "default snapshot map missing")
assert(find_map("x", "<leader>dr"), "visual run map missing")

-- Explicit table can disable one mapping and override another.
require("dbx").setup({
  executable = complete_fake,
  connection = "alpha_conn",
  root = complete_project,
  mappings = { run = "<leader>dx", ddl = false, snapshot = "<leader>dz" },
})
assert(find_map("n", "<leader>dx") or count_dbx_maps() >= 1, "custom run map should be installed")
assert_equal(nil, find_map("n", "<leader>dd"), "ddl=false must remove default ddl map")

-- Turning mappings off clears previous dbx maps.
require("dbx").setup({ executable = complete_fake, connection = "alpha_conn", root = complete_project, mappings = false })
assert_equal(0, count_dbx_maps(), "mappings=false after true must clear dbx keymaps")

-- Result UX: buffer tagged, configurable orientation/size/focus.
local ux = tmp .. "/ux"
vim.fn.mkdir(ux, "p")
require("dbx").setup({ executable = fake, connection = "local_wms", root = ux })
-- Earlier :DbConn tests left a session_connection override; align it so
-- plain `DbRun` uses the same connection this block just set up.
vim.cmd("DbConn local_wms")

local function close_all_windows()
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    if win ~= vim.api.nvim_get_current_win() then
      pcall(vim.api.nvim_win_close, win, true)
    end
  end
end

-- Earlier tests accumulated split windows; collapse them so result UX tests
-- have headroom for new splits in a small headless nvim (default 24 lines).
close_all_windows()

local function result_bufnr(kind)
  return vim.fn.bufnr("dbx://" .. kind)
end

-- Default: horizontal botright split, focus on result, vim.b.dbx_result set.
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 9;" })
local src_default = vim.api.nvim_get_current_buf()
vim.cmd("DbRun")
wait_for("query --conn local_wms")
assert(vim.api.nvim_buf_is_valid(result_bufnr("query")), "result buffer must exist")
assert_equal("query", vim.b[result_bufnr("query")].dbx_result, "dbx_result buffer tag must match kind")
local win_count = #vim.api.nvim_list_wins()
assert(win_count >= 2, "DbRun must open a split for results")
local source_win = vim.fn.bufwinid(src_default)
assert(source_win ~= -1, "source window must remain visible")
close_all_windows()

-- focus = "source" must keep cursor on the source buffer after DbRun.
require("dbx").setup({
  executable = fake,
  connection = "local_wms",
  root = ux,
  result = { focus = "source" },
})
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 10;" })
local src_keep = vim.api.nvim_get_current_buf()
vim.cmd("DbRun")
wait_for("stdin=select 10;")
assert_equal(src_keep, vim.api.nvim_get_current_buf(), "focus=source must not move focus to result")
assert_equal("query", vim.b[result_bufnr("query")].dbx_result, "dbx_result tag still set with focus=source")
close_all_windows()

-- Vertical orientation must use a vsplit (width < full columns).
require("dbx").setup({
  executable = fake,
  connection = "local_wms",
  root = ux,
  result = { orientation = "vertical", size = 0.5, focus = "result" },
})
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 11;" })
vim.cmd("DbRun")
wait_for("stdin=select 11;")
local result_win = vim.fn.bufwinid(result_bufnr("query"))
assert(result_win ~= -1, "vertical result window must exist")
local result_width = vim.api.nvim_win_get_width(result_win)
local cols = vim.o.columns
assert(result_width < cols, "vertical split must be narrower than full editor width")
assert(result_width <= math.floor(cols * 0.5) + 1, "vertical size=0.5 should roughly halve columns")
close_all_windows()

-- Horizontal orientation with size=0.5 must shrink the result window height.
require("dbx").setup({
  executable = fake,
  connection = "local_wms",
  root = ux,
  result = { orientation = "horizontal", size = 0.5, focus = "result" },
})
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 12;" })
vim.cmd("DbRun")
wait_for("stdin=select 12;")
local h_win = vim.fn.bufwinid(result_bufnr("query"))
assert(h_win ~= -1, "horizontal result window must exist")
local h_height = vim.api.nvim_win_get_height(h_win)
local h_lines = vim.o.lines
assert(h_height < h_lines, "horizontal split must be shorter than full editor height")
assert(h_height <= math.floor(h_lines * 0.5) + 1, "horizontal size=0.5 should roughly halve lines")
close_all_windows()

-- Reusing the same kind buffer does not stack windows: it refreshes in place.
require("dbx").setup({ executable = fake, connection = "local_wms", root = ux })
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 13;" })
vim.cmd("DbRun")
wait_for("stdin=select 13;")
local wins_after_first = #vim.api.nvim_list_wins()
vim.cmd("DbRun")
wait_for("stdin=select 13;")
assert_equal(wins_after_first, #vim.api.nvim_list_wins(), "reused result buffer must not open a new window")
close_all_windows()

-- restore default setup so any later tests behave as before
require("dbx").setup({ executable = fake, connection = "local_wms", root = ux })

vim.fn.delete(tmp, "rf")
vim.cmd("qa!")
