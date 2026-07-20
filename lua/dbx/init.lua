local M = {}

local config = {
  executable = "dbx",
  connection = nil,
  ---@type string|fun():string|nil
  root = nil,
  -- Run `dbx danger` on the same SQL before `:DbRun` proceeds.
  -- safe   → silent proceed
  -- warning→ notify WARN, proceed
  -- critical → notify ERROR; restricted_environment_write finding also blocks
  danger_preflight = true,
  -- Install the SQL omnifunc (`v:lua.require'dbx'.omnifunc`) on SQL buffers
  -- via a FileType autocmd. Default ON; opt out with `setup({ sql_omnifunc = false })`.
  sql_omnifunc = true,
  ---@type boolean|table
  -- false/nil: no keymaps; true: default leader maps; table: explicit lhs overrides.
  mappings = false,
  result = {
    -- "horizontal" = botright split (default). "vertical" = botright vsplit.
    orientation = "horizontal",
    -- Fraction of the editor occupied by the result split (0 < size < 1).
    size = 0.4,
    -- Where focus goes after rendering: "result" (default), "source", or "none".
    focus = "result",
  },
}

-- Session connection override set by :DbConn (takes precedence over setup.connection).
local session_connection = nil

-- Tracks the FileType autocmd id installed by setup so re-entrant setup
-- calls replace it instead of stacking duplicates.
local sql_omnifunc_autocmd_id = nil

-- Session-bound caches for completion helpers. keyBy("conn", ts) refreshes
-- every 60 seconds so typing 60+ chars/min worst case still gets fresh data
-- without re-issuing a CLI sync on every keystroke.
---@type table<string, { fetched_at: number, names: string[] }>
local table_cache = {}
local CACHE_TTL_SECONDS = 60

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

--- Resolve the split command for the configured orientation.
---@return string
local function split_command(orientation)
  if orientation == "vertical" then
    return "botright vsplit"
  end
  return "botright split"
end

--- Apply `size` (fraction in (0,1)) to the just-created result split.
---@param win integer
---@param orientation string
---@param size number|nil
local function apply_result_size(win, orientation, size)
  if type(size) ~= "number" or size <= 0 or size >= 1 then
    return
  end
  if orientation == "vertical" then
    local cols = vim.o.columns
    if type(cols) == "number" and cols > 0 then
      local target = math.max(1, math.floor(cols * size))
      vim.api.nvim_win_set_width(win, target)
    end
    return
  end
  local lines = vim.o.lines
  if type(lines) == "number" and lines > 0 then
    local target = math.max(1, math.floor(lines * size))
    vim.api.nvim_win_set_height(win, target)
  end
end

local function result_buffer(kind, filetype, stdout)
  local name = "dbx://" .. kind
  local bufnr = vim.fn.bufnr(name)
  if bufnr < 0 then
    bufnr = vim.api.nvim_create_buf(false, true)
    vim.api.nvim_buf_set_name(bufnr, name)
  end

  local source_win = vim.api.nvim_get_current_win()
  local win = vim.fn.bufwinid(bufnr)
  if win == -1 then
    vim.cmd(split_command(config.result.orientation))
    win = vim.api.nvim_get_current_win()
    vim.api.nvim_win_set_buf(win, bufnr)
    apply_result_size(win, config.result.orientation, config.result.size)
  end

  vim.bo[bufnr].buftype = "nofile"
  vim.bo[bufnr].bufhidden = "wipe"
  vim.bo[bufnr].swapfile = false
  vim.bo[bufnr].modifiable = true
  vim.api.nvim_buf_set_lines(bufnr, 0, -1, false, output_lines(stdout))
  vim.bo[bufnr].filetype = filetype
  vim.b[bufnr].dbx_result = kind
  vim.bo[bufnr].modified = false
  vim.bo[bufnr].modifiable = false

  local focus = config.result.focus
  if focus == "source" and vim.api.nvim_win_is_valid(source_win) then
    vim.api.nvim_set_current_win(source_win)
  elseif focus == "result" then
    vim.api.nvim_set_current_win(win)
  end
  -- focus == "none" or unknown: leave focus where it already is
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
  explain = true,
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

--- Parse a danger envelope from CLI stdout. Returns nil on bad JSON.
---@param stdout string
---@return table|nil
local function decode_danger(stdout)
  if not stdout or stdout == "" then
    return nil
  end
  local ok, parsed = pcall(vim.json.decode, stdout)
  if not ok or type(parsed) ~= "table" then
    return nil
  end
  return parsed
end

