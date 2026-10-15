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

## Where the indices are
Object Manager ships one XML per object class in its installation, in
`configuration/com.grenton.om/device-interfaces/` (`module_*` and `clu_*` files): features (index,
get/set, range, description), methods (`execute`/`set`, index, parameters) and events. Check there
before guessing an index or the direction of a scale. The English hints sometimes contradict the
Polish and German ones; trust the feature's description. Do not commit these XML files.

## Verification (every module)
Most of the work is checking the module against Grenton's tools, not the code. Each sub-ticket's PR
reports every point below with the tool versions (Object Manager, mobile app, CLU firmware) and a
screenshot; a point that could not be checked is listed as such.

- [ ] **Discovery:** Object Manager finds vCLU with the module during CLU discovery; the module type,
  serial number and object list match the real module.
- [ ] **Object Manager compatibility:** the project with the module is sent to vCLU and read back
  without errors; every feature can be read, every method called from a script, every event can
  have a handler; nothing changes in the other modules of the project.
- [ ] **Grenton mobile app:** an interface with the module's objects is built in Object Manager, sent
  to the app, and the app shows the state and controls the object in both directions (app → vCLU,
  a change in vCLU → app).
- [ ] **Scales and directions:** every range (0..1, 0..100, 0..255, degrees) and every direction
  (open/closed, up/down) checked against the XML and in Object Manager, with a contract test for
  each conversion.

## Sub-tickets
- #12 dimmer
- #13 roller
- #14 RGB LED

One per module type; each is independent and has its own acceptance criteria. Not in scope:
thermostat (class 18).
