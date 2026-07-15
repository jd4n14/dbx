-- Pure SQL helpers for the Neovim client (no vim API dependency).

local M = {}

local function is_quote(c)
  return c == "'" or c == '"' or c == "`"
end

local function content_start_of(text, from, to)
  for i = from, to do
    if text:sub(i, i):match("%S") then
      return i
    end
  end
  return nil
end

--- Split `text` into top-level statements terminated by `;`.
--- Semicolons inside `'...'`, `"..."`, or `` `...` `` are ignored.
--- Doubled quotes (`''`, `""`) inside matching quotes are treated as escapes.
---@param text string
---@return { start: integer, finish: integer, content_start: integer, text: string }[]
function M.scan_statements(text)
  text = text or ""
  local statements = {}
  local n = #text
  local start = 1
  local i = 1
  local quote = nil

  local function push(from, to)
    local content_start = content_start_of(text, from, to)
    if not content_start then
      return
    end
    statements[#statements + 1] = {
      start = from,
      finish = to,
      content_start = content_start,
      text = text:sub(from, to),
    }
  end

  while i <= n do
    local c = text:sub(i, i)
    if quote then
      if c == quote then
        local next_c = text:sub(i + 1, i + 1)
        if (quote == "'" or quote == '"') and next_c == quote then
          i = i + 1 -- escaped doubled quote
        else
          quote = nil
        end
      end
    else
      if is_quote(c) then
        quote = c
      elseif c == ";" then
        push(start, i)
        start = i + 1
      end
    end
    i = i + 1
  end

  if start <= n then
    push(start, n)
  end

  return statements
end

local function trim_statement(stmt)
  -- Keep internal newlines and a trailing semicolon; drop surrounding whitespace.
  return (stmt:gsub("^%s+", ""):gsub("%s+$", ""))
end

--- Return the statement that owns `pos` (1-based byte index into `text`).
--- Ownership uses content spans (leading whitespace between statements is not
--- part of the next statement). If `pos` falls on blank space between
--- statements, prefer the previous one. Leading blanks before the first
--- statement resolve to the first statement.
---@param text string
---@param pos integer|nil 1-based index; defaults to 1
---@return string|nil
function M.statement_at(text, pos)
  text = text or ""
  if not text:find("%S") then
    return nil
  end

  pos = pos or 1
  if pos < 1 then
    pos = 1
  elseif pos > #text + 1 then
    pos = #text + 1
  end

  local statements = M.scan_statements(text)
  if #statements == 0 then
    return nil
  end

  local previous = nil
  for _, stmt in ipairs(statements) do
    if pos >= stmt.content_start and pos <= stmt.finish then
      return trim_statement(stmt.text)
    end
    if pos > stmt.finish then
      previous = stmt
    elseif pos < stmt.content_start then
      if previous then
        return trim_statement(previous.text)
      end
      return trim_statement(stmt.text)
    end
  end

  if previous then
    return trim_statement(previous.text)
  end

  return trim_statement(statements[1].text)
end

--- Convert 1-based line / 0-based column into a 1-based byte index in `text`,
--- where lines are joined with `\n` (same as `table.concat(lines, "\n")`).
---@param text string
---@param line integer 1-based
---@param col integer 0-based byte column
---@return integer
function M.cursor_pos(text, line, col)
  text = text or ""
  line = line or 1
  col = col or 0
  if line < 1 then
    line = 1
  end
  if col < 0 then
    col = 0
  end

  local current_line = 1
  local i = 1
  local n = #text

  while i <= n and current_line < line do
    if text:sub(i, i) == "\n" then
      current_line = current_line + 1
    end
    i = i + 1
  end
  local pos = i

  local line_end = pos
  while line_end <= n and text:sub(line_end, line_end) ~= "\n" do
    line_end = line_end + 1
  end
  local line_len = line_end - pos
  if col > line_len then
    col = line_len
  end

  return pos + col
end

--- Extract the statement under a buffer cursor.
---@param text string full buffer text
---@param line integer 1-based
---@param col integer 0-based
---@return string|nil
function M.statement_under_cursor(text, line, col)
  return M.statement_at(text, M.cursor_pos(text, line, col))
end

return M
