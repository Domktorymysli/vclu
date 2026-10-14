# #29: A lost CLU report leaves a wrong state until something else in the session changes

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (checked on a real CLU, see Context)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A state change whose `clientReport` was lost reaches vCLU within one subscription renewal
(TTL/2, 30 s by default), with no extra traffic to the CLU.

## Context
A CLU reports a change of a subscribed object with one UDP packet to port 4344, with no
acknowledgement and no retry. When that packet is lost, vCLU keeps the old value until another
object in the same session changes and the CLU sends a new report. Seen in production: a light
switched off from the myGrenton app stayed "on" in the dashboard for over 10 minutes, while the
CLU's own reports before and after were correct and vCLU logged no receive errors.

`RemoteSubscription.refresh` (`pkg/lua/runtime/grenton/protocol/RemoteSubscription.lua`) renews
every session with `SYSTEM:clientRegister` every TTL/2. The CLU answers a renewal of a live session
with the session's current values, in the same format as the first subscribe
(`clientReport:<sid>:{...}`); checked on a real CLU, session 16 answered `clientReport:16:{1,0,0,0}`.
The renewal goes through `SendCommandNoReply` (`pkg/server/command_sender.go`), which closes its
socket right after sending, so that answer is dropped. The comment on `refresh` ("a live session
gets no report") is about pushes to port 4344, not about this answer.

## Files
- `pkg/server/command_sender.go`
- `pkg/server/client_subscription.go`
- `pkg/lua/runtime/grenton/protocol/RemoteSubscription.lua` (comment on `refresh`)
- `pkg/lua/AGENTS.md` (runtime/grenton section)

## Acceptance criteria
- [ ] A `clientReport` answer to a command sent without waiting (the renewal) updates the registry
  like a pushed report, with origin `grenton` and the CLU as actor.
- [ ] The caller still does not wait: the Lua run loop is not blocked by the answer, and the spacing
  of renewals (`batchDelay`) is unchanged.
- [ ] An answer that is not a `clientReport` (e.g. to `clientDestroy` or `execute`) is ignored.
- [ ] A contract test with a fake CLU on UDP: a stale value is corrected by the answer to a
  renewal; the test fails on the current code.
- [ ] An answer with unchanged values adds no state change (already covered by
  `TestStateChange_SubscriptionReport`, keep it green).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry: a Grenton state change whose report was lost shows up within 30 s.

## Contract impact
New contract row "Grenton: a lost report is corrected by the next renewal" in CONTRIBUTING.md.
