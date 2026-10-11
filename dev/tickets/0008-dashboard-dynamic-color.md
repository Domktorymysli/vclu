# #8: Dashboard tiles change colour with their value

- **Type:** feature
- **Area:** web
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A status tile can turn e.g. red when its value crosses a threshold, and the colour follows the value
on an open page without reloading it.

## Context
A tile's colour is fixed when the dashboard is defined: `opts.color` goes into the definition
(`DashboardAPI.getDefinition` in `pkg/lua/runtime/core/Dashboard.lua`, `color = w.opts.color`), and
`web/static/js/dashboard.js` applies it once while rendering (`resolveColor`, named colours
`accent`, `success`, `warning`, `danger`, `info`, `primary` or any CSS colour). The values polled
every few seconds (`DashboardAPI.getValues`, `GET /api/dashboard/:id/values`, `updateWidget` in
`dashboard.js`) carry `value`, `error` and `disabled`, but no colour.

Real case: a tile with the number of SMS left in a package should turn red below 20. Today the only
option is to put a warning in the text.

Keep the API small (see `pkg/lua/AGENTS.md`): no new widget type and no threshold syntax; a colour
that may be a function is enough, and thresholds stay in the user's Lua.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (`status` options, `getDefinition`, `getValues`)
- `web/static/js/dashboard.js` (`renderStatus`, `renderHeaderWidget`, `updateWidget`)
- `pkg/lua/lua_dashboard_test.go`
- the dashboard API reference in `docs/ai/` and the Lua API reference

## Acceptance criteria
- [ ] `:status(id, label, icon, getValue, unit, { color = function() return 'danger' end })` works
  for section and header tiles; a string `color` keeps working as today.
- [ ] `getValues` includes `color` for a tile whose colour is a function (its current result, or
  nothing when it returns `nil`); `getDefinition` includes the current colour, never a function.
- [ ] An error inside the colour function does not break the dashboard: the tile keeps its value,
  has no colour, and the error is logged once.
- [ ] On an open page the tile's icon and value take the new colour at the next poll and drop it
  when the function returns `nil`.
- [ ] Lua tests in `lua_dashboard_test.go` cover function colour in values and definition, `nil`,
  and an erroring function.
- [ ] The API reference shows the SMS-style example (red below a threshold).

## Contract impact
Adds an accepted type for `opts.color` on `status`; existing dashboards are unchanged.
