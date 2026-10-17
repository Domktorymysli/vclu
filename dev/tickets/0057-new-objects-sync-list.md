# #57: Objects new after an OM import are pointed out on the Sync tab; saving the sync list re-subscribes

- **Type:** feature
- **Area:** grenton, web
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a Grenton CLU to see the reports, welcome)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
After a project with a new module arrives from Object Manager, the user sees which objects are
not synced yet and can add them in one step; saving the sync list takes effect at once.

## Context
vCLU subscribes to the objects of a real CLU in whitelist mode: `sync.objects` in
`clu/.vclu.json`, edited on the Grenton page, Sync tab (`web/templates/grenton.html`,
`GET/POST /api/grenton/sync` in `pkg/web/handle_web_grenton.go`). The list is a real subset: the
maintainer's house has 158 remote objects and 78 on the list (digital inputs, analog outputs
and 1-Wire sensors left out), so adding every new object by itself is not the answer.

A module added in Object Manager and sent to vCLU regenerates the proxy
(`pkg/web/handle_web_om.go`, `handleAPIOMUpload`, `handleAPIOMRegenerate`, and the reset after
a TFTP upload) and the new object appears in the registry and in Home Assistant. Nothing says it
is not on the sync list: commands go out, but no report comes back, so its state is only what
vCLU last sent (a dimmer dimmed at the wall or from OM stays wrong in HA). Found 2026-10-17 with
a new DIMMER: `Ściemniacz` worked from HA, and the missing reports were noticed only by reading
the journal.

Two gaps:
1. The Sync tab lists the registry objects with checkboxes, but a new one looks like any other
   unchecked one. The import does not record which objects are new.
2. `handleAPIGrentonSyncSave` writes the file and pushes the list to Lua
   (`LuaAPI.syncRemoteConfig`), which only restarts the renew timer; the subscriptions change on
   "Sync now" (`POST /api/grenton/sync/now`) or at the next reload. A user who saves and leaves
   has no reports for the new object.

## Files
- `pkg/web/handle_web_om.go`: after a proxy is generated, the object paths that were not in the
  previous proxy of that CLU (compare with the old file, or with the registry before the reload)
  are kept, e.g. in the master config (`sync.pending` or alike) or in memory until the user acts.
- `pkg/web/handle_web_grenton.go`, `web/templates/grenton.html`, `web/static/js/grenton.js`:
  the Sync tab shows "N objects from the last import are not synced" with their names and an
  "Add and sync" action; `handleAPIGrentonSyncSave` re-subscribes when the list changed (the
  save answers after `autoSubscribe`, or triggers it and says so).
- `pkg/lua/runtime/core/LuaAPI.lua` (`syncRemoteConfig`) if the re-subscribe lives there.
- `docs/` (Grenton page: the sync list) and `web/locales/*.json`.
- tests in `pkg/web/` (upload → pending list; save → re-subscribe called) and `pkg/lua/`.

## Acceptance criteria
- [ ] Uploading an om.lua with one more object than before marks that object as new and not
  synced; `GET /api/grenton/sync` (or the status) lists it; the Sync tab shows it with a one-step
  add. A test uploads two versions of an om.lua.
- [ ] An object already on the list, or one the user removed on purpose, is not reported as
  new again after another import of the same project.
- [ ] `POST /api/grenton/sync` with a changed list re-subscribes (a fake `__go_send_command`
  sees a `clientRegister` with the new object) and answers only after it; an unchanged list does
  not re-subscribe.
- [ ] The MCP `vclu_devices` entry (or `vclu_device_get`) of an object not on the sync list says
  so (`synced: false`), so an agent can tell the user.
- [ ] `go test ./...` passes; CHANGELOG entry in "Unreleased".

## Contract impact
None for scripts. The sync API gains fields; existing ones keep their meaning.
