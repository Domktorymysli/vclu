# Contributing to vCLU

Thanks for your interest in contributing! vCLU is an AI-native smart home runtime — a lightweight, programmable controller with a Lua scripting engine, built-in MCP server for AI agents, and a web-based dashboard. We welcome contributions of all kinds.

## Getting Started

### Prerequisites

- Go 1.26+
- Git

### Build

```bash
git clone https://github.com/Domktorymysli/vclu.git
cd vclu
go build ./cmd/vclu/...
```

### Run locally

```bash
# Auto-detect network interface
./vclu --auto --data-dir ./data

# Or specify interface
./vclu --interface en0 --data-dir ./data
```

Web UI at `http://localhost:8080`. On first run, the setup wizard will guide you through configuration.

### Run tests

```bash
go test ./...
```

## Making Changes

### Branch naming

```
feat/short-description    # new feature
fix/short-description     # bug fix
docs/short-description    # documentation only
chore/short-description   # maintenance, deps, CI
```

### Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add auto-backup scheduler
fix: prevent nil panic in client reporter
docs: add Docker installation guide
chore: update bootstrap to 5.3.3
```

- Use imperative mood ("add" not "added")
- Keep the first line under 72 characters
- One logical change per commit

### Pull requests

1. Fork the repo and create your branch from `main`
2. Make your changes
3. Ensure `gofmt -l .` prints nothing, `go vet ./...` reports no issues and `go test ./...` passes
4. Run `go test -race` on the packages you touched (CI runs it on everything)
5. If users notice the change, add a file to `changelog.d/` (see its README); do not edit `CHANGELOG.md`, the release assembles it
6. Open a PR against `main`

Keep PRs focused — one feature or fix per PR. If your change is large, consider splitting it into smaller PRs.

If the `vulncheck` job fails, upgrade the module that brings in the vulnerability (`go get module@fixed-version`, then `go mod tidy`). If there is no fixed version and vCLU does not use the vulnerable code, add the id to `.github/govulncheck-accepted.txt` with the review date and the reason.

#### PR checklist

- [ ] Code builds without errors
- [ ] Tests pass, also with `-race`
- [ ] User-visible changes have an entry in `changelog.d/`
- [ ] New user-visible behaviour has a contract test (see [Contracts](#contracts))
- [ ] No contract test weakened or removed, or the PR says `Contract change:` and why
- [ ] No unrelated changes mixed in

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Keep functions short and focused
- Error messages: lowercase, no punctuation (`"failed to read config"` not `"Failed to read config."`)
- Avoid adding dependencies unless truly necessary — the project aims to stay lightweight

### Frontend

- Vanilla JS (no frameworks) — keep it simple
- Use the existing prototype pattern for JS modules (see `backup.js`, `dashboard.js`)
- All user-facing strings must use the i18n system (translations in `web/locales/`)
- CSS follows the existing Bootstrap 5 + dark theme conventions

### Lua runtime

- Files in `pkg/lua/runtime/core/` implement the object system
- User-facing modules go in `modules/` (auto-loaded at startup)
- Always test Lua changes with `go test ./pkg/lua/...`

## Contracts

vCLU runs real houses, and much of its code is written with AI agents. The
behaviour that user scripts, plugins and integrations rely on is pinned down by
contract tests. A change that breaks one of them breaks someone's house.

| Contract | Guarantees | Tests |
|---|---|---|
| State changes | One real change = exactly one `state_changed` event with `old`, `value`, `origin`, `actor`; the same value emits nothing | `pkg/lua/state_change_test.go` |
| Who changed it | Entry points (web, MCP, MQTT/HA, HomeKit, CLU, Satel, modules, plugins) set `origin`/`actor`; timers keep the context they were created in | `pkg/lua/state_context_test.go`, `pkg/mcp/state_context_test.go`, `pkg/web/state_context_test.go` |
| Access control | `readonly`/`hidden` are enforced where commands execute; hidden objects never show up for bots, history included; remote access gets the hidden `no-access` profile unless one is assigned, and its commands carry origin `remote` | `pkg/lua/access_*_test.go`, `pkg/accesscontrol/remote_test.go`, `pkg/web/access_remote_test.go`, `pkg/lua/ha_*readonly_test.go`, `pkg/mcp/device_history_c0_test.go` |
| Expose | `expose()` options, `filter` thresholds, events of exposed objects | `pkg/lua/exposed_objects_test.go`, `pkg/lua/state_filter_test.go`, `pkg/lua/lua_contract_test.go` |
| Thermostat in Home Assistant | A `climate` publishes current and target temperature and off/heat, announces only those topics, takes a target into `setSetpoint` clamped to `min`/`max`; `exposeRegistry()` leaves it out | `pkg/lua/ha_exposed_climate_test.go`, `pkg/lua/ha_climate_readonly_test.go` |
| Virtual thermostat | `VirtualThermostat` switches its output only on crossing target ± hysteresis (both directions); a source without a change for `staleAfter` or an output on for `maxOn` switches it off; before a reload or a stop every output is switched off (once, only when not already off), and a new one starts with the output off and ignores the source's old value; `persist` keeps set point, mode and state; Home Assistant and `lib/panel` take it as a thermostat | `pkg/lua/virtual_thermostat_test.go`, `pkg/lua/virtual_thermostat_release_test.go` |
| Colour light in HomeKit | An exposed `RemoteRGBLight` gets Hue and Saturation, other lights do not; a colour report alone updates them; Hue and Saturation from the Home app make one `setColor`, keeping brightness, On and white; readonly refuses them | `pkg/homekit/color_test.go`, `pkg/lua/homekit_color_test.go` |
| Virtual presence | `VirtualPresence` is on while a trigger is active, or a keep source after a start, or a closed door after a trigger went on (0 to 1) while it was closed, or one on at the closing is still on `doorGrace` s later (until it opens, also under an override), then `hold` s; a door closed behind someone leaving (trigger off within `doorGrace`) lets the hold run out; `setOverride(nil)` with no override changes nothing; a keep source or a door closed on an empty room never starts it; a signal in the hold extends it with no second `OnSwitchOn`; `setOverride(1/0/nil)`; `getReason`/`getRemaining`/`getDuration`; starts empty, also after a reload; unknown options and unresolved paths fail at `new()`; `expose(p, "occupancy")` is a Home Assistant `binary_sensor` with `device_class: occupancy` and a HomeKit OccupancySensor | `pkg/lua/virtual_presence_test.go`, `pkg/homekit/accessory_factory_test.go`, `pkg/mcp/sandbox_scenario_test.go` |
| Dashboard rooms | `section:area` / `dash:floor` pick widgets by kind in a fixed order, ids `area:` + path stay the same, `dashboard = false/true` | `pkg/lua/dashboard_area_test.go` |
| Dashboard room tiles | `section:room` counts lights, dimmers above 0 and shown switches on, covers open above 0 or moving; temperature and humidity are the average of the area's sensors, or a path or function, left out with none; the room it opens holds what `section:area` gives, with its ids (`#n` when shown twice), and acts through the usual values and actions; a disabled tile disables them; `dash:floor(..., { rooms = true })` gives one section of tiles in the floor's order (ids `area:@` + area); rooms follow expose changes and are not refilled per poll or by auth, PIN or the app manifest; `vclu_dashboards` with an `id` shows the tile, not its room's widgets (without `id` it lists dashboards, no widgets); `link` as a chip's; bad arguments fail at the module's line | `pkg/lua/dashboard_room_test.go`, `TestContract_MCPDashboards_ByID` in `pkg/mcp/tools_answers_contract_test.go` |
| Lua runtime | Timers, GPIO polling, MQTT subscriptions and Go functions survive a Lua reload; pending state is flushed at shutdown | `pkg/lua/lua_contract_test.go`, `pkg/lua/lua_timers_test.go`, `pkg/lua/shutdown_test.go` |
| Persistent state | `persist()` and `persist = true` on virtual objects survive reload and restart | `pkg/lua/persist_test.go`, `pkg/lua/virtual_persist_test.go` |
| History | Changes survive a restart without fake entries; a torn file loses at most its last line; deleting data needs a web session | `pkg/history/`, `pkg/lua/history_test.go`, `pkg/web/handlers_history_test.go` |
| Async HTTP | Every async HTTP request from Lua (plugins, `HTTP:async*`, `HTTPRequest`) gets its own response and the transport error, however many are in flight | `pkg/lua/http_async_contract_test.go` |
| Plugin HTTP errors | A plugin's HTTP callback gets the error as its second argument and as `resp.err`; a poller's `done(data, err)` counts a tick as failed only with `err` | `pkg/lua/plugin_errors_contract_test.go` |
| MCP device tools | `vclu_devices`, `vclu_device_set`, `vclu_device_execute`, `vclu_scenes`, `vclu_scene_execute` keep their JSON field names and types; dimmer `state` is 0..1, cover 0..100 | `pkg/mcp/tools_contract_test.go` |
| MCP dev tool answers | `vclu_logs` without `level` leaves out DEBUG and gives at most 50 entries, identical lines in a row as one entry with `repeated`; `level: "debug"` brings DEBUG back; `vclu_dashboards` without `id` lists `id`, `name`, `auth`, `widgetCount` and no widgets, with `id` that dashboard with its widgets, an unknown `id` is an error; every tool refuses an argument its schema does not name and a missing required one, naming the valid ones | `pkg/mcp/tools_answers_contract_test.go`, `pkg/mcp/tool_args_test.go` |
| Dashboards | The definition of every widget type stays as it is; a header chip gives its text, icon and colour, `nil` leaves it out, an error shows on the chip only; bad arguments fail at the module's line | `pkg/lua/dashboard_definition_test.go`, `pkg/lua/dashboard_chips_test.go` |
| Dashboard cover groups | A cover group's buttons and sliders reach every cover, 50 ms apart without blocking; a new movement or slat command drops the rest of the old series of its kind, stop ends both, disabling stops it; tilt goes only to covers reporting one; counts (no reading = unknown), average and section header follow the covers; a function list is read each time; ids `_group_*`/`_section_*` are refused | `pkg/lua/dashboard_covers_test.go` |
| Dashboard media player | `section:player` takes a MusicCast device or a table of functions; the values carry only playing, title, artist, album, artwork, volume, muted, power, online (artwork only as an absolute http(s) URL); each action calls its function with its argument, a missing function hides its button and its action is an error; volume 0..100; an error in `getState` is the tile's error; bad arguments fail at the module's line | `pkg/lua/dashboard_player_test.go` |
| Dashboard forecast | `section:forecast` takes the Weather plugin (by `getForecast`) or a function; days before today in the house's zone are dropped, then `days` are kept; `hours` is a count (the current hour and `hours - 1` after it); temperatures and rain to one decimal, chance (0-1, or a percent above 1) as a whole percent 0-100, `icon` as an alias of `iconName`, an icon that is not `^[%w-]+$` is `cloud`; `nil` is "no forecast", an error in `src` is the tile's error; bad arguments fail at the module's line. Plugins get `datetime` in the house's zone without `setTimezone` | `pkg/lua/dashboard_forecast_test.go`, `TestPluginSandboxDatetime` in `pkg/lua/lua_plugin_test.go` |
| Dashboard app mode | `:width('full')` is in the definition only when set, a bad value fails at the module's line; `/d/<id>/manifest.webmanifest` is served without the dashboard's auth and holds only its name (a `session` dashboard's id until login), theme colour, icons and scope `/`; unknown dashboard 404; the page and the PIN page link it with the home-screen meta tags | `pkg/lua/dashboard_width_test.go`, `pkg/web/dashboard_app_test.go` |
| Sandbox | `vclu_draft_test` has no real side effects and runs on a virtual clock | `pkg/mcp/sandbox_*_test.go` |
| Grenton protocol | Discovery encryption stays compatible with real CLUs; CLU state subscriptions are kept and renewed; the CLU's answer to a renewal corrects a state whose report was lost | `pkg/cipher/`, `pkg/server/`, `pkg/server/renew_reply_test.go` |

Rules:

- **Do not weaken or delete a contract test to make your change pass.** If a
  contract really has to change, start the PR description with
  `Contract change:`, explain why and how existing user scripts migrate.
- **New user-visible behaviour gets a contract test**: test what a user script
  or an integration sees (events, values, errors), not internals.
- **Tests run without hardware or network.** Use the existing fakes: Lua
  harnesses in `pkg/lua/*_test.go`, the sandbox in `pkg/mcp/`, temp dirs for
  files. A test that needs a real CLU or panel cannot run in CI.
- **No wall-clock sleeps for logic.** Use the virtual clock (sandbox) or an
  injected `now`; when a test must bound real time, loosen it under `-race`.

## Project Structure

```
cmd/vclu/           # main binary entrypoint
pkg/
  config/           # .vclu.json configuration
  server/           # Grenton UDP protocol
  web/              # web UI handlers and routes
  lua/              # Lua engine and Go-Lua bindings
  lua/runtime/      # Lua runtime (bootstrap, core objects)
  mcp/              # MCP (Model Context Protocol) server
  homekit/          # Apple HomeKit bridge
  metrics/          # time-series metrics collector
  accesscontrol/    # profile-based access control
web/
  templates/        # Go HTML templates
  static/           # JS, CSS, vendor libs
  locales/          # i18n translations (en.json, pl.json)
deploy/             # systemd service, install scripts
```

## Reporting Issues

- Use GitHub Issues
- For bugs: include vCLU version (`vclu --version`), OS, and steps to reproduce
- For features: describe the use case, not just the solution

## License

This project uses the [Business Source License 1.1](LICENSE).

**Why BSL?** vCLU is a solo project built over months of evenings and weekends. BSL protects this work from being repackaged and sold as a competing product, while keeping the source fully open for everyone to use, learn from, and contribute to. After 4 years each release converts to Apache 2.0 — fully permissive, no strings attached.

**What this means for you:** You can use vCLU for anything — personal, commercial installations, integrations — except building a competing controller product.

**Contributions are accepted under the [MIT license](https://opensource.org/license/mit).** By
opening a pull request you agree that your contribution may be used under the MIT license, and so
under the project's license, whichever that is now or later.
