package mcp

import (
	"strings"
	"testing"
)

// Contract of the Lua Calendar (pkg/lua/runtime/core/Calendar.lua) as a module sees it:
// which events fire, at what local time, on the sandbox's virtual clock.

const calendarLog = `
local function at() return datetime.format(datetime.now(), '01-02 15:04') end
function calLog(dom)
  for _, k in ipairs({ 'start', 'end', 'reminder' }) do
    dom:on(k, function(e)
      print('CAL ' .. at() .. ' ' .. k .. ' ' .. e.id .. (e.catchup and ' catchup' or '') .. (e.text and (' | ' .. e.text) or ''))
    end)
  end
  dom:on('change', function(v, old, x) print('CAL ' .. at() .. ' change ' .. tostring(v) .. ' ' .. tostring(old)) end)
  dom:on('ready', function() print('CAL ' .. at() .. ' ready') end)
end
`

func runCalendar(t *testing.T, at, module, steps string) []string {
	t.Helper()
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/tz.lua", `datetime.setTimezone("Europe/Warsaw")`+calendarLog)
	writeDraft(t, ws, "modules/kal.lua", module)
	res := runScenario(t, se, []string{"modules/tz.lua", "modules/kal.lua"}, at, steps)
	requireSuccess(t, res)
	var out []string
	for _, l := range res.Logs {
		if i := strings.Index(l, "CAL "); i >= 0 {
			out = append(out, l[i+4:])
		}
	}
	return out
}

func expectLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCalendar_EventsFireAtLocalTime(t *testing.T) {
	got := runCalendar(t, "2026-10-26T12:00:00+01:00", `
local dom = Calendar.new('dom'):holidays('pl')
  :event('bio', { title = 'Bio', every = 'tu', weeks = 2, from = '2026-10-27', time = '06:30', remind = '-12h' })
  :event('szkola', { every = 'workday', time = '08:00', duration = '6h' })
  :build()
calLog(dom)`, `[{"advance": 1382400}]`) // 16 days
	expectLines(t, got,
		"10-26 12:00 ready",
		// the school day that began before the calendar existed still ends
		"10-26 14:00 end szkola",
		"10-26 18:30 reminder bio | Bio 27.10 06:30",
		"10-27 06:30 start bio",
		"10-27 08:00 start szkola", "10-27 14:00 end szkola",
		"10-28 08:00 start szkola", "10-28 14:00 end szkola",
		"10-29 08:00 start szkola", "10-29 14:00 end szkola",
		"10-30 08:00 start szkola", "10-30 14:00 end szkola",
		// 11-02 Monday; 11-01 is Sunday anyway
		"11-02 08:00 start szkola", "11-02 14:00 end szkola",
		"11-03 08:00 start szkola", "11-03 14:00 end szkola",
		"11-04 08:00 start szkola", "11-04 14:00 end szkola",
		"11-05 08:00 start szkola", "11-05 14:00 end szkola",
		"11-06 08:00 start szkola", "11-06 14:00 end szkola",
		"11-09 08:00 start szkola", "11-09 14:00 end szkola",
		"11-09 18:30 reminder bio | Bio 10.11 06:30",
		"11-10 06:30 start bio",
		"11-10 08:00 start szkola", "11-10 14:00 end szkola",
		// 11-11 Independence Day: no school
	)
}

func TestCalendar_ScheduleChanges(t *testing.T) {
	got := runCalendar(t, "2026-10-23T12:00:00+02:00", `
local dom = Calendar.new('dom'):holidays('pl')
  :schedule('taryfa', { default = 'droga',
     { every = 'workday', hours = '13:00-15:00', value = 'tania' },
     { every = 'day', hours = '22:00-06:00', value = 'tania' },
     { every = 'weekend', value = 'tania' } })
  :build()
calLog(dom)
EventBus:getShared():setTimeout(function() print('CAL value ' .. dom:value('taryfa')) end, 3600 * 1000 * 2)`, `[{"advance": 259200}]`)
	// Friday to Monday; 25 Oct is the autumn DST change
	expectLines(t, got,
		"10-23 12:00 change droga nil", "10-23 12:00 ready",
		"10-23 13:00 change tania droga",
		"value tania",
		"10-23 15:00 change droga tania",
		"10-23 22:00 change tania droga",
		"10-26 06:00 change droga tania",
	)
}

