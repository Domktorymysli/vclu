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

## Permissions
`plugin.json` declares what a plugin reaches beyond HTTP to the internet:
`"permissions": {"lan": true, "mqtt": true, "modbus": true}`. `lan` is HTTP to local addresses,
checked in Go after DNS resolution (`pkg/lua/http_lan.go`); `mqtt` and `modbus` give the plugin API
and the `Modbus` table. A plugin without the field gets none of them, except an official one
(installed from the official repository, or by hand in `@vclu`), which keeps all three with a
warning in the log until its manifest declares them (`permissions.go`).
