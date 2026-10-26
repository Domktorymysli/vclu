# #0131: Home Assistant discovery is published after the embedded broker's client connects

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
With the embedded broker and no `mqttClient` section in `.vclu.json`, Home Assistant gets the
discovery configs and `vclu/status` at startup.

## Context
`pkg/server/lua/connect_broker.lua` calls `HAIntegration.setup(MQTT)` right after
`MQTT:connect()`. That call only starts the connection (`MqttClient.lua` returns true while it is
pending; `pkg/lua/mqtt.go` uses `ConnectAsync`), so the retained publishes are queued before the
client is connected and their tokens time out: "[MQTT] MQTT publish to
homeassistant/switch/.../config timed out" and "vclu/status timed out". The `mqttClient` path in
`pkg/server/server.go` reconnects first and is not affected. Happens with and without broker
credentials. State topics published later do arrive; only the startup discovery is lost until
someone presses "publish discovery".

Seen in vclu-e2e (provisioning profile, `TestBrokerCredentialsWithQuotes`).

Do not call `HAIntegration.setup` again from `onConnect`: it runs `HAStatePublisher.setup`, which
registers its `bus:on(...)` handlers on every call (`StatePublisher.lua`), so StateBus events would be
handled twice. Run setup once; after the connect, only publish.

### Not in scope
Republishing after the broker connection drops and comes back: today no path does that
(`HAStatePublisher.setup` publishes once, with no `onConnect`), so it is a separate change.

## Files
- `pkg/server/lua/connect_broker.lua`
- `pkg/lua/runtime/integrations/mqtt/MqttClient.lua` or HAIntegration (publish once connected)

## Acceptance criteria
- [ ] Embedded broker on, no `mqttClient`: after a restart a subscriber on `homeassistant/#` gets the discovery configs (vclu-e2e: drop the knownBug marker in `TestBrokerCredentialsWithQuotes`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0131-ha-discovery-embedded-broker.md`.

## Contract impact
None.
