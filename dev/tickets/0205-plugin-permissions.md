# #0205: Plugin permissions in plugin.json and a sandbox without shared objects

- **Type:** feature
- **Area:** plugins | sandbox
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A plugin reaches only what its `plugin.json` declares, internet hosts included on API 2, and nothing it
holds is a table shared with the core or with another plugin. Plugins written before this keep
running: the manifest names the plugin API it is written for, and the stricter contract is API 2.

## Context
- A plugin runs in `PluginSandbox` (`pkg/lua/runtime/core/PluginSandbox.lua`) with a `setfenv`
  environment; the loader (`pkg/plugins/loader.go`) creates the sandbox and runs `init.lua` in it.
  Today the environment hands the plugin the shared `string`, `table` and `math`, the registry's
  objects and other plugins' instances as they are, and HTTP, MQTT and Modbus with a module's reach.
- `permissions` in `plugin.json`: `{"lan": true, "mqtt": true, "modbus": true}`. `lan` allows
  HTTP to local addresses, checked after DNS resolution in Go (`pkg/lua/http_lan.go`, the dialer's
  `Control`); `mqtt` the plugin's MQTT methods; `modbus` the `Modbus` table. `internet` is the list of internet hosts
  an API 2 plugin reaches (below).
- Plugin API versions (`pkg/plugins/permissions.go`, `PluginAPIMin`/`PluginAPIMax`): `"api"` in
  `plugin.json`, missing = 1. API 1 is the contract from before permissions: `lan`, `mqtt` and
  `modbus` for any plugin, events and expose paths with any name, the `HTTP`, `HttpClient` and
  `MQTT` globals (read-only facades). API 2 adds the permissions, prefixes and the plugin-API-only
  HTTP and MQTT below. Isolation (own standard tables, read-only objects, own instance and timers)
  holds on both. Third-party plugins on API 1 are in use (e.g. a BleBox plugin with LAN HTTP and
  MQTT), so API 1 must run them unchanged. A plugin for an API above `PluginAPIMax` does not load
  and says to update vCLU; `Plugin.apiVersion` is `PluginAPIMax`. `"strict": true` in the
  `plugins` section of `.vclu.json` loads only API 2 or later.
- What a plugin asks for (the manifest) and what it gets are separate: `grants` on the installed
  entry in `.vclu.json` is what the user allowed; the plugin gets the part of its request the grants
  allow. Installing allows what it asks for then; an entry without grants gets them from its files on
  disk on the next load. An update asking for more runs without it until allowed
  (`pendingPermissions`, `POST /api/plugins/:id/permissions`, "Allow" on the plugins page).
- The user narrows a grant in the plugin's settings, not the plugin in its manifest (an author
  does not know the hosts and topics of someone else's house): `lan.hosts` (IP or prefix, checked
  in the dialer after DNS), `mqtt.subscribe` and `mqtt.publish` (topic filters, checked in the
  sandbox). No list: any; an empty list: none. On API 1 the globals are outside it.
- A repository can be one plugin: without `plugins.json` and with `plugin.json` at its root it is
  an index of one (`pkg/plugins/index.go`), namespace from the manifest id or the repository owner.
- Before plugin API versions (superseded): a plugin without the field: an official plugin (`Installed.Repository` names the default
  repository with the official URL, or no repository and namespace `@vclu`) keeps all three with a
  warning in the log; any other plugin gets HTTP to the internet only (`pkg/plugins/permissions.go`).
  The official plugins declare their permissions in `vclu-plugins` (branch `plugins/permissions`);
  both are released together, or third-party plugins installed before the release lose LAN access.
- Read-only views: an object a plugin did not create is a proxy that reads fields, proxies nested
  tables, calls `get`, `getX`, `isX`, `hasX` on the real object and refuses every other method.
  Go reads tables raw (`JSON:encode`, the `table` library), so the sandbox unwraps proxies before
  data leaves (`plain`) and gives the plugin its own `table`, `unpack` and `JSON`.
