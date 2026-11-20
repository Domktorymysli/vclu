### MCP

- **`vclu_device_execute` calls public methods only** - an object's raw and internal functions (`get`, `set`, `execute`, `call`, `emit`, `onEvent`, `updateState`, `new`, anything starting with `_`) answer `method not found` and are no longer listed by `vclu_devices` and `vclu_device_get`; every other method of the object, its class or a method added to it is called as before. A value is written with `vclu_device_set`.
