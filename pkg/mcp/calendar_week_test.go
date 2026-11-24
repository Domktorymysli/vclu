package mcp

import (
	"strings"
	"testing"
)

// Contract of an editable schedule's week (Cal:week, setWeek, resetWeek) and of the edit event,
// as a module sees them on the sandbox's virtual clock.

const weekModule = `
local dom = Calendar.new('dom'):holidays('pl')
  :schedule('komfort', { values = { 'eco', 'komfort', 'noc' }, default = 'eco',
     { every = { 'mo', 'tu', 'we', 'th', 'fr' }, hours = '06:00-08:00', value = 'komfort' },
     { every = 'su', hours = '22:00-06:00', value = 'noc' },
     { every = 'weekend', hours = '09:00-12:00', value = 'komfort' },
     { every = 'holiday', value = 'eco' } })
     :editable():roles('rodzice')
  :build()
calLog(dom)
local function days(w)
  local out = {}
  for _, d in ipairs({ 'mo', 'tu', 'we', 'th', 'fr', 'sa', 'su' }) do
    local p = {}
    for _, iv in ipairs(w.week[d]) do p[#p + 1] = iv.from .. '-' .. iv.to .. '=' .. iv.value end
    out[#out + 1] = d .. ' ' .. table.concat(p, ',')
  end
  return table.concat(out, ' | ')
end
dom:on('edit', function(e)
  print('CAL edit ' .. e.kind .. ' ' .. tostring(e.id) .. ' ' .. e.action .. ' v' .. tostring(e.version) .. ' ' .. tostring(e.origin) .. ' ' .. tostring(e.actor))
end)
`

func TestCalendarWeek_UnsavedIsTheCodes(t *testing.T) {
	got := runCalendar(t, "2026-10-23T12:00:00+02:00", weekModule+`
local w = dom:week('komfort')
print('CAL week v' .. w.version .. ' edited ' .. tostring(w.edited) .. ' fixed ' .. #w.fixed .. ' ' .. w.fixed[1].every .. ' roles ' .. w.roles[1])
print('CAL ' .. days(w))`, `[{"advance": 259200}]`)
	expectLines(t, got,
		"week v0 edited false fixed 1 holiday roles rodzice",
		// Sunday's night runs into Monday
		"mo 00:00-06:00=noc,06:00-08:00=komfort | tu 06:00-08:00=komfort | we 06:00-08:00=komfort | th 06:00-08:00=komfort | fr 06:00-08:00=komfort | sa 09:00-12:00=komfort | su 09:00-12:00=komfort,22:00-24:00=noc",
		"10-23 12:00 change eco nil", "10-23 12:00 ready",
		"10-24 09:00 change komfort eco", "10-24 12:00 change eco komfort",
		"10-25 09:00 change komfort eco", "10-25 12:00 change eco komfort",
		"10-25 22:00 change noc eco",
		"10-26 06:00 change komfort noc", "10-26 08:00 change eco komfort",
	)
}

func TestCalendarWeek_SetAppliesAtOnceAndFixedWins(t *testing.T) {
	got := runCalendar(t, "2026-11-10T12:00:00+01:00", weekModule+`
dom:on('ready', function()
  local v = dom:setWeek('komfort', { tu = { { from = '11:00', to = '13:00', value = 'komfort' } },
    we = { { from = '07:00', to = '24:00', value = 'komfort' } } }, { version = 0 })
  print('CAL set v' .. v .. ' now ' .. dom:value('komfort'))
  print('CAL ' .. days(dom:week('komfort')))
end)`, `[{"advance": 172800}]`)
	expectLines(t, got,
		"11-10 12:00 change eco nil", "11-10 12:00 ready",
		// Tuesday 11:00-13:00 is on now: the value follows at once
		"11-10 12:00 change komfort eco",
		"edit schedule komfort set v1 lua automation:module:kal",
		"set v1 now komfort",
		"mo  | tu 11:00-13:00=komfort | we 07:00-24:00=komfort | th  | fr  | sa  | su ",
		"11-10 13:00 change eco komfort",
		// Wednesday 11 November is a holiday: the module's fixed window keeps eco all day
	)
}

