# #186: Grenton: ROLLER_SHUTTER on fv01 firmware gets no position

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** Grenton ROLLER_SHUTTER 2.0 on fv01 firmware, for the final check

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A ROLLER_SHUTTER on fv01 firmware imported from om.lua moves up, down and stops from vCLU, Home
Assistant, HomeKit and dashboards, without a position it does not have.

## Context
- The OM import marks every class 24 roller `_hasPosition = true` (`generateObjectsForClass` in
  `pkg/omparser/generator.go`; the comment there says om.lua does not tell fv01 apart).
- On fv01 (`module_2_0_ROLLER_SHUTTER_DIN_fv01_02.xml`, `_DIN_3_fv01_02`, `_FM_fv01_02` in Object
  Manager's `device-interfaces`) the roller has no feature 7 Position, no feature 8 LamelPosition
  and no `execute(10)` SetPosition. With `_hasPosition`, `RemoteCover:open()`/`close()` send
  `execute(10, 100|0, 91)`, so the roller most likely does not move, and feature 7 is subscribed.
  Inferred from the definitions, not seen on hardware.
- om.lua does tell them apart. Object Manager writes a `set()` line for every settable feature,
  default values included (checked on DOUT and ANALOG_IN in
  `pkg/omparser/testdata/CLU212345678_om.lua`). Settable roller features:
  fv01: 1 MaxTime, 5, 6. fv02 and later: 5, 6, 9 MaxTime, 10 LamelMoveTimeout, 11. Feature 1 does
  not exist on fv02+. So `ROL…:set(1, …)` on a class 24 object means fv01.
- A roller with no `set` lines at all (the e2e fixture om.lua, a hand-written file) keeps the
  position as today: only a positive fv01 sign takes it away.
- Without `_hasPosition`, `RemoteCover` already falls back to MoveUp/MoveDown and the movement
  state (feature 0); Home Assistant, HomeKit (`pkg/homekit/lua/command.lua`) and the dashboard
  handle a cover without a position.
- Not in scope: regenerating proxies on start. The change reaches an existing install after
  om.lua is imported again (or `POST /api/om/regenerate`), as for tilt in #175.

## Files
- `pkg/omparser/parser.go`: note class 24 objects with a `set(1, …)` line, next to the
  LamelMoveTimeout parsing.
- `pkg/omparser/types.go`: a field for it, next to `HasTilt`.
- `pkg/omparser/generator.go`: `_hasPosition = true` only without the fv01 sign; fix the comment.
- `pkg/omparser/generator_ha.go`: `generateCover` drops the position topics only for ROLLER_SH
  (class 11); drop them for an fv01 roller too.
- tests next to `pkg/omparser/generator_tilt_test.go`.

## Acceptance criteria
- [ ] om.lua with `ROL…:set(1, 30000)` gives a proxy without `_hasPosition` and `_hasTilt`; the
  roller's HA discovery has no position or tilt topics.
- [ ] om.lua with `set(9, …)`/`set(10, …)` (fv02+) gives the same proxy as today, with tilt when
  `set(10, …)` is above 0.
- [ ] A class 24 roller with no `set` lines keeps `_hasPosition`.
- [ ] A `set(1, …)` line on another class changes nothing.
- [ ] Checked on a real fv01 roller (or a real om.lua from one): up, down and stop from Home
  Assistant move it.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0186-roller-fv01-position.md` that says om.lua must be
  imported again.

## Contract impact
Generated proxy files and Home Assistant discovery for fv01 rollers (no position topics). Entities
keep their `unique_id`. No decision record.
