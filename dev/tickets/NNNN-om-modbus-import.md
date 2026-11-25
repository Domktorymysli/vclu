# #NNNN: OM import maps the Modbus objects of a GATE MODBUS

- **Type:** feature
- **Area:** grenton | modbus | mcp
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a GATE MODBUS helps for checking, not required)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
After importing a GATE MODBUS (its om.lua, a diagnostic pack or the `.omp` project), the agent reads every
Modbus object with its register map (line, slave, register, function, type, scaling) in
`ai/modbus/CLU<serial>.md` and can move the device to vclu's `Modbus` without Object Manager.

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
- vclu's `Modbus` (`pkg/lua/runtime/integrations/modbus/Modbus.lua`) is a TCP client: a device
  on the GATE's RS485 reaches vclu through an RS485-to-TCP gateway whose address is not in the
  project. So the import writes a map for the agent, not objects or a module.

## Behaviour
- `omparser.ReadModbus` reads the raw om.lua, before `StripGateSettings`; only on a GATE MODBUS
  (the same classes elsewhere are something else). Objects commented out (`-- REMOVED`) are
  skipped.
- The stored `om_<serial>.lua` and the registry are unchanged: the settings stay stripped and
  the objects are not registered (no HA, HomeKit, MQTT).
- `ai/modbus/CLU<serial>.md`: one row per object of class 200, 201, 203, 205 with name, ID,
  class, line (`19200 8E1`, or `TCP host:port`), slave, register (and count), the `Modbus` read
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
- `pkg/omparser/modbus.go`, `pkg/omparser/types.go`
- `pkg/omproject/project.go` (`peripheralClasses`, features), `pkg/omproject/omlua.go`
- `pkg/web/handle_web_om.go`, `pkg/mcp/server.go`, `web/api/swagger.json`
- `pkg/omparser/testdata/om-modbus-gate.lua`: an anonymised om.lua of a real GATE MODBUS
  (11 objects of class 200, 5 of class 203, 2 removed)

## Acceptance criteria
- [x] `TestReadModbusGate`: 16 objects from the real file, slave, register, function and line
      right for one object of each class, removed ones left out.
- [x] `TestReadModbusOnlyOnGateModbus`: an HTTP GATE and a CLU give no map.
- [x] `TestOMUploadModbusMap`: om.lua and diagnostic pack give the same map; no Modbus object
      in the registry; the kept copy has no settings.
- [x] `TestOMModbusMapLifecycle`: project-style om.lua keeps the map, om.lua without Modbus and
      deleting the CLU remove it.
- [x] `TestOMModbusMapNotFromGateAlarm`: a GATE Alarm's om.lua writes nothing to `ai/modbus/`.
- [x] `TestReadModbusV1AndCells`, `TestOMModbusMapWithoutSettings`: Modbus v1 without parity
      and stop bits, `|` in names, an om.lua without settings and no map before it.
- [x] `TestOMLuaModbusObjects`, `TestOMProjectModbusMap`: a project gives the same map, its
      Modbus objects are no unknown types, a value with quotes or a line end does not reach
      om.lua, a project without the objects removes the map.
- [x] `go test ./...` and `go test -race ./...` pass.
- [x] Changelog entry `changelog.d/NNNN-om-modbus-import.md`.

## Contract impact
OM import: adds `ai/modbus/` and `modbus` in the answer, removes nothing. No decision record.

## Out of scope
- Modbus RTU in vclu (RS485 on the board) and generating a module from the map.
