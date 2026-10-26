# #0130: A thermostat is polled when its CLU's firmware answer is not a version

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A remote thermostat gets its values by polling when its CLU answers `get(17)` with something
that is not a version, as `RemoteThermostat:_plan` already promises ("anything else, a CLU that
has not reported its firmware included, is polled").

## Context
In the poll chain (`pollAll`, `run.step`, firmware step in
`pkg/lua/runtime/grenton/protocol/RemoteSubscription.lua`) `_checkFirmwareAsync` hands back `nil`
both when the CLU did not answer and when the answer was not version-shaped. The code then sets
`run.down[st.clu] = true`, which skips every feature read of that CLU in this run and the same in
every later run. The log says "firmware unknown ... THERMOSTAT v2, poll" and asks `get(17)` on
every tick, but no `THE...:get(n)` ever goes out, so current and target stay nil.

Seen in vclu-e2e with fakeclu answering `nil` to `CLU...:get(17)`. A real CLU answering
`04.08.05`-style versions is not affected; a CLU that does not answer at all is rightly taken as down.

## Files
- `pkg/lua/runtime/grenton/protocol/RemoteSubscription.lua` (poll chain, firmware step)
- `pkg/lua/runtime/grenton/modules/CLU_REMOTE.lua` (`_checkFirmwareAsync`, `_setFirmware`): tell "no answer" from "answer that is not a version"

## Acceptance criteria
- [ ] A CLU that answers `get(17)` with a non-version gets its thermostats polled (vclu-e2e: drop the knownBug marker in `TestThermostatPolledWhenFirmwareAnswerIsNotAVersion`).
- [ ] A CLU that does not answer is still skipped for that run.
- [ ] The log line in `logPlan` (`RemoteSubscription.lua`) tells "no answer to get(17)" from "answer is not a version"; today both print "unknown, no answer to get(17)".
- [ ] A sandbox test with a virtual clock covers both cases.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0130-thermostat-firmware-not-version.md`.

## Contract impact
None.
