# #NNNN: A thermostat widget on dashboards

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
`section:thermostat(id, label, icon, fns, opts)` shows a thermostat on a dashboard: current
temperature, target, state, and `+`/`-` that set the target, or no buttons when it is read-only.

## Context
- Dashboards (`pkg/lua/runtime/core/Dashboard.lua`, `web/static/js/dashboard.js`) have
  `cover`, `dimmer` and `color` widgets taking a table of functions; follow `cover`
  (`renderCover`) for the shape: `fns = { getCurrent, getTarget, setTarget?, getState? }`,
  `opts = { min, max, step = 0.5, unit = "°C" }`. Without `setTarget` the widget is read-only.
- A shortcut for an object with the thermostat interface (`RemoteThermostat`, later
  `VirtualThermostat`) is welcome, as long as the table form stays.
- Set the target once, after the last tap (debounce), and keep showing the chosen value until the
  object reports it, as `lib/panel` does (`Panel.HOLD_S`): a CLU confirms a set point with a delay.
- Show "--" until the first report, not 0.
- Dashboards are used on tablets on the wall: large touch targets, works in both themes.

## Files
- `pkg/lua/runtime/core/Dashboard.lua`, `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `web/locales/*.json`, the dashboard docs (docs.vclu.pl, getting-started/dashboards, runtime/dashboards)
- `pkg/mcp/tools_dev.go` (`vclu_runtime_schema` describes widgets for agents)

## Acceptance criteria
- [ ] The widget renders current and target and sets the target once per series of taps; a test covers the Lua side (widget definition, set call).
- [ ] Read-only has no buttons and the endpoint refuses a set; a test sends one.
- [ ] `go test ./...` passes.
- [ ] CHANGELOG entry; docs with an example.

## Contract impact
New dashboard widget (public Lua API).
