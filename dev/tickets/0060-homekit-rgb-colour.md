# #60: HomeKit: a Grenton RGB light has its colour

- **Type:** feature
- **Area:** homekit
- **Size:** M
- **Epic:** —
- **Blocked by:** #41 (PR #47: `RemoteRGBLight` keeps its colour and white)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a Grenton LED RGB or LED RGBW module and the Apple Home app (to check the colour both ways)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An exposed Grenton RGB light is a colour lightbulb in Apple Home: its colour can be read and set there.

## Context
After #41 an LEDRGB from the OM import is a `RemoteRGBLight`: it keeps the colour the CLU reports
(feature 6 RGB, `getColor()` gives r, g, b) and, on an LED RGBW module, the white (feature 15,
`getWhite()`, 0..255); `setColor(r, g, b)` and `setWhite(w)` drive it. Home Assistant already gets
the colour (`schema: json`, `rgb`/`rgbw`).

The HomeKit bridge makes it a plain Lightbulb: On and Brightness only
(`createExposedLightbulb`, `pkg/homekit/accessory_factory.go`). The exposed list
(`ExposedObjects.lua`, the `dimmable` field; `ExposedObject` in `pkg/homekit/server.go`) carries
one value per object, and state updates reach an accessory as one float (`updateFunc(float64)`).

HomeKit describes colour as Hue (0..360) and Saturation (0..100), with Brightness separate
(`characteristic.Hue`, `characteristic.Saturation` in `github.com/brutella/hap`). So:

- the exposed list must say an object has a colour (as `hasPosition` does for a cover) and give
  the current colour;
- a colour change must reach the accessory without a value change (`RemoteRGBLight` emits
  `OnColorChange`, as Home Assistant uses it);
- Hue and Saturation arrive from the Home app as two separate writes, often both at once: combine
  them into one `setColor` and do not send the CLU two colours in a row;
- the conversion RGB ↔ HSV takes only hue and saturation; brightness stays the light's value, so
  pure colours keep V at full (r, g, b scaled so the largest is 255).

White has no characteristic of its own in a HomeKit Lightbulb. Leave it out: the white stays as the
CLU has it, and Home Assistant or the dashboard still sets it. A light that is off keeps its colour
(Apple Home shows the last one).

A plain dimmer and a light without a colour must stay exactly as they are; adding a characteristic
to an existing accessory changes the bridge's configuration (see `pkg/homekit/AGENTS.md`).

## Files
- `pkg/homekit/accessory_factory.go`, `pkg/homekit/server.go`
- `pkg/lua/runtime/core/ExposedObjects.lua` (the exposed list and the state that reaches HomeKit)
- tests in `pkg/homekit/` and `pkg/lua/`

## Acceptance criteria
- [ ] An exposed `RemoteRGBLight` is a Lightbulb with On, Brightness, Hue and Saturation; a dimmer, a DOUT light and an LED channel get no Hue or Saturation. A test shows both.
- [ ] A colour reported by the CLU (also with no change of value) updates Hue and Saturation; a test drives `fromFeature(6, …)` and reads the characteristics.
- [ ] Hue and Saturation written from the Home app at once end in one `setColor` with the right r, g, b; a test writes both and checks the command sent.
- [ ] RGB ↔ HSV round-trips the primaries, white and a mixed colour within ±1 per channel; unit test.
- [ ] Brightness, On and the white of an RGBW module are unchanged by a colour write.
- [ ] Readonly holds: a readonly RGB light refuses Hue and Saturation writes.
- [ ] Checked by hand in Apple Home with a real module: colour set in Home reaches the strip, and a colour set in Grenton shows in Home (say so in the PR).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
The HomeKit accessory of an exposed RGB light gains two characteristics, so the bridge's
configuration number changes once; accessory ids stay. The exposed list gains a field (additive).
