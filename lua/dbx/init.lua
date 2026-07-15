local M = {}

local config = {
  executable = "dbx",
  connection = nil,
  ---@type string|fun():string|nil
  root = nil,
  ---@type boolean|table
  -- false/nil: no keymaps; true: default leader maps; table: explicit lhs overrides.
  mappings = false,
}

-- Session connection override set by :DbConn (takes precedence over setup.connection).
local session_connection = nil

local default_mappings = {
  run = "<leader>dr",
  ddl = "<leader>dd",
  snapshot = "<leader>ds",
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

local function is_directory(path)
  return path ~= nil and path ~= "" and vim.fn.isdirectory(path) == 1
end

local function path_exists(path)
  return path ~= nil and path ~= "" and vim.fn.filereadable(path) == 1
end

local function normalize_dir(path)
  if not path or path == "" then
    return nil
  end
  return vim.fn.fnamemodify(path, ":p"):gsub("/+$", "")
end

local function parent_dir(path)
  local parent = vim.fn.fnamemodify(path, ":h")
  if parent == path then
    return nil
  end
  return parent
end

--- True when `dir` looks like a dbx/git project root.
local function is_project_marker(dir)
  if path_exists(dir .. "/.dbx/config.yaml") then
    return true
  end
  if is_directory(dir .. "/.dbx") then
    return true
  end
  if is_directory(dir .. "/.git") or path_exists(dir .. "/.git") then
    return true
  end
  return false
end

--- Walk upward from `start_dir` looking for `.dbx/config.yaml`, `.dbx/`, or `.git`.
---@param start_dir string|nil
---@return string|nil
function M.find_project_root(start_dir)
  local dir = normalize_dir(start_dir)
  if not dir or not is_directory(dir) then
    return nil
  end

  local seen = {}
  while dir and not seen[dir] do
    seen[dir] = true
    if is_project_marker(dir) then
      return dir
    end
    dir = parent_dir(dir)
  end
  return nil
end

--- Resolve the stable project directory used as cwd for CLI calls.
--- Order:
--- 1. setup `root` string or function (if non-empty)
--- 2. walk up from current buffer file directory (named file on disk)
--- 3. walk up from Neovim cwd
--- 4. Neovim cwd
---@param bufnr integer|nil
---@return string
function M.resolve_root(bufnr)
  bufnr = bufnr or vim.api.nvim_get_current_buf()

  local override = config.root
  if type(override) == "function" then
    local ok, value = pcall(override)
    if ok and type(value) == "string" and vim.trim(value) ~= "" then
      return normalize_dir(value) or value
    end
  elseif type(override) == "string" and vim.trim(override) ~= "" then
    return normalize_dir(override) or override
  end

  local start_dir = nil
  local name = vim.api.nvim_buf_get_name(bufnr)
  if name ~= "" and path_exists(name) then
    start_dir = vim.fn.fnamemodify(name, ":p:h")
  end

  local found = M.find_project_root(start_dir)
  if found then
    return found
  end

  local cwd = vim.fn.getcwd()
  found = M.find_project_root(cwd)
  if found then
    return found
  end

  return normalize_dir(cwd) or cwd
end

--- When `<root>/.dbx/config.yaml` exists, return that path for --config.
---@param root string|nil
---@return string|nil
function M.project_config_path(root)
  root = normalize_dir(root)
  if not root then
    return nil
  end
  local path = root .. "/.dbx/config.yaml"
  if path_exists(path) then
    return path
  end
  return nil
end

--- Subcommands that accept --config (others only need stable cwd).
local config_flag_commands = {
  query = true,
  ddl = true,
  danger = true,
}

--- Insert `--config <path>` after the subcommand when supported, a project
--- config is found, and argv does not already include --config.
---@param argv string[]
---@param config_path string|nil
---@return string[]
local function with_config_flag(argv, config_path)
  if not config_path or config_path == "" then
    return argv
  end
  local sub = argv[1]
  if not sub or not config_flag_commands[sub] then
    return argv
  end
  for _, arg in ipairs(argv) do
    if arg == "--config" then
      return argv
    end
  end

  local out = {}
  for i, arg in ipairs(argv) do
    out[#out + 1] = arg
    -- Place after the subcommand (first token) so flag parsers accept it.
    if i == 1 then
      out[#out + 1] = "--config"
      out[#out + 1] = config_path
    end
  end
  return out
end

local function run(argv, opts)
  opts = opts or {}
  local executable = config.executable
  if not executable_available(executable) then
    notify("No se encontró el ejecutable dbx configurado: " .. tostring(executable))
    return
  end

  local root = M.resolve_root()
  local config_path = M.project_config_path(root)
  local final_argv = with_config_flag(argv, config_path)

  local command = { executable }
  vim.list_extend(command, final_argv)
  vim.system(command, { cwd = root, stdin = opts.stdin, text = true }, function(result)
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
    value = session_connection and vim.trim(session_connection) or ""
  end
  if value == "" then
    value = config.connection and vim.trim(config.connection) or ""
  end
  if value == "" then
    notify("Configura una conexión con :DbConn <nombre> o require('dbx').setup({ connection = 'nombre' })")
    return nil
  end
  return value
end

--- Active connection for this Neovim session (session override, then setup default).
---@return string|nil
function M.current_connection()
  local value = session_connection and vim.trim(session_connection) or ""
  if value == "" then
    value = config.connection and vim.trim(config.connection) or ""
  end
  if value == "" then
    return nil
  end
  return value
end

local sql = require("dbx.sql")
local complete = require("dbx.complete")

--- Synchronous CLI capture for completion (never blocks the UI long: snapshot list is local).
---@param argv string[]
---@return string|nil stdout
local function run_sync_stdout(argv)
  local executable = config.executable
  if not executable_available(executable) then
    return nil
  end
  local root = M.resolve_root()
  local command = { executable }
  vim.list_extend(command, argv)
  local ok, result = pcall(function()
    return vim.system(command, { cwd = root, text = true }):wait()
  end)
  if not ok or not result or result.code ~= 0 then
    return nil
  end
  return result.stdout
end

--- Snapshot names from `dbx snapshot list` under the resolved project root.
---@return string[]
local function snapshot_names()
  local stdout = run_sync_stdout({ "snapshot", "list" })
  return complete.parse_snapshot_list(stdout)
end

--- Connection names from the project (or discovered) config YAML.
---@return string[]
local function connection_names()
  local root = M.resolve_root()
  local config_path = M.project_config_path(root)
  if not config_path then
    -- Fall back to discovery paths the CLI would use under root cwd.
    local candidates = {
      root .. "/.dbx/config.yaml",
    }
    local xdg = vim.env.XDG_CONFIG_HOME
    if xdg and xdg ~= "" then
      candidates[#candidates + 1] = xdg .. "/dbx/config.yaml"
    end
    local home = vim.env.HOME or vim.fn.expand("~")
    if home and home ~= "" then
      candidates[#candidates + 1] = home .. "/.config/dbx/config.yaml"
    end
    for _, path in ipairs(candidates) do
      if path_exists(path) then
        config_path = path
        break
      end
    end
  end
  if not config_path or not path_exists(config_path) then
    return {}
  end
  local ok, lines = pcall(vim.fn.readfile, config_path)
  if not ok or type(lines) ~= "table" then
    return {}
  end
  return complete.parse_connection_names(table.concat(lines, "\n"))
end

local function complete_connections(arglead, _cmdline, _cursorpos)
  return complete.filter_prefix(connection_names(), arglead)
end

local function complete_snapshots(arglead, _cmdline, _cursorpos)
  return complete.filter_prefix(snapshot_names(), arglead)
end

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
  end, {
    nargs = "?",
    range = true,
    complete = complete_connections,
    desc = "Ejecuta SQL de inspección con dbx",
  })

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
  end, {
    nargs = "?",
    complete = complete_snapshots,
    desc = "Guarda el último resultado como snapshot",
  })

  vim.api.nvim_create_user_command("DbDiff", function(opts)
    if #opts.fargs ~= 2 then
      notify("DbDiff requiere <before> <after>")
      return
    end
    run({ "diff", opts.fargs[1], opts.fargs[2] }, { kind = "diff", filetype = "diff" })
  end, {
    nargs = "+",
    complete = complete_snapshots,
    desc = "Compara dos snapshots",
  })

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
  end, {
    nargs = "+",
    complete = complete_snapshots,
    desc = "Filtra el último resultado o un snapshot",
  })

  vim.api.nvim_create_user_command("DbDanger", function(opts)
    local conn = connection()
    if conn then
      run({ "danger", "--conn", conn }, { stdin = sql_source(opts), kind = "danger", filetype = "json" })
    end
  end, { nargs = 0, range = true, desc = "Analiza SQL peligroso sin ejecutarlo" })

  vim.api.nvim_create_user_command("DbConn", function(opts)
    local name = vim.trim(opts.args or "")
    if name == "" then
      local current = M.current_connection()
      if current then
        notify("Conexión activa: " .. current, vim.log.levels.INFO)
      else
        notify("No hay conexión activa. Usa :DbConn <nombre>")
      end
      return
    end
    session_connection = name
    notify("Conexión activa: " .. name, vim.log.levels.INFO)
  end, {
    nargs = "?",
    complete = complete_connections,
    desc = "Cambia o muestra la conexión activa de la sesión",
  })
end

local function clear_dbx_keymaps()
  for _, map in ipairs(vim.api.nvim_get_keymap("n")) do
    if map.desc and type(map.desc) == "string" and map.desc:match("^dbx:") then
      pcall(vim.keymap.del, "n", map.lhs)
    end
  end
  for _, map in ipairs(vim.api.nvim_get_keymap("x")) do
    if map.desc and type(map.desc) == "string" and map.desc:match("^dbx:") then
      pcall(vim.keymap.del, "x", map.lhs)
    end
  end
end

--- Resolve effective mapping table from setup option.
--- false/nil → none; true → defaults; table → merge overrides (false disables a key).
---@param mappings boolean|table|nil
---@return table|nil map of action -> lhs
local function resolve_mappings(mappings)
  if mappings == nil or mappings == false then
    return nil
  end
  local base = {
    run = default_mappings.run,
    ddl = default_mappings.ddl,
    snapshot = default_mappings.snapshot,
  }
  if mappings == true then
    return base
  end
  if type(mappings) ~= "table" then
    return nil
  end
  for key, lhs in pairs(mappings) do
    if lhs == false or lhs == "" then
      base[key] = nil
    elseif type(lhs) == "string" then
      base[key] = lhs
    end
  end
  return base
end

local function register_keymaps(mappings)
  clear_dbx_keymaps()
  local resolved = resolve_mappings(mappings)
  if not resolved then
    return
  end

  local function map(mode, lhs, rhs, desc)
    if not lhs or lhs == "" then
      return
    end
    vim.keymap.set(mode, lhs, rhs, { silent = true, desc = "dbx: " .. desc })
  end

  if resolved.run then
    map("n", resolved.run, ":DbRun<CR>", "run statement under cursor")
    -- Visual mode: feed the range command so DbRun sees command_opts.range.
    map("x", resolved.run, ":DbRun<CR>", "run visual selection")
  end

  if resolved.ddl then
    map("n", resolved.ddl, function()
      vim.cmd("DbDDL")
    end, "DDL for word under cursor")
  end

  if resolved.snapshot then
    map("n", resolved.snapshot, function()
      local name = vim.fn.input("DbSnapshot name: ")
      if name == nil then
        return
      end
      name = vim.trim(name)
      if name == "" then
        notify("DbSnapshot requiere un nombre")
        return
      end
      vim.cmd("DbSnapshot " .. name)
    end, "snapshot prompt")
  end
end

function M.setup(opts)
  opts = opts or {}
  if opts.executable ~= nil then
    config.executable = opts.executable
  end
  if opts.connection ~= nil then
    -- Updating the setup default does not wipe a live :DbConn session override.
    config.connection = opts.connection
  end
  if opts.root ~= nil then
    -- Allow clearing override with false/empty; keep only string or function.
    if opts.root == false or opts.root == "" then
      config.root = nil
    else
      config.root = opts.root
    end
  end
  if opts.mappings ~= nil then
    config.mappings = opts.mappings
  end
  register_commands()
  register_keymaps(config.mappings)
end

return M
