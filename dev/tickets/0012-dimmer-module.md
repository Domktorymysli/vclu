# #12: Emulated Grenton dimmer module

- **Type:** feature
- **Area:** grenton
- **Size:** M
- **Epic:** #11
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** Grenton Object Manager to verify; no physical module

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Object Manager can add a virtual dimmer module to vCLU, and its dimmer objects (class DIMMER, 5) behave like real ones: value 0..1, ramp, switch on/off with time.

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

## Starting point
Unverified method numbers used by `RemoteLight` (`pkg/lua/runtime/objects/RemoteLight.lua`): `execute(0, value, ramp)` set value, `1` switch, `2` switch on, `3` switch off, each with time and ramp.

## Files
- `pkg/config/modules.go` (module type), `pkg/config/om_generator.go` if the module needs more than DOUT
- `pkg/lua/runtime/grenton/modules/DIMMER.lua` (new), `pkg/lua/runtime/bootstrap_go.lua`, `pkg/lua/runtime/utils/TypeMap.lua`
- tests in `pkg/config` and `pkg/lua`; a docs page for the module (docs.vclu.pl, section modules)

## Acceptance criteria
- [ ] The module can be added in the wizard; Object Manager finds it and lists its objects with their features, methods and events.
- [ ] A script in Object Manager that calls the object's methods works on vCLU (checked in Object Manager; say so in the PR with a screenshot).
- [ ] The Lua object has the same convenience methods as its remote counterpart in `pkg/lua/runtime/objects/`, so code works the same for a real and an emulated module.
- [ ] `expose(obj, "dimmer")` works: value 0..1, Home Assistant and HomeKit brightness 0..100 (same rules as `TestHADimmerScale`).
- [ ] Lua contract tests cover methods, features and events; config tests cover the generated `config.txt`/`CONFIG.JSON` entries.
- [ ] Hardware type, serial prefix and indices are recorded in `pkg/lua/AGENTS.md` with their source.

## Contract impact
New module type and Lua class; existing modules unchanged.
