# #219: myGrenton interface from the OM project, mapped to vclu dashboard widgets

- **Type:** feature
- **Area:** grenton, lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** #178 (project import)
- **Skill:** —
- **Needs hardware:** no (fixtures); a CLU to check the result by hand

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An import of the Object Manager project (or of a diagnostic pack that carries one) also reads the
myGrenton interface the user designed in OM, and vclu knows what each of its widgets reads and does,
in terms of vclu's objects and dashboard widgets. This ticket makes no dashboards: it is the data
and the mapping that the "Dashboards" section on the Grenton page (#220) builds them from.

## Context
- The interface is `nodes/<id>/data.json` in the project (`.omp` or project `.zip`). A diagnostic
  pack carries the project inside, which #178 already reads. A project may have more than one
  `nodes/<id>/`: each is an interface of its own, all are kept, keyed by their `id`.
- Shape (OM 1.8): `{id, label, icon, theme, pages: [{id, label, icon, widgets: [...]}], ...}`. Each
  widget has `id`, `type` and, per type, parts with a `rowId`, a `label`, and calls.
- Widget types and their parts:

  | Type | Parts | vclu widget |
  |---|---|---|
  | `ON_OFF` | `buttonBistable`: `state`, `actionOn`, `actionOff`, `onIndication`/`offIndication` | `toggle` |
  | `VALUE` | `value` (`DESCRIBED_STATUS`): `state`, `unit`, `min`, `max` | `status` |
  | `VALUE_V2` | `value`, `unit`, `precision`, `valueType` (`INTEGER`, `STRING`) | `status` |
  | `TEXT` | `text` | `status` |
  | `CONTACT_SENSOR` | `object`: `value`, `clickAction`; `onIndication`/`offIndication` | `status` (a click: `button`) |
  | `SCENE` | `buttonMonostable`: `actionClick`; `background` | `button` |
  | `SCENE_DOUBLE` | `buttonMonostableFirst`, `buttonMonostableSecond` | two `button`s |
  | `HEADER` | `text` | `group` |
  | `THERMOSTAT` | `thermostatCurrentTemp`, `thermostatTargetTemp` (`min`, `max`), `thermostatState`, `thermostatMode`, `thermostatSchedule`, ... | `thermostat` |
  | `SCHEDULER` | `state`, `value`, `schedule`, `min`, `max`, `start`, `stop`, `setMin`, `setMax`, `setSchedule` | none for now: listed as left out |
  | `DIMMER` | `buttonBistable`, `slider` (or `status`) | `toggle` + `slider` (`_level`) |
  | `DIMMER_V2` (OM 1.8) | `min`, `max`, `object`: `onAction`, `offAction`, `setValueAction` (`PSEUDO_METHOD`, feature 0), `value` | `toggle` + `slider` (`_level`) |
  | `LED` | `buttonBistable`, `sliderBrightness`, `sliderHue`, `sliderSaturation` | `toggle` + a `slider` each |
  | `ROLLER_SHUTTER`, `ROLLER_SHUTTER_V2` | `buttonBistableClick` / `buttonBistableShorter`, `upButton`, `downButton`, `status`, `sliderOpenLevel`, `sliderSlatsLevel` | `button`s (`_up`, `_down`), position and slats as `slider` or `status` |
  | `LED_CCT`, `ROLLER_SHUTTER_V3` | one `object` | none for now (no real file seen): listed as left out |

  A unit is OM's enum: `DEGREE` is °C (° for an LED's hue), `PERCENT` is %, `UNKNOWN` none. A level of 0-1 in
  `PERCENT` (a dimmer's, an LED's brightness and saturation, as OM 1.8 writes them) becomes a 0-100 slider
  (`percent`: `Wire.percent` / `Wire.fromPercent`); a slider of 0-1 would only take 0 and 1.

