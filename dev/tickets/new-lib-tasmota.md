# #NNNN: lib/tasmota: a Tasmota device is one line in a module

- **Type:** feature
- **Area:** mqtt | lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check on a real Tasmota is welcome; the maintainer has a Sonoff Basic, Tasmota 13.3.0)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A module adds a Tasmota device with one line and exposes it like any other object:

```lua
local tasmota = require("lib/tasmota")
local lamp = tasmota.switch("tasmota_75088A")          -- { relay = 2 } for POWER2
expose(lamp, "light", { path = "tasmota.biurko", name = "Lampka biurko", area = "Biuro" })
```

and the state, commands, availability and history work without code of the module's own.

## Context
- Today a Tasmota needs a module of its own of about 80 lines (the maintainer's
  `modules/tasmota/init.lua`, 2026-10-19; `docs/ai/examples.md`, "MQTT Tasmota", shows a shorter
  version that misses most of it). Every device repeats the same mechanics:
  - the state comes from three places: `stat/<topic>/POWER`, `stat/<topic>/RESULT` (JSON, the
    answer to a command) and `tele/<topic>/STATE` (JSON, every TelePeriod); with several relays
    the key is `POWER<n>`;
  - commands go to `cmnd/<topic>/POWER<n>` as `ON`/`OFF`;
  - a state from the device must not go back to it as a command (a `VirtualSwitch` emits
    `OnSwitchOn` on `updateState`, which the module's command handler turns into a publish);
  - Tasmota does not retain its state by default: ask for it (`cmnd/<topic>/STATE`) on start and
    when the device comes back (`tele/<topic>/LWT` = `Online`);
  - availability from LWT (`Online`/`Offline`).
- This is a ready-made policy built from core functions (`MQTT`, `VirtualSwitch`,
  `VirtualNumber`), so it is a library, not core and not a plugin: Tasmota needs no connection of
  its own, the MQTT client exists (`dev/decisions/0002-runtime-libraries.md`). The library
  creates objects; `expose` stays in the module (as for plugins).
- History: a change from the device's own button arrives over MQTT and is recorded as
  `origin = mqtt, actor = service:mqtt` (`pkg/lua/state_context_test.go`). Better: the device as
  the actor (`device:tasmota_75088A`), so history tells a press on the device from a command.
  Check how `StateChange` sets the context for a change made inside an MQTT callback.
- Discovery: Tasmota publishes itself, retained, on `tasmota/discovery/<mac>/config` (topic,
  friendly names, relays, sensors) unless `SetOption19` switched it to HA's format. A list from
  it lets an agent (and `vclu_runtime_schema`) see the devices without the user copying topics
  from the device's web page. Objects are still created only by a module's line: nothing appears
  in Home Assistant without the user deciding.

## Scope
- `tasmota.switch(topic, { relay })`: a `VirtualSwitch` bound as above.
- `tasmota.dimmer(topic)`: brightness through `Dimmer` (0..100), on/off through `POWER`;
  a dimmer object with the core's scale (`VirtualDimmer`, check its brightness scale).
- `tasmota.sensor(topic, "ENERGY.Power", { unit })`: a read-only number from `tele/<topic>/SENSOR`
  (and the answer to `cmnd/<topic>/Status 10`), for energy plugs and temperature sensors.
- `tasmota.online(topic)` and `tasmota.devices()` (from discovery: topic, name, relays).
- One small internal helper for "state topic + command topic + payload map + echo
  suppression", so the next libraries (ESPHome over MQTT, Shelly, zigbee2mqtt) are thin layers
  over it. Keep it internal (`lib/mqtt_device` or local to the library) until a second library
  uses it; do not add it to the core.

## Not in scope
- ESPHome: over MQTT its own ticket on top of the helper above (`<prefix>/<component>/<name>/state`
  and `/command`); its native API (protobuf over TCP, Noise encryption) a spike first, then a plugin.
- Creating objects automatically from discovery.
- Configuring Tasmota from vCLU (rules, timers, `SetOption`s).

## Files
- `pkg/lua/runtime/lib/tasmota.lua` (new), `pkg/lua/tasmota_test.go`
- `docs/ai/examples.md` ("MQTT Tasmota" replaced by the library), `pkg/mcp/tools_dev.go`
  (`vclu_runtime_schema`), `docs/ai/lua-api-reference.md` (generated)
- docs.vclu.pl: the Standalone tutorial's Tasmota step and the MQTT page

## Acceptance criteria
- [ ] A test drives MQTT messages (`MQTT:_handleMessage`) for POWER, RESULT, STATE and LWT and checks the object's state and availability.
- [ ] A command from Lua, a dashboard or Home Assistant publishes `cmnd/<topic>/POWER<n>` once; a state from the device publishes nothing; a test checks both.
- [ ] The state is asked for on start and after LWT `Online`.
- [ ] A change from the device is recorded in history with the device as the actor (or the PR explains why not).
- [ ] `tasmota.devices()` lists a device from a retained discovery message; a test feeds one.
- [ ] A module using the library works in `vclu_draft_test` (if the sandbox cannot deliver an MQTT message to a subscription, add a step for it or say so in the PR).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry.

## Contract impact
New runtime library (`dev/decisions/0002-runtime-libraries.md`): its public functions become a
contract. No core change expected.
