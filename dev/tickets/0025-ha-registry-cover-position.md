# #25: Home Assistant position for covers published from the OM import

- **Type:** bug
- **Area:** home-assistant
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check on a real Grenton roller is welcome, not required)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A position set in Home Assistant on a Grenton roller published from the OM import reaches the
roller through `setPosition`, on the roller's own scale.

## Context
The registry path (objects from the OM import) has the same defect #1 fixed for dimmers:

- `HACommands.handlers.cover` (`Commands.lua`) matches `subpath == "position/set"`, but
  `HACommands.parseTopic` strips the trailing `/set`, so `subpath` is `"position"`. The branch never
  runs and a position command is refused as `unknown payload`.
- Once it runs, it looks for `obj.SetPosition`; `RemoteCover` has `setPosition` (lower case), so the
  write falls through to `obj:set(0, position)` instead of `execute(10, position, lamel)`.
- Discovery (`generateCover` in `pkg/omparser/generator_ha.go`) announces `position_open: 100`,
  `position_closed: 0`. `RemoteCover` documents the opposite (0 = open, 100 = closed). The
  conversion belongs in one place in the handler; the direction is not verified on hardware, so
  follow `RemoteCover`'s documentation and name it in a comment.

The `OPEN`/`CLOSE`/`STOP` branch has the same casing mismatch (`obj.Open` vs `RemoteCover:open`):
it falls back to `execute()` directly and skips `updateState`. Fix it in the same change.

Out of scope: what the publisher reports back. `StatePublisher.lua` sends the object's value as the
position, and `RemoteCover._lastPosition` is never updated. That needs the roller's feature
indexes confirmed on hardware first.

## Files
- `pkg/lua/runtime/integrations/homeassistant/Commands.lua`: the `cover` handler.
- a test next to `TestRegistryDimmerBrightnessScale` in `pkg/lua/ha_light_brightness_test.go`, or
  a new `pkg/lua/ha_cover_position_test.go`.

## Acceptance criteria
- [ ] A command on `vclu/<clu>/cover/<object>/position/set` calls `setPosition` with the value
  converted from HA's 0 = closed .. 100 = open, clamped to 0..100.
- [ ] `OPEN`, `CLOSE` and `STOP` call `open`, `close` and `stop` when the object has them.
- [ ] A non-number position is refused and changes nothing.
- [ ] The test drives `HACommands.handleMessage` with a registry object and fails on the current
  code.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
None. The MQTT topics and discovery payload stay as they are.
