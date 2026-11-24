# #268: Calendar schedules and events are edited from a dashboard and the app

- **Type:** feature
- **Area:** lua-runtime | web | access-control
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`, `.agents/skills/run-local-vclu`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A person changes the hours of a schedule (heating comfort, watering) on a dashboard, in the browser
or in the app through vclu cloud, as far as the module allows; the module's code that reacts
to the schedule does not change, and it can tell who edited what.

## Context
- A schedule (`Calendar:schedule`, #76) exists only in a module's code: its windows are compiled
  in `build()` (`pkg/lua/runtime/core/Calendar.lua`). User events already have a runtime path
  (`add`/`remove`/`skip`/`pause` saved in `persist('calendar.<id>')`); schedules have none.
- The module gives the shape (values, default, exceptions); the person changes the hours. The
  module keeps reading `value()` and `on('change')`: whether the hours came from code or a phone
  is not its business.
- Remote dashboards reach the apps through the cloud's tunnel unchanged (`pkg/web/tunnel.go`):
  a new widget type, a new action or a new `GET /api/dashboard/:id/...` route needs nothing in
  the cloud, only an entry in `remoteRoutes`. The apps (iOS, Android) draw widgets natively and
  show an unknown type as a tile with its `value`, opening the page in a web view on tap (not
  in direct LAN mode). So the page's editor works in today's apps through the cloud; native
  editors are the apps' own tickets.
- Roles: a dashboard's `:roles()` names the home's roles the cloud gives people. Go knows a
  remote viewer's home roles (`ctxViewerRoles`, from the members list or a local token, see
  `tunnel.go`, `local.go`) but passes Lua only the membership role (`owner`/`admin`/`member`)
  as `ctx.role`. Dashboard answers are cached per membership role, never per person (`roleKey`).

## API
```lua
Calendar.new('dom'):holidays('pl')
    :schedule('komfort', { values = { 'eco', 'komfort' }, default = 'eco', title = 'Ogrzewanie',
        { every = { 'mo', 'tu', 'we', 'th', 'fr' }, hours = '06:00-08:00', value = 'komfort' },
        { every = 'weekend', hours = '08:00-22:00', value = 'komfort' },
        { every = 'holiday', hours = '08:00-22:00', value = 'komfort' } })   -- stays code's
        :editable()              -- or editable = true in the table
        :roles('rodzice')        -- or roles = { 'rodzice' }: who may edit; without it everyone who may act
    :event('bio', { ... })       -- the handle passes every other builder method on
    :build()

dom:week('komfort')        -- { format = 1, version, edited, by, at, week, fixed, values, default, colors, title }
dom:setWeek('komfort', week, { version = n })   -- returns the new version; raises on a bad week or version
dom:resetWeek('komfort', { version = n })       -- back to the code's hours
dom:on('edit', [id], function(e) end)           -- e: { calendar, id, kind, action, version?, origin, actor, title }

