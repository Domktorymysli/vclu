# #13: Emulated Grenton roller module

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
Object Manager can add a virtual roller module to vCLU, and its roller objects (class ROLLER, 24, with position) behave like real ones: up, down, stop, position, tilt.

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
Unverified method numbers used by `RemoteCover` (`pkg/lua/runtime/objects/RemoteCover.lua`): `execute(0, time)` up, `1` down, `3` stop, `9` tilt angle, `10` set position (position, lamel). The class without position (ROLLER_SH, 11) may follow later. vCLU counts position 0 = open; HomeKit 100 = open.

## Files
- `pkg/config/modules.go` (module type), `pkg/config/om_generator.go` if the module needs more than DOUT
- `pkg/lua/runtime/grenton/modules/ROLLER.lua` (new), `pkg/lua/runtime/bootstrap_go.lua`, `pkg/lua/runtime/utils/TypeMap.lua`
- tests in `pkg/config` and `pkg/lua`; a docs page for the module (docs.vclu.pl, section modules)

## Acceptance criteria
- [ ] The module can be added in the wizard; Object Manager finds it and lists its objects with their features, methods and events.
- [ ] A script in Object Manager that calls the object's methods works on vCLU (checked in Object Manager; say so in the PR with a screenshot).
- [ ] The Lua object has the same convenience methods as its remote counterpart in `pkg/lua/runtime/objects/`, so code works the same for a real and an emulated module.
- [ ] The object reports its position while moving and after stop (simulated travel time is configurable), so HomeKit and Home Assistant show a real position.
- [ ] `expose(obj, "cover")` opens and closes in the right direction in HomeKit and Home Assistant.
- [ ] Lua contract tests cover methods, features and events; config tests cover the generated `config.txt`/`CONFIG.JSON` entries.
- [ ] Hardware type, serial prefix and indices are recorded in `pkg/lua/AGENTS.md` with their source.

## Contract impact
New module type and Lua class; existing modules unchanged.
