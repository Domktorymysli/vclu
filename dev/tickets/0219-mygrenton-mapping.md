# #219: myGrenton interface from the OM project, mapped to vclu dashboard widgets

- **Type:** feature
- **Area:** grenton, lua-runtime
- **Size:** M
- **Epic:** —
- **Blocked by:** #178 (project import); VARIABLE values by #217
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
  pack carries the project inside, which #178 already reads.
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

- A call is `{callType, cluId, objectName, index, type?, value?}`. Every call seen:

  | callType | Means | In vclu |
  |---|---|---|
  | `ATTRIBUTE` | feature `index` of object `objectName` on `cluId` | the object's value from the registry, `<cluId>.<objectName>` |
  | `PSEUDO_METHOD` | set feature `index` to `value` | `obj:set(index, value)` |
  | `METHOD` | execute method `index` with `value` | `obj:execute(index, value)` |
  | `SCRIPT` | run the CLU script `objectName` with `value`; `index` is null | a call of that script on the CLU |
  | `VARIABLE` | the user feature `index` of the CLU (`objectName` is the CLU) | `clu:get(index)` from #217 |

  `value` `"$value$"` is what the user enters (slider, thermostat target). `type` of an action is
  `ON`, `OFF` or `CLICK`.
- Decided: the mapping lives in a runtime Lua module (e.g. `pkg/lua/runtime/grenton/MyGrenton.lua`),
  so a change in Grenton's format is a change there only. The raw JSON is kept in `imports/mygrenton/`,
  next to the imported om.lua files, not among the user's files.
- The JSON comes from an upload. As in #178, every value that reaches Lua is checked, never escaped
  into code: `cluId` and object IDs against the patterns #178 uses, script and user feature names as
  Lua identifiers, `index` as a number (or an identifier for `VARIABLE`). Labels are data (strings),
  shown as text. A call that fails a check is left out with a warning.

## Files
- `pkg/omproject/` (read `nodes/*/data.json`)
- `pkg/web/handle_web_om.go` (store it in `imports/mygrenton/`, remove it when a new project has none)
- `pkg/lua/runtime/grenton/MyGrenton.lua` (new)

## Acceptance criteria
- [ ] Importing a project or a diagnostic pack with an interface stores it in `imports/mygrenton/`;
      a new import replaces it, one without an interface removes it.
- [ ] `MyGrenton` lists the pages and, per widget, the vclu widget, its label, what it reads and what
      it does; a test with a fixture interface that has every widget type and call type above.
- [ ] Each call type reads or commands the right object (tested against registry objects): ATTRIBUTE,
      PSEUDO_METHOD with a literal and with `$value$`, METHOD, SCRIPT, VARIABLE.
- [ ] A widget or call vclu cannot map (SCHEDULER, an object not in the registry, a failed check) is
      in a list of what was left out, with the reason, and the rest of the page maps.
- [ ] A test with names and values that are Lua code shows none of them runs.
- [ ] Docs on docs.vclu.pl: what is read from the interface and what is left out.
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry in `changelog.d/`.

## Contract impact
None: no dashboard changes until the user creates them (#220).