func TestCalendar_ExceptMovesSkipAndPause(t *testing.T) {
	got := runCalendar(t, "2026-11-01T12:00:00+01:00", `
local dom = Calendar.new('dom')
  :event('bio', { every = 'tu', time = '06:30', moves = { ['2026-11-10'] = '2026-11-12' }, except = { '2026-11-17' } })
  :event('silownia', { every = { 'mo', 'we' }, time = '18:00', tags = { 'sport' } })
  :build()
dom:skip('bio', '2026-11-03')
dom:pause('2026-11-09..2026-11-11', { tags = { 'sport' } })
calLog(dom)`, `[{"advance": 1728000}]`) // 20 days
	expectLines(t, got,
		"11-01 12:00 ready",
		"11-02 18:00 start silownia",
		"11-04 18:00 start silownia",
		"11-12 06:30 start bio",
		"11-16 18:00 start silownia",
		"11-18 18:00 start silownia",
	)
}

func TestCalendar_CatchupAndNotify(t *testing.T) {
	got := runCalendar(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :notify({ send = function(self, e) print('CAL notify ' .. e.id .. ' ' .. e.offset) end })
  :event('szkola', { every = 'day', time = '08:00', duration = '6h', catchup = true })
  :event('dentysta', { date = '2026-10-20', time = '15:30', remind = { '-1h', '-10m' },
     notify = function(e) print('CAL own ' .. e.id .. ' ' .. e.offset) end })
  :event('kino', { date = '2026-10-20', time = '20:00', remind = '-30m' })
  :build()
calLog(dom)`, `[{"advance": 43200}]`)
	expectLines(t, got,
		"10-20 10:00 start szkola catchup",
		"10-20 10:00 ready",
		"10-20 14:00 end szkola",
		"own dentysta -1h", "10-20 14:30 reminder dentysta | dentysta 20.10 15:30",
		"own dentysta -10m", "10-20 15:20 reminder dentysta | dentysta 20.10 15:30",
		"10-20 15:30 start dentysta",
		"notify kino -30m", "10-20 19:30 reminder kino | kino 20.10 20:00",
		"10-20 20:00 start kino",
	)
}

func TestCalendar_Queries(t *testing.T) {
	got := runCalendar(t, "2026-11-10T10:00:00+01:00", `
local dom = Calendar.new('dom'):holidays('pl')
  :event('szkola', { title = 'Szkoła', every = 'workday', time = '08:00', duration = '6h', tags = { 'dzieci' } })
  :event('goscie', { date = '2026-11-14..2026-11-16' })
  :schedule('ogrzewanie', { default = 'eco', { every = 'workday', hours = '06:00-22:00', value = 'komfort' } })
  :build()
local function ids(l) local o = {} for _, e in ipairs(l) do o[#o+1] = e.id .. '@' .. e.date end return table.concat(o, ',') end
print('CAL before ready ' .. tostring(dom:value('ogrzewanie')) .. ' ' .. tostring(dom:isActive('szkola')))
dom:on('ready', function()
  print('CAL today ' .. ids(dom:today()))
  print('CAL upcoming ' .. ids(dom:upcoming(7, { tags = 'dzieci' })))
  print('CAL all ' .. ids(dom:upcoming(7)))
  print('CAL next ' .. dom:next('szkola').date .. ' ' .. dom:next('goscie').date)
  print('CAL active ' .. tostring(dom:isActive('szkola')) .. ' ' .. dom:value('ogrzewanie'))
  print('CAL workday ' .. tostring(dom:isWorkday('2026-11-11')) .. ' ' .. tostring(dom:isHoliday('2026-11-11')) .. ' ' .. tostring(dom:isWorkday('2026-11-12')))
  local id = dom:add({ title = 'Dentysta', date = '2026-11-12', time = '15:00' })
  print('CAL added ' .. id .. ' ' .. ids(dom:upcoming(3)))
  dom:remove(id)
  print('CAL removed ' .. ids(dom:upcoming(3)))
  print('CAL get ' .. tostring(Calendar.get('dom') == dom) .. ' ' .. #Calendar.list())
end)`, `[{"advance": 1}]`)
	expectLines(t, got,
		"before ready nil nil",
		"today szkola@2026-11-10",
		"upcoming szkola@2026-11-10,szkola@2026-11-12,szkola@2026-11-13,szkola@2026-11-16,szkola@2026-11-17",
		"all szkola@2026-11-10,szkola@2026-11-12,szkola@2026-11-13,goscie@2026-11-14,szkola@2026-11-16,szkola@2026-11-17",
		"next 2026-11-12 2026-11-14",
		"active true komfort",
		"workday false true true",
		"added u1 szkola@2026-11-10,szkola@2026-11-12,u1@2026-11-12,szkola@2026-11-13",
		"removed szkola@2026-11-10,szkola@2026-11-12,szkola@2026-11-13",
		"get true 1",
	)
}

func TestCalendar_BuildErrorsNameTheEvent(t *testing.T) {
	for module, want := range map[string]string{
		`Calendar.new('dom'):event('bio', { every = 'tue', time = '06:30' }):build()`:                  `event bio: every "tue"`,
		`Calendar.new('dom'):event('bio', { every = 'tu', tme = '06:30' }):build()`:                    `event bio: unknown option "tme"`,
		`Calendar.new('dom'):event('a', { date = '2026-01-01' }):event('a', { date = '2026-01-02' })`:  `duplicate id "a"`,
		`Calendar.new('dom'):event('bio', { every = 'tu', weeks = 2 }):build()`:                        `event bio: an interval`,
		`Calendar.new('dom'):schedule('t', { { every = 'day', hours = '13-15', value = 1 } }):build()`: `hours expected`,
		`Calendar.new('dom'):holidays('de')`:                                                           `unknown holidays "de"`,
		`Calendar.new('dom'):event('x', { date = '2026-01-01' }):build():on('stat', function() end)`:   `unknown kind "stat"`,
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
		}
	}
}

func TestCalendar_WaitsForTrustedClock(t *testing.T) {
	got := runCalendar(t, "2026-10-20T10:00:00+02:00", `
clockOk = false
__go_calendar_clock = function() if clockOk then return true end return false, 'not synchronised' end
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '10:01' })
  :event('b', { every = 'day', time = '10:05' })
  :build()
calLog(dom)
print('CAL value before ' .. tostring(dom:isActive('a')) .. ' ' .. tostring(dom:isReady()))
EventBus:getShared():setTimeout(function() clockOk = true end, 3 * 60 * 1000)`, `[{"advance": 600}]`)
	expectLines(t, got,
		"value before nil false",
		"10-20 10:03 ready", // polled every 10 s; 10:01 passed while waiting and does not fire
		"10-20 10:05 start b",
	)
}

func TestCalendar_StartsAfterMaxWait(t *testing.T) {
	got := runCalendar(t, "2026-10-20T10:00:00+02:00", `
__go_calendar_clock = function() return false, 'not synchronised' end
local dom = Calendar.new('dom'):event('a', { every = 'day', time = '10:20' }):build()
calLog(dom)`, `[{"advance": 1500}]`)
	expectLines(t, got, "10-20 10:10 ready", "10-20 10:20 start a")
}

func TestCalendar_WaitFalseSkipsTheGuard(t *testing.T) {
	got := runCalendar(t, "2026-10-20T10:00:00+02:00", `
__go_calendar_clock = function() return false, 'not synchronised' end
local dom = Calendar.new('dom'):clock({ wait = false }):event('a', { every = 'day', time = '10:01' }):build()
calLog(dom)`, `[{"advance": 120}]`)
	expectLines(t, got, "10-20 10:00 ready", "10-20 10:01 start a")
}

func TestCalendar_ClockJumpSkipsMissedMoments(t *testing.T) {
	got := runCalendar(t, "2026-10-20T10:00:00+02:00", `
local real = __go_time_ms
jump = { ms = 0 }
__go_time_ms = function() return real() + jump.ms end
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '11:00' })
  :event('b', { every = 'day', time = '14:00' })
  :event('lekcja', { every = 'day', time = '12:00', duration = '2h', catchup = true })
  :schedule('s', { default = 'x', { every = 'day', hours = '12:00-18:00', value = 'y' } })
  :build()
