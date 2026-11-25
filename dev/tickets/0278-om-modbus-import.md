# #278: A GATE MODBUS's Modbus objects as vclu devices, and their map for the agent

- **Type:** feature
- **Area:** grenton | modbus | lua-runtime | home-assistant | mcp
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a GATE MODBUS to check the feature numbers (not checked yet, like #214)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
After importing a GATE MODBUS (its om.lua, a diagnostic pack or the `.omp` project), its Modbus
objects are vclu devices read through the GATE (sensors, binary sensors, switches, numbers), as a
GATE Alarm's Satel objects are, and the agent reads their register map in
`ai/modbus/CLU<serial>.md`.

## Context
- A GATE MODBUS is `GATE:new(5000, ...)` with FwType 00000001, HwType 00000012. Its objects are
  GATE objects: `MOD1234 = GATE:new(203, "MOD1234")`, set up in `OnInit` with
  `MOD1234:set(index, value)`. Object Manager's definitions (`device-interfaces/object_modbus_*.xml`):

  | class | OM name | what it is |
  |---|---|---|
  | 200 | Modbus (v1, v2) | a register over the GATE's RS485 |
  | 201 | ModbusValue | a register over RS485 |
  | 202 | ModbusSlaveConfigRTU | the GATE as a slave |
  | 203 | ModbusRTU | registers over RS485 |
  | 204 | ModbusSlaveRTU | a register the GATE serves |
  | 205 | ModbusClient | registers over Modbus TCP (socket address) |
  | 206 | ModbusServer | a register the GATE serves over TCP |

  The classes number their features differently (e.g. slave address 0 on 200, 5 on 203, 2 on
  205) and name byte orders differently; the index tables are in `pkg/omparser/modbus.go`.
- Before this ticket all of it was lost: `StripGateSettings` removes every `:set` of a GATE's
  objects from the copy vclu keeps (the GATE Alarm's passwords are there), and the parser drops
  GATE classes other than the GATE Alarm's.
- The GATE polls the devices; each object has its value as a feature and OnChange events, so
  vclu reads it like any CLU object. vclu's own `Modbus` (a TCP client) stays the way without
  the GATE, through an RS485-to-TCP gateway; the map gives both.
- Precedent: GATE Alarm (`dev/tickets/0214-gate-alarm.md`, `RemoteGateAlarm.lua`).

## Behaviour
- Only on a GATE MODBUS (the same classes elsewhere are left out with a warning). Objects
  commented out (`-- REMOVED`) are skipped.
- `StripGateSettings` keeps these objects' settings (register, line, scaling: no secrets) in the
  stored `om_<serial>.lua`; everything else it strips stays stripped (GATE Alarm, Wi-Fi).
- Registered as `CLU<serial>.<ID>`, class 200, 201, 203, 205 (202, 204, 206 are the GATE as a
  slave or server: not registered), by a kind from the settings (register type: 0 coils,
  1 discrete inputs, 2 holding, 3 input; Modbus v1/v2 writes only with AccessRights 1; a bit
  value is binary):

  | kind | Lua class | expose |
  |---|---|---|
  | holding without write, input register | `RemoteGateModbusSensor` | read-only number (HA sensor), by `exposeRegistry()` |
  | coil or bit without write, discrete input | `RemoteGateModbusBinarySensor` | like a DIN, by `exposeRegistry()` |
  | coil or bit with write | `RemoteGateModbusSwitch` | switch, only by `expose()` |
  | holding with write | `RemoteGateModbusNumber` | number, only by `expose()` |

- Feature numbers (`object_modbus_*.xml`): value 14 on 200 (written by `set(14, v)`, SetValue),
  20 on 201, 22 on 203, 19 on 205 (written by `execute(1, "v")`, WriteValue with a string);
  ErrorCode 13 / 23 / 25 / 22, IsValueValid 22 / 24 / 21. The Sync list and `Wire.value` follow
  the value feature (`_valueIndex`). The guard (as RemoteGateAlarm's) reads only these features
  (a CLU resets on a feature it has not), writes no setting, and writes the value only of a
  switch or number: a finite number (`set`), a string of one (`WriteValue`), 0 or 1 on a switch.
  A switch's `setValue` takes 1/true/"1"/"on" and 0/false/"0"/"off".
- An object without any setting (a copy an older vclu stripped, an om.lua without OnInit) is a
  read-only sensor: nothing is taken for writable from defaults.
- The map is written from the kept copy, so a setting stripped there (an ID also declared as
  another GATE object, e.g. an alarm object) never reaches it; such an ID is no Modbus object.
- Not surfaced yet: IsValueValid and ErrorCode (readable by `get`, no events or state).
- Unverified on hardware: everything above, in particular a coil or holding register with
  InputOutputCount above 1 (only the first value) and a bit of a holding register as a switch
  (WriteValue writes the whole value).
- `ai/modbus/CLU<serial>.md`: one row per object of class 200, 201, 203, 205 with name, ID,
  class, vclu kind, line (`19200 8E1`, or `TCP host:port`), slave, register (and count), the `Modbus` read
  and write calls (none for read-only objects, discrete and input registers; coils cannot be
  written, vclu has no FC05), data type and width, byte order by OM's names, divisor, refresh
  and timeout. 202, 204, 206 are named only.
- A `.omp` project: the Modbus types (`Modbus`, `ModbusValue`, `ModbusRTU`, `ModbusClient`, and
  the slave/server ones) are known peripherals, like the GATE Alarm's; their features
  (`EmbeddedFeature` index and `initValue` in `system.xml`) go to the om.lua vclu writes from
  the project as `:set` lines, numbers or plain strings only, so the same map comes out.
- An om.lua with Modbus objects but no settings keeps the map an earlier import wrote; one
  without Modbus objects, and deleting the CLU, remove it.
- The import answer per CLU has `modbus` (how many objects); MCP's instructions point to
  `ai/modbus/`.

## Files
- `pkg/omparser/modbus.go`, `types.go`, `parser.go`, `gate_settings.go`, `generator.go`
- `pkg/lua/runtime/objects/RemoteGateModbus.lua`, `objects/init.lua`, `utils/TypeMap.lua`,
  `pkg/lua/AGENTS.md`, `docs/ai/lua-api-reference.md` (regenerated)
- `pkg/omproject/project.go` (`peripheralClasses`, features), `omlua.go`, `mygrenton.go`
- `pkg/web/handle_web_om.go`, `pkg/mcp/server.go`, `web/api/swagger.json`
- `pkg/omparser/testdata/om-modbus-gate.lua`: an anonymised om.lua of a real GATE MODBUS
  (11 objects of class 200, 5 of class 203, 2 removed)

## Acceptance criteria
- [x] `TestReadModbusGate`: 16 objects from the real file, slave, register, function and line
      right for one object of each class, removed ones left out.
- [x] `TestReadModbusOnlyOnGateModbus`: an HTTP GATE and a CLU give no map.
- [x] `TestOMUploadModbusMap`, `TestOMUploadModbusRuns`: om.lua and diagnostic pack give the
      same map and register the objects; the proxy runs; the kept copy keeps the Modbus
      settings and not the timer's or the Wi-Fi's.
- [x] `TestParseGateModbusKinds`, `TestParseModbusClassesOnOtherGate`,
      `TestGenerateProxyGateModbus`, `TestStripGateSettingsKeepsModbus`: kinds from the real
      file and from coils, discrete inputs and bits; nothing on another GATE; the proxy lines;
      a GATE Alarm still loses the same classes' settings.
- [x] `pkg/lua/gate_modbus_test.go`: subscribed feature per class, values (ModbusValue's
      string too) and events, writes as `set(14, v)` / `execute(1, "v")`, guard refusals (other
      features, settings, read-only registers, Wire, a changed ID), exposure.
- [x] `TestGateModbusWireValue`, `TestGateModbusWriteValues`: Wire reads the value feature from
      the Sync list; NaN, ±inf, tables, extra arguments and switch values other than 0/1 are
      refused through set, execute and Wire.
- [x] `TestModbusKindWithoutSettings`, `TestOMModbusMapNoSecretOfOtherClass`: no settings give
      sensors; an ID declared as an alarm object and a Modbus object leaks nothing.
- [x] `TestOMModbusMapLifecycle`: project-style om.lua keeps the map, om.lua without Modbus and
      deleting the CLU remove it.
- [x] `TestOMModbusMapNotFromGateAlarm`: a GATE Alarm's om.lua writes nothing to `ai/modbus/`.
- [x] `TestReadModbusV1AndCells`, `TestOMModbusMapWithoutSettings`: Modbus v1 without parity
      and stop bits, `|` in names, an om.lua without settings and no map before it.
- [x] `TestOMLuaModbusObjects`, `TestOMProjectModbusMap`: a project gives the same map, its
      Modbus objects are no unknown types, a value with quotes or a line end does not reach
      om.lua, a project without the objects removes the map.
- [x] `go test ./...` and `go test -race ./...` pass.
- [x] Changelog entry `changelog.d/0278-om-modbus-import.md`.

## Contract impact
OM import: a GATE MODBUS's objects are registered (new registry paths and Lua classes), adds
`ai/modbus/` and `modbus` in the answer; removes nothing. No decision record.

## Out of scope
- Units and device classes (Object Manager has none): a module sets them by `expose()`.
- Modbus RTU in vclu itself (RS485 on the board).