func TestCalendarWeek_SurvivesReloadAndResets(t *testing.T) {
	got := runCalendar(t, "2026-11-16T12:00:00+01:00", weekModule+`
local st = persist('weektest', { step = 0 })
dom:on('ready', function()
  st.step = st.step + 1
  local w = dom:week('komfort')
  print('CAL load ' .. st.step .. ' v' .. w.version .. ' edited ' .. tostring(w.edited) .. ' now ' .. dom:value('komfort'))
  if st.step == 1 then
    dom:setWeek('komfort', { mo = { { from = '00:00', to = '24:00', value = 'noc' } } }, { version = 0 })
  elseif st.step == 2 then
    dom:resetWeek('komfort', { version = 1 })
    print('CAL reset now ' .. dom:value('komfort') .. ' ' .. days(dom:week('komfort')):sub(1, 30))
  end
end)`, `[{"advance": 60}, {"reload": true}, {"advance": 60}, {"reload": true}, {"advance": 60}]`)
	expectLines(t, got,
		"11-16 12:00 change eco nil", "11-16 12:00 ready",
		"load 1 v0 edited false now eco",
		"11-16 12:00 change noc eco",
		"edit schedule komfort set v1 lua automation:module:kal",
		"11-16 12:01 change noc nil", "11-16 12:01 ready",
		"load 2 v1 edited true now noc",
		"11-16 12:01 change eco noc",
		"edit schedule komfort reset v2 lua automation:module:kal",
		"reset now eco mo 00:00-06:00=noc,06:00-08:00",
		"11-16 12:02 change eco nil", "11-16 12:02 ready",
		"load 3 v2 edited false now eco",
	)
}

func TestCalendarWeek_EventsFireEdit(t *testing.T) {
	got := runCalendar(t, "2026-11-16T12:00:00+01:00", weekModule+`
dom:on('ready', function()
  local id = dom:add({ id = 'wizyta', title = 'Wizyta', date = '2026-11-20', time = '10:00' })
  dom:update('wizyta', { title = 'Lekarz', date = '2026-11-20', time = '11:00' })
  print('CAL updated ' .. dom:next('wizyta').title .. ' ' .. datetime.format(datetime.fromTimestamp(dom:next('wizyta').at), '15:04'))
  print('CAL ' .. select(2, pcall(dom.update, dom, 'nie', { date = '2026-11-20' })):gsub('^.-update', 'update'))
  dom:skip('wizyta', '2026-11-20')
  dom:unskip('wizyta', '2026-11-20')
  dom:pause('2026-12-24..2026-12-26')
  dom:resume()
  dom:remove(id)
end)`, `[{"advance": 60}]`)
	var edits []string
	for _, l := range got {
		if strings.HasPrefix(l, "edit ") {
			edits = append(edits, l)
		}
	}
	if !strings.Contains(strings.Join(got, "\n"), "updated Lekarz 11:00") ||
		!strings.Contains(strings.Join(got, "\n"), `update: "nie" is not a user event`) {
		t.Errorf("update: %v", got)
	}
	expectLines(t, edits,
		"edit event wizyta add vnil lua automation:module:kal",
		"edit event wizyta update vnil lua automation:module:kal",
		"edit event wizyta skip vnil lua automation:module:kal",
		"edit event wizyta unskip vnil lua automation:module:kal",
		"edit event nil pause vnil lua automation:module:kal",
		"edit event nil resume vnil lua automation:module:kal",
		"edit event wizyta remove vnil lua automation:module:kal",
	)
}

