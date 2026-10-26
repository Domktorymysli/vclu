# #0132: Promote counts optional("x") as loading the file

- **Type:** bug
- **Area:** mcp
- **Size:** XS
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A file in a subdirectory that another module loads with `optional("dir/file")` gets no
"It will NOT be loaded automatically" warning at promote.

## Context
The check added in #69 looks for `require(...)` only: `hasRequire` with `requireCallRe` in
`pkg/mcp/tools_sandbox.go`. `optional(name)` (#117) loads a module the same way, so an agent that
uses it is told the file will not load, and may add a needless `require` to `init.lua`. The warning text
in `tools_sandbox.go` also talks about `require()` only; it should name `optional()` too.

## Files
- `pkg/mcp/tools_sandbox.go` (`requireCallRe`, `hasRequire`, the promote warning)
- `pkg/mcp/promote_require_test.go`

## Acceptance criteria
- [ ] `optional("x")`, `optional('x')` and `optional "x"` count like `require`; `x.optional("y")` and a call in a comment do not.
- [ ] Unit tests next to the existing `hasRequire` tests in `pkg/mcp/promote_require_test.go`.
- [ ] The warning says `require()` or `optional()`.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0132-promote-optional-counts.md`.

## Contract impact
None.
