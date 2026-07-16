local root = vim.fn.fnamemodify(debug.getinfo(1, "S").source:sub(2), ":p:h:h")
vim.opt.runtimepath:prepend(root)
vim.cmd.runtime("plugin/dbx.lua")

local function assert_equal(expected, actual, message)
  if not vim.deep_equal(expected, actual) then
    error((message or "values differ") .. "\nexpected: " .. vim.inspect(expected) .. "\nactual: " .. vim.inspect(actual))
  end
end

local commands = { "DbRun", "DbDDL", "DbSnapshot", "DbDiff", "DbPath", "DbDanger" }
for _, name in ipairs(commands) do
  assert(vim.fn.exists(":" .. name) == 2, name .. " is not registered")
end

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

vim.fn.delete(tmp, "rf")
vim.cmd("qa!")
