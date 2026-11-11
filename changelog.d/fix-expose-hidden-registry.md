### Lua runtime

- **`hidden = true` works with `exposeRegistry()`** - an object a module exposes with `{hidden = true}` (`expose(obj, type, opts)` or `obj:expose(opts)`, also on virtual objects such as Satel outputs) is no longer published under its registry path when `exposeRegistry()` in `om.lua` runs before or after the module; one already published is taken back, its Home Assistant entity included.
- **`expose(obj, ...)` and `obj:expose(...)` behave the same** - exposing an object again with a new name renames it in Home Assistant in both forms (before, only `obj:expose` did), and `obj:isExposed()`, `obj:unexpose()` and `obj:getExposeHandle()` see an exposure made by `expose(obj, ...)` or `exposeRegistry()`.
