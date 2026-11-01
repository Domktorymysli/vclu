# #171: Calendar: isDay() and isNight() from the sun, with offsets and fixed hours as a fallback

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
An automation asks a calendar `if dom:isNight() then` instead of building a day/night schedule
or reading the sun itself.

## Context
- Most light automations need "is it dark now". Today a module defines a schedule
  (`:schedule('pora', { ..., { every = 'day', hours = { 'sunset', 'sunrise' }, value = 'noc' } })`)
  and compares `dom:value('pora') ~= 'dzien'`, or hard-codes hours (`datetime.between("21:00", "06:00")`).
  Both are boilerplate the core should give.
- `pkg/lua/runtime/core/Calendar.lua` already has `:location(lat, lon)`, `Cal:sun(date)` (sunrise,
  sunset, dawn, dusk, `polar`) and `Cal:sunPosition(at)`; event and schedule times accept
  `'sunset-30m'`, `'sunrise+15m'`, `'dawn'`, `'dusk'` (`pkg/calendar/sun.go`, `rule.go`).
- `value()` and `isActive()` return nil until the clock is trusted (`:clock()`).

## API
- `Builder:daylight(opts)`, optional; without it the defaults below apply:
  - `from` (default `'sunrise'`), `to` (default `'sunset'`): when the day starts and ends, in the
    same syntax as event times, so `'sunrise+30m'`, `'dusk'`, `'sunset-1h'` work. A plain
    `'07:00'` is a fixed time.
  - `fallback = { from = '06:00', to = '20:00' }` (the default): fixed hours used when the sun
    cannot be computed, i.e. the calendar has no `:location()`.
- `Cal:isDay(at)` → boolean: true between `from` and `to` of that day; `at` a timestamp, default now.
- `Cal:isNight(at)` → `not isDay(at)`.
- Polar day / night (`sun().polar`) is day / night all 24 h. An offset that puts `from` at or after
  `to` (a short winter day with large offsets) makes the whole day night.
- Until the clock is trusted both return nil, as `value()` does.

## Files
- `pkg/lua/runtime/core/Calendar.lua`
- `pkg/calendar/` (only if a helper for resolving a sun time for a date is missing)
- `pkg/lua/` calendar tests
- `pkg/mcp/tools_dev.go` (calendar part of `vclu_runtime_schema`)
- `docs/ai/examples.md` (calendar section), `docs/ai/lua-api-reference.md` (regenerated)

## Acceptance criteria
- [ ] Tests on a fixed clock for a location (e.g. 52.14 N, 20.72 E): `isDay()` false an hour before
      sunrise, true at noon, false an hour after sunset, in winter and in summer (DST).
- [ ] Offsets: with `from = 'sunrise+30m'`, 15 min after sunrise is night, 45 min after is day;
      `to = 'dusk'` is later than `'sunset'`.
- [ ] Without `:location()` the fallback hours decide, default 06:00–20:00, overridable.
- [ ] A polar location (e.g. 78 N) in June is day all day and in December night all day.
- [ ] `from` at or after `to` after offsets: night all day, no error.
- [ ] Invalid `daylight` options (unknown key, a time that does not parse) fail at build with a
      message naming the calendar.
- [ ] Both return nil before the clock is trusted.
- [ ] `vclu_runtime_schema` and `docs/ai/examples.md` show `isDay` / `isNight` and `:daylight`;
      `lua-api-reference.md` regenerated.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0171-calendar-is-day-night.md`.

## Contract impact
Lua API: two new methods and one builder option, additive; no decision record.
