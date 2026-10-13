# #18: Concurrent async HTTP requests lose each other's responses

- **Type:** bug
- **Area:** lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Every async HTTP request made from Lua gets its own response, however many plugins and modules
have requests in flight at the same time.

## Context
`__go_http_poll_async()` (`pkg/lua/http.go`, `GetPendingAsyncID`) pops one completed request id
from a queue shared by the whole runtime. Three waiters poll it: `PluginHelpers.httpRequest`
(`pkg/lua/runtime/core/PluginHelpers.lua`), `HTTP:asyncRequest`
(`pkg/lua/runtime/integrations/http/HttpClient.lua`) and `HTTPRequest.lua`. Each one keeps the id
only if it is its own; any other id is dropped. Its owner never sees the completion and times
out, or gets `HTTP 0`.

Seen in production with three polling plugins (Salda, SolaX, Supla) and an InfluxDB export
module: plugins time out at random, and the Influx module reports `HTTP 0`. A user module
currently works around it by serialising its own requests, which does not help against plugins.

Also: `PluginHelpers.httpRequest` builds the request id as `pluginId .. "_" .. os.time() .. "_" ..
attempt`, so two requests from one plugin in the same second share an id.

## Files
- `pkg/lua/http.go`
- `pkg/lua/runtime/core/PluginHelpers.lua`
- `pkg/lua/runtime/integrations/http/HttpClient.lua`, `HTTPRequest.lua`
- `pkg/mcp/sandbox.go` (sandbox stubs of the same functions)

## Acceptance criteria
- [ ] A waiter checks for its own request id (e.g. `__go_http_get_async_response(id)` returning
  "not ready" until done) instead of popping a shared queue; no completion can be consumed by
  another waiter.
- [ ] Request ids are unique (a counter, not `os.time()`).
- [ ] A contract test starts two or more requests at once against an `httptest` server with
  different delays, from a plugin and from `HTTP:asyncRequest`, and every callback gets its own
  status and body.
- [ ] The new test fails on the current code.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry: plugins and HTTP clients no longer time out at random when several poll at once.

## Contract impact
New contract row "Async HTTP: each request gets its own response" in CONTRIBUTING.md.
