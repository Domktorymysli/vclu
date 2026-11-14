# #227: Dashboards: a slat slider on the single cover tile

- **Type:** feature
- **Area:** web
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** Grenton ROLLER_SHUTTER with slats, for the final check

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A `section:cover(...)` tile shows a slat slider when its cover has slats, as the cover group tile
already does, in the panel and in the vclu app.

## Context
- Only the group tile `section:covers(...)` has the slat slider (`coverTilt` and the `tilt` action
  in `pkg/lua/runtime/core/Dashboard.lua`, `renderCovers`/`coversSlider` in
  `web/static/js/dashboard.js`, `CoversCard` in `../vclu-app-android` `ui/dashboard/Widgets.kt`).
- `section:cover(id, label, icon, fns, opts)` takes `fns` {getPosition, getState, open, close,
  stop, set}; `renderCover` draws buttons and the position slider only.
- A user with slat blinds saw the tilt in Lua (`getTilt()` 0) and nowhere on the dashboard. The
  workaround is a group tile with one cover.

## Files
- `pkg/lua/runtime/core/Dashboard.lua`: `fns.getTilt` and `fns.setTilt(angle)` on `cover`; the
  widget data gets `tilt` (degrees 0..90) while `getTilt` returns a number; a `tilt` action calls
  `setTilt`. Where dashboards are generated from a `RemoteCover`/`VirtualCover`, pass its
  `getTilt`/`setTilt`.
- `web/static/js/dashboard.js`: `renderCover` adds the slat slider (as `coversSlider`, 0..90 °) when
  the data has `tilt`.
- `../vclu-app-android` `Widgets.kt`: `CoverCard` the same (separate PR in that repo).
- docs of the cover widget in the dashboard reference.

## Acceptance criteria
- [ ] A `cover` with `getTilt` returning 30 gives widget data `tilt = 30` and a slat slider; with
  `getTilt` nil or no `getTilt`, no slider and no `tilt` in the data.
- [ ] The `tilt` action calls `setTilt` with the angle clamped to 0..90; without `setTilt` it is
  refused.
- [ ] `disabled` / readonly covers show the slider disabled, as the position slider.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0227-cover-tilt-slider.md`.

## Contract impact
Dashboard widget JSON: `cover` may carry `tilt` and accepts the `tilt` action (the same names as
`covers`). No decision record.
