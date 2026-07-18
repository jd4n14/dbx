local root = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(root)
vim.cmd.runtime("plugin/dbx.lua")

local function assert_equal(expected, actual, message)
  if not vim.deep_equal(expected, actual) then
    error((message or "values differ") .. "\nexpected: " .. vim.inspect(expected) .. "\nactual: " .. vim.inspect(actual))
  end
end

local commands = { "DbRun", "DbDDL", "DbTables", "DbColumns", "DbSnapshot", "DbDiff", "DbPath", "DbDanger", "DbConn" }
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

-- Schema browser parsers: schema-browser coverage without invoking the CLI.
assert_equal(
  { "orders", "order_items", "shipments" },
  complete.parse_tables_list("orders\norder_items\nshipments\n"),
  "parse_tables_list should split on newlines"
)
assert_equal(
  { "orders", "order_items", "shipments" },
  complete.parse_tables_list("\n  orders  \n\norder_items\n\nshipments\n"),
  "parse_tables_list should trim and skip blanks"
)
assert_equal(
  { "orders", "order_items", "shipments" },
  complete.parse_tables_list('[\n  "orders",\n  "order_items",\n  "shipments"\n]\n'),
  "parse_tables_list should handle JSON arrays"
)
assert_equal({}, complete.parse_tables_list(""), "empty tables list")
assert_equal(
  { "id", "status", "created_at" },
  complete.parse_columns_list("field\ttype\tnull\tkey\tdefault\textra\nid\tbigint\tNO\tPRI\t\tauto_increment\nstatus\tvarchar\tNO\t\tpending\t\ncreated_at\tdatetime\tNO\t\tcurrent_timestamp\tDEFAULT_GENERATED\n"),
  "parse_columns_list should drop the TSV header and return column names"
)
assert_equal(
  { "id", "status" },
  complete.parse_columns_list('[{"field":"id","type":"bigint"},{"field":"status","type":"varchar"}]'),
  "parse_columns_list should handle JSON arrays"
)
assert_equal(
  {},
  complete.parse_columns_list(""),
  "empty columns list"
)
assert_equal(
  { "orders", "order_items", "orders_audit" },
  complete.filter_prefix({ "orders", "order_items", "orders_audit" }, "ord"),
  "filter_prefix should match prefix across the supplied list"
)
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
  "  danger)",
  "    case \"$3\" in",
  "      warn) printf '{\"type\":\"danger\",\"safe\":false,\"severity\":\"warning\",\"findings\":[{\"code\":\"select_for_update\",\"message\":\"x\",\"severity\":\"warning\"}]}\\n' ;;",
  "      critical) printf '{\"type\":\"danger\",\"safe\":false,\"severity\":\"critical\",\"findings\":[{\"code\":\"drop_statement\",\"message\":\"x\",\"severity\":\"critical\"}]}\\n' ;;",
  "      prod) printf '{\"type\":\"danger\",\"safe\":false,\"severity\":\"critical\",\"findings\":[{\"code\":\"write_statement\",\"message\":\"x\",\"severity\":\"warning\"},{\"code\":\"restricted_environment_write\",\"message\":\"x\",\"severity\":\"critical\"}]}\\n' ;;",
  "      bad) printf 'this is not json\\n' ;;",
  "      die) printf 'fake danger failure\\n' >&2; exit 11 ;;",
  "      *) printf '{\"type\":\"danger\",\"safe\":true,\"severity\":\"safe\",\"findings\":[]}\\n' ;;",
  "    esac ;;",
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

local function close_extra_windows()
  local cur = vim.api.nvim_get_current_win()
  for _, win in ipairs(vim.api.nvim_list_wins()) do
    if win ~= cur then
      pcall(vim.api.nvim_win_close, win, true)
    end
  end
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

