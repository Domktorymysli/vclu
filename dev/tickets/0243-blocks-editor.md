# #243: Block editor for automations (Blockly): MVP

- **Type:** feature
- **Area:** web, lua-runtime
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
In the panel, a user builds a simple automation from blocks ("when the hall sensor changes, if
it is dark, turn on the hall light for 2 minutes"), tests it and turns it on, without reading
or writing Lua.

## Context
- Installers ask for a way to automate without looking at code. Most of their automations are
  small: an event or a time, a condition, a few device actions.
- [Blockly](https://github.com/google/blockly) (Apache 2.0) ships a Lua generator, so the blocks
  compile to Lua with no compiler of our own. Vendor it next to Monaco in `web/static/vendor/`;
  no CDN, the panel works offline.
- The runtime is gopher-lua (Lua 5.1). Blockly's Lua generator emits `goto` for the "continue"
  block, which Lua 5.1 does not have: leave that block out of the toolbox or override its
  generator. Other generators must be checked by running their output (see the criteria).
- Generated code goes through what agents already use: validation and a sandbox test
  (`pkg/mcp/sandbox.go`), and a promote with a backup. Do not build a second pipeline.
- The idioms the generated code should use are in `docs/ai/examples.md`: `onChange` /
  `onSwitchOn` on objects, `_registry:get(id):set(...)`, `Calendar` for times and sun events,
  `Logger`.
- Blocks are one-way: Lua cannot be turned back into blocks. The block workspace (JSON) is the
  source; the Lua file is generated from it and is not edited by hand.

## MVP blocks
Blockly's own logic, math, text and variables, plus:
- **Triggers:** when *device* changes / turns on / turns off; at *time* on *days*; at sunrise /
  sunset ± *minutes*; every *N* minutes.
- **Conditions:** value of *device*; *device* is on; it is dark (`isNight`); time is between.
- **Actions:** turn on / off / toggle *device*; set *device* to *value*; turn on for *N*
  seconds; wait *N* seconds then …; run *scene*; write to the log.

The device and scene lists come from the registry (the same data as the runtime schema), so
devices from plugins show up without extra work. Actions offered for a device depend on its type.

## Storage
- One automation = `modules/<name>/blocks.json` (the workspace) + `modules/<name>/init.lua`
  (generated, starts with a "generated from blocks, do not edit" header).
- The code editor shows such an `init.lua` read-only, with a link to the block editor.
- Renaming or deleting an automation in the block editor renames or deletes the module.

## Files
- `web/static/vendor/blockly/` (vendored), `web/static/js/blocks-editor.js`, a panel page
- `pkg/web/handle_web_blocks.go` (new): list, load, save, test, enable/disable
- `pkg/web/handle_web_editor.go`: read-only `init.lua` when `blocks.json` is next to it
- `pkg/mcp/sandbox.go`: reused for "Test", not copied

## Acceptance criteria
- [ ] A "Blocks" page in the panel lists automations and opens the editor; new, save, rename,
      delete, enable/disable.
- [ ] Every block in the toolbox has a test that generates its Lua and runs it in the runtime
      (gopher-lua): no syntax errors, no `goto`.
- [ ] "Test" runs the generated module in the sandbox and shows its state changes and calls,
      as `vclu_draft_test` does; nothing touches real devices.
- [ ] "Save and enable" validates, promotes with a backup and reloads only that module; an error
      is shown next to the block that caused it where possible, otherwise as a message.
- [ ] A loop built from blocks cannot hang the runtime: generated loops have an iteration limit
      and the module fails with a readable error when it is hit.
- [ ] Device dropdowns list devices from the registry, including plugin ones; a device that
      disappeared later shows as missing, and saving refuses it.
- [ ] The editor works on a tablet (touch drag and drop) and in Polish and English.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0243-blocks-editor.md`.
- [ ] A short page in the docs: the first automation from blocks, with a screenshot.

## Out of MVP
Blocks for dashboards, plugin-specific blocks beyond devices, converting existing Lua modules to
blocks, sharing automations between installations.

## Contract impact
A new file kind in `modules/` (`blocks.json`) and a panel API; the Lua runtime API does not
change. Write a short decision record for the `blocks.json` format, since saved workspaces must
keep loading after blocks change.
