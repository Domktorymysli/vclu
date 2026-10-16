# #0041: The channels of an LED RGBW module and its white channel work, in Lua and in Home Assistant

- **Type:** feature
- **Area:** grenton
- **Size:** M
- **Epic:** —
- **Blocked by:** — (builds on the LEDRGB method fix, PR "LEDRGB and dimmer send the methods Object Manager lists")
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check on a Grenton LED RGBW module is welcome, not required)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A Grenton LED RGBW module imported from Object Manager can be read and controlled per channel
(R, G, B, W) and as one RGBW light, from Lua, Home Assistant and HomeKit.

## Context
An LED RGBW 2.0 module (DIN and FM) holds two kinds of objects (Object Manager
device-interfaces, `module_2_0_LED_RGBW_*`; see `pkg/lua/AGENTS.md`, "Feature and method
indexes come from Object Manager"):

- `LEDRGB` (class 9): the whole light. vCLU has it as `RemoteRGBLight`. Since the LEDRGB fix it
  has `getWhite`/`setWhite` (SetWhiteValue 12, feature 15 WhiteValue 0..255), but Home Assistant
  is told `supported_color_modes: ["rgb", "hs"]` (`generateRGBLight` in
  `pkg/omparser/generator_ha.go`), so the white channel cannot be set from HA, and the state
  reports only the brightness (only feature 0 Value is subscribed).
- `LED_CHANNEL` (class 49, four per module): one channel. Feature 0 Value is **0..255**, not
  0..1, read-only; execute 0 SetValue(value, ramp), 1 SwitchOn(time, ramp), 2 SwitchOff,
  3 Switch, 4 HoldValue; set 1 RampTime, 2 MaxValue, 3 MinValue. The OM parser does not know
  class 49 (`pkg/omparser/types.go`), so no object is generated: a user who drives a strip per
  channel has nothing in vCLU.

A user on the vCLU Discord reports that the LED RGBW modules do not pass 0-100 % to Home
Assistant; which object they expose is not known yet. Other projects that talk to Grenton
expose each channel as its own light next to the RGBW light; vCLU may take the idea, not code.

## Files
- `pkg/omparser/types.go`, `generator.go`, `generator_ha.go`: class 49, its proxy object, its discovery.
- `pkg/lua/runtime/objects/`: a channel object (value on vCLU's 0..1 dimmer scale, converted from
  0..255 at the object), registered in `objects/init.lua` and `pkg/lua/embed.go`.
- `pkg/lua/runtime/integrations/homeassistant/`: `rgbw` colour mode for an RGBW light (white in the
  JSON command and state), brightness for a channel.
- `pkg/lua/runtime/utils/TypeMap.lua`, HomeKit mapping if a channel becomes a lightbulb there.
- tests in `pkg/lua/` and `pkg/omparser/`.

## Acceptance criteria
- [ ] An om.lua with class 49 objects generates one channel object each; a proxy test shows it.
- [ ] A channel's `setBrightness(50)` sends SetValue 128 (0..255), `switchOn`/`switchOff`/`toggle`
  send execute 1/2/3; a reported Value of 255 reads as brightness 100.
- [ ] Home Assistant gets each channel as a dimmable light whose brightness follows the reports,
  and a brightness from HA reaches SetValue.
- [ ] An RGBW `LEDRGB` is announced with `rgbw`; a JSON command with `color.w` reaches
  `setWhite`; an RGB-only module (no feature 15) is announced as before.
- [ ] Tests fail on the current code; `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased"; the PR says it was not tested on hardware unless it was.

## Contract impact
A new object type in generated proxies (re-import the OM project to get it) and a new colour mode
in Home Assistant discovery for RGBW lights. Existing objects keep their API.
