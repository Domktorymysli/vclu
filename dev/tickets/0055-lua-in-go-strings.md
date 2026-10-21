# #55: Lua that Go runs lives in embedded .lua files, not in Go strings

- **Type:** refactor
- **Area:** lua-runtime, mcp, web, homekit
- **Size:** M
- **Epic:** —
- **Blocked by:** #52 (touches `pkg/mcp/sandbox_scenario.go`)
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Every piece of Lua with its own logic is a `.lua` file. A Go string holds at most a call into Lua.

## Context
`pkg/` has 24 Go raw strings of five or more lines of Lua, 309 lines in all (counted on
2026-10-17, test files and the `om.lua` templates in `pkg/config/om_generator.go` excluded). The
largest: `pkg/mcp/sandbox_scenario.go` (`sandboxHelpers`, 46 lines: step operations and the
history wrapper of `vclu_draft_test`), `pkg/mcp/server.go` (32), `pkg/mcp/sandbox.go` (16),
`pkg/web/handlers_diagnostic.go` (14), `pkg/mcp/tool_mqtt_discover.go` (13 and 11),
`pkg/homekit/server.go` (12), `pkg/mcp/sandbox_clock.go` (10).

Lua in a Go string has no highlighting, no `luacheck`, and a syntax error shows only when a
test runs it. These files cannot move to `pkg/lua/runtime/`: everything there is public API for
user scripts, and sandbox helpers or a tool's query must stay private. `pkg/lua/embed.go` shows
the way: a `.lua` file next to the Go package, `//go:embed`, the same `L.DoString` at the same
place, so the load order does not change.

AGENTS.md rule 4 states the rule since 2026-10-17; this ticket applies it to what is there.

## Files
- new `pkg/mcp/lua/*.lua`, `pkg/web/lua/*.lua`, `pkg/homekit/lua/*.lua` with their `go:embed`
- `pkg/mcp/sandbox_scenario.go`, `pkg/mcp/sandbox.go`, `pkg/mcp/server.go`,
  `pkg/mcp/tool_mqtt_discover.go`, `pkg/mcp/sandbox_clock.go`, `pkg/web/handlers_diagnostic.go`,
  `pkg/web/handle_web_editor.go`, `pkg/homekit/server.go`
- a test that keeps the rule and compiles the embedded files

## Acceptance criteria
- [ ] Every Go raw string that contains a Lua `function` or `local` outside test files and
  `pkg/config/om_generator.go` is gone; the Lua is in a `.lua` file next to its package, loaded
  with `go:embed`, at the same point and in the same order as before.
  Open: the snippets `vclu_mqtt_discover` generates for the user (`generate*Snippet` in
  `pkg/mcp/tool_mqtt_discover.go`) are still Go strings: Go writes them, never runs them, and
  their values would need a text template. The test allows them next to `om_generator.go`.
- [x] A test in the repository fails when such a string comes back (scan of non-test Go files
  under `pkg/`, with the `om_generator.go` templates allowed).
- [x] No behaviour changes: `go test ./...` and `go test -race ./pkg/mcp ./pkg/web ./pkg/homekit`
  pass with no test changed except the new one.
- [x] A test compiles every embedded file (`L.LoadString`, without running it), so a syntax
  error fails `go test` and not a draft test; there is no Lua lint in CI today.
- [x] CHANGELOG entry in "Unreleased" under a maintenance heading, one line.

## Contract impact
None: the Lua runs as before, from the same places.