Dashboard ...:schedule('ogrz', 'Ogrzewanie', dom, 'komfort', { span, visible, remote, step = 10 })
```

**The week.** `week = { mo = { { from = '06:00', to = '08:00', value = 'komfort' }, ... }, tu = ..., su = ... }`:
for each day, intervals in order, not overlapping, `from < to`, `to` up to `'24:00'`, each value one
of `values` and not `default` (the rest of the day is the default), at most 48 per day (half
hours of two values in turn), minutes whole. A day may be missing (all default). Unknown keys are an error. `format = 1` is this shape;
a later shape gets a new number and clients edit only a format they know.

**What the editor edits.** A window is *weekly* when it has only `every` (a weekday, a list of
them, `'day'` or `'weekend'`), `value` and optionally `hours = 'HH:MM-HH:MM'` (without it the
whole day; `from >= to` runs over midnight, equal is 24 h). An editable schedule's weekly
windows come first; the others (`holiday`, `workday`, sun times, `dates`, `weeks`, `from`/`to`,
`except`...) are *fixed*, come after them and stay the code's. `build()` raises:
- for `every = 'workday'` (it follows the holidays, a week does not): use the weekdays and a fixed
  `every = 'holiday'` window, the error says so;
- for a weekly window after a fixed one, or a schedule without any weekly window;
- for `roles` without `editable`, an empty `roles`, a role that is not a string;
- for an editable schedule without `values` (the editor offers them).

The code's week is its weekly windows painted in order (later wins, over midnight split at the
day's end) and merged; `week()` gives it until someone saves. A saved week is compiled as one
window per distinct interval (`every` = the weekday codes that have it, `hours`; `'24:00'` written
`'00:00'`, which ends at the next midnight) followed by the fixed windows, so it behaves exactly
as the code would with those hours. `fixed` lists the fixed windows as text fields (`every`,
`hours`, `value`...), for the editor to show as "from the module".

**Saving.** `persist('calendar.<id>').schedules` maps a schedule to `{ week, version, by, at }`
(a copy reassigned as a whole: Persist saves only top-level writes). `version` starts at 0 and
grows by one on every save and reset; a reset keeps the entry without `week`. A save or reset
naming another version raises; `version` is optional for code, the endpoint always sends it and
answers 409 with the current week. A saved week the code no longer allows (a value gone, the
schedule no longer editable) is dropped at `build()` with a log line and its `week` removed, the
version carries on, and the code's hours apply. After a save the value is applied at once
(`change` fires if it changed, not before the clock is trusted) and the calendar plans again.
`setWeek` and `resetWeek` refuse a schedule that is not editable. Code, `vclu_exec` and the
sandbox call them without role checks: the module is trusted.

**`edit` event.** Every runtime change of a calendar fires `edit` once, after it is saved: for a
schedule `kind = 'schedule'`, `action = 'set' | 'reset'`, `version`; for events `kind = 'event'`,
`action = 'add' | 'remove' | 'skip' | 'unskip' | 'pause' | 'resume'` (`id` the event's, nil for a
pause or resume of the whole calendar, with `date` or `from`/`to`). `origin` and `actor` come from
`StateContext.current()` (web, cloud person, MCP, module), so a module sends a push with who did
it (`docs/ai/examples.md` gets one): `actor` is the entry point's (`cloud:<id>` for a person
through the cloud, `dashboard:<id>` for a PIN page, `user:<login>` for the panel), names are not
mapped here. `on('edit', id, fn)` takes a schedule or an event id known when subscribing;
`on('edit', fn)` gets all, also events added later. A failing handler is logged and does not
undo the change.

**Who may edit through a dashboard** (`mayEdit`, one function for the flag and the action):
- the schedule is editable, the widget is not hidden or disabled, and not `remote = 'readonly'`
  for a remote viewer;
- never a client of the device API (`client:` actors, `lanClientViewer`): it gets the week, not
  the editor;
- no `roles`: everyone the dashboard lets act;
- with `roles`: a remote viewer whose membership role is `owner` or `admin`, or whose home roles
  (new `ctx.roles`, passed by Go from `ctxViewerRoles` as an escaped JSON string) include one of them; unknown home roles
  (no members list) mean no. The house (LAN, no token) only on a dashboard with `auth('pin')` or
  `auth('session')`: a public page on a kitchen tablet does not get round the roles.

**Dashboard widget `schedule`.** Options `{ span, visible, disabled, remote, step }`: `step` is the minutes one tap paints, 5, 10, 15, 30 (default) or 60 (10 for watering, 60 for a tariff). Definition: `calendar`, `schedule`, `title`, `format`, `step`. A schedule without colours has no `colors` (an empty table would reach JSON as a list); `by` goes only to a viewer who may edit. Values:
`value` (a short text: the value now and the next change, for tiles that do not know the type),
`now`, `version`. `GET /api/dashboard/:id/schedule?widget=<id>` (dashboard auth, in
`remoteRoutes`, never cached: it depends on the person; `calendarLimiter` bounds it) gives `week()` plus `editable` for this
viewer and `segments` of today and the next 6 days (the effective values, fixed windows included).
Actions through `POST /api/dashboard/:id/action` (so `actionId` replay covers a repeat): `set`
with `value = { week, version }`, `reset` with `value = { version }`. `actionValueLua` drops
nested tables, so Go also passes a map or list value as JSON text (`ctx.valueJSON`) and the
schedule decodes that; other widgets get their value as today. Every answer of the schedule
action carries `status` (403 when `mayEdit` says no, 409 with the current week on another
version, 400 on a bad week), so the house gets the HTTP status too. After a save other
dashboards show the new hours on their next read; any action drops the calendar answers cached
in Go (30 s), so a calendar widget shows a change at once. The page
draws seven rows of 24 h, a value is painted by tap or drag in steps of `step` minutes (finer saved
intervals are kept until painted over), with Save, Reset to the module's hours, the fixed
windows listed below, and a conflict message that reloads the week. 390 px wide works.

## Part 2: the calendar's events, the same rules

A calendar widget lets people add their own events and skip a day of any event, as far as the
module allows. Same assumptions as the week: the module opts in and names roles, `edit` fires,
the cloud and today's apps need nothing new.

```lua
Calendar.new('dom'):editable():roles('rodzice')   -- right after new(), before events and schedules
    :holidays('pl'):event('szkola', { ... })
    :build()
