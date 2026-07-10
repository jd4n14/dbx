if vim.fn.has("nvim-0.10") ~= 1 or vim.system == nil then
  vim.schedule(function()
    vim.notify("dbx requiere Neovim 0.10 o posterior (con vim.system)", vim.log.levels.ERROR, { title = "dbx" })
  end)
  return
end

require("dbx").setup()
