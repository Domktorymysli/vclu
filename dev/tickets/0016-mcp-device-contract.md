# #15: Contract test for the MCP device and scene tools

- **Type:** test
- **Area:** mcp
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A change to the JSON that the MCP device and scene tools return fails a contract test, so programs
that call these tools keep working across releases.

## Context
Other programs, not only AI agents, call the MCP endpoint (`POST /mcp`) with a bot token: an
external dashboard polls `vclu_devices` and controls devices through `vclu_device_set`,
`vclu_device_execute`, `vclu_scenes` and `vclu_scene_execute`. They depend on the JSON shape, not on
the tool descriptions, which are written for language models and may change.

The shapes are `Device`, `SetResult`, `ExecResult` and `Scene` in `pkg/mcp/types.go`; handlers in
`pkg/mcp/tools.go`. `pkg/mcp/tools_test.go` covers access control (hidden, readonly), not the shape.

## Files
- `pkg/mcp/tools_contract_test.go` (new)
- `CONTRIBUTING.md`, section Contracts: a new row

## Acceptance criteria
- [ ] The test calls each of the five tools the way a client does (through the handler, with a bot
  in the context) and decodes the result as plain JSON, not into the Go structs.
- [ ] It asserts the field names and JSON types a client relies on: `devices[]` with `path`,
  `type`, `name`, `state`, `readonly`, `methods`; `ok` and `new_state` from `vclu_device_set`;
  `ok` and `result` from `vclu_device_execute`; `scenes[]` with `path`, `name`.
- [ ] It asserts the value scales: dimmer `state` 0..1, cover 0..100.
- [ ] Renaming a JSON tag in `pkg/mcp/types.go` makes the test fail (say so in the PR).
- [ ] A row "MCP device tools" in the Contracts table of `CONTRIBUTING.md`.
- [ ] `go test ./...` and `go test -race ./...` pass.

## Contract impact
Adds the contract "MCP device tools". Changing it later needs `Contract change:` in the PR.
