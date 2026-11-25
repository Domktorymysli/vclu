### Grenton

- **GATE MODBUS objects as devices** - importing a GATE MODBUS (its om.lua, a diagnostic pack or
  the `.omp` project) registers its Modbus objects (Modbus, ModbusValue, ModbusRTU,
  ModbusClient) as `CLU<serial>.<ID>`, read through the GATE like a GATE Alarm's Satel objects:
  a register read only is a sensor (a read-only number in Home Assistant), a coil, discrete
  input or bit a binary sensor; a writable coil is a switch and a writable holding register a
  number, both only by `expose()`. Not checked on a GATE MODBUS yet. The kept om.lua now keeps
  these objects' settings (no secrets); in a project they are no longer unknown types
  ([#278](https://github.com/Domktorymysli/vclu/issues/278)).
- **Modbus map for the agent** - the import also writes `ai/modbus/CLU<serial>.md`: every
  Modbus object with its vclu kind, serial line, slave, register, data type, byte order,
  divisor and the vclu `Modbus` call to reach it through an RS485-to-TCP gateway instead; the
  import answer gives `modbus` (how many objects).

### Lua runtime

- `RemoteGateModbusSensor`, `RemoteGateModbusBinarySensor`, `RemoteGateModbusSwitch`,
  `RemoteGateModbusNumber` (`getValue`, `setValue`, `switchOn`/`switchOff`/`toggle`,
  `onSwitchOn`/`onSwitchOff`); a guard reads only the value and error features and writes no
  setting.
