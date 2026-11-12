# #220: Grenton page: create vclu dashboards from the myGrenton interface

- **Type:** feature
- **Area:** grenton, web
- **Size:** M
- **Epic:** —
- **Blocked by:** #219 (myGrenton interface mapping)
- **Skill:** —
- **Needs hardware:** no; a CLU to check the dashboards by hand

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A user who designed the myGrenton interface in Object Manager gets the same pages as vclu dashboards
without writing Lua: on the Grenton page they see the imported pages, pick them, and vclu writes
dashboard files they can edit afterwards.

## Context
- #219 reads the interface on import and maps each widget (`MyGrenton` in the runtime). Nothing is
  created then: dashboards appear only when the user creates them here.
- The Grenton page has the tabs Device, OM Files, Modules and Sync (`web/templates/grenton.html`).
  This ticket adds a "Dashboards" tab.
- One myGrenton page is one dashboard; the pages of one interface are joined by `:tabGroup(...)`,
  which the panel (`dashboard.js`) and the Android app already show as tabs.
- The files are `modules/dashboards/grenton/<page>.lua` plus `modules/dashboards/grenton/init.lua`
  requiring them. `<page>` is made from the page's `id` (`page_<id>`, `[a-z0-9_]+`), never from its
  label. They are the user's files from then on: plain dashboard builder code
  (`pkg/lua/runtime/core/Dashboard.lua`), one `MyGrenton.widget(section, {...})` line per widget
  (#219), so the user can remove, reorder, rename or add to them. The files need only the runtime,
  not the stored import: a later import without an interface does not break them.
- Go (`pkg/web`) writes the files from the checked interface #219 stores: keys as identifiers,
  numbers as numbers, strings through `luaEscape` (`pkg/omparser`, added by #178; `\ddd` for
  control bytes, as Lua 5.1 has no `\x`).
- Loading is explicit: vclu adds `require("dashboards/grenton")` to `modules/init.lua` only when the
  user agrees and only if the line is missing. Nothing is loaded by itself.
- A user may already have a `modules/dashboards/grenton/` directory, or a
  `modules/dashboards/grenton.lua`, which `require("dashboards/grenton")` would load instead of the
  directory. vclu never overwrites a file it did not write: it shows what is there and asks, or
  writes to another name. A file vclu wrote starts with a header line carrying a hash of its
  content; a file whose hash no longer matches has been edited and counts as the user's.
- What goes into the files comes from an uploaded project: IDs and names only after the checks of
  #219. A widget that failed them is not written.
- Creating reloads Lua once the `require` line is in place, as saving a file in the editor does, so
  the dashboards appear without a restart.

## Files
- `web/templates/grenton.html`, `web/static/js/` (new tab)
- `pkg/web/` (API: list the imported pages, create the files, add the `require`)
- `pkg/lua/runtime/grenton/MyGrenton.lua` (what a created file calls)
- `web/locales/pl.json`, `en.json`

## Acceptance criteria
- [ ] The Dashboards tab lists the imported interface's pages with their widget counts and what was
      left out; without an imported interface it says how to get one (import the project).
- [ ] Creating writes one file per chosen page and `init.lua`, in one tab group; the dashboards work
      after a Lua reload: toggles switch, buttons run their action, values and thermostats show.
- [ ] `require("dashboards/grenton")` is added to `modules/init.lua` once, after the user agrees;
      creating again does not add it twice.
- [ ] Creating again over files vclu wrote asks before replacing them; a file vclu did not write is
      never replaced. Tested both ways.
- [ ] A test with labels and names that are Lua code shows the files compile in gopher-lua, none of
      it runs, and every label reads back unchanged.
- [ ] A file vclu wrote and the user then edited counts as the user's (its header hash no longer
      matches); a flat `modules/dashboards/grenton.lua` is reported, not overwritten.
- [ ] Docs on docs.vclu.pl: how to create the dashboards and that they are the user's files after.
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry in `changelog.d/`.

## Contract impact
New API endpoints for the tab; new files in `modules/` only when the user creates them.
