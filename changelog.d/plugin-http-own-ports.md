### Plugins

- **A plugin's HTTP does not reach vclu itself** - on this machine's addresses the ports vclu listens on (the panel, the MQTT broker and its WebSocket, HomeKit, the Lua HTTP server) are refused to a plugin's HTTP and WebSocket on every plugin API, with `lan` too; another service next to vclu, such as InfluxDB on `localhost:8086`, stays reachable. Modules are unchanged.
