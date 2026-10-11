# vCLU

[Polski](README.md) | **English**

**A smart home runtime for AI agents. Automations in code that an agent writes, tests in a sandbox and only then deploys.**

[![Build](https://img.shields.io/github/actions/workflow/status/Domktorymysli/vclu/test.yml?branch=main)](https://github.com/Domktorymysli/vclu/actions)
[![Go Version](https://img.shields.io/badge/go-1.26+-00ADD8.svg)](https://go.dev)
[![codecov](https://codecov.io/gh/Domktorymysli/vclu/graph/badge.svg)](https://codecov.io/gh/Domktorymysli/vclu)
[![License](https://img.shields.io/badge/license-BSL%201.1-blue.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-900+-brightgreen.svg)](#tests-and-contracts)

vCLU is a smart home runtime built for AI agents. It runs on a Raspberry Pi or any small Linux box. Automations are Lua code, and an agent works on them over MCP: it writes a draft, checks it in a sandbox without touching the house, and only then deploys it, with a backup of every change. So you can hand the whole job to agents, because their code passes sandbox tests before it reaches the house.

vCLU comes with basic integrations, and an agent adds new ones as it works, with the same MCP tools. Home Assistant, HomeKit and MQTT are supported, not required.

It is a prototype of an idea: a framework that helps agents program, by giving them contracts, tests and a safe way to deploy.

<!-- TODO: screenshots
![History and audit](docs/images/history.png)
![Editor](docs/images/editor.png)
-->

## What it looks like

A real automation from the author's house: at night, motion in the vestibule turns the light on for a minute. If someone switches the light off by hand, the automation stays quiet for 10 minutes. It knows *who* switched it off, because every change carries its actor.

```lua
-- modules/satel/wiatrolap.lua
local LIGHT = _:get("CLU220000592.DOU4992")   -- Grenton output
local ME = "automation:wiatrolap"

-- Survives Lua reloads and restarts
local state = persist("wiatrolap", { pauseUntil = 0 })

-- Satel motion sensor goes to 1
StateBus:watch("satel.zone.2", function()
    if not datetime.between("21:00", "06:00") or os.time() < state.pauseUntil then return end
    -- Light switched on by someone else: leave it alone
    local last = LIGHT:lastChange()
    if LIGHT:getValue() == 1 and not (last and last.actor == ME) then return end
    LIGHT:switchOn(60 * 1000)      -- the CLU switches it off after 60 s
end, { to = 1, runAs = ME })

-- Switched off by anyone but us (wall switch, web, agent, HA): back off
StateBus:watch("CLU220000592.DOU4992", function(e)
    if e.actor == ME then return end
    state.pauseUntil = os.time() + 600
end, { from = 1, to = 0 })
```

And the test an agent runs before this goes live. It runs on a virtual clock, with the Grenton command recorded instead of sent:

```json
{
  "path": "modules/satel/wiatrolap.lua",
  "at": "2026-10-04T23:00:00+02:00",
  "fixtures": [{ "path": "satel.zone.2", "kind": "satel_zone", "state": 0, "config": { "number": 2 } }],
  "steps": [
    { "set": "satel.zone.2", "value": 1 },
    { "expect_clu_calls": [{ "script": "DOU4992:execute(1, 60000)" }] },
    { "advance": 61 }
  ]
}
```

## Why vCLU

**Code, not configuration.** Automations, scenes, dashboards and virtual devices are Lua files. They can be read, diffed, reviewed, tested and generated, by you or by an agent.

**Safe for AI agents on a live house.** The built-in MCP server does not just expose "turn on the light". Agents write drafts, test them in an isolated sandbox with a virtual clock, see a diff against production, and promote with a backup. After a promote vCLU checks that the modules loaded and reports errors to the agent; the previous version is one click away in the backups. Each bot has its own token and its own view of the house: objects can be `full`, `readonly` or `hidden` per bot.

**It knows who changed what.** Every state change carries `origin` and `actor`: a wall switch (`grenton`), the alarm (`physical`, `device:satel`), a person in the web UI (`user:<login>`), an agent (`agent:<bot>`), Home Assistant (`service:home-assistant`) or your own automation (`automation:<id>`). History is kept on disk and browsable, so "why did the light turn on at 3 a.m.?" has an answer.

**Works with what you have.** Grenton CLUs, Satel ETHM-1, Modbus TCP energy meters, MQTT devices (Zigbee2MQTT, Tasmota, Shelly, ESPHome), Raspberry Pi GPIO, IP cameras. Home Assistant and Apple Home are optional consumers.

**Small.** One Go program plus its web assets. On the author's house (two CLUs, Satel, MQTT broker, history, energy meters) it uses about 75 MB of RAM and under 2% CPU on x86_64. Builds exist for Raspberry Pi from the original Pi 1 (ARMv6) up.

## Features

| Area | What you get |
|---|---|
| **Lua runtime** | gopher-lua with hot reload, one event loop with panic recovery, timers (`after`, `every`, `setTimeout`, `setInterval`), `StateBus:watch` with `from`/`to`/`actor` filters, scenes, `json`/`xml`/`datetime` modules with time zones and `datetime.between`, Logger, KV store |
| **Objects** | Grenton proxies generated from your OM project; virtual switches, dimmers, covers, sensors, numbers and texts with `initial` and `persist`; `persist(name, defaults)` for automation state that survives restarts |
| **History and audit** | Every change with `old`, `value`, `origin`, `actor`, `correlationId`; daily files on disk (90 days / 1 GB by default, written in batches to spare the SD card); restored after a restart without fake changes; `obj:lastChange()`, `obj:since()`, `obj:history(n)` in Lua; Tools → History and audit in the web UI; REST and MCP access |
| **Noise control** | `expose(..., { filter = { delta = 0.2, heartbeat = 600 } })` stops a sensor flapping by 0.1 °C from flooding history, MQTT and HomeKit; `getValue()` stays raw |
| **AI agents (MCP)** | 32 tools, resources and prompts: device control, code search, logs, metrics, history, a sandbox pipeline with scenario tests, diff, promote with backup and a module load check, audit log, incidents, agent memory, MQTT device discovery |
| **Grenton** | Acts as a CLU on the network (encrypted discovery, TFTP, script requests) and as a client of real CLUs (commands, state subscriptions with renewal); imports `om.lua` from Object Manager and generates proxies for outputs, inputs, dimmers, RGB, rollers, analog I/O, 1-Wire and panel sensors |
| **Satel** | ETHM-1 integration: zones, partitions, outputs, ATD-100 temperatures, arming; disarming and force-arming are off unless you allow them; reads names from the panel |
| **MQTT and Home Assistant** | Embedded MQTT broker (mochi-mqtt) and client; Home Assistant discovery with `device_class`/`state_class`, so energy meters work in the HA Energy dashboard; discovery of Zigbee2MQTT, Tasmota, Shelly, ESPHome and HA devices with suggested Lua |
| **HomeKit** | Native bridge: switches, lights, dimmers, covers, motion and binary sensors, temperature, humidity, fans, scenes |
| **Modbus** | Modbus TCP through RS485 gateways (function codes 1–4, 6, 16), asynchronous, one transaction at a time per gateway |
| **More hardware** | Raspberry Pi GPIO inputs and outputs with debounce and press/hold events; HTTP client and HTTP server in Lua; IP camera snapshot proxy (credentials never reach the browser) |
| **Dashboards** | Defined in Lua, served at `/d/<id>` for wall tablets: toggles, sliders, buttons, selects, status tiles with sparklines, charts, images, dimmers, covers, colors; themes, tab groups, dashboards that change with the time of day; public, session or PIN access |
| **Metrics** | Time series from Lua (`metric(...)`) for charts; Prometheus `/metrics` with 30+ series and ready Grafana dashboards in `grafana/` |
| **Plugins** | Sandboxed Lua plugins from a GitHub repository, with their own KV storage, timers, HTTP and MQTT; written as factories so one plugin can drive many devices |
| **Web UI** | Setup wizard, status, Monaco code editor with validation and search, Grenton/OM import, MQTT, Satel, HomeKit, access profiles, MCP bots and drafts, plugins, backups with schedule, history and audit, live log stream, statistics, REST API docs (ReDoc); English and Polish |
| **Security** | bcrypt passwords, 12 h sessions with SameSite=Strict cookies and CSRF tokens, API keys and bot tokens stored as SHA-256 hashes, per-object access profiles for MQTT, HomeKit and MCP |

## Quick start

### Pre-built release

Archives for Linux (amd64, arm64, armv7, armv6) and macOS (arm64) contain the binary, its web assets and an installer for systemd:

```bash
mkdir vclu && tar -xzf vclu-linux-arm64.tar.gz -C vclu
cd vclu && sudo ./install.sh
```

### Docker

```bash
docker run -d --name vclu \
  --network host \
  -v vclu-data:/data \
  ghcr.io/domktorymysli/vclu:latest
```

`--network host` is needed for UDP broadcast (Grenton discovery) and mDNS (HomeKit).

### From source

```bash
git clone https://github.com/Domktorymysli/vclu.git
cd vclu
go build -o vclu ./cmd/vclu
./vclu --auto --data-dir ./data
```

Open `http://localhost:8080`. The setup wizard asks for the device role, Grenton keys and an admin password, and can import your Object Manager project.

Useful flags: `--auto` (pick the network interface), `--interface`, `--ip`, `--data-dir`, `--web-port`, `--list` (show interfaces), `--verbose`.

## Writing automations

Automations live in `modules/` and are loaded from `modules/init.lua`:

```lua
-- modules/init.lua
datetime.setTimezone("Europe/Warsaw")
require("satel/wiatrolap")
require("tasmota_desk")
require("dashboards/home")
```

A Tasmota plug as a switch, visible in the web UI, Home Assistant and Apple Home:

```lua
-- modules/tasmota_desk.lua  (MQTT = the default client, Integrations → MQTT → Client)
local lamp = VirtualSwitch:new("Desk lamp")

MQTT:subscribe("stat/tasmota_desk/POWER", function(topic, payload)
    lamp:updateState(payload == "ON" and 1 or 0)
end)
lamp:onSwitchOn(function()  MQTT:publish("cmnd/tasmota_desk/Power", "ON")  end)
lamp:onSwitchOff(function() MQTT:publish("cmnd/tasmota_desk/Power", "OFF") end)

expose(lamp, "switch", { name = "Desk lamp", path = "tasmota:desk", area = "Office" })
```

Grenton objects from your OM project, with a noise filter on 1-Wire thermometers:

```lua
local TEMP = { delta = 0.2, heartbeat = 600 }
expose(_:get("CLU220000592.ONE6305"), "temperature", { name = "Temperature", area = "Living room", filter = TEMP })
expose(_:get("CLU220000592.DOU0789"), "light", { name = "Sofa", area = "Living room" })
```

A setting that survives restarts:

```lua
local nightMode = VirtualSwitch:new("night_mode", { initial = 0, persist = true })
expose(nightMode, "switch", { name = "Night mode" })
```

A wall-tablet dashboard:

```lua
local r = _registry
Dashboard.new("home")
  :name("Home"):icon("house-door"):auth("pin"):pin("1234")
  :section("Living room", "lamp")
    :toggle("sofa", "Sofa", "lamp",
      function() return r:get("CLU220000592.DOU0789"):getValue() end,
      function(v) local o = r:get("CLU220000592.DOU0789"); if v then o:switchOn() else o:switchOff() end end)
    :status("temp", "Temperature", "thermometer-half",
      function() return r:get("CLU220000592.ONE6305"):getValue() end, "°C")
  :build()
```

More patterns: [`docs/ai/examples.md`](docs/ai/examples.md). Full API: [`docs/ai/lua-api-reference.md`](docs/ai/lua-api-reference.md).

## AI agents

Create a bot in the web UI (MCP page) and connect any MCP client, such as Claude Code, Codex CLI, Gemini CLI and others:

```json
{
  "mcpServers": {
    "vclu": {
      "type": "http",
      "url": "http://YOUR_VCLU_IP:8080/mcp",
      "headers": { "Authorization": "Bearer YOUR_BOT_TOKEN" }
    }
  }
}
```

What a bot can do depends on its settings:

| Level | Tools |
|---|---|
| **Control** (every bot) | list devices, read and set state, call methods, run scenes, read change history (limited to objects its access profile allows) |
| **Dev mode** | read and search files, logs, configuration (no secrets), registry, metrics, dashboards, plugin config, runtime schema, reload; drafts: write, validate, test with scenarios, diff, promote, delete; audit log; MQTT discovery; agent memory |
| **Exec mode** | run a Lua snippet on the live runtime (max 8 KB) |

Dev mode also has a global kill switch in the web UI. A typical change by an agent:

1. Read the house description, the device registry and the history of the objects involved.
2. Write a draft in `modules/`.
3. Validate it and test it in the sandbox: virtual clock, recorded Grenton commands, fixtures for Satel zones, steps like `set`, `advance`, `reload`, `expect_state`, `expect_clu_calls`, `expect_history`.
4. Show the diff, promote with a backup, check that the modules loaded, watch the logs. The previous version can be restored from its backup in the editor.
5. Every step lands in the audit log, and every change the automation makes later carries its actor.

## History and audit

- **Who changed it:** each change records `origin` and `actor`. Entry points set the actor themselves: web users, API keys, bots, HomeKit, MQTT and Home Assistant, Grenton reports, Satel, modules and plugins. `switchOn(ms)` on a Grenton output even attributes the CLU's own switch-off at the end to the command that started it.
- **In Lua:** `obj:lastChange()`, `obj:since()`, `obj:history(n)`, `StateBus:watch(path, fn, { actor = ... })`.
- **On disk:** one JSONL file per day, written every 5 minutes and at shutdown, 0600 permissions, a torn last line after a power cut is skipped. Retention and size limits are set in the UI.
- **In the web UI:** filter by object, origin, actor, text or time; see memory and disk use and write errors; change limits live.
- **API:** `GET /api/history` with filters and cursor paging, `vclu_device_history` for bots (objects hidden from the bot are left out).

## Architecture

```
cmd/vclu/            entry point and flags
pkg/
  lua/               Lua VM, Go bindings (HTTP, MQTT, Modbus, GPIO, Satel, history)
    runtime/         Lua runtime: core, Grenton objects, virtual objects, integrations
  server/            Grenton UDP protocol: discovery, commands, CLU subscriptions
  cipher/ tftp/      Grenton encryption and TFTP
  omparser/          Object Manager project parser and proxy generator
  satel/             Satel ETHM-1 client
  history/           state change history: memory rings, daily files, search
  mcp/               MCP server, sandbox, scenarios, audit, incidents
  workspace/         drafts, backups, promote and rollback
  web/               web UI and REST API (Gin)
  broker/            embedded MQTT broker
  homekit/           HomeKit bridge
  accesscontrol/     access profiles per object and integration
  metrics/ stats/    time series and Prometheus
  plugins/           plugin repository, loader, KV store
  camera/            camera snapshot proxy
web/                 templates, JS, CSS, vendored libraries (works offline), en/pl locales
deploy/              systemd unit and installer
grafana/             Grafana dashboards for the Prometheus metrics
```

Go handles the network, encryption, concurrency and files. Lua handles the house: objects, automations, scenes, dashboards, everything you or an agent edit. All Lua runs on one event loop, so automations never race each other.

## Tests and contracts

```bash
go test ./...          # 900+ tests, no hardware or network needed
go test -race ./...    # CI runs this on every push
```

The behaviour that automations, plugins and integrations depend on is pinned by contract tests: one change gives exactly one event, who-changed-it, access control, reload safety, persistence, history, the sandbox and the Grenton protocol. See [CONTRIBUTING.md](CONTRIBUTING.md#contracts) for the list and the rules.

## Deploy

| | |
|---|---|
| Platforms | Linux amd64, arm64, armv7, armv6 (Raspberry Pi 1 and up); macOS arm64 |
| Ports | web 8080, Grenton UDP 1234, TFTP 69, MQTT 1883, HomeKit 5123 |
| Data | one data directory: `.vclu.json`, modules, KV, history, backups |
| Service | `deploy/vclu.service` (systemd) and `deploy/install.sh` |

Everything is configured in the web UI. Settings live in `.vclu.json` in the data directory.

## Contributing

Contributions are welcome, including ones written with AI agents. That is what the contract tests are for. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Demo

[![vCLU intro](https://img.youtube.com/vi/POgNGuUyhSU/maxresdefault.jpg)](https://www.youtube.com/watch?v=POgNGuUyhSU)

Introduction to vCLU: installation on a Raspberry Pi, GPIO, MQTT, Home Assistant integration.

## Links

- [vclu.pl](https://vclu.pl/): project website
- [docs.vclu.pl](https://docs.vclu.pl): documentation
- [domktorymysli.org](https://domktorymysli.org/): the author's blog, Dom który myśli (Polish)
- [Facebook: Dom który myśli](https://www.facebook.com/domktorymysli)
- [Discord](https://discord.gg/9cgrnXkVP): vCLU community: questions and help (mostly Polish)

## Documentation

- [docs.vclu.pl](https://docs.vclu.pl)
- [Lua API reference](docs/ai/lua-api-reference.md) and [code patterns](docs/ai/examples.md) (also served to agents over MCP)

## License

[Business Source License 1.1](LICENSE): free for personal and commercial smart home installations; you may not build a competing controller product from it. Each release converts to Apache 2.0 after 4 years.
