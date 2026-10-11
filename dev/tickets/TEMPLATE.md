# #NNNN: <short title>

- **Type:** feature | bug | refactor | test | docs | chore | spike
- **Area:** grenton | satel | homekit | home-assistant | mqtt | modbus | plugins | lua-runtime | sandbox | mcp | web | access-control | build
- **Size:** S | M | L
- **Epic:** #NNNN | —
- **Blocked by:** #NNNN | —
- **Skill:** `.agents/skills/<name>` | —
- **Needs hardware:** — | e.g. Grenton DIM module

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
One sentence: what is true once this is merged.

## Context
The minimum the implementer must know. Link to `dev/plans/`, `dev/decisions/`, code; do not copy them.

## Files
- `pkg/...`

## Acceptance criteria
- [ ] Each item can be checked by running a command or reading a test.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry (in an "Unreleased" section at the top) if users notice the change.

## Contract impact
Which contract (see CONTRIBUTING.md) this touches and whether it needs a decision record. Or: none.
