# #192: Sensors from the OM import get a default reporting filter

- **Type:** feature
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A temperature, humidity or light sensor exposed by `exposeRegistry()` reports only real changes,
so history, MQTT and `onChange` are not flooded by a reading that flips by one step.

## Context
- `exposeRegistry()` (`pkg/lua/runtime/core/ExposedObjects.lua`, run from `cmd/vclu/main.go`
  after the modules) exposes every registry object not exposed yet, with no `filter`.
- `filter = { delta, heartbeat }` (`StateChange.setFilter` / `StateChange.filter`) passes a change
  to history, HA/MQTT, HomeKit and `onChange` only when it differs by at least `delta` from the
  last reported value, or when `heartbeat` seconds passed; `getValue()` stays raw. It is allowed
  only on read-only numeric types: `temperature`, `humidity`, `illuminance`.
- Measured on one installation (one day): a panel light sensor flipping 0.1/0.2 lx every ~1 s
  gave 19 000 history entries (3 MB of 8.5 MB); panel temperatures flipping 25.5/25.6 about
  6 000 each. A user reported 45 MB history files per day.
- A module cannot fix an imported sensor with `ExposedObjects:get(path):update({ filter = ... })`:
  the handle does not exist yet when modules load. Today the only way is to `expose()` each
  sensor by hand.

## Behaviour
- `exposeRegistry()` gives a sensor the default filter of its type:

  | Type | Default |
  |---|---|
  | `temperature` | `{ delta = 0.2, heartbeat = 600 }` |
  | `humidity` | `{ delta = 1, heartbeat = 600 }` |
  | `illuminance` | `{ delta = 5, heartbeat = 600 }` |

- A sensor the user exposes with `expose()` keeps what the user wrote: no `filter` means none,
  as today. Only objects that `exposeRegistry()` exposes get the default.
- `handle:update({ filter = false })` and `handle:update({ filter = {...} })` work on an imported
  sensor after start, and a module can set them before (see Open question 2).
- Other types (switches, dimmers, covers, binary sensors, plain `sensor`) get no filter.

## Open questions (decide before implementing)
1. Light: an absolute `delta` is too coarse at night and too fine in daylight (300 <-> 310 lx).
   A relative option (`ratio = 0.1`) would fit better; it is a new filter key, so keep `delta = 5`
   unless the maintainer wants it.
2. How a module sets the filter of an imported sensor before `exposeRegistry()`: an option in
   `exposeRegistry()`'s defaults per type, or `ExposedObjects:defaults(path, opts)` read when the
   object is exposed. Prefer the smallest addition.

## Files
- `pkg/lua/runtime/core/ExposedObjects.lua` (`exposeRegistry`, defaults per type)
- `pkg/lua/runtime/objects/RemoteObject.lua` (`:expose(opts)` passes the filter through)
- `pkg/lua/` tests (exposed registry sensors, filtered history and MQTT)
- `docs/ai/examples.md` ("Noisy sensor"), `docs/ai/lua-api-reference.md` (regenerated),
  `pkg/mcp/tools_dev.go` (`vclu_runtime_schema`, if it lists expose options)

## Acceptance criteria
- [ ] A `RemoteObject` of type `PANELSENSTEMP` in the registry, exposed by `exposeRegistry()`, has
      `options.filter` `{ delta = 0.2, heartbeat = 600 }`; `PANELSENSLIGHT` and `HUMIDITY_SENSOR`
      the defaults above; a `DOUT` none.
- [ ] Readings 25.5, 25.6, 25.5, 25.6 of that sensor give one history entry and one MQTT state
      publish, not four; 25.5 then 25.8 gives two; after `heartbeat` a 0.1 change is reported.
- [ ] `getValue()` returns the raw reading.
- [ ] A sensor exposed by hand without `filter` has none; with `filter = false` none.
- [ ] `handle:update({ filter = false })` on an imported sensor reports every change again.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0192-sensor-default-filter.md` (Contract change, see below).

## Contract impact
Lua runtime behaviour: imported sensors call `onChange` and write history less often than today.
A `Contract change:` changelog entry with how to get every change back (`filter = false`); no
decision record.
