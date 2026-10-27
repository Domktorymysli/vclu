# Connecting another system to vCLU

This page is for someone writing an integration for another system: a dashboard, another hub, a
script that reacts to the house. It says which interface to use, how to read states and send
commands, and how an access profile limits what the integration sees.

vCLU offers an integration only what is **exposed**: the objects of the Grenton import, which vCLU
exposes by itself at startup, and the objects a module passes to `expose()` (see
[`docs/ai/examples.md`](ai/examples.md)). An object that is not exposed is not on MQTT under
`vclu/exposed/...` and is not visible over MCP.

## Which interface

| You are writing | Use | Why |
|---|---|---|
| A program: a dashboard, another hub, Node-RED, a bridge | **MQTT** | vCLU pushes every state change the moment it happens (retained, so a new client gets the current state at once), and takes commands on a topic per object. No polling. |
| An AI agent, or something that writes and tests automations | **MCP** | Tools for listing and controlling devices, plus, in Dev Mode, reading code, a sandbox, and promoting modules. Built for agents, request/response. |

Do not poll MCP for states: `vclu_devices` returns a snapshot, and every call goes through the bot's
token check and the activity log. A program that needs the current state of the house subscribes to
MQTT.

The REST API under `/api/` is the web interface's own API. It has no endpoint for reading or setting
an exposed object under an access profile. The HTTP routes that can change a device are tools of the
web interface, not an integration API: dashboard actions (`POST /api/dashboard/:id/action`) operate
the widgets of one dashboard, `POST /api/clu/execute` sends a Lua script to a CLU over the Grenton
protocol, and `POST /api/editor/run` runs Lua in vCLU, both with administrator rights. None of them
applies an access profile.

## MQTT

### Setup

vCLU publishes and listens through its MQTT client, which connects either to the embedded broker or
to your own (web interface: Integrations → MQTT). The topics below exist when the **Home Assistant
integration** of that client is on (`haIntegration`, on by default); despite the name, they are plain
MQTT topics that any client can use. With the embedded broker enabled and the client not configured,
vCLU connects to the embedded broker on `localhost` and turns them on by itself.

