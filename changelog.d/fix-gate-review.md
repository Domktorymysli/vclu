### MCP

- **`vclu_device_execute` calls only the listed methods** - the methods `vclu_devices` and `vclu_device_get` list: those the object declares (a Grenton object's or a GATE Alarm's methods), or, for an object that declares none, its public functions. Raw and internal ones (`get`, `set`, `execute`, `call`, `emit`, `onEvent`, `updateState`, `new`, anything starting with `_`) answer `method not found` and are no longer listed; a value is written with `vclu_device_set`.