- Events a plugin emits carry its prefix: an official plugin's short id or its first word
  (`sun:`), any other plugin's full id (`@jan/foo:changed`). A plugin's expose path goes under its
  id; a plugin cannot change an object a module exposed.
- Internet hosts on API 2 (decided 25.11, before API 2 ships, the only time the default can be
  set without breaking someone's plugin): `"internet": ["openapi.tuyaeu.com", "*.tuyaeu.com"]`
  reaches only those names (the panel says "connects to: …"); `"internet": "*"` (or `["*"]`) any
  host, with a warning in the panel; no `internet`: no internet. API 1 is unchanged. The host name
  in the URL is checked, also after every redirect (`CheckRedirect`), not the IP (cloud IPs
  change); `*.example.com` matches subdomains, not `example.com`; an IP in the URL matches only
  that IP on the list (or `"*"`). The LAN boundary stays as it is and works independently: a
  listed host that resolves to a local address is still refused without `lan`. An update adding a
  host or changing the list to `"*"` waits for the user like any new permission. The user narrows
  it in `grants.internet` like `lan.hosts`, if cheap; a wizard "server address" field adding its
  host to the grant is #247. A refusal says what to do: "api.example.com is not on the host list
  of @x/foo (permissions.internet)". `mqtt_remote` is covered by the same list later, not here.
- Plugin ids are lower case `a-z`, digits, `.`, `_`, `-`, without `--` or a `-` at either end, so
  two ids never share a directory or a KV file.
- UDP to devices (decided 26.11, for a Gree air conditioner plugin: UDP 7000, discovery by
  broadcast): `"permissions": {"udp": true}` on API 2, apart from `grenton_raw`, which is a CLU's
  commands. `plugin:udpSocket({port})` has the methods of `grentonSocket` without the cipher. It
  reaches local addresses only, not this vclu, not the ports vclu listens on, and not the CLU
  ports 1234 and 69 on any host: those need `grenton_raw` or `tftp`. A broadcast goes only to the
  ports in `"udp": {"broadcastPorts": [7000]}` (whole ports, at most 8, never 1234 or 69, only
  with `"udp": true`; otherwise the manifest is refused at download and at load). Installing
  allows it like `lan` and the confirmation lists the ports; the grant `{"udp": {"hosts": [...]}}`
  narrows it like `grenton_raw`, and with a host list the plugin does not broadcast. The grant
  records the ports (`"broadcastPorts": [7000]`): a port an update adds waits for the user like an
  internet host.
- TCP to devices and telnet (decided 26.11: a local Tuya socket on 6668, telnet consoles):
  `"permissions": {"tcp": true}` on API 2 with the ports it connects to in `"tcp": {"ports": [23]}`
  (whole ports, at most 8, required with `"tcp": true`, never 1234, 69, 502, 1883 or 8883: those
  belong to `grenton_raw`, `tftp`, `modbus` and `mqtt`; otherwise the manifest is refused at
  download and at load). `plugin:tcpConnect({host, port, timeout})` is a client only: an IPv4
  address on the local network, not this vclu, not the external MQTT broker vclu uses; it connects
  in the background (5 s, at most 30) and answers through `onConnect`, `onReceive` and `onClose`;
  `send` queues (1 MiB) and a writer sends with a time limit, so a slow device does not hold Lua.
  Installing allows it like `lan` and the confirmation lists the ports; `{"tcp": {"hosts": [...]}}`
  narrows it, and a narrowed or withdrawn grant closes open connections. The grant records the
  ports (`"ports": [23]`): a port an update adds waits for the user, those allowed before keep
  working, one plugin.json no longer lists is refused; a grant from before ports were recorded
  gets those plugin.json lists at the next load. Telnet is not a permission: `Telnet.wrap(conn)` (`lib/telnet.lua`, a global in
  plugins) answers DO with WONT and WILL with DONT, drops subnegotiation and gives lines and prompts.
- WebSocket client (decided 26.11: Tuya's message service over wss://, Shelly Gen2 and Home
  Assistant over ws://): no permission of its own. `plugin:websocket(url, {headers, protocols,
  timeout})` takes `ws://` or `wss://` and dials through the plugin's HTTP rules (the internet host
  list before DNS and after it, `lan` and its hosts for a local address), and never an address of
  this vclu; a local address only on the ports in `"websocket": {"lanPorts": [80, 8123]}` (with
  `lan`, at most 8, never a port of another permission, as `tcp.ports`), never the host of the MQTT
  broker vclu uses, and the `lan` grant records them (`"websocketPorts"`) like the `tcp` ports; TLS runs after that dial, so wss is checked the same. Headers the handshake sets
  (Host, Upgrade, Connection, Sec-WebSocket-*) are refused, others pass (Authorization, Origin). It
  opens in the background (10 s, at most 30), a redirect is an error with its HTTP status, a message
  from the server is at most 1 MiB (over it the connection closes with 1009), `send` queues 1 MiB
  for a writer with a time limit. A change of `lan` or the internet list closes the connections it no
  longer allows (1008); a reload closes with 1001; `close(code, reason)` takes 1000 or 3000-4999.
  No reconnect in the core. Shares the 16 handles of a plugin.
