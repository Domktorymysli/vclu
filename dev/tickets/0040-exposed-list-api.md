# #40: ExposedObjects:list() is documented public API

- **Type:** docs
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
User code can walk the exposed objects with `ExposedObjects:list()` and rely on what it returns,
because the function and the handle fields it exposes are documented and pinned by contract tests.

## Context
`expose()` is where a user describes an object once: its type, name and area. Code that wants
"every thermometer I expose" (e.g. sending readings to InfluxDB, docs.vclu.pl tutorial 8) should
read that instead of keeping a second list.

`ExposedObjects:list()` (`runtime/core/ExposedObjects.lua`) already returns the map
path → `ExposedHandle` and is used inside the runtime (`MCP.lua`, `homeassistant/Commands.lua`,
`StatePublisher.lua`). It is not marked `@api public`, is missing from
`docs/ai/lua-api-reference.md`, and only one contract test touches it
(`TestContract_Expose_GetAll` in `pkg/lua/lua_contract_test.go`, whose message still names a
`getAll()` that does not exist).

Make the existing function public as it is, without new API (`pkg/lua/AGENTS.md`, "The Lua API is
a contract, keep it small"): no filter argument, filtering is a line of user code. The returned map
is the live internal table, so the docs say it is read-only; copying it on every call would cost
the integrations that walk it.

What user code may rely on, per handle: the map key is the exposed path; `handle.type` (the
expose type, e.g. `"temperature"`); `handle.options.name`, `.area`, `.tags`; `handle:getValue()`.
Everything else on the handle stays internal.

## Files
- `pkg/lua/runtime/core/ExposedObjects.lua`: `@api public` and doc comments on `list()` and `get(path)`.
- `docs/ai/lua-api-reference.md` (regenerated) and an example in `docs/ai/examples.md`.
- `pkg/lua/lua_contract_test.go`: contract tests for the fields above.

## Acceptance criteria
- [ ] `list()` and `get(path)` appear in `docs/ai/lua-api-reference.md` with the fields user code may rely on and the read-only note.
- [ ] A contract test exposes a temperature sensor with name, area and tags, then reads each listed field through `list()` and `get(path)`; a test checks an unexposed object is gone from `list()`.
- [ ] `TestContract_Expose_GetAll` names `list()` in its message.
- [ ] `docs/ai/examples.md` has a short example: every exposed `temperature` object, keyed by path.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
A new public contract on an existing function: the path key, `type`, `options.name/area/tags`
and `getValue()` of a listed handle. No behaviour changes.
