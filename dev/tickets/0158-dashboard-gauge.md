# #158: Dashboards: a gauge tile, an arc or a bar with thresholds and a target

- **Type:** feature
- **Area:** web | lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a tablet on the wall to check readability)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`section:gauge(id, label, getValue, opts)` shows a number between `min` and `max` on a 240° arc
or a horizontal bar, coloured by thresholds, with an optional target marker (a contracted power
limit, a set humidity).

## Context
- A `status` tile shows a number, but not how close it is to a limit: house power against the
  contracted kW, a tank level, CO₂. Today a module colours a status with a `color` function.
- Widgets: `pkg/lua/runtime/core/Dashboard.lua`, `web/static/js/dashboard.js`,
  `web/static/css/dashboard.css`; the tile style of #68 (icon in a circle, name, state line).
- Values reach the page by polling `Dashboard.API.getValues`; a poll is the whole state of a tile.

## Scope
- `section:gauge` and `dash:gauge` with opts `min` (0), `max` (100, above `min`), `unit`,
  `decimals` (0-6, as `status`), `style = "arc" | "bar"`, `thresholds = { {value, color}, ... }`
  (ascending; colours the page knows: `accent`, `success`, `warning`, `danger`, `info`, `primary`,
  `#hex`, `rgb()`, `hsl()`), `target` (a number or a function), `icon`, `disabled`, `span`,
  `size`, `color`. Unknown options and wrong values fail at build, at the module's line; not in
  the header.
- `getValue` returns a number (a decimal string counts, not hex); nil shows `—`; an error, or a value that
  is not a finite number, is the tile's error. A value outside `min..max` is drawn at the end of the
  scale, the number shows it as it is.
- From each threshold's value on, the fill and the tile take its colour; `warning` and `danger`
  make the tile an alarm, as a coloured status does. A target function is read on every poll; nil
  or a failure (logged once) hides the marker.
- The page draws an inline SVG with DOM calls (no HTML from values), in the theme's colours,
  with a transition on a change (none with reduced motion). Large on a wall tablet; on a phone
  the arc shrinks with the tile and a bar puts the value above it.

## Files
- `pkg/lua/runtime/core/Dashboard.lua`, `pkg/lua/dashboard_gauge_test.go`
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go` (`vclu_runtime_schema`), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] The definition carries `min`, `max`, `unit`, `decimals`, `style`, `thresholds` (a list, empty
      without them), `target` and the value; polls carry `value`, `target`, `error`, `disabled`
      (`go test ./pkg/lua -run Gauge`).
- [ ] Each wrong argument fails at build with `section:gauge("id")` and the module's line.
- [ ] A value of 128 on 0..100 fills the whole scale and shows `128 %`; nil shows `—`; a failing
      `getValue` shows the error mark (screenshots, light and dark, tablet and phone).
- [ ] An action on a gauge is refused as read-only.
- [ ] `go test ./...` and `go test -race ./pkg/lua/... ./pkg/web/...` pass.
- [ ] A changelog entry in `changelog.d/0158-dashboard-gauge.md`.

## Contract impact
A new public Lua function (`section:gauge`, `dash:gauge`); no existing widget changes.
