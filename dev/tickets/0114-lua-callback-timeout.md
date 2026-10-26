# #0114: A Lua callback that runs too long is stopped, not the whole runtime

- **Type:** feature
- **Area:** lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A timer callback or event handler that loops (`while true do end`) is stopped after a time limit
with an error in the log, and the rest of vCLU (other timers, CLU events, MQTT, HomeKit,
dashboards) keeps working.

## Context
- The Lua runtime is one `LState` on one goroutine (`RunLoop` in `pkg/lua/lua.go`). Everything runs
  there in turn: timers (`processTimersOnLoop`), work items from the fast and slow queues (CLU
  reports, MQTT, HTTP, dashboard actions, `CallLuaFunction*`) and the HTTP server
  (`processHTTPServerOnLoop`, not a work item). GPIO changes reach Lua through a Lua polling timer.
  One callback that does not return blocks all of them until the service restarts.
- Today nothing stops it. The watchdog in `RunLoop` only logs `[WATCHDOG] Runtime stuck`, and
  `processTimersOnLoop` logs `KILLED callback` after a callback that took over 10 s, though it killed
  nothing: the message is printed once the callback has already returned.
- `ExecuteWithTimeout` and `ExecuteAsWithTimeout` (used by MCP `vclu_exec`, 10 s) already stop a
  script: `L.SetContext(ctx)` with a deadline, and gopher-lua checks the context between VM
  instructions (`vm.go`, `mainLoopWithContext`). The same can wrap each callback on the loop. The
  MCP sandbox has its own `LState` with one context for the whole run (`pkg/mcp/sandbox.go`).
- The check is between VM instructions only: time spent inside a Go function counts towards the
  deadline but is not interrupted, and the callback is stopped right after the Go call returns, so
  its result is lost. Blocking Go functions reachable from Lua:
  - synchronous HTTP (`HTTP:get` and friends → `__go_http_request` in `pkg/lua/http.go`), default
    timeout 30 s (`HttpClient.lua`);
  - `__go_send_command` (`pkg/lua/lua.go`): waits for the CLU's answer, then `time.Sleep(delayMs)`;
  - full `os` and `io` (`lua.NewState()` opens all libraries): `os.execute`, `io.popen`, `io.read`.
- What a user writes in modules and calendar handlers (`core/Calendar.lua`, `EventBus` handlers,
  `setTimeout`/`setInterval`, dashboard `fns`) runs through these paths.

## Open points for the implementer
- The limit: one value for callbacks (proposal 5 s), configurable in `.vclu.json`. Decide whether
  module loading at reload is covered (a longer limit or none) and write it down.
- The limit is wall-clock time including blocking Go calls. A `HTTP:get` in a timer that works today
  (30 s timeout) fails with a 5 s limit. Decide and test one way: a limit above the longest
  synchronous timeout, or synchronous calls cap their timeout at the time left before the deadline.
- One deadline per work item stops everything after it in that work item: once the context is done,
  every next Lua instruction raises, also inside `pcall`. When one work item dispatches to several
  handlers (`EventBus:fire`, `Cal:_emit` with `pcall(h.fn, ...)`), a looping handler also kills the
  handlers after it and their cleanup. Decide: each handler its own deadline, or the rest dies
  (written down).
- `pcall` catches the stop and returns `false, "...context deadline exceeded"`; the loop still ends
  because the next instruction in the outer frame raises again. Reusing the `LState` after a stop is
  safe: the loop calls Lua with `Protect: true` / `PCall`.
- Deadlines nest. Work items with their own timeout (`ExecuteWithTimeout`, `ExecuteAsWithTimeout`,
  module loading if it gets one) keep theirs; they and the wrapper restore the previous context
  instead of calling `RemoveContext` (the comment in `withStateContext`, `pkg/lua/history.go`,
  relies on that).
- Lua that runs after a stop fails too, with the same dead context: `resetStateContext`,
  `StateContext.truncate` (the `restore` of `withStateContext`) after an HTTP endpoint handler. Run
  it after the context is removed or restored, as `withStateContext` already does.
