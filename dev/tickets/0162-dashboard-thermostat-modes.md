# #162: The thermostat widget switches modes

- **Type:** feature
- **Area:** web | lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`section:thermostat` shows a mode switch (manual, schedule, holiday, off...) under the target when
the thermostat or the module's functions can change the mode, and a tap changes it.

## Context
- The widget (#64, `pkg/lua/runtime/core/Dashboard.lua`, `thermostatWidget`, `thermostatState`)
  already reads `getMode` (0 manual, 1 holiday, 2 schedule, 3 heat-up), writes the target only in
  mode 0 and names the mode on the state line.
- `VirtualThermostat` has `setMode(0|1)` (set point, holiday) and `start`/`stop`; its modes come
  without configuration: manual, holiday (no `+`/`-`, the holiday target is in effect) and off.
- `RemoteThermostat` reports Mode (feature 8) but has no method that changes it. Object Manager's
  Thermostat v2 (`object_thermostat_v2.xml`) has execute 0 Start, 1 Stop, 5/6 HolidayModeStart/Stop,
  7/8 AutoModeStart/Stop and set 8 SetMode; none is verified on a CLU, so the Grenton thermostat only
  shows its mode here. Adding `RemoteThermostat:setMode`/`start`/`stop` is a separate ticket with a
  hardware test; the widget then picks them up.
- A schedule is not a thermostat feature: a module keeps a flag and the calendar sets the set point
  while it is on. The widget takes the module's `getMode`/`setMode` with a list of modes.
- `opts.readonly` (true or a function) locks the mode switch as it locks the target.

## Files
- `pkg/lua/runtime/core/Dashboard.lua`, `pkg/lua/dashboard_thermostat_modes_test.go`,
  `pkg/web/dashboard_action_http_test.go`
- `web/static/js/dashboard.js` (`renderThermostat`, `TEXTS`), `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go`, `docs/ai/examples.md` (with the schedule pattern), `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] A `VirtualThermostat` tile has a switch manual / holiday / off; a tap reaches `setMode`, `stop`
  or `start`; in holiday the target buttons go and a set is refused. A test covers each.
- [ ] `opts.modes` (a list `{value, label, icon?, readonly?}` or a map `{value = label}`) with
  `fns.getMode`/`fns.setMode` gives a table of functions a switch; `modes = false` hides an object's.
- [ ] The action `mode` is refused when the widget is disabled, read-only, locked, or the value is not
  listed; nothing reaches the thermostat. A test sends each, one through the HTTP endpoint.
- [ ] `build()` refuses unknown keys in a mode, a `modes` that is neither a list nor a map, a value
  twice, `modes` without `getMode`/`setMode`, `setMode` without `modes`, a value an object does not have.
- [ ] A failing `getMode` leaves the tile without a mode and without `+`/`-`; the other tiles stay.
- [ ] Both themes, a tablet and a phone; `go test ./...` and `go test -race ./pkg/lua/... ./pkg/web/...` pass.
- [ ] A changelog entry; `docs/ai/examples.md` shows a `VirtualThermostat` with modes and the schedule pattern.

## Contract impact
Dashboard Lua API: a new option (`opts.modes`), a new key in `fns` (`setMode`) and a new action
(`mode`). Additive; existing dashboards keep working, a `VirtualThermostat` tile gains the switch.