dom:update(id, opts)   -- a user event's fields again (as add); edit with action = 'update'
```

- `Builder:editable()`/`roles()` straight after `Calendar.new()` make the calendar's events
  editable. Called after an event or a schedule they raise at once (the error names
  `:schedule(...):editable()` for a schedule), so `:schedule(...):editable()` is never mistaken
  for the calendar's. A schedule's own `editable` is separate. `update` is new public API.
- What a person may do (`mayEditCalendar`, the same rules as `mayEditSchedule`: roles, owner and
  admin, the house only on a PIN or session dashboard when `roles` is set, never a device API
  client, never `remote = 'readonly'`, not a hidden or disabled widget): `add` (one-off `date`
  or `'a..b'`, or `every` weekdays/`'day'`; `time` + `duration` or all day; `title` required,
  at most 80 characters; nothing else), `update` and `remove` of a user event (`source = 'user'`),
  `skip` and `unskip` of one occurrence of any event. An event added on a widget with `tags`
  gets them, so it shows there. Answers carry their HTTP status like the week's.
- `GET .../calendar` gives `editable` for this viewer and, when it is true and the page asks
  with `skipped=1`, the skipped occurrences with `skipped = true` (so they can be brought back;
  an app that does not know them gets none). Actions reach only the events the widget shows
  (its `tags`); `update` keeps what the page does not set (`remind`, `icon`, `tags`). Its cache key gains the
  viewer's home roles and client flag; nothing changes for a calendar that is not editable.
- The page: in the day view a `+` adds an event (title, all day or from-to, repeat: none, every
  day, every week on that weekday), an event's row gets skip / bring back, and for user events
  edit and remove. The module's events stay its own: only a day of them can be skipped.
- Limits for the page (the module's own `cal:add` is not bounded): at most 200 user events
  ahead (a one-off whose day has passed or a repeating one past its `to` does not count), and a
  day skipped or brought back from 62 days back to 366 ahead, so a daily event cannot collect
  skips without end. Both answer 400 naming the limit.

## Files
- `pkg/lua/runtime/core/Calendar.lua`: schedule handle, weekly/fixed split, `week`/`setWeek`/`resetWeek`, `edit`
- `pkg/lua/runtime/core/Dashboard.lua`: `section:schedule`, `DashboardAPI.scheduleWeek`, actions, `mayEdit`
- `pkg/web/handle_web_dashboard.go`, `pkg/web/tunnel.go`: the route, `ctx.roles` for actions and the week
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`, `web/locales/*.json`: the editor
- `pkg/mcp/tools_dev.go` (runtime schema), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`
- `CONTRIBUTING.md` (contract row), `changelog.d/268-schedule-edit.md`

## Acceptance criteria
- [ ] An editable schedule that nobody saved gives the same values and changes as before this
  ticket, and `week()` gives its code's hours, over midnight too (Lua contract test).
- [ ] `setWeek` changes `value()` and fires `change` at once when the value now changes, the next
  change follows the new hours on the sandbox's virtual clock, fixed windows still win, and the
  week survives a reload and a restart (Persist).
- [ ] A bad week (overlap, unknown value, the default as a value, `from >= to`, a bad time, an
  unknown key, more than 48 intervals) and a wrong version are refused with an error naming it;
  nothing is saved.
- [ ] `build()` errors: `workday` in an editable schedule, a weekly window after a fixed one,
  `roles` without `editable`, an unknown option; a non-editable schedule is unchanged.
- [ ] `edit` fires once per runtime change of a schedule or an event, with `origin` and `actor`
  of the caller (a dashboard action through the tunnel, a module in the sandbox), after the
  change is saved.
- [ ] Through the HTTP endpoints: a viewer with the role saves, one without gets 403, a stale
  version 409, a repeat with the same `actionId` the first answer and one save; the house on a
  public dashboard cannot edit a schedule with roles, with the PIN it can; `editable` in
  `GET .../schedule` says the same as the action does (`pkg/web/`).
- [ ] The existing cases of `pkg/lua/dashboard_definition_test.go` stay unchanged and pass; a case
  for `schedule` is added. A device API client gets 403 on `set`.
- [ ] Checked on a running vCLU: edit, save, reload, conflict from two tabs, at 390 px; by hand,
  the iOS app opens the page from the unknown tile through the cloud and saves.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] Part 2: through the HTTP endpoints a viewer with the role adds, updates, removes a user
  event and skips and brings back a day of a code event; one without gets 403; a code event
  cannot be updated or removed; `edit` fires for each; `GET .../calendar` shows `editable` per
  viewer and skipped days only to who may edit (`pkg/web/`, `pkg/mcp/`).
- [ ] Changelog entry (`changelog.d/268-schedule-edit.md`).

## Contract impact
New contract row "Calendar schedule editing" (week, versions, `edit`, who may edit). The
Dashboards contract (definitions of the existing types) must hold unchanged. Not in this ticket:
native editors in the apps, a temporary hold of a value (`hold(id, value, until)`), roles given
as a function, editing events from a dashboard (a follow-up ticket with the same rules).
