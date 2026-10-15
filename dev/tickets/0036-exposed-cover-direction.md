# #36: Home Assistant moves and shows an exposed cover in the right direction

- **Type:** bug
- **Area:** home-assistant
- **Size:** S
- **Epic:** —
- **Blocked by:** #25 (PR #32: `RemoteCover` value becomes the position)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check on a real Grenton roller is welcome, not required)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A cover published through `expose(obj, "cover")` opens, closes, stops and reports its position
in Home Assistant in the right direction, through the object's own methods.

## Context
#25 fixed covers published from the OM import (the registry path). Covers published with
`expose()` take another path with the same kind of defects:

- Discovery (`onObjectExposed` in `StatePublisher.lua`) announces `position_topic` and
  `set_position_topic` without `position_open`/`position_closed`, so Home Assistant assumes
  0 = closed .. 100 = open. vCLU counts 0 = open .. 100 = closed (`VirtualCover`, `RemoteCover`,
  HomeKit's `createExposedCover`), and the publisher sends the value unchanged
  (`publishExposedState`), so the position is shown inverted.
- `formatExposedState` reports `open` for a value of 100, which is closed on vCLU's scale.
- The command handler (`handleExposedMessage` in `Commands.lua`, branch `cover`) writes
  `handle:setValue(position)`, `setValue(100)` for OPEN and `setValue(0)` for CLOSE.
  `ExposedHandle:setValue` ends in `obj:set(0, v)` (see `pkg/lua/AGENTS.md`, "A write through the
  generic path skips an object's own methods"): for a Grenton roller that writes feature 0, which
  is read-only, so nothing moves, and its `updateState` stores the value, which HomeKit and
  history then show as the position until the next report. STOP calls `execute(3)` directly.
- A Grenton roller's position is `getPosition()`; without one (class 11, or a proxy not
  regenerated) the value is its movement and there is no position to show.

Convert at the integration edge in one place and use the object's `open`, `close`, `stop` and
`setPosition` when it has them; fall back to `setValue` for objects without them.

## Files
- `pkg/lua/runtime/integrations/homeassistant/StatePublisher.lua`: discovery, `formatExposedState`, `publishExposedState`.
- `pkg/lua/runtime/integrations/homeassistant/Commands.lua`: `handleExposedMessage`, branch `cover`.
- a test next to `pkg/lua/ha_cover_position_test.go`.

## Acceptance criteria
- [ ] A position from Home Assistant on the exposed cover topic reaches `setPosition` converted
  from 0 = closed .. 100 = open, clamped to 0..100 and rounded; a non-number is refused.
- [ ] `OPEN`, `CLOSE` and `STOP` call `open`, `close` and `stop` when the object has them, and
  change neither the object's value nor its position.
- [ ] The published position and the `open`/`closed` state follow Home Assistant's scale, from
  `getPosition()` when the object has it; nothing is published as a position when it returns nil.
- [ ] Tests cover a `VirtualCover` and a `RemoteCover` with and without a position, and fail on the current code.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
The MQTT payloads of exposed covers change direction: an automation in Home Assistant that
compensated for the inverted position needs changing. Say so in the CHANGELOG.
