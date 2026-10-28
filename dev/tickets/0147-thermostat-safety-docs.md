# #147: VirtualThermostat's docs say what it guards against and what it does not

- **Type:** docs
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Someone (or an agent) setting up `VirtualThermostat` on real heating learns from the docs what
happens when a sensor, vCLU or the server fails, and that the heating's own safety stays in place.

## Context
`VirtualThermostat` (#63) switches a real output: an electric heater, a floor heating zone, a
boiler's call for heat. Its guards today (`pkg/lua/runtime/objects/VirtualThermostat.lua`):

- no change of the source's value for `staleAfter` (900 s) switches the output off
  (`getFault() == "stale"`); a sensor stuck on one value counts as no change;
- the target is clamped to `min`..`max` (5..35 °C);
- `maxOn` stops the output after that many seconds on, until a setting changes; off by default;
- the output is switched off before a reload, at a stop and at every start.

What it does not cover: vCLU dying without a stop (`kill -9`, a crash, the server losing power)
leaves the relay as it was until vCLU starts again; #144 (`lease`) closes that for Grenton outputs
only. Every guard switches off, so a long failure in winter means a cold house, not an overheated
one.

`docs/ai/examples.md` lists the options in one paragraph and says none of this as a whole. An agent
writing a thermostat module reads that paragraph and nothing else.

`maxOn` stays without a default: a stop that holds heating off until someone changes a setting
would leave a house cold during a long frost, which is worse than no limit for most setups.

## Files
- `docs/ai/examples.md` (the `VirtualThermostat` paragraph)
- the doc comment of `VirtualThermostat:new` in `pkg/lua/runtime/objects/VirtualThermostat.lua`,
  regenerated `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] `docs/ai/examples.md` has a short table "what happens when": the source stops reporting, the
  source reports nonsense (e.g. 85 °C, -40 °C), the output's command is lost, vCLU reloads,
  vCLU is killed or the server loses power (with and without `lease` once #144 is merged), the CLU
  restarts.
- [ ] Each row says what the code does today and is checked against the code or an existing test;
  a row the code does not handle says so plainly.
- [ ] It says that `VirtualThermostat` is comfort control, not a safety device: the boiler's or heat
  pump's own limits, a floor heating safety thermostat and frost protection stay in place.
- [ ] The example sets `maxOn` explicitly and the text says when to use it (an electric heater, a
  boost) and when not (a whole house in winter).
- [ ] The `new()` doc comment points to that section; `docs/ai/lua-api-reference.md` regenerated.
- [ ] `go test ./...` passes (doc generation checks).

## Contract impact
None: documentation only.