- A plugin's HTTP (decided 26.11) does not reach the ports vclu listens on at this machine's
  addresses (the panel, the MQTT broker and its WebSocket, HomeKit, the Lua HTTP server), with
  `lan`, a host list naming it or a name resolving to it, on every plugin API; another service on
  the same machine (InfluxDB on `localhost:8086`) stays reachable with `lan`. Modules are unchanged.

## Files
- `pkg/lua/runtime/core/PluginSandbox.lua`, `PluginAPI.lua`, `PluginHelpers.lua`, `ExposedObjects.lua`
- `pkg/lua/runtime/objects/RemoteObject.lua` (integer index on `get`/`set`/`execute`)
- `pkg/lua/http.go`, `pkg/lua/http_lan.go`
- `pkg/plugins/permissions.go`, `loader.go`, `udp.go`, `kvstore.go`, `AGENTS.md`
- `pkg/lua/plugin_io.go` (`udp` sockets), `plugin_tcp.go` (`tcp` connections), `plugin_ws.go` (WebSockets), `runtime/lib/telnet.lua`
- `pkg/plugins/tcp.go`
- `pkg/web/handle_web_plugins.go` (an update records its repository; grants), `web/static/js/plugins.js`, `web/templates/plugins.html`
- `pkg/config/config.go`
- Tests: `pkg/lua/plugin_sandbox_escape_test.go`, `plugin_sandbox_data_test.go`,
  `plugin_official_smoke_test.go` (opt-in, `VCLU_PLUGINS_DIR=<vclu-plugins checkout>/plugins`),
  `http_lan_test.go`, `plugin_api_version_test.go`, `pkg/plugins/permissions_test.go`,
  `index_test.go`
- `pkg/plugins/index.go`, `pkg/web/handle_web_plugins.go` (a repository of one plugin)

## Acceptance criteria
- [ ] A plugin without `"api"` loads as API 1: LAN HTTP, MQTT, events and expose paths with any
      name and the `HTTP` global work without `permissions`; the globals cannot be modified
      (`TestPluginAPI1KeepsTheOldContract`).
- [ ] `"api": 3` gives "update vCLU"; strict mode refuses API 1 and loads API 2.
- [ ] An update whose manifest asks for a permission the user has not allowed runs without it
      and lists it as pending; allowing it through the API gives it. A plugin installed before
      grants keeps what its files asked for (`TestPluginsAPIUpdateAskingForMoreWaitsForTheUser`,
      `TestRecordGrantsKeepsWhatTheFilesAskFor`).
