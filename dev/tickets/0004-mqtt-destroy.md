# #4: MQTT client: destroy() and re-creating a client with the same id

- **Type:** bug
- **Area:** mqtt
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A Lua MQTT client can be released completely, and creating a client with an id that already exists
does not leave the old one half alive.

## Context
`pkg/lua/runtime/integrations/mqtt/MqttClient.lua`: `MQTT:new(id)` stores the instance in
`MQTT._instances[id]`, overwriting an existing one without disconnecting it; its subscriptions,
event handlers and the Go-side connection (`__go_mqtt_*`) stay. `MQTT:disconnect(removeInstance)`
disconnects and removes the registry entry, but keeps `_subscriptions` and `_eventHandlers`.
There is no `destroy()`. Modules that create a client at load time, or plugins that create one
per instance, can therefore leak connections and get messages delivered to a dead instance.

The Lua runtime contract says MQTT subscriptions survive a Lua reload
(`pkg/lua/lua_contract_test.go`): keep that.

## Files
- `pkg/lua/runtime/integrations/mqtt/MqttClient.lua`
- the Go side of `__go_mqtt_disconnect` if it does not release everything
- a test in `pkg/lua/`

## Acceptance criteria
- [ ] `MQTT:destroy()` disconnects, removes subscriptions on the broker and in Lua, clears event
  handlers and removes the instance from `MQTT._instances` and from the Go side.
- [ ] `MQTT:new(id)` with an id in use destroys the old instance first and logs a warning naming it.
- [ ] After `destroy()`, a message on a topic the old instance subscribed to reaches no callback.
- [ ] The reload contract tests still pass.

## Contract impact
Adds `MQTT:destroy()` to the Lua API: document it in the API reference.
