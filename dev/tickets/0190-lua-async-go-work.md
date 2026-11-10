# #0190: Lua hands blocking work to Go and gets the result in a callback

- **Type:** feature
- **Area:** lua-runtime
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
One mechanism in the runtime runs a blocking Go operation on its own goroutine and, when it is
done, calls a Lua callback on the run loop with the result. The loop keeps serving timers, MQTT,
dashboards and CLU events meanwhile. Every asynchronous Go call from Lua (today: HTTP, Modbus) uses
it instead of its own goroutine, result map and polling timer, and the first new user of it is
running an external process from a module.

## Context
- The Lua runtime is one `LState` on one goroutine (`RunLoop` in `pkg/lua/lua.go`). Since #114 each
  callback runs under a deadline (`runLimited`, `callback_limit.go`, default 60 s), but the deadline
  is checked between VM instructions only: a blocking Go call inside a callback (`os.execute`,
  `io.popen`, `io.read`, synchronous `HTTP:get`, `__go_send_command` waiting for a CLU) holds the
  whole loop until it returns. Nothing else happens in the house for that time.
- Three features already do blocking work off the loop, each in its own way:
  - HTTP: `HTTPManager.AsyncRequest` (`pkg/lua/http.go`) runs the request on a goroutine and keeps
    the result in `asyncResponses` with a TTL; Lua asks for it with `__go_http_get_async_response`
    from a `__go_setTimeout(poll, 50)` loop in three places (`core/PluginHelpers.lua`,
    `integrations/http/HttpClient.lua`, `integrations/http/HTTPRequest.lua`).
  - Modbus: `ModbusManager.AsyncRequest` (`pkg/lua/modbus.go`), `__go_modbus_poll_async` and
    `__go_modbus_get_response`, the same shape.
  - MQTT: messages and lifecycle events wait in channels that `__go_mqtt_poll` and
    `__go_mqtt_poll_event` drain from a 100 ms `__go_setInterval` in `integrations/mqtt/MqttClient.lua`.
  Each copy costs a timer tick on every poll, adds up to one poll period of latency, and needs its
  own cleanup of results nobody collected.
- The loop already takes work from other goroutines: `luaWorkItem` on `fastQueue` and `slowQueue`
  (`executeFast`, `submitToLoop`, `processWorkItemOnLoop`), used by the web server, MCP and CLU
  reports. A goroutine that finishes its work can submit a work item that calls the Lua callback.
  No polling, no result map, the callback runs under `runLimited` like any other.
- A Lua function value survives only in the `LState` that made it. Timers already handle a reload:
  `timerManager.ClearAll()` when the state is replaced. Work that is still running at a reload must
  not call into the new state with a callback from the old one.
- `lua.NewState()` opens the full `os` and `io` libraries for modules (plugins get `safeOs` from
  `core/PluginSandbox.lua`). A module that needs an external program today blocks the loop for as
  long as the program runs.

## Proposal
- Go side, one primitive on `LuaEngine`: run `fn func(ctx) (results, error)` on a goroutine, then
  submit a fast work item that calls the Lua callback with the results (or `nil, err`) under
  `runLimited`, with the owner (module or plugin id) known for the log, as timers record theirs.
  The callback is held in the registry until it runs; replacing the state drops every pending
  callback, and work that finishes afterwards is discarded with a log line, not delivered.
- Limits: a cap on work in flight per owner and in total (proposal 16 and 64); above it the
  callback gets an error at once. A timeout per task passed as a `context`, which work that
  supports it (processes, HTTP, Modbus) honours. `runtime:async_inflight` as a metric; a log line
  when a task runs longer than its timeout or a fixed threshold.
- First user-facing function, in the core runtime, for modules:
  ```lua
  shell("some-command --flag", function(stdout, err, code) ... end, { timeout = 10 })
  ```
  Stdout and stderr captured (stderr in `err` with a non-zero exit, or as a third field, written
  down), exit code, hard kill of the process group at the timeout (default 30 s), output cut at
  a size limit (proposal 1 MB) with the cut marked in the result. Not in `PluginSandbox` (a plugin
  with a shell owns the host; a permission for it is a later decision). In the MCP sandbox
  (`pkg/mcp/sandbox.go`) it is a blocked stub reported in `blocked_calls`, as `os.execute` and
  `io.popen` are today, and `vclu_draft_test` can deliver a canned result for it through a step so
  a module can still be tested.
