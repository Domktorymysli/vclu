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
  reports, MQTT, HTTP, dashboard actions, `CallLuaFunction*`), the HTTP server and GPIO. One
  callback that does not return blocks all of them until the service restarts.
- Today nothing stops it. The watchdog in `RunLoop` only logs `[WATCHDOG] Runtime stuck`, and
  `processTimersOnLoop` logs `KILLED callback` after a callback that took over 10 s, though it killed
  nothing: the message is printed once the callback has already returned.
- `ExecuteWithTimeout` (used by MCP `vclu_exec` and the sandbox) already stops a script:
  `L.SetContext(ctx)` with a deadline, and gopher-lua checks the context between VM instructions.
  The same can wrap each callback on the loop.
- What a user writes in modules and calendar handlers (`core/Calendar.lua`, `EventBus` handlers,
  `setTimeout`/`setInterval`, dashboard `fns`) runs through these paths.

## Open points for the implementer
- The limit: one value for callbacks (proposal 5 s), configurable in `.vclu.json`; module loading at
  reload may need a longer one or none.
- Check how gopher-lua behaves when the context ends inside `pcall`: a handler wrapped in `pcall`
  (the calendar wraps its handlers) must not swallow the stop and keep looping. A test proves it.
- A Go function that blocks (a synchronous call into Go) is not interrupted by the context; list such
  functions if any are reachable from Lua, and leave them out of scope.
- What state the module is left in after a stop is the module's business, but the log says which
  callback was stopped (timer id, event or work item name, module when known). A callback that
  belongs to a plugin always names the plugin id: `PluginSandbox` knows it.
- Cost: setting a context per callback must not show in the busy time on a Raspberry Pi (measure).

## Files
- `pkg/lua/lua.go` (`processTimersOnLoop`, `processWorkItemOnLoop`, `RunLoop` watchdog)
- `pkg/lua/timers.go` or wherever `TimerManager` lives, `pkg/config` for the setting
- `pkg/lua/AGENTS.md` (the rule for module authors), docs.vclu.pl runtime page on timers

## Acceptance criteria
- [ ] A `setTimeout` callback with `while true do end` is stopped after the limit; a second timer
      scheduled for after it still fires, and a work item queued meanwhile runs. A test shows both.
- [ ] The same for an event handler reached through a work item (e.g. an `EventBus` handler).
- [ ] A loop inside `pcall` inside the callback is stopped too (test).
- [ ] An `setInterval` whose callback was stopped keeps its schedule (next tick runs) or is cleared;
      the choice is written down and tested.
- [ ] The log line names the callback and the limit, and the plugin id for a plugin's callback
      (test with a plugin timer); the misleading `KILLED` message is gone.
- [ ] Callbacks under the limit behave as before; `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0114-lua-callback-timeout.md`.

## Out of scope
- Quarantine of a plugin whose callbacks are stopped again and again (disabled, with a message in
  the panel). A later ticket, once this one shows how often it happens.

## Contract impact
Runtime behaviour for module code: a callback longer than the limit is now stopped. Note it in
CHANGELOG as a contract change if CONTRIBUTING.md counts it as one.
