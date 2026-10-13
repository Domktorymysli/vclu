# #20: Promote warns that `modules/<dir>/init.lua` will not load when `require("<dir>")` loads it

- **Type:** bug
- **Area:** mcp
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`vclu_draft_promote` warns about a module in a subdirectory only when no `require()` in
`modules/init.lua` actually loads it.

## Context
`pkg/mcp/tools_sandbox.go` turns `modules/rekuperator/init.lua` into the require path
`rekuperator/init` and warns "It will NOT be loaded automatically" when `modules/init.lua`
has `require("rekuperator")`. `package.path` resolves a directory name to its `init.lua`
(`modules/init.lua` documents this, e.g. `require("energia")`), so the module does load. A false
warning teaches agents to ignore the real one.

## Files
- `pkg/mcp/tools_sandbox.go` (`hasRequire` and the subdirectory check)
- a test next to the existing promote tests in `pkg/mcp`

## Acceptance criteria
- [ ] For `modules/<dir>/init.lua`, both `require("<dir>")` and `require("<dir>/init")` count.
- [ ] A file that only another module requires (e.g. `require("energia/power")` inside
  `modules/energia/init.lua`) is not reported either.
- [ ] A subdirectory file that nothing requires still gets the warning.
- [ ] Tests cover the three cases; the first fails on the current code.

## Contract impact
None.
