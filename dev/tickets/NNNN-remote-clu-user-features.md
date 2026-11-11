# #NNNN: Read and write user features on a remote CLU

- **Type:** feature
- **Area:** grenton, lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a Grenton CLU with a user feature defined in OM

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A module reads and sets a user feature of a remote CLU in one call, e.g.
`CLU221000290:get("temp_zadana")` and `CLU221000290:set("temp_zadana", 21)`.

## Context
Users ask how to show a CLU user feature (a global variable defined in OM) on a dashboard.
Import of om.lua makes registry objects only for modules and outputs, so the only way today is
`CLU221000290:execute(0, 'getVar("temp_zadana")')`, which returns a string (`"nil"` when unset)
and is not in the docs. OM puts `getVar`/`setVar` on every CLU.

The CLU prepends `return` to the script itself: `"return x"` is a syntax error there and the CLU
does not answer, so `execute` times out (checked on 2026-11-11, docs fixed). The API must hide this.

`CLU_REMOTE:get(key)` and `CLU_REMOTE:set(key, value)` already exist but do nothing (`get` returns
`nil`). They are the natural place for the API.

## Files
- `pkg/lua/runtime/grenton/modules/CLU_REMOTE.lua`
- `docs` repo: `runtime/remote-clu.md` (section "Cechy użytkownika")

## Acceptance criteria
- [ ] `clu:get(name)` returns the value with its type (number, string, boolean, `nil`), not a string.
- [ ] `clu:set(name, value)` sets the feature; strings are quoted and escaped, so a value cannot inject Lua.
- [ ] `name` must be a Lua identifier; anything else is an error, not a script sent to the CLU.
- [ ] A timeout gives `nil, err`, and the run loop is not blocked for longer than today's `execute`.
- [ ] `execute` with a script starting with `return` logs a warning instead of a bare timeout.
- [ ] Checked on a real CLU: read and write of a number and a string feature.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/NNNN-remote-clu-user-features.md`.

## Contract impact
Lua API: new behaviour of `CLU_REMOTE:get/set` (today no-ops). No decision record needed.
