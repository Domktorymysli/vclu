# #0049: A reload does not fire automations whose input is already in the target state

- **Type:** bug
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`StateBus:watch` fires on the changes the history records: the first report of an object after a
start or a Lua reload fires it only when the value differs from the one known before.

## Context
A reload (every `vclu_draft_promote`) re-creates CLU objects without a value. Their first report
is published as `nil → value`, so `StateBus:watch(path, fn, { to = 1 })` fires whenever the input
already is 1: an automation "when the motion sensor is 1, switch the light on" runs on every
promote. The history (`pkg/history/store.go`, `Record`) already treats such a first reading as no
change, or as one change from the known value; watchers did not get that verdict.
Found by the end-to-end suite (`TestReloadDoesNotFireAutomation`).

The draft sandbox must keep firing on a `set` step: there a step is a change made by the agent,
also on an object with no value yet.

## Files
- `pkg/history/store.go` (`RecordChange`)
- `pkg/lua/history.go` (`__go_history_record` returns the verdict)
- `pkg/lua/runtime/core/StateChange.lua`, `pkg/lua/runtime/utils/StateBus.lua`
- `pkg/mcp/sandbox_scenario.go` (`__sandbox_set`)

## Acceptance criteria
- [ ] A first report equal to the value known before a reload does not fire a watch; a different
  one fires once, with `old` = the known value (`TestHistory_WatchSkipsFirstReadingAfterReload`).
- [ ] The very first reading of an object (nothing known) does not fire a watch.
- [ ] Home Assistant, HomeKit and MQTT still get the first reading.
- [ ] A sandbox `set` step on an object with no value fires a watch (`TestScenario_FirstSetFiresWatch`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry.

## Contract impact
`StateBus:watch` semantics: a first reading is no change. The event gets `baseline = true` for it.
