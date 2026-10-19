# #NNNN: VirtualThermostat: a thermostat run by vCLU from any sensor and output

- **Type:** feature
- **Area:** lua-runtime
- **Size:** L
- **Epic:** —
- **Blocked by:** — (shares the thermostat interface with the HA ticket; agree on it there)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`VirtualThermostat:new("salon", { source = sensor, output = heater, hysteresis = 0.5 })` keeps a
room at its target with any temperature sensor (Grenton, Zigbee, a plugin) and any on/off output,
and looks to the panel, Home Assistant, HomeKit and dashboards like a Grenton thermostat.

## Context
- vCLU has virtual objects for switches, dimmers, covers, numbers (`pkg/lua/runtime/objects/Virtual*.lua`)
  but no thermostat; a Grenton thermostat runs only on a CLU and only with Grenton's sensors.
  `RemoteThermostat` (#44) defines the interface to match: `getValue()` current temperature,
  `getTarget()`, `getSetpoint()`/`setSetpoint(v)`, `getState()`, `getMode()`, `CHANGE_EVENTS`.
- Logic: heat (or cool, `direction`) when the temperature leaves target ± hysteresis; the output is
  switched only on a crossing, not on every reading. `min`/`max` bound the set point.
- Safety first: no reading from `source` for `staleAfter` (e.g. 15 min) switches the output off and
  marks the thermostat stale (logged once); a `maxOn` limit is worth considering. Never leave an
  output on after vCLU stops or reloads: decide what happens on reload and test it.
- `persist` keeps the set point and the state across restarts (as other virtual objects).
- Modes: off, set point, holiday (its own value); a schedule (auto) can be a later step or reuse a
  scheduler, say which in the PR.
- Not in scope: appearing in Object Manager as a class 18 object (emulated modules, #11, leave the
  thermostat out); PID control of an analog output.
- Policy (which room, which sensors to average) belongs in modules or a `lib/`, not in the object
  (`pkg/lua/AGENTS.md`, "The Lua API is a contract, keep it small").

## Files
- `pkg/lua/runtime/objects/VirtualThermostat.lua`, `objects/init.lua`, `pkg/lua/embed.go`
- `pkg/lua/runtime/utils/TypeMap.lua` (expose type)
- tests on a virtual clock (`pkg/lua/history_test.go` harness)

## Acceptance criteria
- [ ] Output switches at target ± hysteresis, both directions; a test drives readings on a virtual clock.
- [ ] A stale source switches the output off; a test checks it.
- [ ] Set point and state survive a restart with `persist`.
- [ ] `lib/panel`'s `menu:thermostat` and `menu:add` work with it unchanged; a test shows it.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry; docs.vclu.pl page with an example (Zigbee sensor + Grenton relay).

## Contract impact
New public object class; uses the thermostat interface from the HA ticket.
