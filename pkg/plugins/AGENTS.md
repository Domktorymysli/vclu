# pkg/plugins: plugin system

## A plugin is a factory, not a configured singleton
The sandbox is keyed by plugin id, so one plugin is one instance. No `config` section in the
manifest: the plugin returns an object with `create(opts)`, and each call makes an independent
instance with its own poller and sensors. Configuration is Lua written by the user or an agent.

## The wizard fills data, the code stays a factory (#247, prototype)
`"form": "form.json"` in plugin.json declares the wizard: `settings` (step 1) and an optional
`devices` list (step 2). The plugin may register `plugin:discover(fn(cfg, done))` to fill the
list and `plugin:moduleCode(fn(cfg, devices))` to propose the `create()` calls (step 3); vclu
writes them to `modules/plugins/<ns>__<id>.lua` only after the user saw the diff. Values are
checked in Go (`form.go`) on save; secret fields go to `plugin_secrets.json`, never to the config,
and reach only their plugin as `plugin:secret(name)`. A plugin's older `config` map is step 1.

## Secrets stay out of the registry
A plugin keeps a secret from `plugin:secret` in a local variable. Never in a sensor, an exposed
object, `createObject`, an event or a log line: other plugins, agents over MCP, dashboards and
MQTT/HA read those. The sandbox masks the plugin's secret values (4+ characters) as `***` in
`Logger`, `plugin:log`, `plugin:logSafe` and `print`; anything else it does not catch.

## Apps: a dashboard and actions of the plugin's own
`plugin:dashboard(id)` (id `[a-z0-9_]+`) returns a facade of `Dashboard.new("<shortId>_<id>")`
(`-` in the short id as `_`): the same chain API, auth `"session"` unless the plugin sets `"pin"`.
`build()` refuses an id held by a module's dashboard or another plugin's (`_plugin` on the
builder marks the owner; `Dashboard.API.list()` gives it as `plugin`). Functions the plugin passes
(also inside opts/fns tables) are wrapped: errors go to the plugin's log, once per message, and
back to the page. The raw builder and sections never reach the plugin; `area`, `floor`, `room`,
`covers` and `calendar` are refused, as they resolve house objects by name.

A plugin declares its dashboards in plugin.json: `"dashboards": [{"id": "lista", "name": "Zakupy" | {"pl", "en"},
"description"?: text | {"pl", "en"}}]` (id `[a-z0-9_]{1,64}`, at most 8, unique; a malformed list refuses the
manifest at download and at load, `dashboards.go`). On API 2 `plugin:dashboard(id)` refuses an id not declared
there; API 1 keeps any id. The declared name (Polish first) is the default name.

Who may open a plugin's dashboard is the user's, not the plugin's (the plugin cannot widen its own access
or another plugin's): from plugin code `:auth()` takes only `"session"` or `"pin"`, and `:pin()`, `:remote()`
and `:roles()` (also on a section) are refused with a message naming `dashboardAccess`. A module grants it:
`Plugin.getPlugin("@vclu/zakupy"):dashboardAccess("lista", {name, auth = "session"|"pin"|"public"|"off",
pin = "1234", remote = false|true|{pin = bool, roles = {...}|fn}})`. `pin` (4 digits) is needed for auth
`"pin"` or `remote.pin`; `"off"` is not served (404) and not listed. Errors point at the module's line. The
grant lives in `_dashboardAccess[pluginId][declaredId]` (Dashboard.lua), out of a plugin's reach; the builder
keeps the plugin's own choice in `_pluginOwn` and `Dashboard._applyAccess` sets `_name`, `_auth`, `_pin`,
`_remote` and `_off` from both on build, on the plugin's later `:name`/`:auth` and on a grant. A plugin reload
keeps the grant; a Lua reload starts empty and the module grants it again. `dashboardAccess` called from a
plugin (its proxy, or any function in its environment) is refused.

The wizard shows a Dashboards step for a plugin that declares any (also without a form) and then always a
Code step: the step is checked in Go (`CheckDashboardAccess`), kept in the config under `_dashboards` only to
fill the step again (the plugin does not get the key), and the Code step appends `DashboardAccessCode` (values
through `luastr.Quote`) to the plugin's own `moduleCode`, written after the user saw the diff.