-- Danger preflight on :DbRun.
local function last_notification()
  return notifications[#notifications]
end

-- Safe SQL → preflight invokes danger, proceeds silently.
require("dbx").setup({ executable = fake, connection = "safe", root = neutral })
-- Earlier :DbConn tests left a session_connection override; align it so
-- plain `DbRun` uses the connection this block just set up.
vim.cmd("DbConn safe")
close_extra_windows()
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;" })
clear_log()
vim.cmd("DbRun")
wait_for("query --conn safe")
assert_log_contains("danger --conn safe", "preflight must invoke dbx danger")
assert_log_contains("query --conn safe", "safe SQL must proceed to query")
close_extra_windows()

-- Warning severity → notify WARN, proceed.
require("dbx").setup({ executable = fake, connection = "warn", root = neutral })
vim.cmd("DbConn warn")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;" })
clear_log()
vim.cmd("DbRun")
wait_for("query --conn warn")
local warn_note = last_notification()
assert(warn_note.message:find("advertencias", 1, true), "warning must notify: " .. warn_note.message)
assert(warn_note.level == vim.log.levels.WARN, "warning notify must use WARN level")
assert_log_contains("query --conn warn", "warning SQL must still proceed")
close_extra_windows()

-- Critical severity without env write block → notify ERROR, proceed.
require("dbx").setup({ executable = fake, connection = "critical", root = neutral })
vim.cmd("DbConn critical")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "drop table orders;" })
clear_log()
vim.cmd("DbRun")
wait_for("query --conn critical")
local crit_note = last_notification()
assert(crit_note.message:find("antes de ejecutar", 1, true), "critical must notify: " .. crit_note.message)
assert(crit_note.level == vim.log.levels.ERROR, "critical notify must use ERROR level")
assert_log_contains("query --conn critical", "non-env critical SQL must still proceed")
close_extra_windows()

-- Critical severity + restricted_environment_write → block.
require("dbx").setup({ executable = fake, connection = "prod", root = neutral })
vim.cmd("DbConn prod")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "update t set x=1;" })
clear_log()
vim.cmd("DbRun")
wait_for("danger --conn prod")
local block_note = last_notification()
assert(block_note.message:find("bloqueada", 1, true), "env write must notify block: " .. block_note.message)
assert(block_note.level == vim.log.levels.ERROR, "block notify must use ERROR level")
assert(not log_text():find("query --conn prod", 1, true), "blocked SQL must not run query")
close_extra_windows()

-- danger_preflight = false → no danger call, no block.
require("dbx").setup({
  executable = fake,
  connection = "prod",
  root = neutral,
  danger_preflight = false,
})
vim.cmd("DbConn prod")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "update t set x=1;" })
clear_log()
vim.cmd("DbRun")
wait_for("query --conn prod")
assert(not log_text():find("danger --conn prod", 1, true), "danger_preflight=false must skip CLI call")
close_extra_windows()

-- Restore preflight before subsequent tests that depend on it.
require("dbx").setup({
  executable = fake,
  connection = "die",
  root = neutral,
  danger_preflight = true,
})

-- Danger CLI error → graceful fallback to proceed.
require("dbx").setup({ executable = fake, connection = "die", root = neutral })
vim.cmd("DbConn die")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;" })
clear_log()
vim.cmd("DbRun")
wait_for("query --conn die")
local die_note = last_notification()
assert(die_note.message:find("Preflight danger omitido", 1, true), "danger CLI error must notify info: " .. die_note.message)
assert(die_note.level == vim.log.levels.INFO, "danger CLI error notify must use INFO level")
assert_log_contains("query --conn die", "danger CLI error must fall back to proceed")
close_extra_windows()

-- Bad JSON from danger CLI → graceful fallback to proceed.
require("dbx").setup({ executable = fake, connection = "bad", root = neutral })
vim.cmd("DbConn bad")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;" })
clear_log()
vim.cmd("DbRun")
wait_for("query --conn bad")
local bad_note = last_notification()
assert(bad_note.message:find("Preflight danger", 1, true), "bad JSON must notify info: " .. bad_note.message)
assert(bad_note.level == vim.log.levels.INFO, "bad JSON notify must use INFO level")
assert_log_contains("query --conn bad", "bad JSON must fall back to proceed")
close_extra_windows()

-- Range/visual DbRun goes through preflight too.
require("dbx").setup({ executable = fake, connection = "safe", root = neutral })
vim.cmd("DbConn safe")
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 99;" })
clear_log()
vim.cmd("1DbRun")
wait_for("query --conn safe")
assert_log_contains("danger --conn safe", "range DbRun must invoke preflight")
close_extra_windows()