- [ ] A plugin narrowed to some hosts reaches them and no other local address; a subscription
      or publication outside the allowed filters is refused; an empty publish list allows none;
      a bad host or filter is refused by the API (`TestPluginSandboxLANHosts`,
      `TestPluginSandboxMQTTTopics`, `TestPluginsAPIPermissionsRefusesBadLists`).
- [ ] A repository with only a root `plugin.json` lists one plugin and downloads it.
- [ ] A plugin with `"permissions": {}` gets `a local address needs "lan"` from `plugin:httpGet`
      on a LAN address and a refusal from an internet address; with `lan` and the host in
      `internet` both work.
- [ ] A plugin without `mqtt` cannot subscribe or publish; without `modbus` `Modbus` is `nil`.
- [ ] A plugin cannot change `string`, `table`, `math`, `Logger`, `JSON` or `Plugin` for anyone else;
      on API 2 the `HTTP`, `HttpClient` and `MQTT` globals are absent in the sandbox.
- [ ] An object a plugin did not create is read-only, also through `pairs`, `next` and the keys
      they hand out; only `get`, `getX`, `isX`, `hasX` are callable on it. Another plugin shows only
      its sensors and controls. `JSON:encode`, `table.concat` and `unpack` see the full data;
      `table.insert`, `table.remove` and `table.sort` on it are refused.
- [ ] `Plugin.getPlugin` returns the plugin's own instance; its id cannot be changed; a plugin
      clears only its own timers; a disabled plugin's pollers, HTTP retries and exposed objects go away.
- [ ] On API 2 an event a plugin emits starts with its prefix and an expose path goes under its id; an object a
      module exposed cannot be re-exposed by a plugin; two exposed paths never share an MQTT topic.
- [ ] An API 2 plugin without `permissions` gets none of them, official or not.
      `Installed.Repository` is recorded on update.
- [ ] Every plugin in `vclu-plugins` loads, runs its init with a config, serves its sensors and
      unloads without leaving a timer or a registry entry, with and without `permissions` in its
      manifest (`TestOfficialPluginsSmoke`).
- [ ] API 2 without `internet`: a request to an internet host is refused with a clear error.
- [ ] A host list: a listed host passes, another is refused, a redirect from a listed host to an
      unlisted one is refused.
- [ ] `*.example.com` does not let in `example.com.evil.net` or `evilexample.com`.
- [ ] `"*"` passes and the panel shows the warning.
- [ ] API 1 is unchanged.
- [ ] An update adding a host waits for the user.
- [ ] Every official plugin works with its declared hosts (`TestOfficialPluginsSmoke`).
- [ ] `udp` without `grenton_raw` opens `plugin:udpSocket`, sends and receives; without `udp`
      neither Lua nor Go opens one, and `udp` does not open `grentonSocket`
      (`TestPluginDeviceUDPEcho`, `TestPluginDeviceUDPRefusedWithoutPermission`).
- [ ] A `udp` socket refuses an address outside the local network or of this vclu, the ports
      1234 and 69 and vclu's own, a broadcast to a port not in `broadcastPorts`, and any broadcast
      with a host list; a `grenton_raw` socket keeps its rules (`TestPluginDeviceUDPTargets`,
      `TestPluginDeviceUDPBroadcastPortsFromSandbox`).
- [ ] `broadcastPorts` that are not whole ports, repeated, more than 8, 1234 or 69, or without
      `"udp": true` refuse the manifest (`TestManifestUDPBroadcastPorts`, `TestManifestUDPNeedsThePermission`).
- [ ] The install confirmation lists `udp` with its broadcast ports (`TestPluginsAPIPreviewReadsTheManifest`).
- [ ] `tcp` without other permissions connects, sends and receives in order, and a device closing
      ends it in `onClose`; without `tcp` neither Lua nor Go connects (`TestPluginTCPEchoAndClose`,
      `TestPluginTCPRefusedWithoutPermission`).