- HTTP's async path moves onto the primitive: the three polling loops go, `__go_http_get_async_response`
  and `asyncResponses` with them. `pkg/lua/http_async_contract_test.go` and
  `plugin_errors_contract_test.go` keep passing unchanged; they define what callers see.
- Synchronous blocking calls stay as they are, but a work item or callback that blocks the loop
  for longer than a threshold (proposal 1 s) gets one log line naming it, so a module author sees
  which call to move to `shell` or the async form.

## Open points for the implementer
- The name and place of the Lua function (`shell` in core as above, or a library shipped with the
  runtime, see `dev/decisions/0002-runtime-libraries.md`); core is a contract, a library is lighter.
  Write the choice down in `pkg/lua/AGENTS.md`.
- How the Go-side primitive is exposed to runtime Lua (one `__go_*` function taking a callback, or
  one per kind of work). Keep the goroutine, queue and registry handling in one place either way.
- A task that is still running when vCLU shuts down: wait up to the task's timeout, or cancel its
  context and exit. `shutdown_test.go` shows what the loop promises today.
- Whether a `shell` callback keeps the origin and actor of the callback that started it, as timers
  do (`state_context_test.go`), or runs as the module. Timers' behaviour is the natural default.

## Files
- `pkg/lua/lua.go` (`luaWorkItem`, `submitToLoop`, reload of the state), new `pkg/lua/async.go`,
  `pkg/lua/callback_limit.go` (owner of the callback)
- `pkg/lua/http.go`, `pkg/lua/runtime/core/PluginHelpers.lua`,
  `pkg/lua/runtime/integrations/http/HttpClient.lua`, `pkg/lua/runtime/integrations/http/HTTPRequest.lua`
- `pkg/lua/runtime/core/PluginSandbox.lua`, `pkg/mcp/sandbox.go` (blocked stubs), `pkg/mcp/sandbox_scenario.go` (the canned-result step)
- `pkg/lua/AGENTS.md`, `CONTRIBUTING.md` (contract table), `docs/ai/lua-api-reference.md`
  (`tools/generate-api-reference`), docs.vclu.pl runtime page

## Acceptance criteria
- [ ] A test starts a task that blocks for 500 ms on a goroutine; a timer due at 100 ms fires and a
      work item queued meanwhile runs before the task's callback does. The callback is called once,
      on the loop, with the result.
- [ ] A task that fails calls the callback once with `nil, err`; a panic in the work is an error, not
      a crash of the loop (test).
- [ ] A callback that loops is stopped by the callback limit and the log names the task and its
      owner (test, as in `callback_limit_test.go`).
- [ ] Reload while a task runs: the old callback is not called, the new state never sees it, a log
      line says so (test).
- [ ] The per-owner and total caps are enforced and the callback gets the error at once (test).
- [ ] `shell`: stdout, exit code and error are delivered as written down; a command that outlives its
      timeout is killed with its children and the callback says so; output over the size limit is cut
      and marked (tests with `sh -c`; skipped on Windows if the package builds there).
- [ ] `shell` is absent in a plugin's environment (test in `lua_plugin_test.go`) and a blocked stub
      in the MCP sandbox, listed in `blocked_calls`, with the canned-result step tested.
- [ ] HTTP's async path uses the primitive; no `__go_setTimeout(poll...)` is left in
      `pkg/lua/runtime`; `http_async_contract_test.go` and `plugin_errors_contract_test.go` pass
      unchanged.
- [ ] A callback that blocks the loop over the threshold gets exactly one log line naming it (test).
- [ ] `runtime:async_inflight` is registered with the other runtime metrics (test).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0190-lua-async-go-work.md` with a `### Lua runtime`
      section; the entry starts with `Contract change:`.

## Not in scope
- Moving Modbus and MQTT onto the primitive: a follow-up ticket once HTTP shows the shape. Modbus
  is one-shot like HTTP and fits; MQTT is a stream and may stay as it is.
- A permission that lets a plugin run processes.
- An asynchronous form of `__go_send_command` (the CLU protocol has its own timing rules).

## Contract impact
A new row in the contract table of CONTRIBUTING.md, "Async Go work": a callback handed to Go runs
exactly once, on the loop, with the result or the error, never after the state that registered it
was replaced, and the loop is not held while the work runs; `shell` delivers stdout, exit code and
error as written down and is absent for plugins. Contract test in a new `pkg/lua/async_contract_test.go`
(follow `.agents/skills/write-contract-test`). The existing "Async HTTP" row and its test stay. The
PR description and the changelog entry start with `Contract change:`.
