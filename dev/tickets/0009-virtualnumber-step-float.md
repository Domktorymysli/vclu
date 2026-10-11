# #9: VirtualNumber with a fractional step stores values like 53.900000000000006

- **Type:** bug
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A `VirtualNumber` with `step = 0.1` (or `0.5`, `0.25`...) holds the value on the step exactly as
it prints, e.g. `53.9`, not `53.900000000000006`.

## Context
`VirtualNumber:_clamp` in `pkg/lua/runtime/objects/VirtualNumber.lua` snaps the value to the step:
`v = base + math.floor((v - base) / self._step + 0.5) * self._step`. With `step = 0.1`, `53.9`
becomes `539 * 0.1`, which in floating point is `53.900000000000006`. The value then travels as is:
Home Assistant showed "53,900000000000006 %" for a humidity sensor; HomeKit, dashboards, history and
MQTT get the same.

Seen on a Zigbee humidity sensor exposed through a `VirtualNumber` with `step = 0.1`.

## Files
- `pkg/lua/runtime/objects/VirtualNumber.lua`
- a test in `pkg/lua/` (e.g. next to the other VirtualNumber tests)

## Acceptance criteria
- [ ] After snapping, the value is rounded to the number of decimal places of the step (0.1 → 1,
  0.25 → 2, 1 → 0), so `setValue(53.9)` with `step = 0.1` gives exactly `53.9`.
- [ ] Tests cover steps 0.1, 0.5, 0.25 and 1, with and without `min`, including negative values
  (e.g. `-12.3` with `step = 0.1`).
- [ ] The value published to Home Assistant for such an object is the short form (`53.9`).
- [ ] The new test fails on the current code.

## Contract impact
None: values on the step are what the step already promised.
