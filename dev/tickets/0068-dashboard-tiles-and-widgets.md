# #68: Dashboards: modern tiles, rooms from the house model, new widgets

- **Type:** feature
- **Area:** web | lua-runtime
- **Size:** L (epic: split into tickets per item before work starts)
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (MusicCast for the player tile, a tablet on the wall to check readability)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A vCLU dashboard looks and works like a current smart-home app: tiles with an icon, a name and
a state, a room built from the house model in one line, and widgets for the things people look
at every day (a player, a forecast, groups of covers), with no change to existing dashboards'
Lua code.

## Context
- Widgets today (`pkg/lua/runtime/core/Dashboard.lua`, `web/static/js/dashboard.js`,
  `web/static/css/dashboard.css`): `button`, `toggle`, `slider`, `status` (with sparkline),
  `link`, `chart`, `select`, `image`, `group`, `cover`, `dimmer`, `color`, a header bar, sections,
  tab groups, themes, `when`. Tiles change colour with their value (#8). A thermostat widget is
  #64.
- The Smart Panel library (#44) builds a room from `expose()` (`Panel.area`, `Panel.floor`) and
  picks a row by device kind (`menu:add`). The same idea fits dashboards: the house model
  already names every device and its room for Home Assistant, HomeKit and the panel.
- Dashboards are used on tablets on the wall and on phones: large touch targets, both themes,
  readable from a distance.

## Scope (each item its own ticket)
1. **Tile style.** Every tile: an icon in a coloured circle (colour by state: on, off,
   moving, alarm), the name on the first line, the state on the second ("włączono",
   "40 %", "23,9 °C · 68 %"), softer cards, a consistent grid. Existing widgets get it without
   changes to their Lua definitions; a theme can still override colours.
2. **`section:area("Salon", opts)`** (and `section:floor`): widgets picked from the devices
   `expose()` put in the area, like `Panel.area`: a light is a toggle, a dimmer a slider, a cover
   a cover tile, an RGB light the colour widget, a temperature a status with sparkline; options
   `kinds`, `skip`, and the `panel`-like opt-out (decide whether `panel = false` or a separate
   `dashboard = false`). Filled when the dashboard is built and refreshed when objects are
   exposed later.
3. **Header chips.** Small pills with an icon and a short text (alarm time, a running appliance,
   who is home, the alarm state), each from a function; hidden when the function returns nil.
4. **Room summary tile.** A room's name with its temperature, humidity and what is on, one tile
   per room ("Sypialnia · wyłączono · 25,5 °C · 60 %"), opening the room.
5. **Media player tile.** Artwork, title, artist, play/pause, volume; first for the MusicCast
   plugin, with a small adapter table like `section:color` takes for a light.
6. **Weather forecast.** Days with an icon, min/max temperature and rain as a chart; needs the
   Weather plugin to fetch the forecast (plugin ticket).
7. **Cover groups.** One tile for several covers (all up, all down, a set position or slat
   angle), and a count of covers open/moving/closed in the section header.
8. **Wind rose** (optional): direction and speed from the weather plugin or a local sensor.

Order: 1 and 2 first (every dashboard benefits), then 3 and 4, then the rest.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` and its tests (`pkg/lua/dashboard_*_test.go`)
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`, `web/locales/*.json`
- `pkg/mcp/tools_dev.go` (`vclu_runtime_schema` describes widgets for agents), `docs/ai/examples.md`
- docs.vclu.pl: runtime/dashboards, getting-started/dashboards, a tutorial

## Acceptance criteria (for the tickets split from this one)
- [ ] Before/after screenshots (light and dark theme, tablet and phone width) in each PR.
- [ ] Existing dashboards render with the new tiles without changes to their Lua code; a test builds a dashboard of every widget type and checks the definition JSON.
- [ ] `section:area` picks widgets as `Panel.area` does; a test exposes devices of each kind and checks the widgets.
- [ ] `go test ./...` and `go test -race ./...` pass; CHANGELOG entries.

## Contract impact
New public dashboard functions (`section:area`, chips, new widget types). The tile look changes
for everyone; the Lua API of existing widgets does not.
