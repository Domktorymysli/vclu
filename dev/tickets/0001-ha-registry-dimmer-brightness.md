# #1: Home Assistant brightness for dimmers published from the OM import

- **Type:** bug
- **Area:** home-assistant
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check on a real Grenton dimmer is welcome, not required)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A brightness set in Home Assistant on a Grenton dimmer published from the OM import reaches the
dimmer on the 0..1 scale it uses, the same way it already does for objects exposed with `expose()`.

## Context
vCLU announces Grenton objects to Home Assistant in two ways:

- **`expose()`** in Lua: `StatePublisher.lua` and `HACommands.handleExposedMessage` in
  `Commands.lua`. Brightness is converted (0..100 from HA, 0..1 on the dimmer) and covered by
  `TestHADimmerScale` in `pkg/lua/ha_light_brightness_test.go`.
- **The registry**, objects from the OM import published by `omparser.GenerateHADiscovery`
  (`pkg/omparser/generator_ha.go`, `generateLight`, class `DIMMER`). Discovery announces
  `brightness_scale: 100`, and the state is published as value × 100
  (`StatePublisher.lua`, "Publish brightness for dimmers"). But the command handler
  `HACommands.handlers.light` (`Commands.lua`, the `brightness/set` branch) writes the number
  unscaled: `RemoteLight` has no `SetValue`, so it ends in `obj:set(0, brightness)`. A slider at
  37 % sets the dimmer to 37 instead of 0.37, i.e. full power.

`RemoteLight:setBrightness(percent)` (`pkg/lua/runtime/objects/RemoteLight.lua`) already takes
0..100 and divides by 100. Topics of this path look like `vclu/<clu>/light/<object>/brightness/set`.

## Files
- `pkg/lua/runtime/integrations/homeassistant/Commands.lua`: the `light` handler's brightness branch.
- `pkg/lua/ha_light_brightness_test.go`: the test.
- `CHANGELOG.md`

## Acceptance criteria
- [ ] Through `HACommands.handleMessage` with a registry `DIMMER` object: payload `37` on
  `vclu/<clu>/light/<object>/brightness/set` leaves the object's value at 0.37; `0` gives 0;
  `100` gives 1.
- [ ] Values outside 0..100 are clamped (`150` gives 1, `-5` gives 0); a non-number is refused
  with an error and does not change the object.
- [ ] An object with `setBrightness` gets that call; one without it gets `set(0, percent / 100)`.
- [ ] The new test fails on the code before the fix (say so in the PR).
- [ ] `TestHADimmerScale` and the other tests in `pkg/lua` still pass; `go test -race ./pkg/lua` passes.
- [ ] CHANGELOG entry in an "Unreleased" section at the top.

## Contract impact
None changed: this makes the registry path follow the scale it already announces to Home Assistant.
