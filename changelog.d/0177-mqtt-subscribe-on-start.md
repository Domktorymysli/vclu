### Integrations

- **MQTT subscriptions made while modules load work after a restart** - `MQTT:subscribe` in a module's init used to fail with `client MQTT not found` on start (modules load before the MQTT client connects) and was not restored on reconnect. Go now keeps every subscription of a client, also one made before the client exists or while it connects, and subscribes all of them on each connect and broker reconnect; the Lua side no longer subscribes again on connect ([#177](https://github.com/Domktorymysli/vclu/issues/177)).
