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
