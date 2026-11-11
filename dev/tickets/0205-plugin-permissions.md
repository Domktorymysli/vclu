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
A plugin reaches only what its `plugin.json` declares beyond HTTP to the internet, and nothing it
holds is a table shared with the core or with another plugin.

## Context
- A plugin runs in `PluginSandbox` (`pkg/lua/runtime/core/PluginSandbox.lua`) with a `setfenv`
  environment; the loader (`pkg/plugins/loader.go`) creates the sandbox and runs `init.lua` in it.
  Today the environment hands the plugin the shared `string`, `table` and `math`, the registry's
  objects and other plugins' instances as they are, and HTTP, MQTT and Modbus with a module's reach.
- `permissions` in `plugin.json`: `{"lan": true, "mqtt": true, "modbus": true}`. `lan` allows
  HTTP to local addresses, checked after DNS resolution in Go (`pkg/lua/http_lan.go`, the dialer's
  `Control`); `mqtt` the plugin's MQTT methods; `modbus` the `Modbus` table. Nothing else exists
  yet; a host allowlist for the internet is a later ticket.
- A plugin without the field: an official plugin (`Installed.Repository` names the default
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
- Plugin ids are lower case `a-z`, digits, `.`, `_`, `-`, without `--` or a `-` at either end, so
  two ids never share a directory or a KV file.

## Files
- `pkg/lua/runtime/core/PluginSandbox.lua`, `PluginAPI.lua`, `PluginHelpers.lua`, `ExposedObjects.lua`
- `pkg/lua/runtime/objects/RemoteObject.lua` (integer index on `get`/`set`/`execute`)
- `pkg/lua/http.go`, `pkg/lua/http_lan.go`
- `pkg/plugins/permissions.go`, `loader.go`, `kvstore.go`, `AGENTS.md`
- `pkg/web/handle_web_plugins.go` (an update records its repository)
- `pkg/config/config.go`
- Tests: `pkg/lua/plugin_sandbox_escape_test.go`, `plugin_sandbox_data_test.go`,
  `plugin_official_smoke_test.go` (opt-in, `VCLU_PLUGINS_DIR=<vclu-plugins checkout>/plugins`),
  `http_lan_test.go`, `pkg/plugins/permissions_test.go`

## Acceptance criteria
- [ ] A plugin with `"permissions": {}` gets `a local address needs "lan"` from `plugin:httpGet`
      on a LAN address and a response from an internet address; with `lan` both work.
- [ ] A plugin without `mqtt` cannot subscribe or publish; without `modbus` `Modbus` is `nil`.
- [ ] A plugin cannot change `string`, `table`, `math`, `Logger`, `JSON` or `Plugin` for anyone else;
      the `HTTP`, `HttpClient` and `MQTT` globals are absent in the sandbox.
- [ ] An object a plugin did not create is read-only, also through `pairs`, `next` and the keys
      they hand out; only `get`, `getX`, `isX`, `hasX` are callable on it. Another plugin shows only
      its sensors and controls. `JSON:encode`, `table.concat` and `unpack` see the full data;
      `table.insert`, `table.remove` and `table.sort` on it are refused.
- [ ] `Plugin.getPlugin` returns the plugin's own instance; its id cannot be changed; a plugin
      clears only its own timers; a disabled plugin's pollers, HTTP retries and exposed objects go away.
- [ ] An event a plugin emits starts with its prefix; an expose path goes under its id; an object a
      module exposed cannot be re-exposed by a plugin; two exposed paths never share an MQTT topic.
- [ ] An official plugin without `permissions` keeps `lan`, `mqtt` and `modbus` with a warning;
      any other plugin without the field gets none. `Installed.Repository` is recorded on update.
- [ ] Every plugin in `vclu-plugins` loads, runs its init with a config, serves its sensors and
      unloads without leaving a timer or a registry entry, with and without `permissions` in its
      manifest (`TestOfficialPluginsSmoke`).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0205-plugin-permissions.md`.
- [ ] `DEVELOPMENT.md` in `vclu-plugins` documents `permissions`, the event prefix and the id rule,
      with the vCLU version that introduces them.

## Contract impact
Contract change in the plugin API (CONTRIBUTING.md): `permissions` is a new manifest field; the
shared globals `HTTP`, `HttpClient` and `MQTT` leave the sandbox (`plugin:httpRequest`,
`plugin:mqttSubscribe` replace them); objects a plugin did not create become read-only; event names
get the plugin's prefix; ids are lower case. No official plugin needs a code change. A decision
record is not needed: the contract is the plugin API itself and the PR carries the migration notes.