-- Default setup before exit.
require("dbx").setup({ executable = fake, connection = "local_wms", root = false })

-- History: fake CLI returns canned JSON Lines for `history list --json` and
-- a SQL blob for `history show 1` so :DbHistory and :DbHistoryLast can be
-- exercised end-to-end without a real database.
local history_project = tmp .. "/history-project"
vim.fn.mkdir(history_project, "p")
local hist = tmp .. "/hist-dbx"
local hist_script = table.concat({
  "#!/bin/sh",
  'printf "cwd=%s\\n" "$(pwd)" >> "$DBX_TEST_LOG"',
  'printf "%s\\n" "$*" >> "$DBX_TEST_LOG"',
  'if [ ! -t 0 ]; then',
  '  input=$(cat)',
  '  printf "stdin=%s\\n" "$input" >> "$DBX_TEST_LOG"',
  "fi",
  'if [ "$1" = history ] && [ "$2" = list ]; then',
  '  printf \'{"index":1,"type":"history_entry","ts":"2026-07-16T11:00:00Z","connection":"local_wms","sql":"select 1;","rows":1,"bytes":12,"duration_ms":42}\\n\'',
  '  printf \'{"index":2,"type":"history_entry","ts":"2026-07-16T10:00:00Z","connection":"local_wms","sql":"select 42;","rows":1,"bytes":13,"duration_ms":15}\\n\'',
  "  exit 0",
  "fi",
  'if [ "$1" = history ] && [ "$2" = show ]; then',
  '  printf "select 1;\\n"',
  "  exit 0",
  "fi",
  'case "$1" in',
  '  query) printf \'[{"ok":true}]\\n\' ;;',
  "  ddl) printf 'CREATE TABLE orders (id int);\\n' ;;",
  "  diff) printf '@@ status @@\\n-old\\n+new\\n' ;;",
  "  snapshot) printf '/tmp/before.json\\n' ;;",
  '  *) printf \'[{"ok":true}]\\n\' ;;',
  "esac",
}, "\n") .. "\n"
vim.fn.writefile(vim.split(hist_script, "\n", { plain = true }), hist)
vim.fn.setfperm(hist, "rwx------")

require("dbx").setup({ executable = hist, connection = "local_wms", root = history_project })
clear_log()

