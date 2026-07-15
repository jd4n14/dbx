local M = {}

local config = {
  executable = "dbx",
  connection = nil,
}

local function notify(message, level)
  vim.notify(message, level or vim.log.levels.ERROR, { title = "dbx" })
end

local function output_lines(stdout)
  stdout = (stdout or ""):gsub("\r\n", "\n"):gsub("\n$", "")
  if stdout == "" then
    return { "" }
  end
  return vim.split(stdout, "\n", { plain = true })
end

local function result_buffer(kind, filetype, stdout)
  local name = "dbx://" .. kind
  local bufnr = vim.fn.bufnr(name)
  if bufnr < 0 then
    bufnr = vim.api.nvim_create_buf(false, true)
    vim.api.nvim_buf_set_name(bufnr, name)
  end

  local win = vim.fn.bufwinid(bufnr)
  if win == -1 then
    vim.cmd("botright split")
    win = vim.api.nvim_get_current_win()
    vim.api.nvim_win_set_buf(win, bufnr)
  else
    vim.api.nvim_set_current_win(win)
  end

  vim.bo[bufnr].buftype = "nofile"
  vim.bo[bufnr].bufhidden = "wipe"
  vim.bo[bufnr].swapfile = false
  vim.bo[bufnr].modifiable = true
  vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, output_lines(stdout))
  vim.bo[bufnr].filetype = filetype
  vim.bo[bufnr].modified = false
  vim.bo[bufnr].modifiable = false
end

local function executable_available(executable)
  return executable ~= nil and executable ~= "" and vim.fn.executable(executable) == 1
end

local function run(argv, opts)
  opts = opts or {}
  local executable = config.executable
  if not executable_available(executable) then
    notify("No se encontró el ejecutable dbx configurado: " .. tostring(executable))
    return
  end

  local command = { executable }
  vim.list_extend(command, argv)
  vim.system(command, { stdin = opts.stdin, text = true }, function(result)
    vim.schedule(function()
      if result.code ~= 0 then
        local detail = vim.trim(result.stderr or "")
        if detail == "" then
          detail = "el proceso terminó con código " .. result.code
        end
        notify(detail)
        return
      end
      if opts.on_success then
        opts.on_success(result.stdout or "")
      elseif opts.kind and opts.filetype then
        result_buffer(opts.kind, opts.filetype, result.stdout or "")
      end
    end)
  end)
end

local function connection(override)
  local value = override and vim.trim(override) or ""
  if value == "" then
    value = config.connection and vim.trim(config.connection) or ""
  end
  if value == "" then
    notify("Configura una conexión con require('dbx').setup({ connection = 'nombre' })")
    return nil
  end
  return value
end

local sql = require("dbx.sql")

local function buffer_text(bufnr)
  return table.concat(vim.api.nvim_buf_get_lines(bufnr, 0, -1, false), "\n")
end

local function range_source(bufnr, command_opts)
  local lines = vim.api.nvim_buf_get_lines(bufnr, command_opts.line1 - 1, command_opts.line2, false)
  local start_mark = vim.api.nvim_buf_get_mark(bufnr, "<")
  local end_mark = vim.api.nvim_buf_get_mark(bufnr, ">")
  if vim.fn.visualmode() == "v"
    and start_mark[1] == command_opts.line1
    and end_mark[1] == command_opts.line2
    and #lines > 0
  then
    lines[#lines] = lines[#lines]:sub(1, end_mark[2] + 1)
    lines[1] = lines[1]:sub(start_mark[2] + 1)
  end
  return table.concat(lines, "\n")
end

--- SQL source for range-aware commands (DbDanger keeps full buffer without range).
local function sql_source(command_opts)
  local bufnr = vim.api.nvim_get_current_buf()
  if command_opts.range and command_opts.range > 0 then
    return range_source(bufnr, command_opts)
  end
  return buffer_text(bufnr)
end

--- DbRun: range/visual selection, otherwise the statement under the cursor.
local function dbrun_source(command_opts)
  local bufnr = vim.api.nvim_get_current_buf()
  if command_opts.range and command_opts.range > 0 then
    return range_source(bufnr, command_opts)
  end

  local text = buffer_text(bufnr)
  local cursor = vim.api.nvim_win_get_cursor(0)
  local statement = sql.statement_under_cursor(text, cursor[1], cursor[2])
  if not statement or statement == "" then
    notify("No hay un statement SQL bajo el cursor")
    return nil
  end
  return statement
end

local function register_commands()
  if vim.g.loaded_dbx_commands then
    return
  end
  vim.g.loaded_dbx_commands = true

  vim.api.nvim_create_user_command("DbRun", function(opts)
    local conn = connection(opts.args)
    if not conn then
      return
    end
    local source = dbrun_source(opts)
    if source == nil then
      return
    end
    run({ "query", "--conn", conn }, { stdin = source, kind = "query", filetype = "json" })
  end, { nargs = "?", range = true, desc = "Ejecuta SQL de inspección con dbx" })

  vim.api.nvim_create_user_command("DbDDL", function(opts)
    local conn = connection()
    local table_name = vim.trim(opts.args)
    if table_name == "" then
      table_name = vim.fn.expand("<cword>")
    end
    if table_name == "" then
      notify("DbDDL requiere una tabla o una palabra bajo el cursor")
    elseif conn then
      run({ "ddl", "--conn", conn, "--table", table_name }, { kind = "ddl", filetype = "sql" })
    end
  end, { nargs = "?", desc = "Muestra el DDL de una tabla" })

  vim.api.nvim_create_user_command("DbSnapshot", function(opts)
    local name = vim.trim(opts.args)
    if name == "" then
      notify("DbSnapshot requiere un nombre")
      return
    end
    run({ "snapshot", "--from-last", "--name", name }, {
      on_success = function(stdout)
        notify("Snapshot guardado en " .. vim.trim(stdout), vim.log.levels.INFO)
      end,
    })
  end, { nargs = "?", desc = "Guarda el último resultado como snapshot" })

  vim.api.nvim_create_user_command("DbDiff", function(opts)
    if #opts.fargs ~= 2 then
      notify("DbDiff requiere <before> <after>")
      return
    end
    run({ "diff", opts.fargs[1], opts.fargs[2] }, { kind = "diff", filetype = "diff" })
  end, { nargs = "+", desc = "Compara dos snapshots" })

  vim.api.nvim_create_user_command("DbPath", function(opts)
    if #opts.fargs < 1 or #opts.fargs > 2 then
      notify("DbPath acepta [snapshot] <path>")
      return
    end
    local argv = { "path" }
    if #opts.fargs == 2 then
      vim.list_extend(argv, { "--snapshot", opts.fargs[1], opts.fargs[2] })
    else
      table.insert(argv, opts.fargs[1])
    end
    run(argv, { kind = "path", filetype = "json" })
  end, { nargs = "+", desc = "Filtra el último resultado o un snapshot" })

  vim.api.nvim_create_user_command("DbDanger", function(opts)
    local conn = connection()
    if conn then
      run({ "danger", "--conn", conn }, { stdin = sql_source(opts), kind = "danger", filetype = "json" })
    end
  end, { nargs = 0, range = true, desc = "Analiza SQL peligroso sin ejecutarlo" })
end

function M.setup(opts)
  opts = opts or {}
  if opts.executable ~= nil then
    config.executable = opts.executable
  end
  if opts.connection ~= nil then
    config.connection = opts.connection
  end
  register_commands()
end

return M
