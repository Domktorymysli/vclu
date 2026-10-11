# #6: Config writes under the lock: move WriteMasterConfig callers to UpdateMasterConfig

- **Type:** refactor
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Two saves of different settings at the same time cannot overwrite each other.

## Context
`pkg/config/config.go`: `UpdateMasterConfig(dir, fn)` reads, changes and writes `.vclu.json`
under `masterConfigMu`; `WriteMasterConfig` writes atomically (temp file + rename) but without
the lock, so a caller that read the file earlier overwrites changes made in between. Callers of
`WriteMasterConfig` outside tests: `config.go` (8), `handle_web_plugins.go` (7),
`handle_web_mcp.go` (5), `handle_api_wizard.go` (5), `handlers_forms.go` (4),
`handle_web_access.go` (3), `handle_web_homekit.go` (2), `handle_web_grenton.go` (2), and one each
in `handlers_backup.go`, `handle_web_satel.go`, `handle_web_mqtt.go`, `server/server.go`,
`config/local.go`, `config/auth.go`. See `pkg/config/AGENTS.md`.

## Files
- the callers above; `pkg/config/config.go`

## Acceptance criteria
- [ ] Every read-change-write of `.vclu.json` outside `pkg/config` goes through `UpdateMasterConfig`.
- [ ] `WriteMasterConfig` is unexported or documented as "only under the lock" and has no callers
  outside `pkg/config` (except a restore from backup, which replaces the whole file on purpose).
- [ ] A race test: two goroutines changing different sections through the web handlers end with both
  changes in the file (`go test -race`).

## Contract impact
None.