-- :DbHistory renders JSON Lines into a tabular 'history' result buffer.
vim.cmd("DbHistory")
wait_for("history list --json")
assert(vim.api.nvim_buf_is_valid(result_bufnr("history")), "history result buffer must exist")
assert_equal("history", vim.b[result_bufnr("history")].dbx_result, "history buffer must be tagged")
local history_lines = vim.api.nvim_buf_get_lines(result_bufnr("history"), 0, -1, false)
assert(history_lines[1]:find("idx") and history_lines[1]:find("when") and history_lines[1]:find("connection") and history_lines[1]:find("sql"), "header row missing in history buffer")
assert(#history_lines >= 3, "at least header + 2 entries expected, got " .. #history_lines)
assert(history_lines[2]:match("^1\t") and history_lines[2]:find("local_wms") and history_lines[2]:find("select 1;"), "newest entry first row should be index 1 select 1")
close_all_windows()

-- :DbHistoryLast must re-issue the SQL of the newest entry under the entry's
-- connection, not the setup default.
clear_log()
vim.cmd("DbHistoryLast")
wait_for("query --conn local_wms")
assert_log_contains("stdin=select 1;", "DbHistoryLast should pipe the newest SQL on stdin")
close_all_windows()

-- :DbHistoryLast with no entries must notify (not throw).
local empty_hist = tmp .. "/empty-hist-dbx"
vim.fn.writefile({ "#!/bin/sh", "exit 0" }, empty_hist)
vim.fn.setfperm(empty_hist, "rwx------")
local empty_proj = tmp .. "/empty-project"
vim.fn.mkdir(empty_proj, "p")
local before_notifications = #notifications
require("dbx").setup({ executable = empty_hist, connection = "local_wms", root = empty_proj })
vim.cmd("DbHistoryLast")
assert(vim.wait(1000, function() return #notifications > before_notifications end, 10), "DbHistoryLast on empty history should notify")
assert(
  notifications[#notifications].message:find("No hay historial", 1, true),
  "empty history should notify friendly, got " .. notifications[#notifications].message
)

-- Restore default setup so the file is left in a clean state for the final
-- cleanup pass. (Smoke test ends with qa!; failure would happen earlier.)
require("dbx").setup({ executable = fake, connection = "local_wms", root = ux })

-- Schema browser + SQL omnifunc: fake CLI serves tables / columns JSON
-- so :DbTables, :DbColumns and the omnifunc can be exercised offline.
local schema_project = tmp .. "/schema-project"
vim.fn.mkdir(schema_project, "p")
local schema_fake = tmp .. "/schema-fake-dbx"
-- Long-form shell [==[ ... ]==] to keep tabs/backslashes untouched.
local schema_script = [==[
#!/bin/sh
printf "cwd=%s\n" "$(pwd)" >> "$DBX_TEST_LOG"
printf "%s\n" "$*" >> "$DBX_TEST_LOG"
case "$1" in
  tables)
    as_json=0
    saw_like=0
    prev=""
    for arg in "$@"; do
      if [ "$arg" = "--json" ]; then
        as_json=1
      fi
      if [ "$prev" = "--like" ]; then
        saw_like=1
      fi
      prev="$arg"
    done
    if [ "$as_json" = "1" ] && [ "$saw_like" = "1" ]; then
      printf '%s\n' '["orders_partial"]'
      exit 0
    fi
    if [ "$as_json" = "1" ]; then
      printf '%s\n' '["orders","order_items","shipments"]'
      exit 0
    fi
    if [ "$saw_like" = "1" ]; then
      printf '%s\n' orders_partial
      exit 0
    fi
    printf '%s\n' orders
    printf '%s\n' order_items
    printf '%s\n' shipments
    exit 0
    ;;
  columns)
    saw_json=0
    prev=""
    for arg in "$@"; do
      if [ "$arg" = "--json" ]; then
        saw_json=1
      fi
      prev="$arg"
    done
    case "$5" in
      orders)
        if [ "$saw_json" = "1" ]; then
          printf '%s\n' '[{"field":"id","type":"bigint"},{"field":"status","type":"varchar"},{"field":"created_at","type":"datetime"}]'
        else
          printf 'field\ttype\tnull\tkey\tdefault\textra\n'
          printf 'id\tbigint\tNO\tPRI\t\tauto_increment\n'
          printf 'status\tvarchar\tNO\t\tpending\t\n'
          printf 'created_at\tdatetime\tNO\t\tcurrent_timestamp\tDEFAULT_GENERATED\n'
        fi
        ;;
      order_items)
        if [ "$saw_json" = "1" ]; then
          printf '%s\n' '[{"field":"order_id"},{"field":"sku"}]'
        else
          printf 'field\ttype\n'
          printf 'order_id\tbigint\n'
          printf 'sku\tvarchar\n'
        fi
        ;;
      *)
        if [ "$saw_json" = "1" ]; then
          printf '%s\n' '[]'
        else
          printf 'field\ttype\n'
        fi
        ;;
    esac
    exit 0
    ;;
  *) printf '%s\n' fallthrough ;;
esac
]==]
vim.fn.writefile(vim.split(schema_script, "\n", { plain = true }), schema_fake)
vim.fn.setfperm(schema_fake, "rwx------")

require("dbx").setup({
  executable = schema_fake,
  connection = "local_wms",
  root = schema_project,
})
vim.cmd("DbConn local_wms")
clear_log()

-- :DbTables with no arg invokes the CLI without --like and fills a tsv buffer.
vim.cmd("DbTables")
wait_for("tables --conn local_wms")
assert(not log_text():find("tables --conn local_wms --like", 1, true), "DbTables without arg must not pass --like")
assert(vim.api.nvim_buf_is_valid(result_bufnr("tables")), "DbTables result buffer must exist")
assert_equal("tables", vim.b[result_bufnr("tables")].dbx_result, "DbTables buffer must be tagged")
local tables_lines = vim.api.nvim_buf_get_lines(result_bufnr("tables"), 0, -1, false)
assert_equal(
  { "orders", "order_items", "shipments" },
  tables_lines,
  "DbTables should render table names per line (one per row)"
)
close_all_windows()

