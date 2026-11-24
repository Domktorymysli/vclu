# #0274: The agent keeps alarms, locks and gates behind a PIN in the cloud

- **Type:** feature
- **Area:** mcp
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An agent that sends an alarm partition, a lock, a door, a gate or a garage to the cloud adds a PIN
(gates: at least a confirmation), and `vclu_draft_validate` warns when a draft does not.

## Context
`:remote()` makes a device reachable from the cloud app, voice and Google: a stolen phone or a
taken account is then enough to disarm the alarm or open the gate. `:pin()` and `:confirm()`
(docs.vclu.pl, cloud/devices) stop that, but nothing tells the agent to use them, and most
`expose()` calls are written by the agent. The cloud cannot tell a Satel partition from any other
switch; vclu's source can.

The check reads the draft's source (`expose(...)` and its chained calls, comments and strings
skipped) and guesses from the object, the expose type, `device_class`, `pulse`, the id and the name.
It warns and never fails a draft: the owner may want it, the agent has to ask.

## Files
- `pkg/mcp/validate_remote.go`, `pkg/mcp/validate_remote_test.go`
- `pkg/mcp/sandbox.go` (`ValidateDraft`)
- `pkg/mcp/server.go` (`mcpInstructionsBase`, rule 8)

## Acceptance criteria
- [x] An alarm partition or zone (Satel, GATE Alarm), a lock or a door with `:remote()` and no `:pin()` gets a warning; `:confirm()` alone is not enough.
- [x] A gate, garage or intercom (name, `pulse`, `device_class`, `garage_door`) with `:remote()` and neither `:confirm()` nor `:pin()` gets a warning.
- [x] No warning for `:remote(false)`, `readonly`, sensors, a device without `:remote()`, or code in comments and strings.
- [x] The draft stays valid (warning only).
- [x] The MCP instructions tell the agent to add the PIN, ask the owner for it, and never remove one.
- [x] `go test ./...` and `go test -race ./...` pass.
- [x] A changelog entry.

## Contract impact
None: `vclu_draft_validate` gains warnings, its result keeps its shape.
