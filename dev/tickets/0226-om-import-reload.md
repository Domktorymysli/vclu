# #226: Grenton: importing om.lua leaves the running Lua on the old objects until a reload

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
After an om.lua or diagnostic-pack import (or `POST /api/om/regenerate`) the running vCLU uses the
new proxy: subscriptions, `expose()`, dashboards and integrations see the imported objects without
a restart or a manual reload.

## Context
- `processOMFile` and `handleAPIOMRegenerate` (`pkg/web/handle_web_om.go`) write `proxy_<serial>.lua`
  and only `Execute` it in the running runtime. The proxy assigns new `RemoteCover`/... objects
  to the CLU's namespace, while the CLU subscriptions (feature lists from `subscribeFeatures()`),
  `expose()`, dashboards and Home Assistant keep the objects created at start.
- Seen by a user on v1.11.0 after importing om.lua with slats: `getPosition()` nil, the movement
  stuck at 1, `getTilt()` nil; after `vclu_reload` (MCP) position 100, stopped, tilt 0. The
  changelog of #175 and #186 says only "import om.lua again", which on its own does not apply it.
- The panel already reloads the runtime elsewhere through `s.luaReloader.Reload()`
  (`handle_web_editor.go`, `handlers_backup.go`).

## Files
- `pkg/web/handle_web_om.go`: after a successful upload (once per ZIP, not per CLU), a delete and a
  regenerate, `s.luaReloader.Reload()` instead of executing the proxy; a failed reload or failed
  user modules are reported in the response, as `vclu_reload` reports them.
- `web/static/js/` of the OM files page: show that the runtime was reloaded, or the error.
- tests next to the existing OM upload tests.

## Acceptance criteria
- [ ] A test imports om.lua with a roller, then again with `set(10, …)` above 0: without a reload
  call, the roller reachable from Lua and from a dashboard has `_hasTilt` and subscribes feature 8.
- [ ] A ZIP with several CLUs reloads once.
- [ ] Delete and regenerate reload as well; a reload error is in the JSON response.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0226-om-import-reload.md`.

## Contract impact
`POST /api/om/upload`, the OM delete and `regenerate` reload the Lua runtime (state kept only in
Lua variables is lost, as with any reload). No decision record.
