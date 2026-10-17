# #0051: A top-level module that init.lua requires runs once

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
A file directly in `modules/` runs once, whether or not `modules/init.lua` requires it.

## Context
The loader runs `init.lua`, then every other file directly in `modules/` with `DoFile`.
`docs/ai/examples.md` and the README tell users and agents to `require()` every file from
`init.lua`, including top-level ones (`require("tasmota_biurko")`). Such a file ran twice: two
objects, two sets of watchers and timers. The draft sandbox loads drafts the same way.
Found by the end-to-end suite.

## Files
- `pkg/lua/lua.go` (`LoadUserModules`, `RunModule`)
- `pkg/mcp/sandbox.go` (draft loading)
- `docs/ai/examples.md`, `pkg/mcp/server.go` (load order)

## Acceptance criteria
- [ ] A top-level file that `init.lua` requires is not run again by the loader, and a later
  `require` of a loaded file gets its result (`TestLoadUserModules_RequiredTopLevelRunsOnce`).
- [ ] The sandbox behaves the same (`TestSandbox_RequiredDraftRunsOnce`).
- [ ] A file named like a runtime module (`string.lua`, `TypeMap.lua`) still runs and does not
  replace the runtime module (`TestLoadUserModules_RuntimeModuleNameStillRuns`).
- [ ] A file that failed inside `init.lua`'s `require` is not run a second time
  (`TestLoadUserModules_RequiredFileErrorRunsOnce`).
- [ ] The docs describe the load order as it is.
- [ ] `go test ./...` passes.
- [ ] CHANGELOG entry.

## Contract impact
None: a module runs once, as users expected.
