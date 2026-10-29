# #NNNN: Remove the unused remote access profile

- **Type:** chore
- **Area:** access-control
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

## Goal
The Access page has no "Remote access" profile or column, and no code for it is left.

## Context
#39 (PR #143, v1.8.0) added a `remote` integration to the access profiles, a `RemoteAccess`
Lua module and the page's box and column. Nothing calls `RemoteAccess`, so the setting does
nothing, and people do not understand it. This reverts #143 and drops a `remote` assignment
when an access file written by v1.8.0 is loaded.

## Files
- `pkg/accesscontrol/manager.go`, `pkg/web/handle_web_access.go`, `pkg/web/server.go`
- `pkg/lua/runtime/core/AccessControl.lua`, `pkg/lua/runtime/integrations/remote/` (removed)
- `web/templates/access.html`, `web/static/js/access.js`, `web/static/js/access-profiles.js`, locales, `web/api/swagger.json`

## Acceptance criteria
- [ ] The Access page shows MQTT/HA, HomeKit and Bots only.
- [ ] An access file with `assignments: { remote: [no-access] }` loads and keeps the rest
  (`TestLoadDropsUnknownIntegration`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/drop-remote-profile.md`.

## Contract impact
The access-control contract row in CONTRIBUTING.md loses its remote part. The API no longer
accepts `remote` as an integration.
