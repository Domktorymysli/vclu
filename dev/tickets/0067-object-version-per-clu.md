# #67: A remote object knows its version on the CLU, and a subscription registers only what that version has

- **Type:** bug
- **Area:** grenton | lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a CLU on firmware 4.x (the maintainer's CLU220000592, 04.08.05) with Thermostat objects; a CLU on 5.x to compare

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
vCLU knows which version of an object class each remote CLU runs (from the CLU's firmware) and
registers only features that version has, so a subscription never resets a CLU.

## Context
- 2026-10-19, v1.6.0 on the maintainer's installation: CLU220000592 (firmware **04.08.05**,
  `FwVersion 00000198` in om.lua) **reset twice**, each time a second after vCLU registered new
  subscription sessions that included its two thermostats (`RemoteThermostat`, features 3, 6, 8,
  12, 14): at 17:08:28Z after a Lua reload and at 17:15:15Z after "Sync now" with batch size 8.
  Its uptime (`CLU:get(0)`) showed it; the other CLUs (firmware 5.15) did not reset. Renewals of the
  same sessions every ~28 s did not reset it, and after the first reset the renewals that
  re-created the sessions did not either; what exactly triggers it is not known yet. The
  maintainer: firmware 4.x is sensitive to such registers.
- Object Manager's device definitions map firmware to the Thermostat object version:
  `clu_ft00000003_fv197` and `clu_ZWAVE_ft00000003_fv1f5` have v1 (no CurrentTemp 14, no State 6 /
  Mode 8 setters), all others listed have v2, including `clu_ZWAVE_ft00000004_fv198` (4.08, the
  maintainer's CLU). So the definitions say v2 for this CLU, and it resets anyway: the definition
  alone is not enough, the version has to be checked against the CLU.
- Today the OM import marks only FwType 3 / fv197, fv1f5 as v1 (`thermostatV1` in
  `pkg/omparser/generator.go`, `_version = 1`), and `RemoteThermostat:subscribeFeatures` drops 14
  then. A remote object does not know the CLU's firmware at runtime, and the subscription does
  not ask.
- The CLU reports its firmware: `CLU:get(17)` FirmwareVersion (`"04.08.05"`, `"05.15.12-2607"`),
  index from the CLU object's definition. `CLU_REMOTE` could read it once per CLU (also after the
  CLU comes back) and objects could ask their CLU.
- A Thermostat's values can also be read with `get` (one `execute` per feature): slower, but a
  fallback for a CLU where subscribing a feature is not safe.
- The cut-off is likely the CLU generation: CLU220000592 is a **CLU v1.0** (hardware), and
  04.08.05 is among the newest firmware for it (the maintainer). Treat every CLU v1.0 on firmware
  4.x (`FwType 00000004`, `FwVersion` 0x1xx, i.e. 4.xx; Object Manager's `clu_ZWAVE_ft00000004_*`)
  as one where a thermostat is not subscribed with the v2 features until the hardware test says
  otherwise; 5.x (CLU 2.0, `clu_ZWAVE_2_*`, FwVersion 0x1f6 and up) keeps the v2 subscription.
  The firmware→version table must be data (one place, with its source), not `if`s.
- Don't test on a whole house blindly: each failed register resets the CLU (~30 s without
  control). A test registers one feature in one session at a time, agreed with the maintainer.

## Scope
1. `CLU_REMOTE` knows the CLU's firmware (from om.lua `FwType`/`FwVersion` at import, checked with
   `get(17)` at runtime) and exposes it to its objects (`clu:firmware()`).
2. An object class with versions (`RemoteThermostat` first) picks its features by the CLU's
   version: a table firmware → version → features, from Object Manager's definitions plus what
   the hardware test shows. Until the test says otherwise, a CLU v1.0 (firmware 4.x) gets no
   thermostat subscription: its thermostats are polled (3).
3. `RemoteSubscription.autoSubscribe` registers an object only with features that are safe for its
   CLU; a class may say "poll" instead (values read with `get` at an interval, `RemoteThermostat`
   on 4.x if no feature set turns out safe).
4. A log line names the CLU, its firmware and the version chosen, once per CLU.

## Hardware test (with the maintainer)
On CLU220000592, outside hours anyone needs the house: one session with one feature of
`THE0855` at a time (14, 12, 3, 6, 8), watch `CLU:get(0)` for a reset. Record which features reset
the CLU and whether a fresh session (not a renewal) is what does it; put the result in
`pkg/lua/AGENTS.md` next to the Thermostat indices.

Result, 2026-10-24 on 1.6.0-1e67f02 (CLU220000592, 04.08.05, uptime checked every few seconds):
- each feature of `THE0855` (14, 12, 3, 6, 8) alone in a fresh session, 70 s with renewals, then
  `clientDestroy`: no reset;
- one session with all five features of `THE0855`, and one with the batch-8 shape of 2026-10-19
  (five of `THE4346`, three of `THE0855`): no reset;
- both thermostats on the Sync list, reload and "Sync now": polled, values read, no reset;
- the old behaviour forced in memory (a `{major = 4, follow = "subscribe"}` row), then `autoSubscribe`:
  both thermostats subscribed with the rest of the CLU (five sessions, 38 entries, while the
  previous 28 had not expired yet), two minutes of renewals: no reset.

The 2026-10-19 resets could not be reproduced, so what triggered them is still unknown. Firmware
4.x stays polled; subscribing it is a new row in `FIRMWARE` once the cause is known.

## Files
- `pkg/lua/runtime/grenton/` (`CLU_REMOTE`), `pkg/lua/runtime/objects/RemoteThermostat.lua`,
  `pkg/lua/runtime/grenton/protocol/RemoteSubscription.lua`
- `pkg/omparser/generator.go` (firmware in the proxy), `pkg/lua/AGENTS.md`

## Acceptance criteria
- [x] A remote object can tell its CLU's firmware; a test with a fake CLU answering `get(17)`.
- [x] Subscribing a thermostat on a CLU marked unsafe for a feature leaves that feature out (or polls); a test checks the registered script.
- [x] The hardware test is done and its result recorded; on the maintainer's CLU a reload and "Sync now" with the thermostats on the Sync list do not reset it (uptime checked).
- [x] `go test ./...` and `go test -race ./...` pass.
- [x] CHANGELOG entry.

## Contract impact
Internal to the runtime; `clu:firmware()` would be a new public method of `CLU_REMOTE`.
