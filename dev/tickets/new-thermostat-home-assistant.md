# #NNNN: A thermostat exposed with expose() works in Home Assistant as a climate entity

- **Type:** feature
- **Area:** home-assistant | lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** — (builds on `RemoteThermostat` from #44)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check on a Grenton CLU thermostat is welcome, not required)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`expose(thermostat, "climate", { name = "Salon", area = "Salon" })` shows the thermostat in
Home Assistant with its current and target temperature and its mode, and a target set in HA
reaches the thermostat; with `readonly = true` HA only shows it.

## Context
- Since #44 the OM import creates a CLU's thermostats (class 18) as `RemoteThermostat`
  (`pkg/lua/runtime/objects/RemoteThermostat.lua`): value = temperature at its sensor,
  `getTarget()` (in effect, with the mode), `getSetpoint()`/`setSetpoint(v)` (PointValue),
  `getState()` (1 active), `getMode()` (0 set point, 1 holiday, 2 auto, 3 heat-up). Indices in
  `pkg/lua/AGENTS.md`. They are in the registry (Sync list) but not exposed.
- The expose type `climate` exists only in part: `ExposedObjects.lua` lists its capabilities,
  `StatePublisher.lua` announces `current_temperature_topic`, `temperature_state_topic`,
  `temperature_command_topic` and mode topics in discovery, but nothing publishes to those topics
  and no command handler takes `target_temp/set` or `mode/set`. An entity built from it today shows
  nothing and sets nothing.
- Define the object side once, so a virtual thermostat (separate ticket) and a plugin's thermostat
  fit too: an object is a thermostat when it has `getValue`, `getTarget` and `setSetpoint`
  (optional: `getState`, `getMode`, `start`/`stop`). Changes besides the value come with the events
  in `CHANGE_EVENTS` (`OnSettingsChange`); the publisher must listen to them, not only `onChange`.
- Modes: Grenton's Mode (set point / holiday / auto / heat-up) is not HA's `hvac_mode`
  (off / heat / cool / auto). Decide the mapping (e.g. `off` when State is 0, `heat` otherwise,
  `preset_mode` for holiday and auto) and record it in `pkg/lua/AGENTS.md`. A mode command may stay
  unsupported in a first version; then do not announce mode command topics.
- `readonly = true` must hold on the command path too (`pkg/lua/AGENTS.md`, "Read-only and hidden
  must hold on commands"): a target sent to a read-only climate is refused.
- Today `RemoteThermostat:_getExposeType()` returns nil, so `exposeRegistry()` leaves it out and
  `th:expose()` refuses: a switch would write feature 0, which on a thermostat is Source (its
  sensor binding). This ticket makes it `climate` (`_getExposeType` and `TypeMap.grentonToExpose`).
  Decide whether thermostats are then exposed automatically like other registry objects or only
  by `expose()`; with automatic exposure read-only is the safer default.
- A renewal answer can undo a value kept aside (the set point) set between the renewal and its
  answer (`applyRenewAnswer` skips only objects that published since); harmless for the panel,
  check it for a target set from HA.

## Files
- `pkg/lua/runtime/integrations/homeassistant/StatePublisher.lua`, `Commands.lua`, `Discovery.lua`
- `pkg/lua/runtime/core/ExposedObjects.lua`, `pkg/lua/runtime/utils/TypeMap.lua`
- tests next to `pkg/lua/ha_*_test.go`

## Acceptance criteria
- [ ] Discovery for an exposed `RemoteThermostat` announces only the topics that get published; a test checks the payload.
- [ ] A report from the CLU (temperature, target) publishes current and target temperature; a change of the target alone publishes too.
- [ ] A target from HA calls `setSetpoint` (clamped to `min`/`max` of the expose options); a read-only climate refuses it; tests send the command.
- [ ] `expose(th)` without a type exposes it as `climate`; it is never exposed as a switch (a test runs `exposeRegistry()`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry.

## Contract impact
Expose type `climate` gets defined behaviour (it was announced but empty), and the thermostat
object interface (`getValue`, `getTarget`, `setSetpoint`, `CHANGE_EVENTS`) becomes a contract
shared by HA, HomeKit, the panel and the dashboard.
