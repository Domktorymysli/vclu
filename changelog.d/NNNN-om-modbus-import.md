### Grenton

- **Modbus map from a GATE MODBUS** - importing a GATE MODBUS's om.lua, alone or in a diagnostic
  pack, writes `ai/modbus/CLU<serial>.md`: every Modbus object (Modbus, ModbusValue, ModbusRTU,
  ModbusClient) with its serial line, slave, register, data type, byte order, divisor and the
  vclu `Modbus` call to read and write it, so the agent can move the device to vclu through an
  RS485-to-TCP gateway. The objects are not registered and the kept om.lua still has none of
  the GATE's settings; the import answer gives `modbus` (how many objects)
  ([#NNNN](https://github.com/Domktorymysli/vclu/issues/NNNN)).