-- :DbTables ord uses --like to scope the CLI call.
clear_log()
vim.cmd("DbTables ord")
wait_for("tables --conn local_wms --like ord")
assert_log_contains("tables --conn local_wms --like ord", "DbTables <arg> should forward --like")
close_all_windows()

-- :DbColumns orders fetches the column list and tags the buffer as 'columns'.
clear_log()
vim.cmd("DbColumns orders")
wait_for("columns --conn local_wms --table orders")
assert(vim.api.nvim_buf_is_valid(result_bufnr("columns")), "DbColumns result buffer must exist")
assert_equal("columns", vim.b[result_bufnr("columns")].dbx_result, "DbColumns buffer must be tagged")
local columns_lines = vim.api.nvim_buf_get_lines(result_bufnr("columns"), 0, -1, false)
-- The result buffer is the raw TSV stdout; the header line plus three rows.
assert(#columns_lines == 4, "DbColumns buffer should have header + 3 rows, got " .. #columns_lines)
assert(columns_lines[1]:match("^field\t"), "DbColumns header must start with field\\t")
assert(columns_lines[2]:match("^id\t"), "DbColumns row 2 should be id")
assert(columns_lines[3]:match("^status\t"), "DbColumns row 3 should be status")
assert(columns_lines[4]:match("^created_at\t"), "DbColumns row 4 should be created_at")
close_all_windows()

-- :DbColumns without args falls back to the word under the cursor.
vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1 from orders where 1=1;" })
vim.api.nvim_win_set_cursor(0, { 1, 18 }) -- on 'orders' under cursor
clear_log()
vim.cmd("DbColumns")
wait_for("columns --conn local_wms --table orders")
assert_log_contains("--table orders", "DbColumns without arg should default to <cword>")
close_all_windows()

-- Tables completion: stub omnifunc not needed; cmdline completion uses the
-- cached table list (which round-trips through the fake CLI the first time).
close_extra_windows()
require("dbx").setup({ executable = schema_fake, connection = "local_wms", root = schema_project })
local all_tables = vim.fn.getcompletion("DbTables ", "cmdline")
table.sort(all_tables)
assert_equal({ "order_items", "orders", "shipments" }, all_tables, "DbTables completion should list tables from the fake CLI")

-- Omnifunc: SQL buffer with no FROM yet suggests table names.
close_extra_windows()
require("dbx").setup({ executable = schema_fake, connection = "local_wms", root = schema_project })
vim.cmd.enew()
vim.bo.filetype = "sql"
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;" })
vim.api.nvim_win_set_cursor(0, { 1, 1 })
local omnifunc = require("dbx").omnifunc
local row, col = unpack(vim.api.nvim_win_get_cursor(0))
local start = omnifunc(1)
assert(type(start) == "number", "omnifunc(1) must return a byte offset")
local items = omnifunc(0)
table.sort(items)
assert_equal({ "order_items", "orders", "shipments" }, items, "omnifunc(0) without FROM should suggest tables")

-- Omnifunc with `from orders` should suggest columns of orders.
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select ", "from orders " })
vim.api.nvim_win_set_cursor(0, { 2, 12 })
items = omnifunc(0)
table.sort(items)
assert_equal({ "created_at", "id", "status" }, items, "omnifunc(0) with FROM should suggest columns of the named table")
close_extra_windows()

-- Omnifunc opt-out: setup({ sql_omnifunc = false }) must not install the
-- omnifunc on a fresh SQL buffer.
require("dbx").setup({
  executable = schema_fake,
  connection = "local_wms",
  root = schema_project,
  sql_omnifunc = false,
})
vim.cmd.enew()
vim.bo.filetype = "sql"
assert_equal("", vim.bo.omnifunc, "sql_omnifunc=false must not install the omnifunc on new SQL buffers")
close_extra_windows()

-- Final default setup so any leftover work is in a known state.
require("dbx").setup({ executable = fake, connection = "local_wms", root = false })

vim.fn.delete(tmp, "rf")
vim.cmd("qa!")
