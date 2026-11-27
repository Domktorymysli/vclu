### Plugins

- **A plugin's dashboard is opened with the same Lua as any dashboard** - a module sets who opens a dashboard a plugin declares with the chain of `Dashboard.new`: `Plugin.getPlugin("@vclu/zakupy"):dashboard("lista"):name("Zakupy"):auth("public"):remote({ pin = false }):roles("domownicy")`, also `:icon()` and `:pin()`. The plugin wizard writes it so. The table form `dashboardAccess(id, {...})` still runs and logs that it is deprecated.