func TestCalendarWeek_BuildErrors(t *testing.T) {
	for module, want := range map[string]string{
		`Calendar.new('dom'):holidays('pl'):schedule('t', { values = { 'a', 'b' }, default = 'a', { every = 'workday', hours = '06:00-08:00', value = 'b' } }):editable():build()`:                      `schedule t window 1: 'workday' follows the holidays`,
		`Calendar.new('dom'):schedule('t', { values = { 'a', 'b' }, default = 'a', { every = 'mo', time = '18:00', duration = '1h', value = 'b' }, { every = 'tu', value = 'b' } }):editable():build()`: `schedule t window 2: the weekly windows (every, hours, value) of an editable schedule come before the others (window 1)`,
		`Calendar.new('dom'):schedule('t', { values = { 'a', 'b' }, default = 'a', { every = 'mo', time = '18:00', duration = '1h', value = 'b' } }):editable():build()`:                                `schedule t: an editable schedule needs a weekly window`,
		`Calendar.new('dom'):schedule('t', { default = 0, { every = 'mo', hours = '06:00-08:00', value = 1 } }):editable():build()`:                                                                     `schedule t: an editable schedule needs its values`,
		`Calendar.new('dom'):schedule('t', { values = { 'a', 'b' }, default = 'a', { every = 'mo', value = 'b' } }):roles('x'):build()`:                                                                 `schedule t: roles is for an editable schedule: add :editable()`,
		`Calendar.new('dom'):schedule('t', { values = { 'a', 'b' }, default = 'a', { every = 'mo', value = 'b' } }):editable():roles()`:                                                                 `schedule t: roles expects names`,
		`Calendar.new('dom'):schedule('t', { values = { 'a', 'b' }, default = 'a', editable = 'yes', { every = 'mo', value = 'b' } })`:                                                                  `schedule t: editable must be true or false`,
		`Calendar.new('dom'):schedule('t', { values = { 'a', 'b' }, default = 'a', { every = 'mo', value = 'b' } }):build():setWeek('t', {})`:                                                           `schedule "t" is not editable`,
		"Calendar.new('dom'):event('x', { date = '2026-11-11' }):editable()":                                                                                                                            "editable() of the calendar goes right after Calendar.new(), before its events and schedules (a schedule's own: :schedule(id, { ... }):editable())",
		"Calendar.new('dom'):roles('rodzice')":    "roles() follows :editable()",
		"Calendar.new('dom'):build():week('nie')": `week: unknown schedule "nie"`,
	} {
		se, ws, _ := newTestSandbox(t)
		writeDraft(t, ws, "modules/kal.lua", module)
		res, err := se.RunScenario([]string{"modules/kal.lua"}, SandboxScenario{At: mustTime(t, "2026-10-20T10:00:00+02:00")})
		if err != nil {
			t.Fatal(err)
		}
		all := strings.Join(res.Errors, "\n")
		if res.Success || !strings.Contains(all, want) {
			t.Errorf("%s:\nwant error with %q, got success=%v errors=%s", module, want, res.Success, all)
		} else if !strings.Contains(all, "modules/kal.lua:1: ") {
			t.Errorf("%s:\nthe error does not name the module's line: %s", module, all)
		}
	}
}

func TestCalendarWeek_BadWeeksAreRefused(t *testing.T) {
	got := runCalendar(t, "2026-11-10T12:00:00+01:00", weekModule+`
local function try(week, opts)
  local ok, err = pcall(dom.setWeek, dom, 'komfort', week, opts)
  print('CAL ' .. (ok and 'saved' or tostring(err):gsub('^.-setWeek', 'setWeek')))
end
try({ mo = { { from = '06:00', to = '08:00', value = 'komfort' }, { from = '07:00', to = '09:00', value = 'noc' } } })
try({ mo = { { from = '06:00', to = '08:00', value = 'komfrt' } } })
try({ mo = { { from = '06:00', to = '08:00', value = 'eco' } } })
try({ mo = { { from = '08:00', to = '08:00', value = 'noc' } } })
try({ mo = { { from = '6', to = '08:00', value = 'noc' } } })
try({ mo = { { from = '06:00', to = '08:00', value = 'noc', x = 1 } } })
try({ pn = {} })
try({ mo = { { from = '06:00', to = '08:00', value = 'noc' } } }, { version = 3 })
print('CAL version ' .. dom:week('komfort').version .. ' edited ' .. tostring(dom:week('komfort').edited))`, `[{"advance": 60}]`)
	want := []string{
		"setWeek komfort: mo interval 2: overlaps the one before or is out of order",
		"setWeek komfort: mo interval 1: value komfrt is not one of the schedule's values",
		"setWeek komfort: mo interval 1: value eco is the default, leave the time out instead",
		"setWeek komfort: mo interval 1: from must be before to",
		"setWeek komfort: mo interval 1: from expected as HH:MM",
		`setWeek komfort: mo interval 1: unknown key "x" (from, to, value)`,
		`setWeek komfort: unknown day "pn" (mo, tu, we, th, fr, sa, su)`,
		"setWeek: version 3 is not the current one (0): the week changed",
		"version 0 edited false",
	}
	var lines []string
	for _, l := range got {
		if strings.HasPrefix(l, "setWeek") || strings.HasPrefix(l, "version") {
			lines = append(lines, l)
		}
	}
	expectLines(t, lines, want...)
}

