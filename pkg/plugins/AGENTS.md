# pkg/plugins: plugin system

## A plugin is a factory, not a configured singleton
The sandbox is keyed by plugin id, so one plugin is one instance. No `config` section in the
manifest: the plugin returns an object with `create(opts)`, and each call makes an independent
instance with its own poller and sensors. Configuration is Lua written by the user or an agent.

## Sensor ids are global
Ids of sensors and controls are flat across the whole of vCLU. Derive them from the instance's
options and refuse a duplicate with an error in the log instead of replacing another instance's.

## What the sandbox gives
`pkg/lua/runtime/core/PluginSandbox.lua` builds the environment from an allow-list: no
`setmetatable`, `require`, `io`, `debug`, `HTTP` or `MQTT`; `os` is a safe subset. Nothing a plugin
can reach is a shared mutable table: `string`, `table` and `math` are its own copies, `Logger`,
`JSON` and expose handles are facades bound to the real object, its own instance is a proxy
(identity fields read-only, sandbox hidden), and other plugins and registry objects are read-only
proxies whose only callable methods are reads (`get`, `getX`, `isX`, `hasX`), `pairs` included.
Functions a plugin puts on its instance (`create`, `init`, `get`) get its proxy as `self` also when
the core or a module calls them on the real instance. Arguments of those read methods are numbers,
strings and booleans only. Event payloads reach a plugin read-only. Timers, pollers, events, MQTT
subscriptions, exposed objects and the registry entry go through the plugin API and are cleaned up
on disable or reload; after cleanup the API refuses every call, so code still reachable (a sensor a
module kept) starts nothing. A plugin clears only its own timers and exposes only under its id. Methods of
facades (handles, pollers) and proxies refuse a self other than their own; `init`, `cleanup`,
`timers` are the core's. Events a plugin emits start with its prefix: an official plugin's short
id or the id's first word (`sun-position` → `sun:`), any other plugin's full id (`@jan/foo:`). `pkg/lua/plugin_sandbox_escape_test.go` holds the attempts to get out; a
change to the sandbox keeps them failing.

## Plugin ids
A namespace and a short id are lower case, without `--` or a `-` at either end
(`validPluginID`): the directory and KV file name `ns--id` then belong to one id, also on a
case-insensitive file system. Exposed paths of plugins that would share an MQTT topic or HA
unique_id after its normalization are refused (`ExposedObjects:expose`).

## Permissions
`plugin.json` declares what a plugin reaches beyond HTTP to the internet:
`"permissions": {"lan": true, "mqtt": true, "modbus": true}`. `lan` is HTTP to local addresses,
checked in Go after DNS resolution, including this machine's own networks (`pkg/lua/http_lan.go`); `mqtt` and `modbus` give the plugin API
and the `Modbus` table. A plugin on API 1 (no `"api"`) asks for all three; on API 2 for what it
declares, nothing without the field (`permissions.go`).

What it asks for is not what it gets: `grants` on the installed entry in `.vclu.json` is what the
user allowed, and the plugin gets the part of its request the grants allow. Installing from the
panel allows what the plugin asks for then; an entry without grants (installed by hand, or before
grants existed) is given what its files on disk ask for on the next load, before an update.
An update that asks for more runs without it, logs it and lists it as `pendingPermissions` in
`GET /api/plugins/installed` until `POST /api/plugins/:id/permissions` allows it (the plugins
page: "Allow", or the shield button for the details).

A grant is set when its key is present and can be narrowed:
`{"lan": {"hosts": ["192.168.1.20", "10.0.0.0/24"]}, "mqtt": {"subscribe": ["shelly/#"], "publish": []}, "modbus": {}}`.
No list allows any host or topic, an empty list none. Hosts are checked in the dialer after DNS
(`lanHostsDialer`), topics in the sandbox's `plugin:mqttSubscribe`/`mqttPublish` (a subscription
must lie inside an allowed filter: `shelly/#` covers `shelly/+/status`, `+` does not cover `#`).
`ValidateGrants` refuses a host that is not an IP or prefix and a malformed filter. On API 1 the
`HTTP`/`HttpClient`/`MQTT` globals pick their own instance, so grants cannot reach them: the
sandbox gives them only while `lan` (`mqtt`) is allowed in full, without lists. An update from
another repository resets grants to what installing gives; without the old files nothing is
allowed until the user does. `POST .../permissions` refuses unknown keys.

`grenton_raw`, `tftp` and `serial` (#240) reach a CLU's commands, files on devices and the device
on a port: `Grants()` leaves them out, so neither installing nor recording grants from the files
allows them, only the user in the panel. Their grants are `{"grenton_raw": {"hosts": [...]},
"tftp": {"hosts": [...]}, "serial": {"ports": ["/dev/ttyUSB0"]}}`; without `ports` the plugin
opens no port. The handles live in Go (`pkg/lua/plugin_io.go`, `PluginIO`), keyed by plugin id,
which checks the grant again: the plugin's socket and TFTP client refuse this machine's addresses
and anything outside the local network, the socket cannot bind ports vCLU reserved
(`ReserveUDPPorts`), a broadcast goes only to the CLU port, where the server drops datagrams from
plugin sockets (`FromPlugin`). Data comes back through `__vclu_plugin_io` on the run loop, queued per
handle so a slow callback does not stop the reader. `PluginSandbox:cleanup` releases everything
the plugin opened; a reload closes all of it.
