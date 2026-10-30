# #156: Dashboards: widgets, groups and sections shown by state

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`opts.visible = function() -> bool` hides a widget, a group or a section while it returns false
or nil, and the page follows it on every poll.

## Context
`when(periods, fn)` runs once, when the dashboard is built, and knows only the time of day. A tile
that only matters in some states ("open covers" while one is open, an alarm section while a
partition is armed) needs a condition read with the values, like `disabled`.

- Read in `Dashboard.API.getValues` next to `disabled`; the answer gets `_hidden`, a map of what is
  hidden (widget ids, `_section_N`, `_section_N/_group_M`; group ids repeat across sections), left
  out when nothing is. The definition carries the same map as `hidden`, so the first render is right.
- A group's visible hides its label and the widgets after it, up to the next group. A section,
  or a group label, with widgets but none of them shown goes too, header included.
- A hidden widget is not read, in values nor in the definition (which still describes it, so the
  page can show it later), and its actions are refused. A hidden chip is a chip without a state, as
  when its function returns nil. A section's cover counts leave hidden tiles out; a hidden chart
  gets no metric data and a hidden calendar no range (the Go side caches both for 30 s).
- `visible` hides from the page; it is not access control. The definition still names hidden
  widgets: secrets belong behind `:auth('session')` or a PIN.
- An error in visible shows the widget, logged once per widget (fail open).
- A room tile's overlay (its room's widgets) goes with the tile: hidden, not read, actions refused.
  `dash:floor(..., {visible})` applies to each of its sections; `section:group` takes
  `{visible}` only; `when` is unchanged.
- The page fades a tile in and out (no fade with reduced motion) and changes only what changed;
  the grid is not redrawn.
- gopher-lua: a caught error in visible splits upvalues of closures (pkg/lua/AGENTS.md), so the
  code that walks a section keeps its state in tables.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (option, validation, `_hidden`, actions)
- `pkg/lua/dashboard_visible_test.go`
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go` (runtime schema), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] Every widget, chip, room tile, cover group, `section:group`, `dash:section` and `dash:floor`
  take `visible`; anything but a function fails at the module's line; a test.
- [ ] Values and the definition list what is hidden; a group hides what follows it; a section and a
  group with none of their widgets shown are hidden; nothing hidden leaves `_hidden` out; a test.
- [ ] A hidden widget is not read (values and definition) and a tap on it is refused, also in the
  overlay of a hidden room tile; shown again on the next poll; a test.
- [ ] Cover counts, chart metrics and calendar ranges leave hidden widgets out; a test.
- [ ] An error in visible shows the widget and logs once; a test.
- [ ] On the page a widget toggling visible fades in and out, takes no room while hidden, and a
  section without shown widgets loses its header; checked light and dark, tablet and phone.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0156-dashboard-visible.md`.

## Contract impact
Lua API: a new option, nothing removed. `Dashboard.API.getValues` gains the `_hidden` key and a
widget id `_hidden` is reserved. No decision record.
