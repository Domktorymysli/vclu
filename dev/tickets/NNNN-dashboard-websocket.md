# #NNNN: Dashboards get their values pushed over a WebSocket

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`, `.agents/skills/run-local-vclu`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An open dashboard shows a change as it happens, not on the next poll, and ten open pages cost
vCLU what one does.

## Context
`dashboard.js` polls `GET /api/dashboard/<id>/values` every `poll` seconds (default 2), each page
on its own: every poll is a `getValues` on the Lua loop, and a change waits up to `poll` seconds.

- **One endpoint, the same JSON.** `GET /api/dashboard/<id>/ws`, the auth of `/values` (PIN cookie,
  session). The first message is what `/values` answers; then only the widgets that changed and
  those gone (a hidden chip, a `getValue` that gave nil). Other clients (an app rendering the same
  definition) use it as the page does.
- **Values are Lua functions**, so vCLU cannot tell when one changes: it reads `getValues` again
  after a state change (StateBus), after an action, and every `poll` seconds for the rest (the
  time, a variable), once per dashboard for all its sockets. A change after a quiet spell is read
  within 250 ms; while changes keep coming the wait grows to 1 s, so a noisy sensor cannot make
  an open page cost more than a read a second. A dashboard nobody watches is not read.
- **Limits:** 64 sockets, 8 per address; auth checked again every 15 s and at every reload.
- **A Lua reload** asks the pages to fetch the definition again.
- **Actions stay on `POST /action`.**
- **Polling stays** as the fallback: a browser without WebSocket, a proxy that does not pass the
  upgrade, a socket that dropped (until it opens again).

## Files
- `pkg/web/dashboard_live.go`, `pkg/web/handle_web_dashboard.go`, `pkg/web/server.go`
- `cmd/vclu/main.go` (state changes and reloads to the web server)
- `web/static/js/dashboard-live.js` (the socket, polling as the fallback), `web/static/js/dashboard.js`,
  `web/templates/dashboard.html`
- `pkg/lua/runtime/core/Dashboard.lua` (`getAuth` gives `poll`; the `:poll()` doc)

## Acceptance criteria
- [ ] The first message equals `/values`; a state change, an action and a hidden chip arrive as
  patches with only those widgets; a reload sends `definition` and the values again
  (`TestDashboardLiveValues`).
- [ ] Two sockets of one dashboard cost one `getValues`; none is read with no socket open
  (`TestDashboardLiveSharedRead`).
- [ ] A PIN dashboard without its cookie, an unknown dashboard and another site's `Origin` are
  refused, and a reload that changes the PIN closes an open socket (`TestDashboardLiveAuth`);
  one address gets at most 8 sockets (`TestDashboardLivePerAddress`), counted by `limitKey`;
  a client sending more than 30 messages in 10 s is closed with 1008 (`TestDashboardLiveFlood`);
  stopping the server closes the sockets (`TestDashboardLiveShutdown`).
- [ ] A dashboard with no values is an empty state, not an error (`TestDashboardLiveNoValues`); a
  socket that joins while `getValues` fails gets the error, without Lua's text
  (`TestDashboardLiveErrorForLateSocket`); reads under constant changes slow to one a second
  (`TestDashboardLiveGap`).
- [ ] In a running vCLU (`tools/demo/run.sh`) a dashboard page updates a toggle changed through the
  API without polling `/values`; against a vCLU without `/ws` it polls as before.
- [ ] The page's side on a virtual clock: no `/values` while the socket is open, polling after
  5 s without it and at once after a drop, retries 2 s doubling to 30 s plus up to half again,
  at once when the browser fires `online`, nothing after `stop`; a ping 2.5 s after each pong,
  9 s for the answer, the latency averaged (`TestDashboardLiveClient`, node); values no state change shows arrive every `poll`
  (`TestDashboardLivePollInterval`); a socket that does not read is dropped
  (`TestDashboardLiveSlowSocket`); a PIN changed without a reload closes a socket, a Lua error in
  the check does not (`TestDashboardLiveAuthRecheck`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/`.

## Contract impact
New endpoint; `/values` and the dashboard JSON are unchanged. None.
