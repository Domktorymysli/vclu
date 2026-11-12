### Lua runtime

- **Gates and intercoms: `pulse` for an exposed switch** -
  `expose(DOMOFON, "switch", { id = "domofon", pulse = 1000 })` makes "on" from Home Assistant,
  HomeKit, the vclu app, voice, MCP bots and API clients one pulse of 1000 ms (100-60000), and
  "off" does nothing. A Grenton DOUT is pulsed by the CLU itself (`SwitchOn` with a time, new
  `RemoteSwitch:pulse(ms)`), so a vCLU restart cannot leave the relay on; other objects are
  switched off by a timer. A pulse asked while one runs is ignored. Through the cloud "open" and
  "close" pulse too ("otwórz bramę"), and the device list says `pulse: true`. MCP's
  `execute_method` is refused on such a device. Lua calling the object itself is not limited.