The embedded broker listens on port 1883 and has one optional user name and password, with read and
write access to every topic; set them if the broker is reachable from your network. Anyone who can
log in to the broker can publish commands; what vCLU accepts is decided by the access profile (see
[Access](#access)), not by the broker. vCLU publishes with QoS 0; the `homeassistant/` prefix of
the discovery topics is fixed.

### Topics

| What | Topic | Payload | Retained |
|---|---|---|---|
| Availability | `vclu/status` | `online` / `offline` | yes |
| State | `vclu/exposed/<base>/<component>/<id>/state` | depends on the type, see below | yes |
| Brightness (dimmer, light with brightness) | `vclu/exposed/<base>/light/<id>/brightness` | `0`..`100` | yes |
| Position (cover with a position) | `vclu/exposed/<base>/cover/<id>/position` | `0`..`100` | yes |
| Command | `vclu/exposed/<base>/<component>/<id>/set` | depends on the type | no |
| Brightness command | `vclu/exposed/<base>/light/<id>/brightness/set` | `0`..`100` | no |
| Position command | `vclu/exposed/<base>/cover/<id>/position/set` | `0`..`100` | no |
| Discovery | `homeassistant/<component>/<unique_id>/config` | JSON | yes |

`vclu/status` is set to `online` when vCLU connects. Its last will sets `offline` when the
connection is lost without a clean disconnect (a crash, the network); the broker notices after the
keep-alive timeout, up to about 90 s. The embedded broker publishes `offline` when it stops. A normal
shutdown against your own broker leaves `online`.

How `<base>`, `<id>` and `<component>` come from the object:

- The object's path is split at the first `:`: `demo:kitchen` gives base `demo`, id `kitchen`. A
  path without `:` (a Grenton object from the Object Manager import, such as `CLU220000592.DOU0789`)
  gets base `vclu` and the whole path as id. Both are lowercased and every character that is not a
  letter or digit becomes `_`: `vclu/exposed/vclu/switch/clu220000592_dou0789/state`.
- `<component>` is the Home Assistant component of the expose type: `switch` → `switch`,
  `light` and `dimmer` → `light`, `cover` → `cover`, `sensor` and `motion` → `binary_sensor`,
  `temperature`, `humidity`, `illuminance` and `text` → `sensor`, `number` → `number`,
  `scene` → `switch`. The state and command topics keep this component even when the object is
  read-only.
- `<unique_id>` is `<base>_<id>` in the same form (`demo_kitchen`).

You do not have to build topic names yourself. The discovery message of each object, retained under
`homeassistant/+/+/config`, carries its name, `state_topic`, `command_topic` (only when it accepts
commands), the brightness and position topics, and the room (`device.suggested_area`). Subscribe to
`homeassistant/+/+/config` to get the list of objects, then to the topics it names. On a shared
broker the same topics carry other publishers (Zigbee2MQTT, Tasmota...): vCLU's messages have
`"availability_topic":"vclu/status"`. An empty retained payload means the object is no longer
announced under that component: it was removed, hidden, or announced again under another component
(a switch made read-only moves to `binary_sensor`). Key your list by `unique_id`.

Do not publish commands with the retain flag: vCLU subscribes to the command topics again on every
reconnect and would receive a retained command again.

### Payloads

**Switch** (`switch`): state `ON` / `OFF`; command `ON` / `OFF` on `.../set`.

**Dimmer or light with brightness** (`dimmer`, or `light` whose object has a brightness):

- state `ON` (value above 0) / `OFF`, and the brightness on its own topic as `0`..`100`, the
  object's value (`0`..`1`) times 100, rounded;
- command `ON` / `OFF` on `.../set`, and `0`..`100` on `.../brightness/set` (clamped to that range).
  The scale is 0..100, not 0..255 and not the object's 0..1;
- an `ON` that arrives within 2 seconds after a brightness command is ignored, so the light keeps
  the brightness it was just given (Home Assistant sends one after the other).

**On/off light** (`light` whose object has no brightness, e.g. a digital output): state `ON` /
`OFF`, command `ON` / `OFF`; a brightness command is refused and no brightness topic is published.

**Colour light** (a Grenton RGB light): one JSON on the state topic, `{"state":"ON","brightness":60,"color_mode":"rgb","color":{"r":255,"g":120,"b":0}}`
(`rgbw` with `"w"` on an RGBW module; no `color_mode` before the first colour report), and one JSON
command on `.../set` with any of `state`, `brightness` (0..100) and `color` (`r`, `g`, `b`, `w`
0..255, or `h` 0..360 and `s` 0..100). Its discovery says `"schema":"json"`.

**Cover** (`cover`):

- position `0`..`100` on `.../position`, where **0 = closed and 100 = open**, the same scale as in
  vCLU, Grenton and Home Assistant;
- state `open` (position above 0), `closed`, `opening`, `closing`, or `stopped` (a roller without a
  position that is not moving);
- command `OPEN`, `CLOSE` or `STOP` on `.../set`, and `0`..`100` on `.../position/set` (clamped and
  rounded);
- a roller without a position (Grenton `ROLLER_SH`, or a module whose firmware has none) has no
  position topic, and a position command is refused.

**Others:** a scene (`scene`) runs on `ON` sent to its `.../set`; a `number` takes a number;
`sensor` and `motion` publish `ON` / `OFF`; `temperature`, `humidity`, `illuminance`, `number` and
`text` publish the value as text. Other types (`lock`, `fan`, `climate`, `garage_door`, `button`)
follow their discovery message; an unknown type is announced as `sensor`.

### Example

Against the demo house (`tools/demo/run.sh`) with its MQTT client pointed at a local broker. Replace
the topics with those from your discovery messages.

```sh
# everything vCLU publishes: availability, discovery, states
mosquitto_sub -h localhost -v -t 'vclu/#' -t 'homeassistant/#'
```

```text
vclu/status online
homeassistant/switch/demo_kitchen/config {"name":"Kitchen","unique_id":"demo_kitchen",...,"state_topic":"vclu/exposed/demo/switch/kitchen/state","availability_topic":"vclu/status",...,"command_topic":"vclu/exposed/demo/switch/kitchen/set"}
vclu/exposed/demo/switch/kitchen/state OFF
vclu/exposed/demo/light/living_lamp/state ON
vclu/exposed/demo/light/living_lamp/brightness 60
vclu/exposed/demo/cover/blinds/state open
vclu/exposed/demo/cover/blinds/position 40
```

```sh
mosquitto_pub -h localhost -t vclu/exposed/demo/switch/kitchen/set -m ON
mosquitto_pub -h localhost -t vclu/exposed/demo/light/living_lamp/brightness/set -m 30
mosquitto_pub -h localhost -t vclu/exposed/demo/cover/blinds/position/set -m 75
mosquitto_pub -h localhost -t vclu/exposed/demo/cover/blinds/set -m CLOSE
```

```text
vclu/exposed/demo/switch/kitchen/state ON
vclu/exposed/demo/light/living_lamp/state ON
vclu/exposed/demo/light/living_lamp/brightness 30
vclu/exposed/demo/cover/blinds/state open
vclu/exposed/demo/cover/blinds/position 75
vclu/exposed/demo/cover/blinds/state closed
vclu/exposed/demo/cover/blinds/position 0
```

A command changes the object; the new state comes back on the state topic like any other change.
History records these changes with origin `mqtt` and actor `service:home-assistant`. A command that
is refused or not understood gets no answer on MQTT; nothing changes, and vCLU logs refusals.

## Access

What an integration may see and do is set in the web interface on the access page, as **profiles**.
A profile has a default level and per-object levels, each one of:

| Level | Meaning |
|---|---|
| `full` | visible and controllable (the default) |
| `readonly` | visible, commands refused |
| `hidden` | not visible, commands refused |

Profiles are assigned to MQTT and, separately, to each MCP bot. When several profiles apply, the
most permissive level wins. A fresh install assigns the built-in `full-access` profile everywhere,
and a bot always has at least one profile: one created without a choice gets `full-access`, and so
does a bot whose last profile is deleted.

Remote access (from outside the local network) has its own profiles and starts closed: a fresh
install, and an access file without a `remote` assignment, give it the built-in `no-access` profile
(default `hidden`), never `full-access`. It sees only exposed objects that a profile opens;
`readonly` ones, and ones exposed with `readonly = true`, are shown but refuse commands. Commands
are checked in vCLU and recorded with origin `remote` and the sender as `user:<login>`.

In a module, `expose(obj, type, { readonly = true })` makes an object read-only over MQTT and MCP
whatever the profile says, and `{ mqtt = false }` keeps it out of discovery and state publishing.

### Over MQTT

One profile setting applies to everything that talks to vCLU over MQTT; the broker cannot tell
integrations apart. Hiding or restricting an object applies at once. A Grenton object that was
hidden when vCLU started becomes visible again only after a Lua reload or a restart.

- **readonly**: states are published; the discovery message has no `command_topic`, and a
  controllable type is announced as a read-only component (`switch`, `light`, `dimmer`, `fan` as
  `binary_sensor`, `number` as `sensor`). A command sent to the `.../set` topic anyway is refused.
- **hidden**: the discovery message is removed (empty retained payload on every component), new
  states are not published, and commands are refused.

### Over MCP

Each bot has its own list of profiles, chosen when the bot is created and editable later. MCP only
lists exposed objects.

- **readonly**: the object is listed with `"readonly": true`; `vclu_device_set` and
  `vclu_device_execute` return an error.
- **hidden**: the object is not in `vclu_devices` or `vclu_scenes`; `vclu_device_get` answers
  `device not found or hidden`, and the tools that change something return the same error as for a
  read-only object.
- Running a scene with `vclu_scene_execute` needs `full`.

## MCP

- **Endpoint:** `POST http://<vclu-address>:8080/mcp`, MCP over Streamable HTTP, stateless. MCP has to be
  enabled in the web interface; a disabled server answers `503`.
- **Token:** create a bot on the MCP page of the web interface. Its token (`vclu_mcp_...`) is shown
  once; vCLU keeps only its SHA-256 hash. Send it as `Authorization: Bearer <token>`; without a valid
  token the answer is `401`.

```json
{
  "mcpServers": {
    "vclu": {
      "type": "http",
      "url": "http://VCLU_ADDRESS:8080/mcp",
      "headers": { "Authorization": "Bearer BOT_TOKEN" }
    }
  }
}
```

Device and scene tools (every bot has them, within its profiles):

| Tool | Arguments | Returns |
|---|---|---|
| `vclu_devices` | `type` (optional filter: `switch`, `dimmer`, `cover`...) | `{"devices":[{"path","type","name","state","readonly","methods","lastChanged","area",...}]}` |
| `vclu_device_set` | `path`, `value` | `{"ok":true,"new_state":...}` |
| `vclu_device_execute` | `path`, `method`, `args` (optional array) | `{"ok":true,"result":...}` |
| `vclu_scenes` | none | `{"scenes":[{"path","name"}]}` |
| `vclu_scene_execute` | `path` | `{"ok":true}` |

`vclu_device_get` (`path`) returns one device with its events and capabilities. A tool error comes
back as a result with `isError: true` and a message. When nothing is visible, the lists come back
as `null` (`{"devices":null}`, `{"scenes":null}`), not as `[]`.

Values follow the object: `1`/`0` for a switch, `0`..`1` for a dimmer. `vclu_device_set` writes the
object's value directly; for a cover use `vclu_device_execute` with `setPosition` (0 = closed,
100 = open), `open`, `close` or `stop`, and for a dimmer `setBrightness` takes 0..100. The `methods`
field of `vclu_devices` lists what an object has.

The behaviour of these tools is pinned down by the contract test
[`pkg/mcp/tools_contract_test.go`](../pkg/mcp/tools_contract_test.go).

Dev Mode tools (code, logs, sandbox, promoting modules) are listed in the README and described to
the agent by the server itself.