--- Reduce a danger envelope to a flat list of finding codes.
---@param result table
---@return string[]
local function finding_codes(result)
  local codes = {}
  for _, f in ipairs(result.findings or {}) do
    if type(f) == "table" and type(f.code) == "string" then
      codes[#codes + 1] = f.code
    end
  end
  return codes
end

--- True when the envelope carries the env-based write block.
local function has_env_write_block(codes)
  for _, c in ipairs(codes) do
    if c == "restricted_environment_write" then
      return true
    end
  end
  return false
end

--- Translate a danger envelope into a notify + proceed/block decision. Pure:
--- never touches IO or vim.system.
---@param result table
---@return { proceed: boolean, block: boolean }
local function decide_danger(result)
  if type(result) ~= "table" then
    return { proceed = true, block = false }
  end
  local severity = result.severity
  if severity == "safe" or result.safe == true then
    return { proceed = true, block = false }
  end
  local codes = finding_codes(result)
  local list = #codes > 0 and table.concat(codes, ", ") or "(sin códigos)"
  if severity == "warning" then
    notify("SQL con advertencias antes de ejecutar: " .. list, vim.log.levels.WARN)
    return { proceed = true, block = false }
  end
  if severity == "critical" then
    if has_env_write_block(codes) then
      notify(
        "Ejecución bloqueada: SQL crítico en entorno prod/readonly ("
          .. list
          .. "). Cambia de conexión, desactiva danger_preflight o ejecuta DbDanger manualmente.",
        vim.log.levels.ERROR
      )
      return { proceed = false, block = true }
    end
    notify(
      "SQL crítico detectado antes de ejecutar: "
        .. list
        .. ". Para omitir el aviso, usa setup({ danger_preflight = false }).",
      vim.log.levels.ERROR
    )
    return { proceed = true, block = false }
  end
  -- Unknown severity: never block on speculation.
  return { proceed = true, block = false }
end

--- Run `dbx danger --conn <conn>` on `source` and route the decision to
--- `on_decision`. Preflight never silently blocks: when the CLI errors or
--- returns bad JSON we proceed with an INFO notification so existing setups
--- keep working.
---@param source string
---@param conn string
---@param on_decision fun(decision: { proceed: boolean, block: boolean })
local function preflight_danger(source, conn, on_decision)
  if not config.danger_preflight then
    on_decision({ proceed = true, block = false })
    return
  end
  local executable = config.executable
  if not executable_available(executable) then
    on_decision({ proceed = true, block = false })
    return
  end

  local root = M.resolve_root()
  local config_path = M.project_config_path(root)
  local argv = { "danger", "--conn", conn }
  local final_argv = with_config_flag(argv, config_path)

  local command = { executable }
  vim.list_extend(command, final_argv)
  vim.system(command, { cwd = root, stdin = source, text = true }, function(result)
    vim.schedule(function()
      if result.code ~= 0 then
        local detail = vim.trim(result.stderr or "")
        if detail == "" then
          detail = "el proceso terminó con código " .. result.code
        end
        notify("Preflight danger omitido: " .. detail, vim.log.levels.INFO)
        on_decision({ proceed = true, block = false })
        return
      end
      local parsed = decode_danger(result.stdout)
      if not parsed then
        notify("Preflight danger: respuesta no válida del CLI, se omite", vim.log.levels.INFO)
        on_decision({ proceed = true, block = false })
        return
      end
      on_decision(decide_danger(parsed))
    end)
  end)
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

local function now_seconds()
  return math.floor(os.time())
end

--- Cached table-name lookup for the active connection. Honours a 60s TTL
--- so completion stays responsive on schemas with many tables.
---@return string[]
local function table_names(arglead)
  local conn = M.current_connection()
  if not conn or conn == "" then
    return {}
  end
  local now = now_seconds()
  local entry = table_cache[conn]
  if not entry or (now - (entry.fetched_at or 0)) >= CACHE_TTL_SECONDS then
    local argv = { "tables", "--conn", conn, "--json" }
    -- Pass --like as a substring so the server-side filter prunes large
    -- schemas. The Neovim side still applies filter_prefix, but doing this
    -- here keeps the wire response bounded on huge databases.
    if arglead and arglead ~= "" then
      argv[#argv + 1] = "--like"
      argv[#argv + 1] = arglead
    end
    local stdout = run_sync_stdout(argv)
    local names = complete.parse_tables_list(stdout)
    table_cache[conn] = { fetched_at = now, names = names }
    entry = table_cache[conn]
  end
  return entry.names or {}
end

local function column_names(table_name)
  if not table_name or table_name == "" then
    return {}
  end
  local stdout = run_sync_stdout({ "columns", "--conn", M.current_connection() or "", "--table", table_name })
  return complete.parse_columns_list(stdout, table_name)
end

local function complete_table_names(arglead, _cmdline, _cursorpos)
  return complete.filter_prefix(table_names(arglead), arglead)
end

local function complete_column_names(arglead, _cmdline, _cursorpos)
  -- Look back on the cmdline for the closest `from <table>` / `update <table>`
  -- preceding the cursor so we ask the CLI for the right table's columns.
  -- Falls back to empty (caller is asked to specify the table) when no
  -- preceding token names a table.
  local cmdline = _cmdline or ""
  local cursorpos = _cursorpos or #cmdline
  local prefix = cmdline:sub(1, cursorpos)
  local lowered = prefix:lower()
  local hint = nil
  for token in lowered:gmatch("[%s,(](from%s+[%w_]+)") do
    hint = token:match("from%s+([%w_]+)")
  end
  if not hint then
    for token in lowered:gmatch("[%s,(](update%s+[%w_]+)") do
      hint = token:match("update%s+([%w_]+)")
    end
  end
  if not hint then
    for token in lowered:gmatch("[%s,(](into%s+[%w_]+)") do
      hint = token:match("into%s+([%w_]+)")
    end
  end
  if not hint then
    return {}
  end
  return complete.filter_prefix(column_names(hint), arglead)
end

--- Locate the most recent FROM / UPDATE / INSERT INTO / INTO clause in
--- `text` and return the table it references. Returns nil when no clause
--- is found or the captured token is not a simple identifier.
---@param text string
---@return string|nil
local function nearest_table_hint(text)
  if not text or text == "" then
    return nil
  end
  local lowered = text:lower()
  -- Walk backwards so the most-recent clause wins (relevant when the
  -- buffer has multiple statements; we only need the one nearest the cursor).
  local last
  for token in lowered:gmatch("[%s,(](from%s+[%w_]+)") do
    last = token:match("from%s+([%w_]+)")
  end
  if not last then
    for token in lowered:gmatch("[%s,(](update%s+[%w_]+)") do
      last = token:match("update%s+([%w_]+)")
    end
  end
  if not last then
    for token in lowered:gmatch("[%s,(](into%s+[%w_]+)") do
      last = token:match("into%s+([%w_]+)")
    end
  end
  return last
end

--- Omnifunc for SQL buffers. findstart=1: return byte offset of the
--- identifier currently being completed. findstart=0: list candidates
--- (table names or column names for the FROM/UPDATE/INTO clause nearest
--- the cursor).
---@param findstart integer
---@return integer|string[]
function M.omnifunc(findstart)
  if findstart == 1 then
    local line = vim.api.nvim_get_current_line()
    local cursor = vim.api.nvim_win_get_cursor(0)[2]
    local i = cursor
    while i > 0 do
      local ch = line:sub(i, i)
      if not ch or not ch:match("[%w_]") then
        break
      end
      i = i - 1
    end
    return i
  end

  local bufnr = vim.api.nvim_get_current_buf()
  if vim.bo[bufnr].filetype ~= "sql" then
    return {}
  end

  local ok, conn = pcall(M.current_connection)
  if not ok or not conn or conn == "" then
    return {}
  end

  local row = vim.api.nvim_win_get_cursor(0)[1]
  local before = vim.api.nvim_buf_get_lines(bufnr, 0, row, false)
  local text = table.concat(before, "\n")
  local hint = nearest_table_hint(text)
  if hint then
    return complete.filter_prefix(column_names(hint), "")
  end
  return complete.filter_prefix(table_names(""), "")
end

--- Parse `dbx history list --json` (one JSON object per line) into a list.
---@param stdout string|nil
---@return table[]
local function parse_history_list(stdout)
  local out = {}
  if not stdout or stdout == "" then
    return out
  end
  for line in stdout:gmatch("[^\r\n]+") do
    if line ~= "" then
      local ok, decoded = pcall(vim.fn.json_decode, line)
      if ok and type(decoded) == "table" then
        out[#out + 1] = decoded
      end
    end
  end
  return out
end

--- Read the parsed history listing from the CLI under the project root.
---@return table[]
local function history_entries()
  local stdout = run_sync_stdout({ "history", "list", "--limit", "50", "--json" })
  return parse_history_list(stdout)
end

--- Return the newest history entry (entry[1] in CLI output) or nil.
---@return table|nil
local function newest_history_entry()
  local entries = history_entries()
  if #entries == 0 then
    return nil
  end
  return entries[1]
end

--- Completion function returning the list of available history indexes.
local function complete_history_indexes(arglead, _cmdline, _cursorpos)
  local items = {}
  for _, e in ipairs(history_entries()) do
    if e.index then
      items[#items + 1] = tostring(e.index)
    end
  end
  return complete.filter_prefix(items, arglead)
end

--- Render JSONL history into a tabular buffer (one line per entry) using the
--- shared result_buffer scratch buffer with filetype=tsv.
---@param stdout string
local function render_history(stdout)
  local entries = parse_history_list(stdout)
  if #entries == 0 then
    notify("No hay historial todavía", vim.log.levels.INFO)
    return
  end
  local lines = { "idx\twhen\tconnection\tsql" }
  for _, e in ipairs(entries) do
    local ts = e.ts or ""
    local conn_name = e.connection or ""
    local sql = (e.sql or ""):gsub("\r?\n", " "):gsub("\t", " ")
    table.insert(lines, string.format("%d\t%s\t%s\t%s", e.index or 0, ts, conn_name, sql))
  end
  result_buffer("history", "tsv", table.concat(lines, "\n"))
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
    preflight_danger(source, conn, function(decision)
      if not decision.proceed then
        return
      end
      run({ "query", "--conn", conn }, { stdin = source, kind = "query", filetype = "json" })
    end)
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

  vim.api.nvim_create_user_command("DbTables", function(opts)
    local conn = connection()
    if not conn then
      return
    end
    local arg = vim.trim(opts.args or "")
    local argv = { "tables", "--conn", conn }
    if arg ~= "" then
      argv[#argv + 1] = "--like"
      argv[#argv + 1] = arg
    end
    run(argv, { kind = "tables", filetype = "tsv" })
  end, {
    nargs = "?",
    complete = complete_table_names,
    desc = "Lista tablas (--like opcional)",
  })

  vim.api.nvim_create_user_command("DbColumns", function(opts)
    local conn = connection()
    if not conn then
      return
    end
    local arg = vim.trim(opts.args)
    local table_name = arg
    if table_name == "" then
      table_name = vim.fn.expand("<cword>")
    end
    if table_name == "" then
      notify("DbColumns requiere una tabla o una palabra bajo el cursor")
      return
    end
    run({ "columns", "--conn", conn, "--table", table_name }, { kind = "columns", filetype = "tsv" })
  end, {
    nargs = "?",
    desc = "Lista columnas de una tabla (--table implícito)",
  })

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

  vim.api.nvim_create_user_command("DbExport", function(opts)
    local name = vim.trim(opts.args)
    if name == "" then
      notify("DbExport requiere un nombre de snapshot")
      return
    end
    -- Mirrors :DbSnapshot ergonomics: default args run `dbx export <name>`
    -- (CSV, sidecar ON). Power users wanting a different format or
    -- destination can shell out via `:!dbx export ...`.
    run({ "export", name }, {
      on_success = function(stdout)
        notify("Export guardado en " .. vim.trim(stdout), vim.log.levels.INFO)
      end,
    })
  end, {
    nargs = "?",
    complete = complete_snapshots,
    desc = "Exporta un snapshot a CSV (con sidecar JSON por defecto)",
  })

  -- :DbExplain mirrors :DbRun: SQL comes from the buffer (statement under
  -- the cursor or the active range). Connection name can be supplied as
  -- the first arg; falls back to the session/default connection. Plan 009
  -- default is tabular to the result buffer.
  vim.api.nvim_create_user_command("DbExplain", function(opts)
    local conn = connection(opts.args)
    if not conn then
      return
    end
    local source = dbrun_source(opts)
    if source == nil then
      return
    end
    run({ "explain", "--conn", conn }, {
      stdin = source,
      kind = "explain",
      filetype = "tsv",
    })
  end, {
    nargs = "?",
    range = true,
    complete = complete_connections,
    desc = "EXPLAIN del statement bajo el cursor (tabla)",
  })

  -- :DbExplainJson forces EXPLAIN FORMAT=JSON. SQL comes from the buffer
  -- (statement under the cursor or the active range). When the current
  -- buffer is a file on disk, the JSON output is written next to it
  -- (`<file>.explain.json`) so the sidecar can live alongside — matching
  -- the plan's "sidecar written next to the buffer's file" rule. Result
  -- buffer mirrors the file path for easy diffing.
  vim.api.nvim_create_user_command("DbExplainJson", function(opts)
    local conn = connection(opts.args)
    if not conn then
      return
    end
    local source = dbrun_source(opts)
    if source == nil then
      return
    end
    local out_arg = nil
    local bufnr = vim.api.nvim_get_current_buf()
    local name = vim.api.nvim_buf_get_name(bufnr)
    if name and name ~= "" and vim.fn.filereadable(name) == 1 then
      out_arg = name .. ".explain.json"
    end
    local argv = { "explain", "--json", "--conn", conn }
    if out_arg then
      table.insert(argv, "-o")
      table.insert(argv, out_arg)
    end
    run(argv, {
      stdin = source,
      kind = "explain_json",
      filetype = "json",
    })
  end, {
    nargs = "?",
    range = true,
    complete = complete_connections,
    desc = "EXPLAIN FORMAT=JSON del statement bajo el cursor",
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

  vim.api.nvim_create_user_command("DbHistory", function(opts)
    -- Renders recent history in a fresh 'history' result buffer. JSON is the
    -- transport so we can parse columns robustly without TTY columns.
    run({ "history", "list", "--json" }, {
      kind = "history",
      filetype = "tsv",
      on_success = function(stdout)
        render_history(stdout)
      end,
    })
  end, { nargs = 0, desc = "Muestra el historial reciente de queries ejecutados" })

  vim.api.nvim_create_user_command("DbHistoryLast", function(opts)
    -- Re-run the most recent successful query: re-uses :DbRun's connection
    -- resolution + rendering so behavior stays consistent with a fresh run.
    local entry = newest_history_entry()
    if entry == nil then
      notify("No hay historial todavía", vim.log.levels.INFO)
      return
    end
    local conn = entry.connection or ""
    if conn == "" then
      notify("La entrada de historial no tiene conexión; no se puede re-ejecutar")
      return
    end
    run({ "query", "--conn", conn }, {
      stdin = entry.sql or "",
      kind = "query",
      filetype = "json",
    })
  end, { nargs = 0, desc = "Re-ejecuta el último query exitoso del historial" })
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

--- Deep-merge `incoming` into `base`, returning a fresh table; only known keys
--- (or any scalar sub-key if `base` had it) get replaced. We keep things
--- minimal here: each leaf field is replaced wholesale.
---@param base table
---@param incoming table|nil
---@return table
local function merge_table(base, incoming)
  local out = {}
  for k, v in pairs(base) do
    out[k] = v
  end
  if type(incoming) ~= "table" then
    return out
  end
  for k, v in pairs(incoming) do
    out[k] = v
  end
  return out
end

--- Replace (or install for the first time) the FileType autocmd that points
--- SQL buffers at `dbx.omnifunc`. Tracking the id keeps re-entrant `setup`
--- calls idempotent — the previous autocmd is detached first.
local function install_sql_omnifunc_autocmd()
  if sql_omnifunc_autocmd_id then
    pcall(vim.api.nvim_del_autocmd, sql_omnifunc_autocmd_id)
    sql_omnifunc_autocmd_id = nil
  end
  if not config.sql_omnifunc then
    return
  end
  local group = vim.api.nvim_create_augroup("dbx_omnifunc", { clear = false })
  sql_omnifunc_autocmd_id = vim.api.nvim_create_autocmd("FileType", {
    group = group,
    pattern = "sql",
    callback = function(args)
      if not vim.api.nvim_buf_is_valid(args.buf) then
        return
      end
      vim.bo[args.buf].omnifunc = "v:lua.require'dbx'.omnifunc"
    end,
  })
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
  if opts.result ~= nil then
    config.result = merge_table(config.result, opts.result)
  end
  if opts.danger_preflight ~= nil then
    config.danger_preflight = opts.danger_preflight and true or false
  end
  if opts.sql_omnifunc ~= nil then
    config.sql_omnifunc = opts.sql_omnifunc and true or false
  end

  -- Reset cached table lists on every setup call so configuration changes
  -- (new connection, root, executable) take effect immediately.
  table_cache = {}

  install_sql_omnifunc_autocmd()

  register_commands()
  register_keymaps(config.mappings)
end

--- True while a SQL buffer in the current Neovim session has the dbx
--- omnifunc installed. Exposed for tests and `:checkhealth`-style
--- inspection.
---@return boolean
function M.sql_omnifunc_active()
  if not config.sql_omnifunc then
    return false
  end
  if not sql_omnifunc_autocmd_id then
    return false
  end
  local autocmds = vim.api.nvim_get_autocmds({
    group = vim.api.nvim_create_augroup("dbx_omnifunc", { clear = false }),
    event = "FileType",
  })
  return autocmds and #autocmds > 0
end

return M
