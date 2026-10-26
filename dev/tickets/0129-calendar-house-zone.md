# #129: The calendar widget shows times in the house's zone without setTimezone

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
The calendar widget shows event times on the house's clock in any browser, whether or not
`datetime.setTimezone(...)` was called.

## Context
The calendar expands its rules in `time.Local` when no zone is set (`calendarZone` in
`pkg/lua/calendar.go`). `Dashboard.API.calendarRange` (`pkg/lua/runtime/core/Dashboard.lua`) sends
`zone = datetime.timezone()`, which is `""` in that case (`datetimeTimezone` in
`pkg/lua/parsers.go`). With an empty zone `hhmm()` in `web/static/js/dashboard.js` formats in the
browser's zone, against its own comment ("times in the house's zone ... a viewer abroad sees the
house's clock").

Seen in vclu-e2e (container in UTC): an event at `time='18:00'` shows as 19:00–20:00 in a Warsaw
browser and 03:00–04:00 in Tokyo. A house whose server and browsers share one zone does not see it.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (`calendarRange`)
- `pkg/lua/parsers.go` or `pkg/lua/calendar.go` (the zone name to send)

## Acceptance criteria
- [ ] Without `setTimezone`, the range answer carries the zone the calendar uses (`time.Local`'s IANA name; `TZ`/`/etc/localtime` in Docker).
- [ ] An 18:00 event shows as 18:00–19:00 in a browser in another zone (vclu-e2e: drop the knownBug marker in `calendar.spec.ts`, "day view lists events with their times in the house zone").
- [ ] With `setTimezone` the behaviour does not change.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/`.

## Contract impact
None (the `zone` field already exists; it gets a value where it was empty).