// An error of add(), update() or week() names the line that called it, also inside a helper.
func TestCalendarWeek_ErrorsNameTheCallersLine(t *testing.T) {
	for module, want := range map[string]string{
		"local dom = Calendar.new('dom'):build()\nlocal function wrap(f) f() end\nwrap(function() dom:add({ title = 5, date = '2026-11-20' }) end)": "modules/kal.lua:3: Calendar dom:add: title must be a string",
		"local dom = Calendar.new('dom'):build()\nlocal function wrap(f) f() end\nwrap(function() dom:week('nie') end)":                             `modules/kal.lua:3: Calendar dom:week: unknown schedule "nie"`,
	} {
		se, ws, _ := newTestSandbox(t)
		writeDraft(t, ws, "modules/kal.lua", module)
		res, err := se.RunScenario([]string{"modules/kal.lua"}, SandboxScenario{At: mustTime(t, "2026-10-20T10:00:00+02:00")})
		if err != nil {
			t.Fatal(err)
		}
		if all := strings.Join(res.Errors, "\n"); !strings.Contains(all, want) {
			t.Errorf("want %q in %s", want, all)
		}
	}
}

// A calendar without editable() is as before: state saved by an older version (no schedules)
// loads, a chain ending in :schedule() builds, user ids go on from seq and a skipped day is
// left out of between().
func TestCalendarWeek_PlainCalendarUnchanged(t *testing.T) {
	got := runCalendar(t, "2026-11-20T10:00:00+01:00", `
local st = persist('calendar.dom', { events = { u1 = { title = 'Stare', date = '2026-11-20' } },
  skips = { ['u1@2026-11-20'] = true }, pauses = {}, seq = 1 })
local dom = Calendar.new('dom'):clock({ wait = false })
  :event('a', { every = 'day', time = '12:00' })
  :schedule('t', { default = 0, { every = 'day', hours = '13:00-15:00', value = 1 } })
  :build()
print('CAL built ' .. tostring(Calendar.get('dom') == dom))
print('CAL add ' .. dom:add({ title = 'Nowe', date = '2026-11-21' }))
local ids = {}
for _, e in ipairs(dom:between('2026-11-20', '2026-11-20')) do ids[#ids + 1] = e.id .. tostring(e.skipped) end
print('CAL between ' .. table.concat(ids, ','))
print('CAL saved ' .. type(st.schedules) .. ' ' .. tostring(next(st.schedules)))`, `[{"advance": 60}]`)
	expectLines(t, got,
		"built true",
		"add u2",
		"between anil",
		"saved table nil",
	)
}

// The agent's calendar tool says a schedule is editable and, once a person saved a week, that
// its hours come from that save (the module's code no longer decides them).
func TestCalendarWeek_AgendaTellsTheAgent(t *testing.T) {
	got := runCalendar(t, "2026-11-10T12:00:00+01:00", weekModule+`
local function show(tag)
  local a = json.decode(Calendar.API.agenda('dom', 'today'))
  local sc = a.calendars[1].schedules[1]
  print('CAL ' .. tag .. ' editable ' .. tostring(sc.editable) .. ' edited ' .. tostring(sc.edited and sc.edited.version))
end
dom:on('ready', function()
  show('before')
  dom:setWeek('komfort', { tu = { { from = '11:00', to = '13:00', value = 'komfort' } } })
  show('after')
end)`, `[{"advance": 60}]`)
	expectLines(t, got,
		"11-10 12:00 change eco nil", "11-10 12:00 ready",
		"before editable true edited nil",
		"11-10 12:00 change komfort eco",
		"edit schedule komfort set v1 lua automation:module:kal",
		"after editable true edited 1")
}
