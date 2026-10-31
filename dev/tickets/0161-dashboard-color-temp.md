# #161: A colour temperature widget on dashboards

- **Type:** feature
- **Area:** web | lua-runtime
- **Size:** M
- **Epic:** #68
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** — (a check on two Grenton dimmers as warm and cold white is welcome)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`section:colorTemp(id, label, fns, opts)` shows a tunable white light on a dashboard: a slider from
warm to cool white in kelvin, with a switch and a brightness slider when the light has them.

## Context
- Dashboards (`pkg/lua/runtime/core/Dashboard.lua`, `web/static/js/dashboard.js`) have `dimmer` and
  `color` widgets taking a table of functions or an object; follow them.
- `fns = { getKelvin, setKelvin, isOn?, switchOn?, switchOff?, getBrightness?, setBrightness? }`, or
  an object with `getColorTemp`/`setColorTemp` in kelvin. Mireds (Zigbee `color_temp`) only through
  `opts.unit = "mired"`: the widget converts 1e6 / x, rounded.
- No vCLU object reports a colour temperature today (no `color_temp`, mired or HomeKit
  ColorTemperature anywhere); `color.kelvinToRGB` in `pkg/lua/parsers.go` is a helper only.
- Grenton has no tunable white object: two dimmers (or two LED channels) wired to the warm and the
  cold strip. Their mix is a library (`lib/cct`, `dev/decisions/0002-runtime-libraries.md`), not
  part of the widget. The CLU drops bursts: the second channel's command goes out 50 ms after the first.
- `section:area` gives the widget (2700-6500 K, kelvin) to a light or dimmer whose object has `getColorTemp`/`setColorTemp`.
- Everything a poll reads runs in the widget's own `pcall`: one failing light does not break the dashboard.

## Files
- `pkg/lua/runtime/core/Dashboard.lua`, `pkg/lua/runtime/lib/cct.lua`
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/lua/dashboard_color_temp_test.go`, `pkg/lua/cct_test.go`
- `pkg/mcp/tools_dev.go`, `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] Definition and values carry `kelvin`, `on`, `brightness`, `min`, `max`, `step` and capabilities; a test covers functions, an object and mireds.
- [ ] `setKelvin` is clamped to `min`..`max`; a reading outside them is shown as it is; a value that is not a decimal number is refused; a test sends them.
- [ ] Unknown option, `min >= max`, `step <= 0` and a bad `unit` fail at build with the module's line; a test covers both forms.
- [ ] `cct.mix(warm, cold)` keeps the brightness when the kelvin changes and the kelvin when the brightness changes, and spaces its two commands; tests on a virtual clock.
- [ ] The tile shows "3000 K · ciepła" (warm / neutral / cool in English) and a gradient track, in both themes, on a tablet and a phone.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0161-dashboard-color-temp.md`.

## Contract impact
New dashboard widget and a new library function (public Lua API); nothing existing changes.
