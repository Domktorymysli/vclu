# #235: Bind an object to a CLU user feature

- **Type:** feature
- **Area:** grenton, lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a Grenton CLU with a user feature and a script defined in OM

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
One call keeps a CLU user feature equal to an object's value:
`CLU221000290:bind("ha_temp_balkon", balkon)` and
`CLU221000290:bind("ha_pralka", pralka, { script = "HaZmiana" })`.

## Context
A value from outside Grenton (Home Assistant, Satel, Modbus, Zigbee, a thermostat) often has to
land in a CLU user feature, so Object Manager scripts and myGrenton can use it. Since 1.11.0 a
module does it with `CLU:set` (#217), and every such module repeats the same code and misses the
same things:
- `set` blocks until the CLU answers (up to the timeout when the CLU is down);
- after a CLU restart or a configuration sent from OM the feature is back at its OM value, and
  nothing writes it again;
- a feature has no "on change" event on the CLU, so each module calls an OM script by hand.

The CLU puts `return` before a script itself, features are read and written with
`getVar`/`setVar`, and a script fits in one UDP packet (about 1440 characters). Nothing in vclu
notices a CLU restart today: a renewal recreates a lost subscription without telling Lua. The CLU's
feature 0 is its uptime (`clu_*.xml`), so a restart shows as an uptime that went back.

Motivation: tutorial 21 (Home Assistant → vclu → CLU user feature) for installers who do not know
Lua. A Home Assistant source plugin is a separate ticket: Domktorymysli/vclu-plugins#21.

## Files
- `pkg/lua/runtime/grenton/modules/CLU_REMOTE.lua`
- `pkg/lua/remote_clu_bind_test.go`
- `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`, `web/static/js/lua-completions.json`

## Acceptance criteria
- [ ] `clu:bind(name, obj [, { script = "Name" }])` writes the object's value when it changes,
      not on every report of the same value, without blocking the run loop.
- [ ] All bound features of a CLU are written again when the CLU comes back (restart, OM
      configuration, back after not answering), without running the script.
- [ ] `script` runs an OM script on the CLU after a write caused by a change.
- [ ] `nil` is not written; `bind` returns a function that removes the binding.
- [ ] The feature and script names must be Lua identifiers; values are quoted like `set`, so a
      value cannot inject Lua.
- [ ] Bindings do not survive a Lua reload.
- [ ] Checked on a real CLU: the write after a restart and after a configuration sent from OM.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0235-clu-bind.md`.

## Contract impact
Lua API: new public method `CLU_REMOTE:bind`. No decision record needed.
