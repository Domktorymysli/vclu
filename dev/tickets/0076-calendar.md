# #76: House calendar: events, schedules of values and a calendar widget

- **Type:** feature
- **Area:** lua-runtime | web | mcp
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A module describes the house's time in Lua (events for people, schedules of values such as an
energy tariff or heating comfort hours) and reacts to them, without polling the clock, and a
dashboard shows the calendar as a month and as a day.

## Context
- Today a timed automation is a `setInterval` that compares `os.date()` (`docs/ai/examples.md`,
  "Harmonogram"): no DST, no holidays, misses a minute when the loop is busy.
- Replaces the plan in the private roadmap ("Czas: kalendarz domu i cron", 2026-10-11): a builder
  like `Dashboard`, mechanisms in Go, policy in Lua. Cron is left out: `every` and `schedule`
  cover the cases; RRULE strings are not the Lua API (an `.ics` import can translate them later).
- A Raspberry Pi without RTC starts with a wrong clock and NTP corrects it later; nothing may fire
  from the wrong time, and moments skipped by a jump must not fire later.

## API
```lua
local dom = Calendar.new('dom'):name('Dom'):holidays('pl'):location(52.23, 21.01)   -- sun times
    :notify(function(e) end)                       -- or an object with :send(e)
    :clock({ wait = false })                       -- optional: do not wait for NTP
    :event('bio', { title = 'Bio', every = 'tu', weeks = 2, from = '2026-10-27',
        time = '06:30', remind = '-12h', moves = { ['2026-11-10'] = '2026-11-12' } })
    :event('szkola', { every = 'workday', time = '08:00', duration = '6h', tags = { 'dzieci' },
        catchup = true, except = { '2026-12-22..2027-01-06' } })
    :schedule('taryfa', { default = 'droga',
        { every = 'workday', hours = '13:00-15:00', value = 'tania' },
        { every = 'weekend', value = 'tania' } })
    :build()
dom:on('start' | 'end' | 'reminder' | 'change' | 'ready', [id], fn)   -- returns unsubscribe
dom:value(id), dom:isActive(id), dom:next(id), dom:today(), dom:upcoming(days, { tags })
dom:between(from, to, { tags }), dom:day(date)          -- dates ('YYYY-MM-DD') or timestamps; day has holidayName, namedays
dom:add(opts), dom:remove(id), dom:skip(id, date), dom:pause('a..b', { tags }), dom:resume()
dom:nextChange(id), dom:color(id[, value]), dom:segments(id, from, to)   -- schedules: { title, colors = { value = color } }
Dashboard ...:calendar('kal', 'Kalendarz', dom, { view = 'month' | 'day', tags = { ... }, schedules = { 'taryfa' },
    holidays = true, namedays = false })   -- defaults; a viewer switches them on the widget
```
Rules: `every` = `day`, `workday`, `weekend`, `holiday`, weekdays, `month` (`day`, `on`),
`year` (`date = 'MM-DD'`, or `month` with `on`); `time = 'sunset-30m'`, `'dusk'`, `hours = { 'sunset', 'sunrise' }`, `sun = { azimuth = {135, 225}, elevation = 15 }` with a location; `dates` (a list of days, e.g. a municipality's waste PDF); `weeks`/`months`/`years` with `from`; `from`, `to`, `times`; one-off
`date` (a range is all day); `time` + `duration` or `hours`; `except`; `moves`.

## Files
- `pkg/calendar/`: rules, Polish holidays, schedules, clock trust (adjtimex on Linux)
- `pkg/lua/calendar.go`: `__go_calendar_*` (registered with the parser libraries, so the sandbox has them)
- `pkg/lua/runtime/core/Calendar.lua`, `bootstrap_go.lua`
- `pkg/lua/runtime/core/Dashboard.lua`, `pkg/web/handle_web_dashboard.go` (`GET /api/dashboard/:id/calendar`),
  `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go` (runtime schema), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [x] Rules expand correctly, including every n-th week, last workday of a month, Polish
  holidays (Easter-based too), except, moves, DST gap and windows over midnight
  (`pkg/calendar/rule_test.go`).
- [x] On the sandbox's virtual clock, start, end, reminders, schedule changes, skip and pause
  fire at the local time a module expects (`pkg/mcp/calendar_scenario_test.go`).
- [x] Nothing fires before the clock is trusted; after at most 10 minutes it starts anyway
  (year ≥ 2025); `wait = false` skips the wait; a clock jump skips the moments in between and
  re-applies schedules and `catchup` events (same file).
- [x] A bad definition stops `build()` with an error naming the event (same file).
- [x] The calendar endpoint follows the dashboard's auth, serves only a calendar widget on that
  dashboard, at most 42 days (`pkg/web/dashboard_calendar_test.go`).
- [x] Checked on a running house: reminder, start and end fired at the second; month and day
  views on a desktop and at 390 px.
- [x] `go test ./...` and `go test -race ./...` pass.
- [x] CHANGELOG entry.

## Before merging
- [ ] Merge PR #69 (ticket #20) first: `vclu_draft_promote` warns that a module's own file
  (`modules/kalendarz/smieci.lua`, required by `modules/kalendarz/init.lua`) will not load.
  Then rebase this branch on `main`.

## Follow-ups (separate tickets)
- MCP tool `vclu_calendar`: agenda for agents ("what is tomorrow?") without `vclu_exec`, proposing
  events as a module draft.
- Plugin: agenda for people (tomorrow on the Smart Panel idle screen, a morning summary).
- Plugin: hourly energy prices (PSE RCE) fed into a schedule; in the core only "the cheapest
  n hours between a and b".
- Plugin: school calendar (winter break by voivodeship, school year) as `schoolday`.
- Plugin: waste collection (EcoHarmonogram, municipal ICS) feeding events.
- `.ics` import (read-only subscription, Google) and export (the house calendar on a phone).
- Adding and skipping events from the dashboard (needs write auth on the widget).
- The sun-position plugin: retire it or build it on the calendar (fixed `timezoneOffset`, an hour
  off in summer; its azimuth reads 0 at solar noon instead of 180).
- MCP `vclu_report_issue`: agents report vCLU bugs to a queue in the panel; a person sends them on.

## Contract impact
New public API (`Calendar`, `:calendar()` widget, `/api/dashboard/:id/calendar`), covered by the
tests above. Nothing existing changes.
