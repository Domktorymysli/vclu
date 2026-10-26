# #0129: The calendar and forecast widgets show times in the house's zone without setTimezone

- **Type:** bug
- **Area:** web
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
The calendar and forecast widgets show times on the house's clock in any browser, whether or not
`datetime.setTimezone(...)` was called.

## Context
The calendar expands its rules in `time.Local` when no zone is set (`calendarZone` in
`pkg/lua/calendar.go`). `Dashboard.API.calendarRange` (`pkg/lua/runtime/core/Dashboard.lua`) sends
`zone = datetime.timezone()`, which is `""` in that case (`datetimeTimezone` in
`pkg/lua/parsers.go`). With an empty zone `hhmm()` in `web/static/js/dashboard.js` formats in the
browser's zone, against its own comment ("times in the house's zone ... a viewer abroad sees the
house's clock"). The forecast widget has the same bug: `Dashboard.lua` sets
`widget.zone = datetime.timezone()` and `dashboard.js` formats its hours with `hhmm(h.at, w.zone)`.

`time.Local.String()` is not the name to send: Go returns `"Local"` when the zone comes from
`/etc/localtime` (and on macOS), and the browser rejects `"Local"` as `timeZone`. Only `TZ=Europe/Warsaw`
gives a real name. So the name comes from `TZ` first, then from `readlink /etc/localtime` (the part
after `.../zoneinfo/`); when neither gives a name, send `""` and keep today's behaviour.

`datetime.timezone()` stays as it is: `""` means "no zone set" (`datetimeTimezone` in
`pkg/lua/parsers.go`), and tests in `pkg/lua/lua_parsers_test.go` hold that. The name for the browser
comes from a separate helper (e.g. next to `calendarZone` in `pkg/lua/calendar.go`).

Seen in vclu-e2e (container in UTC): an event at `time='18:00'` shows as 19:00–20:00 in a Warsaw
browser and 03:00–04:00 in Tokyo. A house whose server and browsers share one zone does not see it.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (`calendarRange`, the forecast widget's `zone`)
- `pkg/lua/calendar.go` (a helper for the zone name to send, next to `calendarZone`)

## Acceptance criteria
- [ ] Without `setTimezone`, the range answer and the forecast widget carry the zone's IANA name: from `TZ`, else from the `/etc/localtime` link; `""` when there is no name (never `"Local"`).
- [ ] A Go test for the helper: `TZ` set, `/etc/localtime` linked into `zoneinfo`, and no name found.
- [ ] An 18:00 event shows as 18:00–19:00 in a browser in another zone (vclu-e2e: drop the knownBug marker in `calendar.spec.ts`, "day view lists events with their times in the house zone").
- [ ] With `setTimezone` the behaviour does not change; `datetime.timezone()` still returns `""` without it.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0129-calendar-house-zone.md`.

## Contract impact
None (the `zone` field already exists; it gets a value where it was empty; `datetime.timezone()` does not change).