- The log names the callback that was stopped (timer id, event or work item name, module when
  known). For a plugin's callback it names the plugin id, which Go must know: the `PluginSandbox`
  wrapper catches the stop with `pcall`, but its `Logger:error` runs with the dead context and
  raises again. Record the owner when the callback is registered (`PluginSandbox` passes the plugin
  id to `__go_setTimeout`/`__go_setInterval`), or take it from the chunk name (plugin code is loaded
  with `loadstring(code, pluginId)`).
- Coroutines: `coroutine.create` gives a new thread a child context only if one is set at creation
  (`LState.NewThread`). A coroutine created at module load has none, so a loop inside it is not
  stopped; one created in callback A is dead after A's deadline (every later `resume` fails with
  `context canceled`). At least a test of today's behaviour and a note in `pkg/lua/AGENTS.md`.
- `setInterval`: today `MarkFired` clears `Pending` also after an error, so the interval keeps
  ticking; that is the natural default. A looping interval then blocks the loop for the limit on
  every tick.
- Cost: with a context gopher-lua runs `mainLoopWithContext`, a `select` per VM instruction. A
  `go test -bench` on a compute loop with and without a context, run on ARM (Raspberry Pi);
  proposal: at most 10% slower.

## Files
- `pkg/lua/lua.go` (`processTimersOnLoop`, `processWorkItemOnLoop`, `processHTTPServerOnLoop`,
  `RunLoop` watchdog, `ExecuteWithTimeout`), `pkg/lua/history.go` (`ExecuteAsWithTimeout`,
  `withStateContext`)
- `pkg/lua/timers.go` (`TimerManager`), `pkg/lua/runtime/core/PluginSandbox.lua` (owner of a timer)
- `pkg/config/config.go` for the setting
- `pkg/lua/lua_contract_test.go`, `CONTRIBUTING.md` (contract table)
- `pkg/lua/AGENTS.md` (the rule for module authors), docs.vclu.pl runtime page on timers

## Acceptance criteria
- [ ] A `setTimeout` callback with `while true do end` is stopped after the limit; a second timer
      scheduled for after it still fires, and a work item queued meanwhile runs. A test shows both.
- [ ] The same for an event handler reached through a work item (e.g. an `EventBus` handler), and
      the choice for the other handlers of the same work item is tested.
- [ ] A looping HTTP endpoint handler (`HTTPServer:_handleRequest`) is stopped and the client gets
      500.
- [ ] A loop inside `pcall` inside the callback is stopped too, and `while true do pcall(f) end`
      (tests).
- [ ] A `setInterval` whose callback was stopped keeps its schedule (next tick runs) or is cleared;
      the choice is written down and tested.
- [ ] A synchronous Go call in a callback behaves as decided above (test with a fake HTTP server).
- [ ] `ExecuteWithTimeout` inside the loop keeps its own timeout and the previous context is back
      afterwards (test).
- [ ] The limit is set in `.vclu.json` (test in `pkg/config`); whether module loading is covered is
      written down and tested.
- [ ] The log line names the callback and the limit, and the plugin id for a plugin's callback
      (test with a plugin timer); the misleading `KILLED` message is gone.
- [ ] The benchmark above runs on ARM and its result is in the PR.
- [ ] Callbacks under the limit behave as before; `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0114-lua-callback-timeout.md`, starting with
      `Contract change:`.

## Not in scope
- Quarantine of a plugin whose callbacks are stopped again and again (disabled, with a message in
  the panel). A later ticket, once this one shows how often it happens.

## Contract impact
A contract change for module code: a callback that runs longer than the limit, counted in
wall-clock time including blocking Go calls, is now stopped. The `Lua runtime` row in
CONTRIBUTING.md does not cover it: add a row (callback time limit) with a contract test, e.g. in
`pkg/lua/lua_contract_test.go`, and start the PR description and the changelog entry with
`Contract change:`.
