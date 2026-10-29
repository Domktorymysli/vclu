# #144: VirtualThermostat can switch a Grenton output on for a time and renew it

- **Type:** feature
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** #63 (PR #142: `VirtualThermostat`)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a Grenton CLU with a DOUT relay (to check that a renewal does not click the relay)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
With `lease = 90`, a `VirtualThermostat` switches a Grenton output on for 90 s at a time and renews
it while heating, so the CLU switches the output off by itself when vCLU stops answering.

## Context
`VirtualThermostat` (#63) switches its output off on a reload, a stop and at every start. It cannot
do anything when vCLU dies without a stop (`kill -9`, a crash, the server losing power) while the
CLU keeps running: the relay stays on until vCLU starts again.

A Grenton output can be switched on for a time: `RemoteSwitch:switchOn(time)` and
`RemoteLight:switchOn(time)` send the time in ms, and the CLU switches the output off when it runs
out. Renewing that time while the thermostat wants heat makes the CLU the last guard.

- **Opt-in.** `lease` (seconds) is off by default; without it nothing changes.
- **Grenton outputs only.** An output that cannot be switched on for a time (Zigbee, plugins,
  virtual objects) is an error at `new()` when `lease` is given, not a silent plain `switchOn()`.
  How to tell is the implementer's choice (say it in the PR); duck typing on `switchOn` is not enough,
  every output has one.
- **UDP gets lost.** A CLU drops packets, especially in series. Renew well before the time runs out
  (e.g. every third of it), so two lost renewals in a row still do not switch the output off. Bound
  `lease` from below (e.g. 30 s) so a short one cannot make the relay chatter.
- **No noise.** A renewal is not a change: no history entry, no `OnChange`, no `OnSettingsChange`.
  `RemoteSwitch:switchOn(time)` expects the CLU to switch off later (`StateChange.expectLater`); a
  renewal must not leave an expected change that marks a later real change as vCLU's own.
- **Off stays off.** Switching off (crossing, `stop()`, stale source, `maxOn`, release before a
  reload) is a plain `switchOff()` and ends the renewals.

## Files
- `pkg/lua/runtime/objects/VirtualThermostat.lua`
- `pkg/lua/runtime/objects/RemoteSwitch.lua`, `RemoteLight.lua` only if a renewal needs support there
- tests on the virtual clock next to `pkg/lua/virtual_thermostat_test.go`
- `docs/ai/examples.md`, regenerated `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] Without `lease` the commands sent to the output are exactly as before; a test shows it.
- [ ] With `lease = 90` heating sends `switchOn(90000)` and renews it before a third of the time
  is left; a test on the virtual clock reads the CLU calls.
- [ ] Renewals stop on every switch-off path (crossing, `stop()`, stale, `maxOn`, release); a test.
- [ ] `lease` on a non-Grenton output, or below the lower bound, is an error at `new()`; a test.
- [ ] A renewal leaves no history entry and fires no event; a later change of the output from
  Grenton is recorded as Grenton's; a test.
- [ ] Checked on a DOUT relay: a renewal does not click or blink the relay, and with vCLU killed
  (`kill -9`) the relay goes off within `lease` (say so in the PR).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/<ticket>-<slug>.md`.

## Contract impact
`VirtualThermostat` gains one optional option. Nothing else changes.
