# #163: Dashboard energy flow widget

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/run-local-vclu`
- **Needs hardware:** — (a PV inverter and a bidirectional meter to check it on a real house)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`section:energy(id, label, opts)` shows PV, the grid, a battery and the home as nodes, with the
power flowing between them drawn as moving dots, on public, PIN and session dashboards.

## Context
A house with PV reads its power from an inverter plugin and a meter at the entry; a status tile
per number does not show where the power goes. The widget takes functions, as the other widgets do,
and never a plugin: the module maps its sources (`_:value(...)`).

- **Signs:** `pv` 0 or more; `grid` + taken from the grid, - given to it; `battery` + charging,
  - discharging. `invert = { grid = true, battery = true }` flips a source counting the other way.
  An inverter readout that sleeps at night gives nil: the PV node shows "—" and the balance
  counts it as 0.
- **home** without a function is `pv + grid - battery`, at least 0; unknown while grid or battery
  has no reading or a source fails. A measured home scales the lines into it to its value.
- **Lines** (positive from the first node to the second): pv→home, pv→grid, pv→battery,
  grid→home, grid↔battery, battery→home. PV goes to the battery first, then to the grid, the rest
  to the home; a discharging battery feeds the home, the rest goes to the grid. Computed in Lua so
  it is tested; a line touching an unread node carries nothing. Readings out of step: the
  remainder is dropped, no line goes negative.
- Each source is read in its own `pcall`: a failing one, a non-number (decimal strings count),
  NaN or inf shows "—" on its node, not a broken tile. `today` gives kWh under the nodes.
- The page builds the drawing with DOM APIs (`createElementNS`, `textContent`), colours from CSS
  variables on the tile, one `requestAnimationFrame` loop for all tiles; `prefers-reduced-motion`
  shows arrows instead of dots. Below 10 W a line is idle.

Later, not here: today's kWh from metrics aggregated per day (the bars widget).

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (`energyWidget`, `energyState`, definition and values)
- `pkg/lua/dashboard_energy_test.go`
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go` (runtime schema), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] The definition and values carry each node's power in W, the battery's SoC, the lines' power
  and today's kWh (`go test ./pkg/lua/ -run DashboardEnergy`).
- [ ] home from the balance, invert, `unit = 'kW'`, a node left out without its function, and a
  failing source on its node only are covered by tests.
- [ ] Unknown options, non-functions, invert and today keys fail at build with the module's line.
- [ ] Screenshots light and dark, tablet and phone, from the demo with sources changing over time.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0163-dashboard-energy-flow.md`.

## Contract impact
Adds `section:energy` and `DashboardBuilder:energy` to the public Lua API (new, nothing changes).
