# #11: Emulated Grenton modules: dimmer, roller, RGB LED

- **Type:** feature
- **Area:** grenton
- **Size:** L (epic)
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** Grenton Object Manager to verify; no physical modules

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Besides digital inputs and outputs, vCLU can show Object Manager a virtual dimmer, roller and RGB
LED module whose objects behave like the real ones.

## Why
- Scenes and scripts in Object Manager can be built and tested without the hardware.
- Devices that are not Grenton (Zigbee, Tasmota, Shelly) can be bridged in Lua into a module that
  Object Manager and existing Grenton logic see as native.

## How emulated modules work today
vCLU can present itself to Object Manager as a CLU with its own (virtual) modules. Today only
`DOUT8T`, `RELAY4` and `DIN8` exist:

- module types: `PredefinedModules` in `pkg/config/modules.go` (hardware type byte, serial prefix,
  I/O types with `LuaType` = the OM object class);
- the wizard adds a module; `pkg/config/om_generator.go` writes `config.txt` and `CONFIG.JSON`, Object
  Manager reads them and generates `om.lua`;
- the Lua class of each object lives in `pkg/lua/runtime/grenton/modules/` (pattern: `DOUT.lua`:
  `FEATURE_*`, `METHOD_*`, `EVENT_*` constants, `new/get/set/execute/add_event`, convenience
  methods, `onChange` returning an unsubscribe function) and is loaded in `bootstrap_go.lua`;
- `utils/TypeMap.lua` maps the class to an `expose()` type.

Not known yet and part of this ticket: the hardware type byte and serial prefix Object Manager
expects for this module, and the exact feature, method and event indices of the object class.
Take them from a real Object Manager project containing the module and from Grenton
documentation, and record them in `pkg/lua/AGENTS.md` (section runtime/grenton) and the docs page.

## Sub-tickets
- #12 dimmer
- #13 roller
- #14 RGB LED

One per module type; each is independent and has its own acceptance criteria. Not in scope:
thermostat (class 18).