calLog(dom)
-- at 10:30 NTP moves the clock 2 h forward
EventBus:getShared():setTimeout(function() jump.ms = 2 * 3600 * 1000 end, 30 * 60 * 1000)`, `[{"advance": 7260}]`)
	// times printed are the virtual clock; the wall clock is 2 h ahead after 10:30
	expectLines(t, got,
		"10-20 10:00 change x nil", "10-20 10:00 ready",
		// the next check (at most a minute later) sees the jump: 11:00 and 12:00 are
		// skipped, the schedule and catch-up events follow the new time
		"10-20 10:30 change y x", "10-20 10:30 start lekcja catchup",
		"10-20 12:00 start b", "10-20 12:00 end lekcja",
	)
}

func TestCalendar_ScheduleQueries(t *testing.T) {
	got := runCalendar(t, "2026-10-23T12:00:00+02:00", `
local dom = Calendar.new('dom'):holidays('pl')
  :schedule('taryfa', { title = 'Taryfa', default = 'droga', colors = { tania = 'success', droga = 'danger' },
     { every = 'workday', hours = '13:00-15:00', value = 'tania' },
     { every = 'day', hours = '22:00-06:00', value = 'tania' },
     { every = 'weekend', value = 'tania' } })
  :build()
local function hm(ts) return datetime.format(datetime.fromTimestamp(ts), '01-02 15:04') end
print('CAL before ' .. tostring(dom:nextChange('taryfa')))
dom:on('ready', function()
  local n = dom:nextChange('taryfa')
  print('CAL next ' .. n.value .. ' ' .. hm(n.at) .. ' color ' .. dom:color('taryfa') .. ' ' .. dom:color('taryfa', 'tania'))
  local parts = {}
  for _, s in ipairs(dom:segments('taryfa', dom:day().date)) do parts[#parts + 1] = s.value .. '@' .. hm(s.from) .. '-' .. hm(s.to) .. ':' .. tostring(s.color) end
  print('CAL segments ' .. table.concat(parts, ' '))
end)
EventBus:getShared():setTimeout(function()
  -- Friday 22:00 night window runs into the weekend: the next change is Monday 06:00
  local n = dom:nextChange('taryfa')
  print('CAL friday ' .. n.value .. ' ' .. hm(n.at))
end, 11 * 3600 * 1000)
`, `[{"advance": 43200}]`)
	expectLines(t, got,
		"before nil",
		"next tania 10-23 13:00 color danger success",
		"segments tania@10-23 00:00-10-23 06:00:success droga@10-23 06:00-10-23 13:00:danger tania@10-23 13:00-10-23 15:00:success droga@10-23 15:00-10-23 22:00:danger tania@10-23 22:00-10-24 00:00:success",
		"friday droga 10-26 06:00",
	)
}

func TestCalendar_YearlyOnBetweenAndDay(t *testing.T) {
	got := runCalendar(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom'):holidays('pl')
  :event('czas', { title = 'Zmiana czasu', every = 'year', month = 10, on = 'last su', time = '03:00' })
  :event('goscie', { date = '2026-11-14..2026-11-16' })
  :build()
local function ids(l) local o = {} for _, e in ipairs(l) do o[#o+1] = e.id .. '@' .. e.date .. '..' .. e.last end return table.concat(o, ',') end
dom:on('ready', function()
  print('CAL next ' .. dom:next('czas').date)
  print('CAL between ' .. ids(dom:between('2026-10-25', '2026-11-15')))
  print('CAL ts ' .. #dom:between(dom:day('2026-11-15').starts, dom:day('2026-11-15').ends))
  local d = dom:day('2026-11-11')
  print('CAL day ' .. d.date .. ' ' .. d.weekday .. ' ' .. tostring(d.holiday) .. ' ' .. tostring(d.workday) .. ' ' .. tostring(d.ends - d.starts))
  print('CAL names ' .. d.holidayName .. ' | ' .. tostring(dom:day('2026-10-20').holidayName) .. ' | ' .. tostring(#d.namedays > 3) .. ' ' .. tostring(table.concat(dom:day('2026-12-24').namedays, ','):find('Adam') ~= nil))
  print('CAL plain ' .. tostring(Calendar.new('x'):event('a', { date = '2026-01-01' }):build():day('2026-11-11').namedays))
  local ok, err = pcall(dom.between, dom, '2026-10-25', 'jutro')
  print('CAL bad ' .. tostring(ok) .. ' ' .. (tostring(err):gsub('^.-(Calendar dom)', '%1')))
end)`, `[{"advance": 1}]`)
	expectLines(t, got,
		"next 2026-10-25",
		"between czas@2026-10-25..2026-10-25,goscie@2026-11-14..2026-11-16",
		"ts 1",
		"day 2026-11-11 we true false 86400",
		"names Święto Niepodległości | nil | true true",
		"plain nil",
		"bad false Calendar dom:between: expected a timestamp or YYYY-MM-DD",
	)
}

