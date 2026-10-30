# #159: Dashboards: press and hold, and a gate tile

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/run-local-vclu`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A dashboard button or toggle can require a press held for a set time, and `section:gate` shows a
gate's state with Open and Close held, so a stray tap on a wall tablet or phone opens nothing.

## Context
Today the only guard is `confirm` on a button (`pkg/lua/runtime/core/Dashboard.lua`), the
browser's `confirm()` dialog: awkward on a wall tablet and missing on toggles. A gate is built from
a button and a status tile by hand.

- `opts.hold = true | ms` (true = 800 ms, 300-5000) on `button` and `toggle`; `confirm` keeps
  working, `hold` with `confirm` fails the build. `confirm` exists on buttons only.
- The page (`web/static/js/dashboard.js`) fills a ring around the tile's icon while pressed
  (mouse, touch, Enter/Space held); an early release, the pointer leaving or a scroll cancels; a
  tap shows "Przytrzymaj" / "Hold"; completion vibrates (`navigator.vibrate(30)`) and runs the action.
- The action request carries `held: true`; `Dashboard.API.executeAction` refuses an action that
  needs a hold without it. Anyone can send the flag: it stops a stray tap and a naive script, not a
  deliberate caller; auth (`pin`, `session`) stays the protection. The only action endpoint is
  `POST /api/dashboard/:id/action` (public, PIN and session dashboards alike).
- `section:gate(id, label, {open, close?, stop?, state?, hold (default true), icon, disabled, span})`:
  state `"open"|"closed"|"opening"|"closing"|"moving"|nil`, open in warning colour, closed in
  success, moving in info; Open and Close held, Stop a tap; unknown options fail the build.

## Files
- `pkg/lua/runtime/core/Dashboard.lua`, `pkg/web/handle_web_dashboard.go`
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go` (runtime schema), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`
- tests: `pkg/lua/dashboard_hold_test.go`, `pkg/web/dashboard_action_http_test.go`

## Acceptance criteria
- [ ] `hold` outside 300-5000, not a number or boolean, or with `confirm` fails at the module's line
  (`TestDashboardHoldArgumentErrors`); so do a gate without `open`, an unknown option, a non-function
  callback and a gate in the header.
- [ ] The definition gives `hold` in ms; a gate its `state` and `capabilities` {close, stop}; a poll
  gives the gate's state, an error for another word or a failing function (`TestDashboardHoldDefinition`,
  `TestDashboardHoldGateValues`).
- [ ] Without `held: true` tap, toggle, open and close of a held widget are refused and nothing runs;
  with it they run; stop and widgets without hold run either way, through the endpoint on a public
  and a PIN dashboard (`TestDashboardHoldActions`, `TestDashboardActionEndpointHold`).
- [ ] In the demo: an early release, leaving the button and a click on a held toggle run nothing; a
  full mouse, touch or Space hold runs the action; screenshots light and dark, tablet and phone.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0159-dashboard-hold-gate.md`.

## Contract impact
Lua API: new `opts.hold` and `section:gate`/`dash:gate` (additive). `Dashboard.API.executeAction`
takes a fifth argument `held`; the HTTP action body takes `held` (a boolean). Nothing existing
changes behaviour: a widget without `hold` ignores the flag.