- [ ] A `tcp` connection refuses an address outside the local network or of this vclu, a name, a
      port not in `"tcp"` or of another permission, the MQTT broker vclu uses, a host outside the
      list, a timeout over 30 s; it times out, and a plugin opens at most 16 handles
      (`TestPluginTCPTargets`, `TestPluginTCPTimeoutAndHandleLimit`).
- [ ] The plugin's cleanup and a narrowed grant close an open connection (`TestPluginTCPClosedByReleaseAndGrant`).
- [ ] `"tcp"` ports that are not whole ports, repeated, more than 8 or of another permission, `"tcp"`
      without the permission and the permission without ports refuse the manifest
      (`TestManifestTCPPorts`, `TestManifestTCPNeedsThePermissionAndPorts`).
- [ ] `lib/telnet` refuses options, drops commands split across reads and gives lines and prompts
      (`TestTelnetLibBytes`, `TestPluginTelnetLogin`).
- [ ] `plugin:websocket` opens with the server's protocol and sends and receives text and binary in
      order; the server closing reaches `onClose` with its code (`TestPluginWSEchoAndClose`).
- [ ] A WebSocket reaches what the plugin's HTTP reaches: a listed internet host, not another one,
      a listed name on a local address only with `lan`, a local address only with `lan` and its
      hosts, never this vclu; API 1 as its HTTP (`TestPluginWSInternetHosts`, `TestPluginWSLAN`,
      `TestPluginWSAPI1`).
- [ ] A WebSocket to a local address needs its port in `websocket.lanPorts`; a port of another
      permission and the MQTT broker's host are refused; the manifest refuses bad ports and the
      field without `lan`; the `lan` grant records the ports and an update adding one waits
      (`TestPluginWSLAN`, `TestPluginWSLANPorts`, `TestManifestWebSocketLANPorts`,
      `TestWebSocketPortsInTheLANGrant`).
- [ ] Handshake headers are refused and others reach the server; a message over 1 MiB, a redirect,
      no answer in time, a timeout over 30 s and a non-ws URL end or refuse it
      (`TestPluginWSHeaders`, `TestPluginWSLimits`).
- [ ] `close(code, reason)` sends the code, a reload closes with 1001, 16 handles at most, and a
      narrowed `lan` closes an open connection (`TestPluginWSCloseReloadAndLimit`,
      `TestPluginWSGrantWithdrawn`).
- [ ] A plugin's HTTP to vclu's own port is refused, by its address or a name resolving to it,
      also with a host list naming it; another port on the machine and a module's request pass
      (`TestHTTPManagerPluginNotToOwnAddress`).
- [ ] Installing records the `udp` and `tcp` ports in the grant; an update adding a port shows
      it waiting and the plugin reaches only the ports allowed before; allowing it gives it; a
      port plugin.json drops is not reached; an old grant records the listed ports at load
      (`TestPortsInTheGrant`, `TestPortsReachTheSandboxAndOldGrantsRecordThem`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0205-plugin-permissions.md`.
- [ ] `DEVELOPMENT.md` in `vclu-plugins` documents `permissions`, the event prefix and the id rule,
      with the vCLU version that introduces them.

## Contract impact
Contract change in the plugin API (CONTRIBUTING.md), versioned: `api` and `permissions` (with the
`internet` host list, `udp` and `tcp`), `udp.broadcastPorts`, `tcp.ports` and `websocket.lanPorts` are new manifest fields, and `plugin:websocket` a new method under the existing `internet` and `lan`. API 1 (no `api`) keeps the old contract. API 2: the shared globals `HTTP`,
`HttpClient` and `MQTT` leave the sandbox (`plugin:httpRequest`, `plugin:mqttSubscribe` replace
them), event names and expose paths get the plugin's prefix, and only declared permissions apply.
On every version objects a plugin did not create become read-only and ids are lower case. No
official plugin needs a code change; their manifests move to `"api": 2`. A decision
record is not needed: the contract is the plugin API itself and the PR carries the migration notes.