func TestCalendar_SunTimes(t *testing.T) {
	got := runCalendar(t, "2026-10-20T12:00:00+02:00", `
local dom = Calendar.new('dom'):location(52.2297, 21.0122)
  :event('rolety', { every = 'day', time = 'sunset-30m' })
  :schedule('swiatlo', { default = 'off', { every = 'day', hours = { 'sunset', 'sunrise+15m' }, value = 'on' } })
  :build()
calLog(dom)
local s = dom:sun('2026-10-20')
local function hm(ts) return datetime.format(datetime.fromTimestamp(ts), '15:04') end
print('CAL sun ' .. hm(s.sunrise) .. ' ' .. hm(s.sunset) .. ' ' .. tostring(s.dayLength > 10 * 3600 and s.dayLength < 11 * 3600))
print('CAL polar ' .. tostring(Calendar.new('t'):location(69.65, 18.96):event('x', { date = '2026-01-01' }):build():sun('2026-12-21').polar))
print('CAL none ' .. tostring(Calendar.new('n'):event('x', { date = '2026-01-01' }):build():sun()))
local ok, err = pcall(function() Calendar.new('e'):event('x', { every = 'day', time = 'sunset' }):build() end)
print('CAL nolocation ' .. tostring(ok) .. ' ' .. tostring(err:find('location', 1, true) ~= nil))`, `[{"advance": 86400}]`)
	// times printed are the sandbox clock; sunset in Warsaw on 20 Oct 2026 is 17:33 CEST
	expectLines(t, got,
		"sun 07:07 17:33 true",
		"polar night",
		"none nil",
		"nolocation false true",
		"10-20 12:00 change off nil", "10-20 12:00 ready",
		"10-20 17:03 start rolety",
		"10-20 17:33 change on off",
		"10-21 07:24 change off on",
	)
}

