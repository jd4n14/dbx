-- Pure completion helpers for the Neovim client (minimal vim API use).
-- Parsing and filtering are pure so smoke tests can cover them without a CLI.

local M = {}

--- Filter `items` keeping those that start with `arglead` (prefix match).
---@param items string[]
---@param arglead string|nil
---@return string[]
function M.filter_prefix(items, arglead)
  items = items or {}
  arglead = arglead or ""
  if arglead == "" then
    local copy = {}
    for i, item in ipairs(items) do
      copy[i] = item
    end
    return copy
  end

  local out = {}
  for _, item in ipairs(items) do
    if type(item) == "string" and item:sub(1, #arglead) == arglead then
      out[#out + 1] = item
    end
  end
  return out
end

--- Parse `dbx snapshot list` stdout into snapshot names.
--- Lines are either `name` or `name\tRFC3339`.
---@param stdout string|nil
---@return string[]
function M.parse_snapshot_list(stdout)
  local names = {}
  if not stdout or stdout == "" then
    return names
  end
  for line in stdout:gmatch("[^\r\n]+") do
    local name = line:match("^([^\t]+)")
    if name then
      name = name:match("^%s*(.-)%s*$") or name
      if name ~= "" then
        names[#names + 1] = name
      end
    end
  end
  return names
end

--- Parse `dbx tables` text stdout into table names.
--- Default stdout is one table name per line. Blank lines and surrounding
--- whitespace are ignored. The optional `--json` form (a JSON array of
--- strings) is also accepted.
---@param stdout string|nil
---@return string[]
function M.parse_tables_list(stdout)
  local names = {}
  if not stdout or stdout == "" then
    return names
  end

  -- Detect a JSON array (the --json form): single non-empty line starting
  -- with `[`. Fall through to line parsing otherwise.
  local trimmed = stdout:gsub("^%s+", ""):gsub("%s+$", "")
  if trimmed:sub(1, 1) == "[" then
    local ok, decoded = pcall(vim.json.decode, trimmed)
    if ok and type(decoded) == "table" then
      for _, v in ipairs(decoded) do
        if type(v) == "string" and v ~= "" then
          names[#names + 1] = v
        end
      end
      return names
    end
  end

  for line in stdout:gmatch("[^\r\n]+") do
    local cleaned = line:match("^%s*(.-)%s*$")
    if cleaned and cleaned ~= "" then
      names[#names + 1] = cleaned
    end
  end
  return names
end

--- Parse `dbx columns <table>` output into a flat list of column names.
--- Accepts the default TSV form (header `field\ttype\tnull\tkey\tdefault\textra`
--- followed by one row per column) or the `--json` form (array of objects
--- with a `field` key). The `default_table` argument is unused; it lets
--- callers anchor completions to a specific table when re-using this helper
--- for omnifunc context (kept so the API mirrors `parse_tables_list`).
---@param stdout string|nil
---@param default_table string|nil
---@return string[]
function M.parse_columns_list(stdout, default_table)
  _ = default_table -- accepted for API symmetry; not used here.
  local columns = {}
  if not stdout or stdout == "" then
    return columns
  end

  local trimmed = stdout:gsub("^%s+", ""):gsub("%s+$", "")
  if trimmed:sub(1, 1) == "[" then
    local ok, decoded = pcall(vim.json.decode, trimmed)
    if ok and type(decoded) == "table" then
      for _, v in ipairs(decoded) do
        if type(v) == "table" and type(v.field) == "string" and v.field ~= "" then
          columns[#columns + 1] = v.field
        end
      end
      return columns
    end
  end

  local first = true
  for line in stdout:gmatch("[^\r\n]+") do
    if first then
      first = false
      -- Skip the TSV header (first cell must equal "field").
      local first_cell = line:match("^([^\t]+)") or ""
      if first_cell:match("^%s*field%s*$") then
        goto continue
      end
    end
    local col = line:match("^([^\t]+)")
    if col then
      col = col:match("^%s*(.-)%s*$") or col
      if col ~= "" then
        columns[#columns + 1] = col
      end
    end
    ::continue::
  end
  return columns
end

--- Extract top-level connection names from a dbx YAML config body.
--- Only understands the common map form under `connections:`; ignores flow `{}`
--- and nested keys deeper than the first indent level of the map.
---@param text string|nil
---@return string[]
function M.parse_connection_names(text)
  local names = {}
  if not text or text == "" then
    return names
  end

  local in_connections = false
  local base_indent = nil

  for line in (text .. "\n"):gmatch("(.-)\n") do
    -- Strip full-line comments and trailing comment noise after content.
    local code = line:match("^([^#]*)") or line
    if not code:find("%S") then
      -- blank / comment-only
    elseif not in_connections then
      if code:match("^connections:%s*$") then
        in_connections = true
        base_indent = nil
      elseif code:match("^connections:%s*{") then
        -- flow / empty map — no names we can extract
        break
      end
    else
      local indent_str, key = code:match("^(%s+)([%w_][%w_.-]*)%s*:")
      if indent_str and key then
        local indent = #indent_str
        if base_indent == nil then
          base_indent = indent
        end
        if indent == base_indent then
          names[#names + 1] = key
        elseif indent < base_indent then
          break
        end
        -- deeper indent: nested field under a connection; ignore
      elseif code:match("^%S") then
        -- next top-level key ends the connections block
        break
      end
    end
  end

  return names
end

return M