- A call is `{callType, cluId, objectName, index, type?, value?}`. Every call seen:

  | callType | Means | In vclu |
  |---|---|---|
  | `ATTRIBUTE` | feature `index` of object `objectName` on `cluId` | the object's value from the registry, `<cluId>.<objectName>` |
  | `PSEUDO_METHOD` | set feature `index` to `value` | `obj:set(index, value)` |
  | `METHOD` | execute method `index` with `value` | `obj:execute(index, value)` |
  | `SCRIPT` | run the CLU script `objectName` with `value`, its arguments as Lua source (`1,-1`, `"on","2"`); `index` is null | the arguments read on import as a list of numbers and plain strings (`args`), anything else left out; `clu:executeNoReply(0, name .. "(" .. literals .. ")")` with each through `RemoteObject.literal`, the name checked as an identifier |
  | `VARIABLE` | the user feature `index` of the CLU (`objectName` is the CLU) | the user feature, as `clu:get(index)` (#217) reads it |

  `value` `"$value$"` is what the user enters: a number for a slider or a thermostat target,
  otherwise a string, and always through `RemoteObject.literal`. `type` of an action is `ON`, `OFF`
  or `CLICK`.
- Reads must not block. `clu:get` (#217) and `RemoteObject:get(index)` wait for the CLU's answer,
  and a dashboard reads on every poll. A VARIABLE, and an ATTRIBUTE other than the value the
  registry already follows, are read with `executeAsync` in the background and the widget shows
  the last value read.
- Commands go through the proxies, so their guards apply: a METHOD call that disarms a SatelZone
  (#214) is refused unless disarming is allowed.
- Decided: an anti-corruption layer. Only `pkg/omproject` knows Grenton's format: it translates the
  interface into vclu's own model (`pkg/dashspec`: interfaces, pages, widgets of the kinds group,
  toggle, status, slider, button and thermostat, each with what it reads and what it does), and a
  change in Grenton's format stops there. The model is what is stored in `imports/panels/<id>.json`,
  next to the imported om.lua files, not among the user's files; Grenton's JSON is not kept. #220
  generates the dashboards from the model alone.
- The generated files are plain Dashboard code; what reads a CLU or commands it goes through small
  helpers of a runtime module (`pkg/lua/runtime/grenton/Wire.lua`): `Wire.value(path)`,
  `feature(path, n)`, `variable(clu, name)`, `isOn(read)`, `text(read, on, off)`,
  `set(path, n, value)`, `execute(path, n[, value])`, `script(clu, name, ...)`, `onOff(on, off)`,
  `Wire.INPUT` for what the user enters, and `Wire.load(module)` to load a page on its own.
  The helpers check their arguments again, as users edit the files. Commands go only to objects,
  through their proxies; the CLU itself is only read.
- The JSON comes from an upload. As in #178, every value that reaches Lua is checked, never escaped
  into code: `cluId` and object IDs against the patterns #178 uses, script and user feature names as
  Lua identifiers, `index` as a number (or an identifier for `VARIABLE`). Labels are data (strings),
  shown as text. A call that fails a check is left out with a warning.

## Files
- `pkg/omproject/` (read `nodes/*/data.json`)
- `pkg/web/handle_web_om.go` (store it in `imports/panels/`, remove it when a new project has none)
- `pkg/dashspec/` (new: the model, its checks, the Lua generator)
- `pkg/lua/runtime/grenton/Wire.lua` (new: the helpers)

## Acceptance criteria
- [ ] Importing a project or a diagnostic pack with an interface stores it in `imports/panels/`;
      a new import replaces it, and an import of a project or pack without one removes it. Uploading
      a single om.lua leaves it alone.
- [ ] The model lists the pages and, per widget, the vclu widget, its label, what it reads and what
      it does. A test with a fixture interface that has every widget type and call type above.
- [ ] No read blocks the run loop: a test with a CLU that does not answer shows the dashboard still
      answers, with the last value or none.
- [ ] Each call type reads or commands the right object (tested against registry objects): ATTRIBUTE,
      PSEUDO_METHOD with a literal and with `$value$`, METHOD, SCRIPT, VARIABLE.
- [ ] A widget or call vclu cannot map (SCHEDULER, an object not in the registry, a failed check) is
      in a list of what was left out, with the reason, and the rest of the page maps.
- [ ] A test with names and values that are Lua code shows none of them runs.
- [ ] Docs on docs.vclu.pl: what is read from the interface and what is left out.
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry in `changelog.d/`.

## Contract impact
The `Wire` helpers become a public Lua API: the files #220 writes call them, and users edit
them. The stored model (`imports/panels/`) is vclu's own and may change with a new import. No
dashboard changes until the user creates them (#220).
