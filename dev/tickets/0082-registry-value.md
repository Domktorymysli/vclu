# #82: `_:value(path)` reads an object's value, or nil

- **Type:** feature
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A module reads a value with `_:value("CLU220000592.DIN0095")` and gets the value, or `nil` when
the object does not exist or has no reading yet, without writing its own helper.

## Context
- Today a module writes `_:get(path):getValue()`. It fails with `attempt to index a nil value`
  when the path is wrong or the object is gone after an OM change, so modules that report state
  (a Telegram bot, a panel, a status page) each carry the same helper:
  ```lua
  local function value(path)
      local obj = _:get(path)
      return obj and obj:getValue()
  end
  ```
  (docs tutorial 13, the maintainer's `modules/telegram`).
- `_:get` already returns `nil` for an unknown path, and `getValue()` of remote objects returns the
  cached `_lastState` (`nil` before the first report), so no `pcall` is needed.
- `_` is `_registry` with `_registry_methods` as its metatable's `__index`
  (`pkg/lua/runtime/core/Registry.lua`). `_:get("list")` for a name without an object returns
  the `list` method itself; `_:value` must not.
- The method list for the editor is in `tools/generate-completions/main.go` and
  `web/static/js/lua-completions.json`.

## Files
- `pkg/lua/runtime/core/Registry.lua`
- `pkg/lua/registry_value_test.go`
- `tools/generate-completions/main.go`, `web/static/js/lua-completions.json`
- `pkg/lua/runtime/core/PluginSandbox.lua`, `pkg/mcp/sandbox_scenario.go`
- `docs/ai/examples.md`, `docs/ai/lua-api-reference.md` (generated)

## Acceptance criteria
- [ ] `_:value(path)` and `_:value(name)` return the object's `getValue()`; 0 stays 0.
- [ ] Unknown path, removed object, object without `getValue`, object without a reading,
      a registry method's name: `nil`, no error.
- [ ] A non-string argument raises an error that names `_:value`.
- [ ] An error inside `getValue` is not hidden.
- [ ] `value` is not listed as an object by `_:list()`, `_:find()` or `_:toJSON()`.
- [ ] The same in a plugin, whose `_` is a read-only proxy (`PluginSandbox.lua`).
- [ ] In a plugin, any other key of `_` no longer overflows the stack: registry methods raise an error
      that names `_:get` and `_:value`, other keys are `nil`, and `_:sync` changes nothing.
- [ ] `vclu_draft_test` warns about a missing object read with `_:value`, as with `_:get`.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] Changelog fragment in `changelog.d/`.

## Contract impact
Adds one public function to the Lua API (`_`). No change to existing ones.