`plugin:action(name, {description, params}, fn)` (name `[a-z][a-z0-9_]*`, at most 32): description
a text or `{pl, en}`, params `{p = {type = "string"|"number"|"boolean", description, required}}`.
`PluginSandbox:callAction` checks the params (unknown refused, types exact, strings up to 1000
characters) and answers JSON `{result}` or `{error, code}` with code `not_found`, `invalid` or
`failed`; fn returns a result (text, number, boolean, plain table) or `nil, err`. Secrets are
masked in both. Go reaches them through `actions.go` (`GET /api/plugins/actions`,
`POST /api/plugins/:id/actions/:name`, MCP `vclu_app_actions`/`vclu_app_action`, gated by the
bot's profiles on the plugin id). Cleanup removes the plugin's dashboards and actions.

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

## Time budget
Plugin code shares the one Lua state: `pkg/lua/plugin_budget.go` gives it 2 s of one callback
(10 s at load: the loader wraps init.lua and init in `__go_plugin_budget_loading`), told by the
sandbox's environment, which `PluginSandbox:create` registers and `cleanup` forgets. Three
overruns in 10 min suspend it: `Loader.Suspend` unloads it, `LoadPlugin` and `LoadAllEnabled`
skip it until `Resume` (POST `/api/plugins/:id/resume`); runtime only, a restart clears it.

## Permissions
`plugin.json` declares what a plugin reaches:
`"permissions": {"internet": ["api.example.com", "*.example.net"], "lan": true, "mqtt": true, "modbus": true}`.
`lan` is HTTP to local addresses,
checked in Go after DNS resolution, including this machine's own networks (`pkg/lua/http_lan.go`),
but never the ports vclu listens on at this machine's addresses (`LuaEngine.SetOwnTCPPorts`, set in
`cmd/vclu`; without it every port of this machine); `mqtt` and `modbus` give the plugin API
and the `Modbus` table. A plugin on API 1 (no `"api"`) asks for all three; on API 2 for what it
declares, nothing without the field (`permissions.go`).

`internet` (API 2; API 1 reaches any host, outside the grants) lists host names, `*.domain` for
the subdomains (not the domain itself), exact IP addresses, or `"*"` (string or `["*"]`) for any
host. A scheme, path, port, user or `*` elsewhere refuses the manifest at download and at load
(`internet.go`). The URL's host name is checked before the request and on every redirect
(`dialPolicy.checkURL`), and again in the dialer: an address that is local after DNS needs `lan`
(and its hosts), any other needs the name on the list. A plugin with `lan` reaches local names
without an internet list. The grant is `{"internet": {"hosts": [...]}}`, the hosts the user
allowed: a new host or `"*"` in an update waits (`pendingInternet`), the ones allowed before keep
working.

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
(`dialPolicy`), topics in the sandbox's `plugin:mqttSubscribe`/`mqttPublish` (a subscription
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

`udp` (API 2) is UDP to devices on the local network, for protocols such as a split air
conditioner's: granted on install like `lan`, narrowed like `grenton_raw` (`{"udp": {"hosts": [...]}}`,
limits `udpHosts`). `plugin:udpSocket` opens the same handle as `grentonSocket` but marked `udp`:
`SendUDP` refuses the CLU ports 1234 and 69 (those need `grenton_raw` or `tftp`) and the ports vCLU
listens on, and a broadcast goes only to the ports in plugin.json's `"udp": {"broadcastPorts": [7000]}`
(at most 8, never 1234 or 69, only with `"udp": true` in permissions: `udp.go`, checked at download
and at load), and never with a host list. A `grenton_raw` socket keeps its rules.

`tcp` (API 2) is a TCP client to devices on the local network (a local Tuya socket, telnet):
granted on install like `udp`, narrowed by hosts (limits `tcpHosts`). plugin.json lists the ports in
`"tcp": {"ports": [23]}` (required with the permission, at most 8, never a port of another
permission: `tcp.go`). `plugin:tcpConnect` (`pkg/lua/plugin_tcp.go`) connects in the background and
answers through the handle's queue (`connect`, data, `close` with a reason); sends go through a
writer with a time limit. `SetGrant` closes connections a new grant does not allow; `SetTCPTaken`
keeps the MQTT broker vclu uses out of reach. `lib/telnet.lua` is pure Lua over such a connection,
given to plugins as the global `Telnet`.

The grant records the ports of `udp` and `tcp` (`HostsGrant.BroadcastPorts`, `.Ports`; the
manifest's reach `Permissions` as `BroadcastPorts`/`TCPPorts`, never parsed from "permissions"):
`allowedBy` splits them like internet hosts, a port an update adds is pending (and the
permission's name with it) and the sandbox gets only the allowed ones. A nil list is a grant from
before ports were recorded: it allows the listed ports and `recordPorts` writes them at load.

`plugin:websocket` (`pkg/lua/plugin_ws.go`) has no permission of its own: it dials through the
plugin's HTTP `dialPolicy` (internet list, `lan` and its hosts) plus a refusal of this vclu's
addresses and, for a local address, `wsLANPort`: only the ports of plugin.json's
`"websocket": {"lanPorts": [80]}` (`websocket.go`, as `tcp.ports`; the `lan` grant records them in
`websocketPorts`), never a port of another permission or the MQTT broker's host, and gorilla/websocket does TLS after that dial. The handle lives in `PluginIO` like a
`tcp` connection (queue, writer, 16 handles); `HTTPManager.SetOnPolicy` lets a changed `lan` or
internet list close the connections it no longer allows.
