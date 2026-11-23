### Lua runtime

- **Schedules edited from a dashboard and the app** - `Calendar:schedule(...):editable():roles('rodzice')` lets people change a schedule's weekly hours; the module's code that reads `value()` and `on('change')` stays as it is. `cal:week(id)`, `cal:setWeek(id, week, { version })` and `cal:resetWeek(id)` do the same from code; a saved week survives a restart, the module's fixed windows (holidays, sun times) still win ([#268](https://github.com/Domktorymysli/vclu/issues/268)).
- **`edit` event of a calendar** - `cal:on('edit', [id], fn)` fires after a schedule's week is saved or reset and after an event is added, removed, skipped or paused while running, with `origin` and `actor` of who did it: a module can send a push ([#268](https://github.com/Domktorymysli/vclu/issues/268)).
- **Editable calendar** - `Calendar.new('dom'):editable():roles('rodzice')` lets those people add their own events on the calendar widget, change and remove them, and skip or bring back a day of any event; `cal:update(id, opts)` changes a user event from code ([#268](https://github.com/Domktorymysli/vclu/issues/268)).

### Dashboards

- **Schedule widget** - `section:schedule(id, label, cal, scheduleId)` shows the week as seven 24 h rows; who the schedule's roles allow taps Edit, paints values in half hours and saves; a save from elsewhere in the meantime is reported and the current week is loaded. It works through vclu cloud, and today's apps open it from the tile in the page ([#268](https://github.com/Domktorymysli/vclu/issues/268)).
- **Calendar widget edits** - on an editable calendar the day view has `+ Dodaj`, and each event skip and bring back, a person's own also change and remove ([#268](https://github.com/Domktorymysli/vclu/issues/268)).
