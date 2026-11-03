# #175: Home Assistant tilt for Grenton blinds

- **Type:** feature
- **Area:** home-assistant
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** Grenton ROLLER_SHUTTER 2.0 with slats (a blind), for the final check

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A Grenton blind exposed to Home Assistant shows a slat (tilt) slider there, reports the slat
angle and sets it.

## Context
Reported by a user: an exposed blind appears in HA as a plain roller, without tilt.

- `ROLLER_SHUTTER` (class 24, Object Manager `module_2_0_ROLLER_SHUTTER_DIN_4_fv03_02.xml`):
  feature 8 `LamelPosition` (read-only, 0..90 °, 90 = fully closed, 0 = fully open), method
  `execute(9, Position)` SetLamelPosition (0..90), feature 10 `LamelMoveTimeout` (0 = the roller
  has no slats). See `pkg/lua/AGENTS.md`.
- `RemoteCover` (`pkg/lua/runtime/objects/RemoteCover.lua`) has `getTilt`/`setTilt`, but
  `_lastTilt` is never written: feature 8 is not in `subscribeFeatures`, so `getTilt` always
  returns nil. The dashboard slat slider (`coverTilt` in `Dashboard.lua`) never shows for a Grenton
  roller for the same reason.
- Exposed path: discovery in `StatePublisher.lua` (the `exposedType == "cover"` block) has no
  `tilt_status_topic`/`tilt_command_topic`; the exposed `cover` branch of `Commands.lua` takes only
  `position` and `OPEN`/`CLOSE`/`STOP`.
- Registry path (objects from the OM import): `generateCover` in `pkg/omparser/generator_ha.go`
  has no tilt either, and `HACommands.handlers.cover` has no `tilt` subpath.
- `MqttHandler` already subscribes `+/+/+/tilt/set` and calls `setTilt`; reuse the topic shape.

Scale: HA tilt is 0..100 with 100 = open by default. Convert in one place (degrees 90 = closed,
0 = open ↔ HA 0 = closed, 100 = open) and name the direction in a comment. `setTilt` keeps taking
degrees, as the dashboard calls it.

Which rollers get tilt: only class 24 with a position. A blind is not told apart from a shutter in
om.lua unless `LamelMoveTimeout` is set there; if it is not, advertise tilt once the roller has
reported feature 8, or on every class 24 roller, and write down the choice in the PR.

Tilt on the exposed path goes through the same access gate as the position (readonly, hidden,
mqtt = false).

## Files
- `pkg/lua/runtime/objects/RemoteCover.lua`: subscribe feature 8, store `_lastTilt`, emit a change.
- `pkg/lua/runtime/integrations/homeassistant/StatePublisher.lua`: tilt topics in discovery, publish
  the tilt on change.
- `pkg/lua/runtime/integrations/homeassistant/Commands.lua`: `tilt` subpath on both cover paths.
- `pkg/omparser/generator_ha.go`: tilt in `generateCover` for class 24.
- tests next to `pkg/lua/ha_exposed_cover_test.go` and `pkg/lua/ha_cover_position_test.go`.

## Acceptance criteria
- [ ] Discovery of an exposed class 24 roller with slats carries `tilt_status_topic`,
  `tilt_command_topic` and the tilt range; a ROLLER_SH (class 11) and a roller without a position
  do not.
- [ ] A reported feature 8 value is published on the tilt state topic on the HA scale.
- [ ] A command on the tilt topic calls `execute(9, degrees)` with the value converted and clamped
  to 0..90; a non-number is refused and changes nothing.
- [ ] A readonly or hidden exposed roller refuses a tilt command.
- [ ] `getTilt` on a Grenton roller returns the last reported angle; the dashboard slat slider
  shows for it.
- [ ] Checked on a real blind: the HA slider moves the slats in the right direction.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0175-ha-cover-tilt.md`.

## Contract impact
Home Assistant discovery payload for covers (new optional fields; existing entities keep their
`unique_id`). No decision record.