func TestCalendar_SunSkyAndDusk(t *testing.T) {
	got := runCalendar(t, "2026-06-21T06:00:00+02:00", `
local dom = Calendar.new('dom'):location(52.2297, 21.0122)
  :event('swiatla', { every = 'day', time = 'dusk' })
  :schedule('rolety', { default = 'otwarte',
     { every = 'day', sun = { azimuth = { 135, 225 }, elevation = 15 }, value = 'zaslon' } })
  :build()
calLog(dom)
local s = dom:sun()
local function hm(ts) return datetime.format(datetime.fromTimestamp(ts), '15:04') end
print('CAL dusk ' .. tostring(s.dusk > s.sunset) .. ' ' .. tostring(s.dawn < s.sunrise))
local p = dom:sunPosition(s.noon)
print('CAL noon ' .. tostring(p.azimuth > 178 and p.azimuth < 182) .. ' ' .. tostring(p.elevation > 60 and p.elevation < 62))
local ok, err = pcall(function() Calendar.new('e'):location(52, 21):event('x', { every = 'day', sun = { azimuth = 180 } }):build() end)
print('CAL bad ' .. tostring(ok) .. ' ' .. tostring(err:find('azimuth expects', 1, true) ~= nil))`, `[{"advance": 86400}]`)
	if len(got) != 8 || got[0] != "dusk true true" || got[1] != "noon true true" || got[2] != "bad false true" ||
		got[3] != "06-21 06:00 change otwarte nil" || got[4] != "06-21 06:00 ready" ||
		!strings.HasSuffix(got[5], "change zaslon otwarte") || !strings.HasSuffix(got[6], "change otwarte zaslon") ||
		!strings.HasSuffix(got[7], "start swiatla") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
	t.Log(strings.Join(got, "\n"))
}
