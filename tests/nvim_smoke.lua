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

require("dbx").setup({ executable = fake, connection = "local_wms" })

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

vim.cmd.enew()
vim.api.nvim_buf_set_lines(0, 0, -1, false, { "select 1;", "select 2;" })
local source = vim.api.nvim_get_current_buf()
vim.cmd("1DbRun override_conn")
wait_for("query --conn override_conn")
current_result("json")
assert(table.concat(vim.fn.readfile(log), "\n"):find("stdin=select 1;", 1, true), "DbRun did not pass selected SQL")
assert_equal({ "select 1;", "select 2;" }, vim.api.nvim_buf_get_lines(source, 0, -1, false), "DbRun mutated source")

vim.api.nvim_set_current_buf(source)
vim.cmd("DbDanger")
wait_for("danger --conn local_wms")
current_result("json")
assert(table.concat(vim.fn.readfile(log), "\n"):find("stdin=select 1;\nselect 2;", 1, true), "DbDanger did not pass full buffer")

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
require("dbx").setup({ executable = tmp .. "/missing", connection = "local_wms" })
vim.cmd("DbPath metadata.status")
assert_equal(before_notifications + 1, #notifications, "missing executable must notify")
assert(notifications[#notifications].message:find("No se encontró", 1, true), "missing executable notification is unclear")

require("dbx").setup({ executable = fake, connection = "" })
vim.cmd("DbRun")
assert(notifications[#notifications].message:find("Configura una conexión", 1, true), "missing connection must notify")

require("dbx").setup({ executable = fake, connection = "local_wms" })
vim.cmd("DbSnapshot")
assert(notifications[#notifications].message:find("requiere un nombre", 1, true), "missing snapshot name must notify")

-- Exercise the process-error branch through the private runner's public executable contract.
local failing = tmp .. "/failing-dbx"
vim.fn.writefile({ "#!/bin/sh", "printf 'fake failure\\n' >&2", "exit 7" }, failing)
vim.fn.setfperm(failing, "rwx------")
require("dbx").setup({ executable = failing, connection = "local_wms" })
before_notifications = #notifications
vim.cmd("DbPath metadata.status")
assert(vim.wait(3000, function() return #notifications > before_notifications end, 10), "process failure did not notify")
assert(notifications[#notifications].message:find("fake failure", 1, true), "stderr was not shown on error")

vim.fn.delete(tmp, "rf")
vim.cmd("qa!")
