# Changelog

## Unreleased

### Home Assistant

- **A cover published with `expose()` moves, and runs the right way** - a position from Home Assistant was written to the object's feature 0, which on a Grenton roller is its read-only state, so the slider moved nothing; OPEN and CLOSE were sent the same way, and the position went to HA unconverted (vCLU counts 0 = open, HA 0 = closed). Commands now call the object's own `setPosition`, `open`, `close` and `stop` (`setValue` only for objects without them), positions are converted both ways, discovery announces `position_open: 100` / `position_closed: 0`, and the state is `open` whenever the cover is not fully closed. A roller without a position (class 11, or a proxy not regenerated) gets no position slider in HA, a position sent to it is refused (it would call a method class 11 does not have), a position retained by older versions is cleared. A Grenton roller's movement is shown as `opening`/`closing` while it moves (also when only the movement changed, e.g. a stop at the same position), and a roller without a position reports `stopped` when it stops. **Contract change:** the position on `vclu/exposed/.../cover/.../position` is now on HA's scale; a Home Assistant automation that compensated for the inverted position needs changing ([#36](https://github.com/Domktorymysli/vclu/issues/36), thanks to Dawkaxxl for the logs). Not tested on Grenton roller hardware.
- **`open()` and `close()` of a Grenton roller with a position go to `SetPosition`** - they sent MoveUp/MoveDown, which stop a roller that is moving (Object Manager's device definitions), so a second OPEN from Home Assistant, HomeKit or Lua stopped the roller halfway. With a position and no time they now send SetPosition 100 % / 0 %; with a time (`open(5000)`) or without a position MoveUp/MoveDown stay. Not tested on Grenton roller hardware.

## v1.4.0 (2026-10-15)

### Installation

- **`deploy/update.sh` updates an installation** - running the installer again reset the web port to 8080 and rewrote the service without a backup. The updater reads the install and data directories and the port from the systemd service, does nothing when that version is installed (`--force` to reinstall, `--version` to pick one), stops vCLU and packs the program, web files, runtime, service file and the configuration from the data directory (Lua, plugins, KV, HomeKit pairing; not history, metrics or backups) into `<install dir>/backup-<version>-<time>.tar.gz`, readable by root only (the three newest are kept, pruned only after a good update), starts vCLU again if packing fails, and prints how to restore it if the install fails. A stopped service with the target version installed is installed again rather than reported up to date. Without `curl`, `tar` or `gzip` it stops before changing anything and says how to install them. It stops when the service file was edited, since the installer rewrites it (`--force` to go on; keep changes in `systemctl edit vclu`). Data is not touched except the bundled AI docs, as by the installer.

### Dashboards

- **A status tile's colour can follow its value** - `color` on `:status()` (section or header) can be a function, e.g. `function() if smsLeft < 20 then return 'danger' end end`. It is read on every poll: an open page recolours the tile (in the header, its icon) without a reload and drops the colour when the function returns nil. An error in the function leaves the value shown, drops the colour and is logged once per tile. A string `color` works as before ([#8](https://github.com/Domktorymysli/vclu/issues/8)).

### Lua runtime

- **A `VirtualNumber` with a fractional step stores the value on the step** - snapping to `step = 0.1` left binary noise, so 53.9 was stored and published as 53.900000000000006 to Home Assistant, HomeKit, MQTT and history. The snapped value is rounded to the step's decimals (#9, thanks @wzd8888).
- **EventBus warns when callbacks for one event pile up** - code that calls `EventBus:on` in a timer or on every re-create without `off` adds callbacks for ever and every `emit` runs them all. At 100 callbacks for one event name `on` logs a warning with the name and the count, and again at 200, 400...; the callbacks are still added ([#7](https://github.com/Domktorymysli/vclu/issues/7)).
- **`expose()` warns about an object whose `onChange` returns no unsubscribe function** - `unexpose()` calls that function to remove its callback; without it the callback stayed registered and kept running, silently. The log now shows one warning with the object's path. Plugin sensors and controls (`plugin:sensor`, `plugin:control`) now return one too, so `unexpose()` removes their callback ([#5](https://github.com/Domktorymysli/vclu/issues/5)).

### Plugins and HTTP

- **Plugins see the request error in `resp.err`** - the plugin docs describe `resp = { status, body, headers, json, err, errCode }` and plugins check `if resp.err then`, but the runtime only passed the error as the callback's second argument, so `resp.err` was always nil: a refused connection reached plugins as a parse error and SolaX logged `HTTP 0`. `resp.err` now carries the same text as the second argument ([vclu-plugins#3](https://github.com/Domktorymysli/vclu-plugins/issues/3)).
- **A poller warns when `done()` gets a string and has no `onData`** - `done("message")` is data to the poller, so the tick counts as a success and `onError` never runs. The poller logs one warning pointing to `done(nil, err)`; behaviour is unchanged.

- **Concurrent HTTP requests from Lua no longer lose each other's responses** - plugins (`PluginHelpers.httpRequest`), HTTP clients (`HTTP:async*`) and Grenton `HTTPRequest` objects took completed requests from one shared queue and dropped the ones that were not theirs, so with several plugins polling at once some timed out at random or got `HTTP 0`. Each request now collects its own response. Two requests from one plugin in the same second no longer share an id (one of them used to get the other's response), an HTTP client reports the real transport error instead of `HTTP 0`, and responses nobody collects are dropped after 5 minutes ([#19](https://github.com/Domktorymysli/vclu/issues/19)).

### Grenton

- **A state change whose report was lost shows up within 30 s** - a CLU reports a change with one UDP packet and no retry; when it was lost, vCLU kept the old state until another object in the same subscription changed (a light switched off from the myGrenton app stayed on in the dashboard). The CLU answers every subscription renewal (every 30 s) with the current values, and vCLU threw that answer away; it now applies it like a report ([#29](https://github.com/Domktorymysli/vclu/issues/29)).
- **A roller reports its position** - vCLU subscribed every object to feature 0, for a roller its movement (0 stopped, 1 up, 2 down), so Home Assistant showed 0-2 % and a stopped roller as closed, and HomeKit showed no position. A `ROLLER_SHUTTER` (class 24) is now subscribed to feature 7 `Position` as well; the position is the roller's value and its movement is read with `getState()`. Home Assistant gets the position with open/closed derived from it. Import the OM file again (or `POST /api/om/regenerate`) after updating: only a regenerated proxy marks a roller as having a position, an older one keeps the old behaviour. The first firmware of the 2.0 roller modules (fv01) has no position and om.lua does not tell it apart; set `_hasPosition = false` on such a roller.
- **A roller's position runs the right way** - Grenton counts 0 % = closed .. 100 % = open (feature 7 and `SetPosition`, Object Manager's device definitions), vCLU 0 = open .. 100 = closed. `RemoteCover` passed the value unchanged, so `setPosition(30)` (30 % closed) left the roller 30 % open, from Lua, HomeKit and Home Assistant alike. It now converts both ways. Not tested on Grenton roller hardware.
- **Contract change:** the value (`_lastState`, `onChange`, history) of a `RemoteCover` with a position is that position, 0 = open .. 100 = closed, not its movement; `getState()` still returns the movement and `getPosition()` the position. A roller without a position (class 11, or a proxy not regenerated) keeps its movement as the value and `getPosition()` returns nil.

### Web

- **After updating a plugin, the plugins page no longer shows it as unconfigured** - Install/Update stored the plugin under its short id while the page keys installed plugins by full id (`@vclu/...`), so the Installed tab showed a second entry with version `?`, "not configured" and an empty config form until a reload. Install and update now answer with `fullId` and `version`; the page updates the entry under the full id, keeping its configuration and enabled state, and matches installed plugins to the catalog by full id, so the description and the update button appear on the Installed tab ([#31](https://github.com/Domktorymysli/vclu/issues/31)).

### Home Assistant

- **A dimmer published from the OM import follows the scale it announces** - the registry `light` handler looked for the subpath `brightness/set` while the topic parser already strips the trailing `/set`, so a brightness command was refused as an "unknown payload" and the dimmer never moved. It also passed Home Assistant's 0..100 value straight to an object whose value is 0..1. A brightness command now uses the same path as `expose()`: 0..100 in, 0..1 out, clamped to the range, through `setBrightness` when the object has it ([#1](https://github.com/Domktorymysli/vclu/issues/1)).
- **A roller published from the OM import moves to the position set in Home Assistant** - the registry `cover` handler looked for the subpath `position/set` while the topic parser already strips the trailing `/set`, so a position command was refused as an "unknown payload". It also looked for `SetPosition`/`Open`/`Close`/`Stop` while a Grenton roller has them in lower case. A position now goes through `setPosition`, clamped to 0..100 and rounded, and OPEN/CLOSE/STOP through `open`/`close`/`stop` ([#25](https://github.com/Domktorymysli/vclu/issues/25)).

### MQTT

- **`MQTT:destroy()` releases a client, and `MQTT:new(id)` with an id in use no longer leaves the old one half alive** - `new` overwrote the registry entry while the old client stayed connected and subscribed, and `disconnect()` kept its subscriptions and handlers, so a module or plugin that re-created its client leaked connections. `destroy()` unsubscribes on the broker, disconnects, clears subscriptions and event handlers and removes the client from Lua and Go; `new(id)` with an id in use destroys the old client first and logs a warning with the id. Messages and connection events still arriving from the old client are dropped instead of reaching the new one. The built-in client (global `MQTT`, used by the Home Assistant integration) cannot be destroyed or re-created. A Lua reload starts with an empty registry, so it does not trigger the warning ([#4](https://github.com/Domktorymysli/vclu/issues/4)).

## v1.3.0 (2026-10-10)

### HomeKit

- **The bridge is no longer empty after a restart** - since 2026-03-27 it started before user.lua, modules and `exposeRegistry()` ran, so it came up with 0 accessories after every restart of vCLU and stayed that way until a manual restart on the HomeKit page; a Lua reload did not update it either. It now starts after the first load (retrying, not publishing an empty bridge, if Lua is busy), adds objects exposed by a reload and applies access changes. An object leaves the bridge only when it is hidden for HomeKit or on an explicit restart on the HomeKit page: a module that fails to load or a read in the middle of a reload no longer removes accessories, and a failed read keeps the running bridge.
- **Accessories keep their ids** - ids came from Go map order, so every bridge restart shuffled them and a tile or a Home automation could end up controlling another object. Each exposed path now keeps its id in `homekit-aids.json` (back it up with `homekit-db/`), a removed object's id is never reused, and accessories are published in id order. A broken or unreadable id file is kept aside and numbering continues from 10000, so no remembered id lands on another object. The first start with this version gives every accessory a new id once: rooms and automations in the Home app need to be set again one time.
- **No more accidental switches** - types without a HomeKit counterpart (`number`, `button`, `lock`, `garage_door`, `climate`, unknown) became a writable switch, so the Home app or Siri's "turn everything off" wrote 0/1 into analog outputs and setpoints, and a gate or lock exposed as a switch skipped the unlock HomeKit asks for. They are left out of HomeKit now; `illuminance` is a light sensor and `binary_sensor` a contact sensor (1 = open).
- **Satel objects are read-only in HomeKit** - a partition or output was a switch whose write only changed the state in vCLU (the panel got no command), so Apple Home could show the alarm disarmed while it was armed.
- **Access changes apply at once** - a HomeKit write is checked against the current profile (read-only or hidden objects were still switchable until the bridge restarted), a refused, failed or paused (during a reload) write puts the tile back, a read-only scene cannot be run, a refused fan write no longer changes the speed shown, and hiding or showing an object, editing a profile or its assignment updates the bridge. Saving a profile now also applies it to Lua, Home Assistant and MQTT, which until now waited for a restart.
- **Stopping the bridge cannot freeze vCLU** - stop held a lock while waiting for the state refresh, which could be waiting for the Lua loop, which could be waiting for that lock; the accessory list is now swapped in under the lock instead of written while the Lua loop reads it.
- **One bad value cannot empty the bridge** - the list of exposed objects was hand-built JSON: a NaN or infinite reading (e.g. a Modbus counter) or a backslash or control character in a name made it unparsable and the bridge started with no accessories. It is now encoded properly; `JSON.encode` also turns infinity into `null` (gopher-lua's `math.huge` is the largest float, not infinity) and escapes every control character.
- **Outdoor temperatures below zero** - HomeKit showed 0 °C for a negative reading (the characteristic was limited to 0..100).
- **Lights switch with 1, dimmers get a slider** - "on" from the Home app wrote 100 to every light, also to Grenton outputs (DOUT) exposed as `light`, where on is 1, and to dimmers whose value is 0..1. On/off lights now get 1/0; dimmable objects (with `getBrightness`) get a Brightness slider (value 0..1 shown as 0..100 %), and on/off uses their `switchOn`/`switchOff`, which keep the last brightness.
- **Covers move the right way** - HomeKit counts 100 = open, vCLU 0 = open, and the position was passed through unchanged, so "open" closed the blind. Grenton rollers showed their movement state (0-2) as the position and a position was written with `set` instead of `setPosition`. Covers now convert the direction, read `getPosition` and write `setPosition`. A Grenton roller still shows no position in the Home app: vCLU does not receive the roller's position from the CLU yet, and its movement state is no longer shown as one. Not tested on Grenton roller hardware.
- **A slow iPhone cannot stall the Lua loop** - state changes were sent to the Home app on the Lua loop (network writes without a deadline); they are now queued and sent by a separate goroutine, keeping only the latest value per object.
- **HomeKit settings apply when saved** - the bridge restarted with the old PIN, port and name until vCLU was restarted, and turning HomeKit off did nothing. Saving now applies them and turning it off stops the bridge (turning on a bridge that was off at startup still needs a restart). A PIN that is not 8 digits or that HomeKit refuses as insecure (12345678, 00000000...) is rejected instead of being replaced by the default; a busy port or bad PIN fails the start with an error instead of a bridge that only looks running, and a bridge that failed to start keeps retrying in the background. An empty PIN or bridge name still gets the defaults (`00102003`, `vCLU Bridge`).
- An object that has not reported yet is no longer shown as off, and a Lua reload started by Object Manager pauses Home app writes like a manual reload does.
- More than 149 accessories (the HomeKit bridge limit) are logged and the newest objects left out instead of failing the pairing; objects that already have an id stay.
- **A pause of Home app writes always ends** - writes are ignored during a Lua reload; a reload that failed before its end kept them ignored until the next good reload, and an earlier reload could end a later one's pause. A pause now belongs to its reload and ends after 60 s at the latest.
- **Names changed after start reach the bridge** - Satel zone names come from the panel some seconds after start, when the bridge was already built, so HomeKit showed `satel.zone.1` until the next reload. The bridge now checks every 30 s whether names, objects or access changed and rebuilds (at most every 2 minutes; accessory ids stay), and a rename raises the configuration number so the Home app fetches the new names. Accessories already renamed in the Home app keep the name given there.
- Persisted virtual objects and MQTT JSON payloads also turn infinity into nothing/`null` (same `math.huge` mistake): an infinite value could fail every write of the shared KV namespace.

### Home Assistant

- **On/off lights have no brightness slider** - every object exposed as `light` was announced with brightness, so a Grenton output (DOUT) got a slider, "on" wrote 100 and the slider 0..100 to an output where on is 1. Brightness is now announced only for objects that have it (`getBrightness`); an on/off light writes 1/0 and refuses a brightness command, and the brightness value retained on the broker by earlier versions is cleared. MCP `get_device` reports the same per-object capabilities.
- **Dimmers get the right scale** - a dimmer's value is 0..1, but "on" from HA wrote 100 and the slider wrote 0..100, so any brightness meant full power. The slider now goes through `setBrightness` (or value/100), "on" through `switchOn` (a virtual dimmer restores its last brightness; a Grenton dimmer gets the module's own SwitchOn), "off" through `switchOff`, and the "on" HA sends right after a brightness change no longer overrides that brightness. The `dimmer` range reported to agents and the MCP tool descriptions now say 0..1. Not tested on Grenton dimmer hardware.

### Satel

- **A name given in `expose()` stays** - names read from the panel replaced the name given in `expose()` or `rename()` on every connection to the panel. A panel name now fills only objects nobody named; to name zones yourself, expose them with their path, e.g. `expose(_:get("satel.zone.1"), "sensor", { path = "satel.zone.1", name = "Motion", area = "Hall" })`.
- **A scan right after another is accepted** - a scan returned its result before marking itself finished, so a scan started right after it was refused as "already running".

### Dashboards

- **Charts on public dashboards** - charts and status sparklines read `/api/metrics/data`, which needs a login, so on a dashboard with `:auth('public')` they stayed empty. A dashboard now serves its own metrics under its own auth (`/api/dashboard/<id>/metrics`): only metrics it draws and no further back than the range it draws.
- **The PIN of a dashboard is checked** - any cookie of 64 hex characters opened a `:auth('pin')` dashboard without the PIN. The cookie now carries an expiry and a signature over the dashboard, its current PIN and the expiry (changing the PIN ends old cookies), with a random key kept in `dashboard-pin.key`; PIN tries are limited (5, then one per 6 s; behind a reverse proxy the limit is shared by everyone coming through it). A PIN given as a number (`:pin(1234)`) is accepted. Devices already unlocked ask for the PIN once more.

### Build

- **Go 1.26** - release binaries, the Docker image and CI were built with Go 1.24, which no longer gets security fixes; govulncheck reported 22 reachable issues in the standard library (HTTP/2 server, TLS, `html/template`) and in `golang.org/x/net`, `golang.org/x/text` and `quic-go`. Builds now use Go 1.26 (toolchain 1.26.9) and updated modules. One known issue remains in `go-chi/chi` v1 (open redirect in its `RedirectSlashes` middleware, no fixed version), pulled in by the HomeKit library, which does not use that middleware. Building from source needs Go 1.26.

### MCP

- **The sandbox no longer stops a draft for memory it does not keep** - to confirm a draft over the memory limit the guard runs a GC, and what the draft allocated during that GC counted as kept: under CPU load a draft churning large strings was stopped as "allocates without bound". That memory is now subtracted; past three times the limit the draft is stopped regardless.
- **`vclu_mqtt_discover` suggests code that runs** - the suggested Lua used `MQTT_CLIENT` (not defined unless `user.lua` creates it), `JSON.parse`/`JSON.stringify` (the functions are `JSON.decode`/`JSON.encode`), `VirtualSwitch()` instead of `VirtualSwitch:new(name)` and `setValue` on a binary sensor, so it failed on the first line. Snippets now use the client of the broker the device was found on (`MQTT`, or `MQTT.getInstance(id)` for another Lua client; a note for the embedded broker), follow the Tasmota pattern from the agent examples without sending a device's own report back to it, and expose Zigbee lights as switches (on/off). Zigbee2MQTT sensors get one object per known payload key (temperature, humidity, illuminance, occupancy, contact, water leak; multi-endpoint keys like `temperature_l1` are read from `property`), contact and water-leak sensors and illuminance stay out of HomeKit, where they would show up as switches. ESPHome gets a template for one sensor. Covers, locks, fans, climate, multi-endpoint switches and sensors without a known key get no snippet instead of wrong code. Each snippet is run in the Lua runtime in tests, names and IDs from the broker are quoted so they cannot change the code.
- **Agents are no longer told promote rolls back on its own** - the server instructions promised an automatic rollback that promote does not do; promote reports modules that failed to load. The never-called 15 s error-count watchdog behind that promise is removed: there is no automatic rollback by design, the agent fixes the module. A promote whose modules fail to load now records an incident (`vclu://incidents/latest`: recent logs, audit trail, the diff backup → promoted) and its result points to it; before, nothing produced incidents. The web panel no longer says incidents come from a rollback or watchdog.

## v1.2.0 (2026-10-09)

### Security

- **Access control enforced where commands execute** - readonly/hidden set on the Access page was honoured only when entities were advertised, so a device marked readonly could still be switched from HA. Commands to exposed objects now check access, authorization uses the object's real registry path (not one rebuilt from the MQTT topic; the `_G` fallback is gone), readonly from the panel remaps HA discovery to read-only components, and an unreadable `access_control.yaml` denies instead of falling back to full access.
- **Draft tests cannot start processes or write files** - `vclu_draft_test` had the full Lua `os` and `io` libraries, so a draft could run a shell command or write any file as the vCLU user during a test (the name check in validation is bypassed by building the name at runtime). In the sandbox `os.execute`, `os.exit`, `os.remove`, `os.rename`, `os.tmpname`, `os.setenv`, `io.popen`, `io.tmpfile`, `io.output(file)` and `io.open` for writing are now stubs listed in `blocked_calls`; reading files works as in production.
- **A runaway draft test cannot take vCLU down** - the sandbox runs in the vCLU process, so a draft looping over a growing table filled memory (about 15 GB in the 5 s test window on a desktop) and on a Raspberry Pi the kernel would kill vCLU, the house controller with it. A test whose heap grows by more than 256 MB (15% of RAM on smaller machines, at least 64 MB) is now stopped with `sandbox memory limit exceeded`, and `string.rep` refuses a result over a quarter of that. A single library call on already huge data (e.g. `table.concat` of hundreds of large strings) can still overshoot the limit before it is checked. Also fixed: once a draft was loaded, the test's time limit no longer reached event handlers and timers, so a `while true do end` in a handler kept a CPU core busy until vCLU restarted.
- **MCP file tools do not follow symlinks** - reading, searching, listing and deleting files, drafts, diffs, backups, promote and rollback refuse a path when it or a directory on the way is a symlink, so a link in `modules/` cannot expose or overwrite a file outside the data directory; a `modules/` directory that is itself a symlink is no longer visible to these tools (the data directory may still be one). `vclu_draft_test` accepts only draft paths (`modules/...`), and rollback rejects backup names that would restore outside `modules/`.

### Integrations

- **Modbus TCP gateway** - read RS485 devices (e.g. energy meters) through a Modbus TCP gateway; async, one transaction at a time per gateway, sockets pooled across reloads. A reply with a bogus MBAP length, a non-zero protocol id or only stale frames drops the socket, so later replies are not read shifted (previously the socket stayed desynchronised until restart).
- **Plugins without configuration load** - factory plugins driven from user modules were skipped because their config was empty; only plugins whose manifest asks for configuration are skipped now. `Plugin.get("@vclu/x")` (dot instead of colon) now raises and points to `Plugin.getPlugin`.
- **HA discovery: device_class and state_class from expose()** - so e.g. an energy meter can be exposed in a form the HA Energy dashboard accepts.
- **Satel: panel scan no longer times out** - reading ~300 device names takes over a minute on a real ETHM-1 (~275 ms per name), past the 60 s request limit, so the scan failed with `satel: scan timeout`. The limit is now 5 min and the page shows a progress bar (zones / partitions / outputs, done/total). The page now says that the alarm state is not refreshed during the scan and control commands (arm, outputs) wait until it ends (polling the panel mid-scan made the ETHM-1 stop answering). A second scan while one is queued or running is refused (HTTP 409); stopping the integration interrupts a running scan. Each scan logs a one-line summary of reply times and result codes.
- **Satel: last scan is kept** - a successful scan is saved (`satel_scan.json` in the data dir, `GET /api/satel/scan`) and shown again when the page opens, with the current configuration pre-checked, so the selection can be changed later without rescanning. Factory names (`Wyjście 12`, `Wejście 3`, `Output 12`) count as unnamed for the "Named" quick select. Manual ranges are collapsed under "Advanced" and explained as the configuration the scan selection fills in.
- **Satel: Polish letters in names** - zone/partition/output names read from the panel are decoded as Windows-1250 (as in openHAB's Satel binding); previously letters like Ł, Ż showed as `�` in the UI and in Home Assistant entity names.
- **Satel: errors name the stalled command** - a panel that stops answering now reports which command it choked on (e.g. `no response to 0xEE 0149`) instead of a bare `i/o timeout`. Data is shown only for short queries; control commands carry the user code, which never appears in errors.

### Lua runtime

- **`datetime.setTimezone(name)` / `datetime.timezone()`** - the server clock is often UTC, so automations reading `datetime.now().hour` got the wrong local time. Call `datetime.setTimezone("Europe/Warsaw")` as the first line of `modules/init.lua`; `now`, `fromTimestamp`, `parse` (strings without an offset), `format`, `add` and `diff` then work in that zone, DST included, and tables carry `tz` (e.g. `CEST`). Without the call nothing changes. The zone database is built into the binary. `os.date` stays server time.
- **`filter` for noisy sensors in `expose()`** - `expose(t, "temperature", { filter = { delta = 0.2, heartbeat = 600 } })`: a change is reported (history, HA/MQTT, HomeKit, `onChange`) only when it differs by at least `delta` from the last reported value, or after `heartbeat` seconds (checked on the next reading). `getValue()` keeps the raw value. Read-only numeric sensors only (a filter on a control would swallow commands); on plugin objects it covers history and HA, not the plugin's own `onChange`. A wrong option is rejected before the object is exposed.
- **`history = false` in `expose()`** - keeps an object's changes out of the history store (computed or technical values you do not want logged); events still reach HA/MQTT, HomeKit and watchers. `lastChange()`/`since()`/`history()` then return nothing. Can be switched with `handle:update({ history = true })`.
- **History on disk and Tools -> History and audit** - every state change is written to daily files (`data/clu/history/<UTC day>.jsonl`, in batches every 5 min and at shutdown; 90 days / 1 GB by default) and the last 24 h are read back at startup, so history and the last known values survive a restart (no more fake `0 -> value` entries after a deploy). The new page under Tools browses changes by object, origin, who (actor), text or correlation id and time range, shows memory/disk status and write errors, and edits the limits (applied live; turning history or the disk off needs a restart); writing now, clearing memory and deleting the files need a web session and a confirmation, never an API key. REST: `GET /api/history`, `/api/history/status`, `GET/PUT /api/history/settings`, `POST /api/history/flush|clear-memory|clear-disk`. `vclu_device_history` reads the same store with these filters and pages, no longer needs Dev Mode, and leaves out objects the bot may not read.
- **Virtual objects: `initial` and `persist` in every constructor** - `VirtualSwitch:new("tryb_nocny", { initial = 1, persist = true })`; `VirtualSensor`, `VirtualDimmer`, `VirtualCover`, `VirtualNumber` and `VirtualText` take the same options. With `persist` (true = the object's name, or a string key) the object starts from its last value after a reload or restart instead of `initial`; restoring is not a state change, a restored number is clamped to the current bounds and a value of another type is ignored. All persisted objects share one KV namespace (`virtual-objects`).
- **`persist(name, defaults)` - automation state that survives a reload and a restart** - `local st = persist("wiatrolap", { pauseUntil = 0 })`, then `st.pauseUntil = ...` is saved to KV (namespace `module:<name>`) a second later; pending writes are flushed before a Lua reload and at shutdown. A flat map of JSON values; replace nested tables as a whole or call `st:save()`. `st:get/set/save/toTable` are methods, so those names cannot be keys; keys must be strings and values cannot be functions; names are case-insensitive, and a second `persist()` with the same name adds its missing defaults. A corrupt saved state gives the defaults and an error in the log. Each save rewrites the namespace's KV file, at most once a second per namespace. In `vclu_draft_test` the state lives in the scenario's in-memory KV and survives a `reload` step.
- **Who changed it: `origin`, `actor`, `correlationId` on every change** - in events, history and `vclu_device_history`. Entry points set a trusted actor: MCP (`agent:<bot>`), web (`user:<login>`, `dashboard:<id>`, `service:web-api` for an API key), HomeKit (`service:homekit`), MQTT (`service:mqtt`, HA command topics `service:home-assistant`), CLU reports (`grenton`, `device:<CLU>`), Satel (`physical`, `device:satel`); modules act as `lua`/`automation:module:<file>`, plugins start as `plugin:<id>`, plain Lua is `automation:unknown`, HTTP requests to Lua endpoints `api`/`service:http`, scripts sent by a CLU `device:<CLU name>`. `StateContext.with(ctx, fn)` sets a context in Lua and timers keep the one they were created in. `StateBus:watch(path, fn, {runAs = "automation:<id>"})` makes the reaction act as that automation. `switchOn(ms)` on a CLU switch: the CLU's own switch-off at the end (from 2 s before to 15 s after) is attributed to the command, so an automation can tell its own switch-off from a person's; any other change of the light or another command in between drops that expectation. Known limits: `switchOff(ms)`, dimmers and lights with a time are not attributed this way, a GPIO button object reports as Lua, and Lua code can set any actor for itself.
- **Per-object history: `obj:lastChange()`, `obj:since()`, `obj:history(n)`** - every state change is kept in memory in Go (24 h, 10 000 per object, 50 000 in total, set in `.vclu.json` under `history`) and survives Lua reloads. It is recorded before `StateBus` listeners and watchers run, so they already see it; `obj:onChange` handlers run earlier, before the record. A first reading after startup or reload is not a change. `vclu_device_history` reads the same store. About 11 MB at the full limit.
- **`StateBus:watch(path, fn, {from, to, origin, actor})`** - calls `fn(event)` for changes of one object (path, name or `*`), also for objects created later; returns an unsubscribe function, and a failing watcher does not stop the others.
- **Commands from automations are state changes** - `switchOn()` and other optimistic updates on objects that are not exposed published nothing, and the CLU echo with the same value did not either, so such changes never reached history, HA or watchers.
- **KV returns numbers as numbers** - a whole number stored with KV came back as a string until restart.
- **`datetime.between(from, to[, at])`** - whether the local time of day is in `[from, to)`, e.g. `datetime.between("21:00", "06:00")` for a night window across midnight; `"HH:MM"` or `"HH:MM:SS"`, equal ends mean the whole day. Uses the `setTimezone` zone, so the window follows DST; the optional third argument checks a datetime table or timestamp (seconds or milliseconds) instead of now. Wall-clock semantics: on the spring DST day a window inside the skipped hour never matches, in autumn the repeated hour matches twice. The end of the day is `"00:00"`, not `"24:00"`.
- **vCLU's own CLU date/time features follow `datetime.setTimezone`** - `FEATURE_HOUR`, `FEATURE_TIME`, `FEATURE_DATE`, `FEATURE_DAY_OF_WEEK` etc. read by Grenton scripts used the server clock (UTC, 1-2 h off in Poland). Also fixed: `FEATURE_MINUTE` always returned nil, `FEATURE_DAY_OF_WEEK` was off by one (now 0 = Sunday as documented), `FEATURE_UNIX_TIME` was wrong on servers not running in UTC.
- **One `state_changed` per real change** - a `_:sync` or `setValue` on an exposed object emitted the event twice (HomeKit notified twice, history entries doubled), and `vclu_device_history` never filled `old`. All producers now go through `StateChange.publish`: one event per change, none for the same value, with `{id, path, old, value, atMs, type, origin, actor, correlationId, obj}`. History entries carry `old` (plus `new`/`time` as before). An exposed object always publishes under its expose path (a `_:sync` by registry path used to emit under both paths), an exposed CLU object now also publishes its first value after startup, and `setValue` with the current value no longer re-publishes. `setValue` on objects readable only through `getValue()`/`get(0)` publishes the state actually reached (a clamped or rejected write no longer reports the requested value); only write-only controls report the requested value. `oldValue` stays as an alias of `old`.
- **Plugin sensors and controls emit `OnChange`** - plugins call `sensor:_notify()` after updating state, but the method never existed, so `onChange` on a plugin sensor never fired and an exposed plugin control with an async setter (e.g. Salda fan speed) never reported its new value. `_notify()` now emits `OnChange(value, old)` when the value changed.
- **A failing Lua command runs once** - `Execute` and `ExecuteWithTimeout` (CLU commands, the web console, plugin loading, `vclu_exec`) retried a script that failed at runtime in up to three wrappers, repeating every side effect before the error, such as a command already sent to a CLU. The wrapper is now chosen at compile time and the script runs exactly once.
- **A cyclic table no longer crashes vCLU** - `json.encode(t)` or storing `t` in KV where `t` refers to itself overflowed the Go stack and killed the whole process; it now returns an error. Nesting over 64 levels or more than a million elements is refused the same way, and a sparse table like `{[1e9] = x}` encodes as an object instead of allocating a billion-element array.

### Core

- **`.vclu.json` is written atomically** - the config is written to a temporary file, synced and renamed over the old one, so a crash or power cut mid-write can no longer leave a truncated config.

### Web

- **Links to the OM files page work again** - links on the HomeKit, Access and Debug pages pointed to `/om-files`, a route that no longer exists (404); they now open the OM Files tab on the Grenton page.

### Build

- **Linux builds find the a/ templates** - the code asked for `MAIN.LUA`, `OM.LUA`, `USER.LUA` while git has them lowercase; macOS hid the mismatch, CI and goreleaser on Linux failed.
- **CI enforces formatting and runs the race detector** - `gofmt` check, a separate `go test -race` job, and a 5 min limit on the regular test run. A GPIO test that failed about 1 run in 8 (fixed 50 ms sleep) now waits for the event.
- **LDoc generation works again** - `task docs` failed: `Taskfile.yml` did not parse and LDoc aborted on doc comments inside a local table.
- **Release archives keep the Lua runtime tree** - archives put every runtime file flat in `runtime/`, so a fresh install from a release archive could not load its modules. The Docker image is now built for amd64 and arm64.

### Docs

- **README rewritten** from the code: real API examples, AI agents, history, integrations, measured footprint.
- **Contracts for contributors** - `CONTRIBUTING.md` lists the behaviours covered by contract tests (state changes, history, access control, sandbox, ...) and the rules for changing them.
- **Agent docs in English** - `docs/ai/examples.md` and the `house.md` template.

### MCP

- **Scenario tests in `vclu_draft_test`** - a draft can now be tested end to end without the house: time is virtual (`at` sets the start, `os.time`, `os.date`, `datetime.now` and timers follow it) and moves only on `{"advance": seconds}`; `{"set": path, "value": v}` feeds a value the way a CLU report does; `{"reload": true}` starts a fresh Lua state keeping the clock and KV; `expect_state`, `expect_clu_calls` and `expect_history` check the outcome and failures land in `errors`. Commands to CLUs are recorded in `clu_calls` (exact transport script, e.g. `DOU4992:execute(1, 60000)`) instead of vanishing. CLU objects from `imports/proxy_*.lua` now exist in the sandbox (they were missing, so `_:get("CLU….DOU…")` returned nil), and Satel zones come from the live runtime or from `fixtures`, and the `datetime` zone from the live runtime (or `timezone`); looking up an object the sandbox lacks logs a warning. Timers follow the production limits (1000 timers, 24 h max), user.lua loads before om.lua as in production, and `state_changes` are sorted by path. Logger lines appear once in `logs` (they were doubled). An error that the runtime only logs, such as a failing EventBus or StateBus handler, now fails the scenario step or injected event that caused it (it used to pass silently).
- **Sandbox has the production libraries** - `json`, `xml`, `datetime` and `color` were missing in `vclu_draft_test`, so any draft using them (e.g. `datetime.setTimezone` in `init.lua`) failed in the sandbox while working in production.
- **`vclu_grep` searches subdirectories** - the default glob matched only files one level deep, so modules in `modules/<dir>/` were never searched. `**` in a glob now spans directories (`modules/**/*.lua`).
- **Rollback restores the right file** - backup names stored `/` as `_`, so a module with `_` in its name (`modules/foo_bar.lua`) was restored to `modules/foo/bar.lua` and the broken version stayed in place while the promote reported a rollback. New backups use `~` for `/` (older ones still read as before), a rollback after a failed multi-file promote (and, in the web editor, after a failed reload) restores each file to the path it was promoted to and removes files the promote created, rollback failures are reported instead of ignored, promote writes the module atomically (a failed write, e.g. a full SD card, keeps the old version instead of a truncated file), and two promotes of one file within a second no longer share a backup.
- **Draft pipeline reports failures** - sandbox `package.path` mirrors production; module load results are reported per module on reload/promote; promote separates failures it caused from pre-existing ones; `vclu_logs` gains a level filter.

## v1.1.0 (2026-10-04)

### API

- **OpenAPI/Swagger documentation** - 60 core REST endpoints annotated with swaggo (`system`, `settings`, `satel`, `grenton`, `mqtt`, `homekit`, `access`, `backup`, `stats`, `registry`). Spec generated to `web/api/swagger.json` (`go generate ./pkg/web`), served at `/api/openapi.json` and rendered with Redoc at `/api-docs` (both behind auth; Redoc vendored, no CDN).

### Grenton Integration

- **Fix: remote sync sessions silently dying on the CLU** - the periodic renew sent all `clientRegister` packets back-to-back; the CLU drops packets in such a burst, so sessions further down the list were never renewed, expired after TTL and stopped reporting (state in vCLU/HA stayed wrong for days, e.g. lights shown on, frozen temperatures). Renew now sends them one at a time, each scheduled by a timer after the previous one (gap = `batchDelay`, at least 50 ms), so the runtime never waits and a stalled runtime can't release them as a burst. Affects v1.0.0 (regression since the switch to fire-and-forget renew).

### Security

- **Secret files are now 0600** - `.vclu.json` (auth hashes, MQTT credentials, Satel PIN, HomeKit PIN) and the plugin `kvstore.json` (API tokens stored by plugins) are written with mode 0600 instead of 0644. Existing installations are tightened automatically on the next config write (`chmod` for `.vclu.json`, atomic rename for `kvstore.json`). `keys.json` was already 0600.

### Integrations

- **Satel: force arm blocked by default** - `ForceArm` now requires `allowForceArm: true` (it arms despite violated zones, weakening protection). Enforced in Go like the disarm guard.
- **Satel web panel** - new "Satel" page under Integrations: connection form (host/port/PIN/integration key, poll interval, arm mode, allowDisarm with warning) with **Test connection** (`satel.TestConnection` — dials ETHM-1, reads panel model/version via 0x7E, validates the encryption key), **panel scan** (`Panel.Scan` reads names of all zones/partitions/outputs via 0xEE in the session goroutine) with checkbox tables that compile back to range strings, and a **live state** tab (partitions/zones/outputs/temperatures from the Go cache, 3s refresh). API: `/api/satel/{status,config,test,scan,state}`. Secrets are write-only (empty form field keeps the stored value).
- **Satel Integra** - first non-Grenton native integration. New `pkg/satel/` implements the ETHM-1 Plus integration protocol (TCP :7094): frame encoding with CRC + byte stuffing, optional AES-192 encryption, panel identification (0x7E/0x7C), state polling with delta detection, device name reading (0xEE) and control commands (arm/disarm/clear alarm/outputs). All I/O runs in a dedicated goroutine — Lua reads cached state and drains events via `__go_satel_poll_event` (work-queue safe, no RunLoop blocking). Lua objects `SatelZone`/`SatelOutput`/`SatelPartition`/`SatelThermometer` inherit Virtual\* classes, register as `satel.zone.N` etc. and expose to MQTT/HA/HomeKit/MCP out of the box. Configured via `satel` section in `.vclu.json`; `disarm`/`clearAlarm` are **blocked by default** (`allowDisarm: false`). Covered by protocol test vectors (CRC, AES from public reference implementations) and an in-process fake Integra TCP server (plain + encrypted).

## v1.0.0 (2026-06-08)

First public release.

### Core

- **Lua runtime engine** - sandboxed Lua 5.1 via gopher-lua with hot-reload, EventBus, StateBus, timers (`setInterval`, `setTimeout`, cron)
- **Object registry** - unified `_registry` with type mapping (DOUT, DIMMER, ROLLER, DIN, AnalogOUT), `expose()` auto-registration, `onChange()` subscriptions
- **Virtual devices** - VirtualSwitch, VirtualDimmer, VirtualRoller, VirtualNumber, VirtualText with full state management
- **Remote devices** - RemoteSwitch, RemoteLight, RemoteDimmer, RemoteCover, RemoteSensor proxying real Grenton CLU via encrypted UDP
- **Module system** - auto-loaded `modules/*.lua` scripts for user automation, scenes, schedules
- **Plugin system** - sandboxed Lua plugins with KV store, lifecycle hooks, permission model, hot-loading
- **Channel-based work queue** - replaced engine mutex with fast/slow queues and an `engineState` state machine; per-item panic recovery (one crash no longer kills the run loop)
- **Async MQTT I/O** - fire-and-forget publish, async connect/subscribe, non-blocking event polling

### Grenton Integration

- **Full protocol compatibility** - UDP discovery, AES-128-CBC encryption, TFTP server, client reporting
- **Object Manager pairing** - TFTP-based config exchange, `config.txt` + `CONFIG.JSON` generation
- **Remote subscriptions** - optimistic updates, fire-and-forget commands (`__go_send_command_noreply`), periodic `clientRegister` re-registration instead of destroy+recreate
- **Multi-CLU** - execute Lua on any registered CLU from web console
- **Sensor import from OM** - ONE_WIRE, PANELSENSTEMP, PANELSENSLIGHT, HUMIDITY_SENSOR parsing; `RemoteThermometer` + parametrized `RemoteValueSensor` with temperature/illuminance/humidity mappings

### Integrations

- **MQTT** - embedded broker (mochi-mqtt) + client, per-object access control
- **Home Assistant** - MQTT auto-discovery with device classes, availability topics
- **HomeKit** - native Apple HomeKit bridge (brutella/hap), accessory protocol
- **Prometheus** - time-series metrics collection, `/metrics` endpoint, custom `metric()` API
- **HTTP client** - GET/POST/PUT/DELETE from Lua for webhooks and external APIs

### AI / MCP

- **MCP server** - 36+ tools for AI agents (Claude, Codex, Gemini)
- **Layer 1** - device control, scenes, registry, logs, metrics, dashboards
- **Layer 2 (Dev Mode)** - code read, draft write, sandbox test, promote-to-production pipeline
- **Per-bot access control** - API keys, profile assignments, `botCanRead`/`botCanControl` filtering
- **Agent memory** - persistent context between sessions via `vclu_context_save`

### Web UI

- **16+ pages** - Status, Settings, Wizard, Editor, Tools Console, Dashboards, Backup, Debug, MQTT, HomeKit, MCP, Access Control, Plugins, OM Files
- **Lua editor** - Monaco-based with syntax highlighting, autocompletion, validation, search, diff view, draft management
- **Interactive console** - terminal-style command/result log with JSON tree rendering, command history, local + remote execution
- **Dashboard builder** - tab groups, widget registry, real-time state updates
- **5-step setup wizard** - device type, modules, Grenton keys, security, finish
- **i18n** - English + Polish

### Security

- **Authentication** - bcrypt passwords, 12h sessions, SameSite=Strict cookies
- **CSRF protection** - token-based, auto-injected in all templates
- **API keys** - SHA-256 hashed, per-bot MCP authentication
- **Profile-based access control** - named profiles with per-object overrides, `full-access` built-in, auto-migration from legacy format

### Hardware

- **GPIO** - Raspberry Pi (ARM64) support with `GPIO_DOUT` (relay), `GPIO_DIN` (button), pin registry, input polling with debounce
- **Mock controller** - GPIO simulation for development on non-Pi platforms

### Infrastructure

- **Single binary** - ~13 MB on ARM64, ~12.8 MB RAM on Raspberry Pi
- **Docker** - multi-stage Alpine image (~45 MB), docker-compose with host networking
- **Systemd** - service file with security hardening (NoNewPrivileges, ProtectSystem)
- **Backup/Restore** - ZIP-based with auto-backup scheduling, restore with safety backup
- **CI/CD** - GitHub Actions: test + vet + lint, GoReleaser multi-arch releases (linux/amd64, arm64, armv7, armv6, darwin/arm64)
- **One-line install** - `curl -sSL https://vclu.pl/install.sh | sudo bash`
- **API naming standardization** - `HttpRequest`/`HttpListener` → `HTTPRequest`/`HTTPListener`, removed legacy OM compatibility shims

### License

Business Source License 1.1 (BSL)

---

## Roadmap

Items considered for future releases:

- **Plugin marketplace** - community plugin registry with install/update from UI
