# #281: User features of a CLU picked on the Grenton page, kept as objects

- **Type:** feature
- **Area:** grenton
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a Grenton CLU with number and string user features defined in OM

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
On the Grenton page, each CLU lists its user features; the ones the user ticks become objects in
vclu that keep their current value, so they can go on a dashboard, to Home Assistant, HomeKit or
the apps like any other object, without writing Lua.

## Context
- Installers keep computed values in user features: humidity, temperatures, a dew point, a status text.
  Today vclu reaches them only from Lua:
  `CLU221000290:get("name")` / `:set("name", v)` (#217, `CLU_REMOTE.lua`).
- The project import (`.omp`) already reads user feature names where myGrenton screens use them
  (`pkg/omproject/mygrenton.go`), but does not list a CLU's user features. The import should
  collect, per CLU, each user feature's name, type and initial value. Without an imported project
  the user adds a feature by name.
- Objects come into vclu from `om.lua` and the sync list (`sync.objects`, Grenton page); access
  profiles decide what each client sees. A picked feature should follow the same path.
- Updates: find out first (spike, real CLU) whether the CLU's remote subscription
  (`SYSTEM:clientRegister`) accepts a user feature, so the CLU reports changes itself. If not, poll:
  several features of one CLU in one request, every few seconds, within the CLU limits (a request
  of 1472 bytes after encryption, an answer of 2047 characters).
- Writing goes through `setVar`, with the same quoting and name checks as `CLU:set`.

## Files
- `pkg/omproject/` (collect user features per CLU)
- `pkg/config/config.go` (the picked features in the sync config)
- `pkg/web/handle_web_grenton.go`, `web/templates/grenton.html` (the picker under each CLU)
- `pkg/lua/runtime/grenton/` (objects for picked features, reading and writing)
- `pkg/accesscontrol/` (features in access profiles like other objects)

## Acceptance criteria
- [ ] After a project import, the Grenton page lists the user features under each CLU; a feature
      can also be added by name.
- [ ] A ticked feature is an object (e.g. `CLU221000290.temperatura_sypialnia`) with its typed value
      (number, string, boolean), `onChange` and history; unticking removes it.
- [ ] It can be exposed to Home Assistant (a sensor for a number, a text for a string) and shown on
      a dashboard and in the apps; access profiles apply to it like to any object.
- [ ] A change on the CLU reaches vclu within the sync interval (subscription or polling, per the
      spike); a value written from vclu reaches the CLU.
- [ ] A feature name that is not a Lua name is refused in the picker and in the config.
- [ ] Checked on a real CLU: a number and a string feature, read and written.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0281-clu-user-features-sync.md`.

## Contract impact
Sync config (`.vclu.json`): a new list of picked features per CLU; existing configs load unchanged.
Object paths: a new kind of object under a CLU. No decision record needed unless the spike changes
the sync protocol.
