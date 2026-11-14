# #NNNN: Grenton: a roller already at its end stays "opening" after open()

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** Grenton ROLLER_SHUTTER (fv02 or later), for the final check

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A Grenton roller shows the movement its CLU reports: `open()` on an open roller or `close()` on a
closed one leaves it stopped, and a movement vCLU assumed but the CLU never reported ends by itself.

## Context
- `RemoteCover:open()`/`close()` (`pkg/lua/runtime/objects/RemoteCover.lua`) call `_moved(1|2)`
  right after the command: an optimistic movement until the CLU reports feature 0.
- A roller already at 100 % (or 0 %) does not move, so its movement (feature 0) stays 0 and the
  CLU reports nothing. The optimistic movement is never replaced: dashboards show "otwiera się" /
  "zamyka się", Home Assistant `opening`/`closing`, until something calls `stop()`.
- Seen by a user on v1.11.0 with nine rollers after a scene that opened them all; `stop()` on each
  cleared it. Calling `stop()` after `open()` is not a workaround: it stops a roller that moves.
- Without a position (`_hasPosition` false) `_moved` writes the value itself; same problem.

## Files
- `pkg/lua/runtime/objects/RemoteCover.lua`: no optimistic movement when the known position is
  already the target; an optimistic movement the CLU has not confirmed ends after a timeout
  (`TIMERS:setTimeout`, as `ExposedObjects` does for pulses), cancelled by any feature 0 report.
- tests next to `pkg/lua/ha_cover_position_test.go`.

## Acceptance criteria
- [ ] `open()` on a roller whose position is 100 leaves `getState()` 0 and emits no
  `OnMovementChange`; the same for `close()` at 0.
- [ ] `open()` at 40 gives movement 1 at once; a feature 0 report of 1 then 0 ends it as today.
- [ ] Movement 1 or 2 with no feature 0 report goes back to 0 after the timeout, with
  `OnMovementChange`; a report before the timeout cancels it.
- [ ] A roller without a position: the same timeout, on its value.
- [ ] Home Assistant gets `open` (not `opening`) for the first case.
- [ ] Checked on a real roller: a scene opening an open roller leaves the tile "otwarta".
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/NNNN-roller-stuck-moving.md`.

## Contract impact
None: the movement reported to dashboards, Home Assistant and HomeKit follows the CLU more closely.
No decision record.
