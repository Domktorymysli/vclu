# #154: VirtualPresence: room presence from many sensors

- **Type:** feature
- **Area:** lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`VirtualPresence:new("lazienka", { trigger = {...}, keep = {...}, door = ..., hold = 120 })` turns
motion detectors, presence radars, doors and "something is in use" signals into one presence per
room, so a module switches a light on presence and off when the room is empty, without the light
going off while someone sits still.

## Context
Today a module does this by hand (`docs/ai/examples.md`, "kitchen motion sensor"): `StateBus`
plus `setTimeout`, and new motion does not extend the time, so the light goes off after 5 min even
while someone moves. Every house writes the same timer, each with its own bugs.

A single motion detector is not enough:
- **Sitting still.** A PIR (a Satel detector, a Grenton input) does not see a person who does not
  move. In the day it is often the only sensor in a room.
- **False starts.** A radar sees a curtain, a TV is switched on from a phone in an empty room.
- **Context.** In the evening more says someone is there: a TV, a computer drawing power, a lamp
  switched on by hand, a closed bathroom door.

Home Assistant has no built-in room presence; community integrations (Magic Areas, Area Occupancy
Detection, HA Soft Presence) solve it with hold timers, door logic ("wasp in a box"), overrides and
an explanation of what holds the presence. Some use Bayesian probability learned from history.
This ticket takes the deterministic parts only: a rule a contract test can state and an agent can
explain.

### Sources
- **`trigger`** (a list): starts presence and keeps it while active. Motion detectors, a button,
  a door opening. A `SatelZone`, a Grenton `DIN`, a Zigbee occupancy sensor, any object with
  `getValue()`; active is `1` or `true`.
- **`keep`** (a list): only keeps presence that is already on, never starts it. A radar, a TV, a
  socket with a computer, a lamp. So a curtain seen by a radar or a TV switched on remotely does
  not light an empty room.
- **`door`** (one object, `1` = open), "wasp in a box": once someone is seen inside after the door
  closed, presence stays on until the door opens; `hold` starts then. Seen inside means a trigger
  goes on (0 to 1) after the door closed, or a trigger on at the closing is still on `doorGrace` s
  later (default 10 s: walking in and closing the door while the detector still sees you). A
  trigger that clears within `doorGrace` is the person leaving: closing the door behind you lets
  `hold` run out as without a door. A detector with a long timeout of its own (many Zigbee
  occupancy sensors) needs a larger `doorGrace`, or belongs in `keep`. A bathroom with a closed door
  stays lit while someone sits still. The lock ends when the door opens, also under an override.

Presence is on while a trigger is active, or a keep source is active after a start, or the door is
closed with someone seen inside after it closed, and for `hold` seconds after the
last of these ends. A new signal during
`hold` only extends it: no second `OnSwitchOn`.

**A trap to document:** an output switched by this presence must not be its own `keep` source
(the light keeps the presence that keeps the light). `new()` cannot always see it; the docs and the
AI examples say it plainly.

### API (compatible with `VirtualSensor`, `_type = "DIN"`)
- `getValue()` / `isActive()`: `1` / `0`
- `onSwitchOn(cb)` / `onSwitchOff(cb)`
- `getReason()`: what holds presence now, e.g. `"trigger: satel.zone.3"`, `"keep: zigbee.salon.tv"`,
  `"door"` (only while the door holds it), `"hold 42 s"`, `"override"`, `nil` when empty
- `getRemaining()`: seconds of `hold` left (`0` while a source holds it or when empty)
- `getDuration()`: seconds since presence started (`0` when empty); "sits for over an hour" is a
  module's check, not a separate state
- `trigger(sec?)`: a signal from code ("I'm here"), extends by `sec` or `hold`
- `setOverride(1 | 0 | nil)`: forced present (guests) or forced empty (alarm armed); `nil` returns
  to the sensors
- `setHold(sec)` / `getHold()`: day and night, a hallway and a living room differ; a module or the
  calendar changes it (no "sleep mode" in the core)

### Rules
- Starts empty, also after a restart and a reload; the state from before does not count.
- `hold` default 300 s, `doorGrace` 10 s (both seconds >= 0); unknown options are an error at `new()`, like `VirtualThermostat`.
- Sources may be objects or registry paths; a path that does not resolve is an error at `new()`.
- Timing on `TIMERS` and `__go_time_ms()`, so the sandbox virtual clock tests it.

### `expose` type `occupancy`
`expose(p, "occupancy", {...})`: Home Assistant `binary_sensor` with `device_class: occupancy`,
HomeKit `OccupancySensor`. `motion` stays as it is.

## Out of scope
Probability and weights, learning from history, BLE room tracking as its own type (it can be a
`keep` source), light level, switching lights (a module does it on the events).

## Files
- `pkg/lua/runtime/objects/VirtualPresence.lua` (new), registered in `objects/init.lua`
- `pkg/lua/runtime/core/ExposedObjects.lua`, `utils/TypeMap.lua`,
  `integrations/homeassistant/StatePublisher.lua`: type `occupancy`
- `pkg/homekit/accessory_factory.go`: `OccupancySensor`
- tests next to `pkg/lua/virtual_thermostat_test.go` on the virtual clock
- `docs/ai/examples.md` (replace the hand-made motion timer), `docs/integrating.md`, regenerated
  `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] A trigger starts presence; it goes off `hold` s after the last trigger ends; a new trigger
  during `hold` extends it without a second `OnSwitchOn`; a test on the virtual clock.
- [ ] A keep source alone does not start presence; after a start it keeps it; a test.
- [ ] Door closed and a trigger going on inside, or one on at the closing and still on after
  `doorGrace`, keeps presence with no other source until the door opens, then `hold`; leaving and
  closing the door behind (the trigger off within `doorGrace`, no new one) goes off after `hold`;
  a door closed on an empty room does not start it; a test.
- [ ] `setOverride(1)`, `setOverride(0)`, `setOverride(nil)` with events; a test.
- [ ] `getReason()`, `getRemaining()`, `getDuration()` give the values above in each case; a test.
- [ ] Starts empty after reload; unknown option and unresolved path are errors at `new()`; a test.
- [ ] `expose(..., "occupancy")` gives the HA discovery with `device_class: occupancy` and a
  HomeKit `OccupancySensor`; a test.
- [ ] Works with a `SatelZone` as a trigger; a test with the Satel dispatch.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/<ticket>-<slug>.md`.

## Contract impact
A new object and a new `expose` type. Nothing existing changes.
