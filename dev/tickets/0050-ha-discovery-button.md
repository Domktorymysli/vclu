# #0050: Publishing Home Assistant discovery from the API does not freeze entities

- **Type:** bug
- **Area:** home-assistant
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`POST /api/ha/discovery/publish` republishes the discovery the runtime publishes on start, so
Home Assistant entities keep following their objects.

## Context
The endpoint built discovery in Go from the OM files (`omparser.GenerateHADiscovery`) with state
topics `vclu/<clu>/<object>/state`. Since CLU objects are exposed automatically, the runtime
publishes their state on `vclu/exposed/vclu/<type>/<clu>_<object>/state` under the same
`unique_id`. After a call, Home Assistant listened to topics nobody updates: entities froze until
the next reload. The panel button that called it is gone; the endpoint stayed in the API.
`HAStatePublisher.refresh()` already republishes discovery with access control applied.
Found by the end-to-end suite (`TestHADiscoveryButtonKeepsStateTopics`).

## Files
- `pkg/web/handle_web_mqtt.go`
- `web/static/js/mqtt.js` (dead `publishHADiscovery`)
- `web/api/swagger.json` (`go generate ./pkg/web`)

## Acceptance criteria
- [ ] The endpoint calls the runtime and answers with the number of exposed objects; discovery
  points at the runtime's state topics (`TestHADiscoveryPublishUsesRuntime`).
- [ ] With the Home Assistant integration off it answers 409 and publishes nothing.
- [ ] `go test ./...` passes.
- [ ] CHANGELOG entry.

## Contract impact
REST API: the response has `published` only (no `states`, `files`, `errors`); 409 when the
integration is off.
