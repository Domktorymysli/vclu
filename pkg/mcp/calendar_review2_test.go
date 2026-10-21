package mcp

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

// Second review of pkg/lua/runtime/core/Calendar.lua: handlers, rebuilds, Persist,
// the firing loop and queries on the sandbox's virtual clock.

// calLog2 prints with seconds (and the year when long), so a late firing shows.
const calendarLog2 = `
function calLog2(dom, fmt)
  fmt = fmt or '01-02 15:04:05'
  local function at() return datetime.format(datetime.now(), fmt) end
  for _, k in ipairs({ 'start', 'end', 'reminder' }) do
    dom:on(k, function(e)
      print('CAL ' .. at() .. ' ' .. k .. ' ' .. e.id .. '@' .. e.date .. (e.catchup and ' catchup' or ''))
    end)
  end
  dom:on('change', function(v, old) print('CAL ' .. at() .. ' change ' .. tostring(v) .. ' ' .. tostring(old)) end)
  dom:on('ready', function() print('CAL ' .. at() .. ' ready') end)
end
`

// runCal2 runs a scenario and returns the result without requiring success.
func runCal2(t *testing.T, at, module, steps string) *SandboxResult {
	t.Helper()
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/tz.lua", `datetime.setTimezone("Europe/Warsaw")`+calendarLog+calendarLog2)
	writeDraft(t, ws, "modules/kal.lua", module)
	return runScenario(t, se, []string{"modules/tz.lua", "modules/kal.lua"}, at, steps)
}

func runCal2OK(t *testing.T, at, module, steps string) []string {
	t.Helper()
	res := runCal2(t, at, module, steps)
	requireSuccess(t, res)
	return calLines(res)
}

// jitterOf mirrors Calendar.lua's jitterOf (djb2 mod 2^31-1 in float64).
func jitterOf(key string, span int) int {
	h := 5381.0
	for i := 0; i < len(key); i++ {
		h = math.Mod(h*33+float64(key[i]), 2147483647)
	}
	return int(math.Mod(h, float64(2*span+1))) - span
}

// ---------- 1. handlers ----------

func TestCalendarReview2_HandlerErrorDoesNotStopOthers(t *testing.T) {
	res := runCal2(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '10:01' })
  :event('b', { every = 'day', time = '10:02' })
  :build()
dom:on('start', function(e) error('boom ' .. e.id) end)
calLog2(dom)`, `[{"advance": 300}]`)
	expectLines(t, calLines(res), "10-20 10:00:00 ready", "10-20 10:01:00 start a@2026-10-20", "10-20 10:02:00 start b@2026-10-20")
	logs := strings.Join(res.Logs, "\n")
	if !strings.Contains(logs, "[CALENDAR] dom start a:") || !strings.Contains(logs, "boom a") || !strings.Contains(logs, "boom b") {
		t.Errorf("handler errors not logged:\n%s", logs)
	}
}

func TestCalendarReview2_UnsubscribeDuringEmit(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '10:01' })
  :event('b', { every = 'day', time = '10:02' })
  :build()
local u = {}
u.one = dom:on('start', function(e) print('CAL one ' .. e.id); u.one() end)
u.two = dom:on('start', function(e) print('CAL two ' .. e.id); u.three(); u.three() end)
u.three = dom:on('start', function(e) print('CAL three ' .. e.id) end)
dom:on('start', function(e) print('CAL four ' .. e.id) end)`, `[{"advance": 300}]`)
	// emit works on a copy: removed handlers still get the event in progress; a second
	// unsubscribe call removes nothing else
	expectLines(t, got, "one a", "two a", "three a", "four a", "two b", "four b")
}

func TestCalendarReview2_ChangesFromHandlers(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '10:01' })
  :event('b', { every = 'day', time = '10:05' })
  :event('c', { every = 'day', time = '10:06', tags = { 'x' } })
  :build()
calLog2(dom)
dom:on('start', 'a', function(e)
  dom:add({ id = 'nowy', date = e.date, time = '10:03' })
  dom:skip('b', e.date)
  dom:pause(e.date, { tags = { 'x' } })
end)`, `[{"advance": 600}]`)
	expectLines(t, got, "10-20 10:00:00 ready", "10-20 10:01:00 start a@2026-10-20", "10-20 10:03:00 start nowy@2026-10-20")
}

// An event added at runtime (dashboard, MCP, a timer) waits for the next minute check.
func TestCalendarReview2_AddedEventFiresLate(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:30+02:00", `
local dom = Calendar.new('dom'):event('a', { date = '2026-01-01' }):build()
calLog2(dom)
EventBus:getShared():setTimeout(function()
  dom:add({ id = 'swiatlo', date = '2026-10-20', time = '10:02', duration = '1m' })
end, 70 * 1000)`, `[{"advance": 300}]`)
	expectLines(t, got, "10-20 10:00:30 ready", "10-20 10:02:00 start swiatlo@2026-10-20", "10-20 10:03:00 end swiatlo@2026-10-20")
}

func TestCalendarReview2_ReadyOnceAndLateReadyHandler(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom'):event('a', { every = 'day', time = '10:01' }):build()
calLog2(dom)
EventBus:getShared():setTimeout(function()
  dom:on('ready', function() print('CAL late ready') end)
  print('CAL ready? ' .. tostring(dom:isReady()))
end, 30 * 1000)`, `[{"advance": 7200}]`)
	// a 'ready' handler added after ready is never called: check isReady() first
	expectLines(t, got, "10-20 10:00:00 ready", "ready? true", "10-20 10:01:00 start a@2026-10-20")
}

// ---------- 2. rebuild ----------

const timerCount = `
tc = { live = {}, n = 0, max = 0 }
local set, clr = __go_setTimeout, __go_clearTimer
__go_setTimeout = function(fn, ms)
  local box = {}
  box.id = set(function() if tc.live[box.id] then tc.live[box.id] = nil; tc.n = tc.n - 1 end; fn() end, ms)
  tc.live[box.id] = true; tc.n = tc.n + 1
  if tc.n > tc.max then tc.max = tc.n end
  return box.id
end
__go_clearTimer = function(id)
  if tc.live[id] then tc.live[id] = nil; tc.n = tc.n - 1 end
  return clr(id)
end
`

func TestCalendarReview2_RebuildStopsTheOldTimer(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", timerCount+`
Persist.DEBOUNCE_MS = 1
local function make(tag)
  local d = Calendar.new('dom'):event('a', { every = 'day', time = '10:05' }):build()
  d:on('start', function(e) print('CAL ' .. tag .. ' start ' .. e.id) end)
  d:on('change', function() end)
  return d
end
make('old')
EventBus:getShared():setTimeout(function()
  make('new')
  make('newest')
end, 60 * 1000)
EventBus:getShared():setTimeout(function() print('CAL timers ' .. tc.n .. ' list ' .. #Calendar.list()) end, 600 * 1000 + 500)`,
		`[{"advance": 700}]`)
	// one calendar = one timer (plus the printing one)
	expectLines(t, got, "newest start a", "timers 1 list 1")
}

func TestCalendarReview2_ReloadDuringAnEvent(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T08:30:00+02:00", `
local dom = Calendar.new('dom')
  :event('lekcja', { every = 'day', time = '09:00', duration = '2h', catchup = true })
  :event('swiatlo', { every = 'day', time = '09:00', duration = '2h' })
  :schedule('s', { values = { 'x', 'y' }, default = 'x', { every = 'day', hours = '09:00-11:00', value = 'y' } })
  :build()
calLog2(dom)`, `[{"advance": 3600}, {"reload": true}, {"advance": 7200}]`)
	expectLines(t, got,
		"10-20 08:30:00 change x nil", "10-20 08:30:00 ready",
		// at one instant events fire before the schedules' change
		"10-20 09:00:00 start lekcja@2026-10-20", "10-20 09:00:00 start swiatlo@2026-10-20",
		"10-20 09:00:00 change y x",
		// after the reload: change from nil again, start again only with catchup
		"10-20 09:30:00 change y nil", "10-20 09:30:00 start lekcja@2026-10-20 catchup", "10-20 09:30:00 ready",
		"10-20 11:00:00 end lekcja@2026-10-20", "10-20 11:00:00 end swiatlo@2026-10-20",
		"10-20 11:00:00 change x y",
	)
}

// ---------- 3. Persist ----------

func TestCalendarReview2_UserChangesSurviveReload(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '12:00', tags = { 'x' } })
  :event('b', { every = 'day', time = '12:01' })
  :event('c', { every = 'day', time = '12:02' })
  :build()
calLog2(dom)
dom:on('ready', function()
  local ev = {}
  for _, e in ipairs(dom:info().events) do ev[#ev + 1] = e.id .. ':' .. e.source end
  print('CAL events ' .. table.concat(ev, ',') .. ' pauses ' .. #dom:paused())
end)
local once = persist('test.once', { done = false })
EventBus:getShared():setTimeout(function()
  if once.done then return end
  once.done = true
  dom:add({ id = 'dentysta', title = 'Dentysta', date = '2026-10-20', time = '11:00', remind = '-5m' })
  dom:skip('b', '2026-10-20')
  dom:skip('c', '2026-10-20'); dom:unskip('c', '2026-10-20')
  dom:pause('2026-10-20', { tags = { 'x' } })
end, 5 * 1000)`, `[{"advance": 10}, {"reload": true}, {"advance": 9000}]`)
	expectLines(t, got,
		"10-20 10:00:00 ready", "events a:code,b:code,c:code pauses 0",
		"10-20 10:00:10 ready", "events a:code,b:code,c:code,dentysta:user pauses 1",
		"10-20 10:55:00 reminder dentysta@2026-10-20",
		"10-20 11:00:00 start dentysta@2026-10-20",
		"10-20 12:02:00 start c@2026-10-20",
	)
}

// Persist keeps whatever was saved; a calendar must build and run on odd shapes.
func TestCalendarReview2_OddPersistedStateStopsTheCalendar(t *testing.T) {
	for name, set := range map[string]string{
		"events string":    `st.events = 'x'`,
		"events list":      `st.events = { 'x', 5 }`,
		"pauses nil":       `st.pauses = nil; st.skips = nil; st.events = nil; st.seq = nil`,
		"pauses string":    `st.pauses = 'x'`,
		"pause without to": `st.pauses = { { from = '2026-10-01' } }`,
		"skips string":     `st.skips = 'x'`,
	} {
		t.Run(name, func(t *testing.T) {
			res := runCal2(t, "2026-10-20T10:00:00+02:00", `
local st = persist('calendar.dom', { events = {}, skips = {}, pauses = {}, seq = 0 })
`+set+`
local dom = Calendar.new('dom'):event('a', { every = 'day', time = '10:05' }):build()
calLog2(dom)`, `[{"advance": 600}]`)
			got := calLines(res)
			if len(got) != 2 || got[1] != "10-20 10:05:00 start a@2026-10-20" {
				t.Errorf("events did not fire:\n%s\nerrors: %s", strings.Join(got, "\n"), strings.Join(res.Errors, "\n"))
			}
		})
	}
}

// One bad user event (a title that is a table) stops every event of the calendar.
func TestCalendarReview2_ErrorInTheLoopStopsTheCalendar(t *testing.T) {
	res := runCal2(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('swiatlo', { every = 'day', time = '10:00', duration = '8h' })
  :build()
calLog2(dom)
assert(not pcall(dom.add, dom, { title = { 'x' }, date = '2026-10-20' }), 'a table title is refused')
dom:add({ id = 'zly', date = '2026-10-20', time = '11:00', remind = '-5m' })
dom._text = function() error('boom') end`,
		`[{"advance": 36000}]`)
	got := calLines(res)
	if !strings.Contains(strings.Join(got, "\n"), "10-20 18:00:00 end swiatlo") {
		t.Fatalf("the light never turns off:\n%s\nerrors: %s", strings.Join(got, "\n"), strings.Join(res.Errors, "\n"))
	}
}

// ---------- 4. the firing loop ----------

func TestCalendarReview2_SameSecondOrderedAndOnce(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('c', { every = 'day', time = '10:01' })
  :event('a', { every = 'day', time = '10:01' })
  :event('b', { every = 'day', time = '10:01' })
  :event('d', { date = '2026-10-20', time = '10:01', remind = '+0s' })
  :build()
calLog2(dom)`, `[{"advance": 300}]`)
	expectLines(t, got, "10-20 10:00:00 ready",
		"10-20 10:01:00 start a@2026-10-20", "10-20 10:01:00 start b@2026-10-20", "10-20 10:01:00 start c@2026-10-20",
		"10-20 10:01:00 start d@2026-10-20", "10-20 10:01:00 reminder d@2026-10-20")
}

// At one instant the occurrence that ends must end before the next one starts, or a
// handler that switches a light on at start and off at end leaves it off.
func TestCalendarReview2_StartBeforeEndAtTheSameInstant(t *testing.T) {
	got := runCal2OK(t, "2026-10-19T12:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('dzien', { every = 'day' })                                  -- whole days back to back
  :event('zmiana', { every = 'day', time = '06:00', duration = '24h' })
  :event('rano', { every = 'day', time = '06:00', duration = '6h' })
  :event('dzienna', { every = 'day', time = '12:00', duration = '6h' }) -- sorts before 'rano'
  :build()
light = { dzien = 0, zmiana = 0, swiatlo = 0 }
dom:on('start', function(e) local k = (e.id == 'rano' or e.id == 'dzienna') and 'swiatlo' or e.id; light[k] = 1 end)
dom:on('end', function(e) local k = (e.id == 'rano' or e.id == 'dzienna') and 'swiatlo' or e.id; light[k] = 0 end)
EventBus:getShared():setTimeout(function()
  EventBus:getShared():setTimeout(function()
    print('CAL ' .. light.dzien .. ' ' .. light.zmiana .. ' ' .. light.swiatlo)
  end, 3 * 3600 * 1000)
end, 24 * 3600 * 1000)`, `[{"advance": 100000}]`)
	// 10-20 15:00: every one is going on
	expectLines(t, got, "1 1 1")
}

func TestCalendarReview2_EventExactlyAtTheCheckBoundary(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T09:59:59.999+02:00", `
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '10:01' })
  :event('b', { every = 'day', time = '10:02' })
  :build()
calLog2(dom)`, `[{"advance": 59.999}, {"advance": 60}, {"advance": 60}, {"advance": 120}]`)
	expectLines(t, got, "10-20 10:00:00 ready", "10-20 10:01:00 start a@2026-10-20", "10-20 10:02:00 start b@2026-10-20")
}

func TestCalendarReview2_RemindersAfterStartAndBeforeTheModule(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('pranie', { date = '2026-10-20', time = '09:30', remind = '+1h' })  -- started before
  :event('kino', { date = '2026-10-20', time = '10:30', remind = { '-1h', '-10m', '+1h' } })
  :build()
calLog2(dom)`, `[{"advance": 10800}]`)
	// kino -1h was due at 09:30, before the module: not fired late
	expectLines(t, got, "10-20 10:00:00 ready",
		"10-20 10:20:00 reminder kino@2026-10-20",
		"10-20 10:30:00 start kino@2026-10-20", // same instant: by event id
		"10-20 10:30:00 reminder pranie@2026-10-20",
		"10-20 11:30:00 reminder kino@2026-10-20")
}

const jitterModule = `
local dom = Calendar.new('dom')
  :event('rolety', { every = 'day', time = '10:30', jitter = '10m' })
  :build()
calLog2(dom)`

func TestCalendarReview2_JitterSameAcrossReload(t *testing.T) {
	plain := runCal2OK(t, "2026-10-20T10:00:00+02:00", jitterModule, `[{"advance": 3600}]`)
	reloaded := runCal2OK(t, "2026-10-20T10:00:00+02:00", jitterModule, `[{"advance": 300}, {"reload": true}, {"advance": 3300}]`)
	j := jitterOf("rolety2026-10-20", 600)
	want := fmt.Sprintf("10-20 10:%02d:%02d start rolety@2026-10-20", 30+(j/60)-boolInt(j%60 < 0), ((j%60)+60)%60)
	expectLines(t, plain, "10-20 10:00:00 ready", want)
	expectLines(t, reloaded, "10-20 10:00:00 ready", "10-20 10:05:00 ready", want)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A catch-up after a reload compares with the occurrence's time, not its jittered start.
func TestCalendarReview2_CatchupIgnoresJitter(t *testing.T) {
	// find dates where the start is shifted late (fires twice) and early (never fires)
	late, early := "", ""
	tz, _ := time.LoadLocation("Europe/Warsaw")
	for d := 0; d < 365 && (late == "" || early == ""); d++ {
		date := time.Date(2026, 11, 1+d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		j := jitterOf("lekcja"+date, 600)
		if j >= 180 && late == "" {
			late = date
		}
		if j <= -180 && early == "" {
			early = date
		}
	}
	module := `
local dom = Calendar.new('dom')
  :event('lekcja', { every = 'day', time = '10:00', duration = '1h', catchup = true, jitter = '10m' })
  :build()
calLog2(dom)`
	// late: reload at 10:01, after 10:00 but before the jittered start
	at := func(date, hm string) string {
		d, _ := time.ParseInLocation("2006-01-02 15:04", date+" "+hm, tz)
		return d.Format(time.RFC3339)
	}
	got := runCal2OK(t, at(late, "09:59"), module, `[{"advance": 120}, {"reload": true}, {"advance": 1800}]`)
	starts := 0
	for _, l := range got {
		if strings.Contains(l, "start lekcja") {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%s (shifted late): %d starts:\n%s", late, starts, strings.Join(got, "\n"))
	}
	// early: reload at 09:58, after the jittered start and before 10:00
	got = runCal2OK(t, at(early, "09:40"), module, `[{"advance": 1080}, {"reload": true}, {"advance": 1800}]`)
	if n := strings.Count(strings.Join(got, "\n"), "start lekcja"); n != 2 {
		t.Errorf("%s (shifted early): want the start and its catch-up after the reload, got:\n%s", early, strings.Join(got, "\n"))
	}
}

// Skipping, pausing or removing an occurrence that is going on must still end it.
// Cancelling an occurrence that is going on ends it now: what its start switched on goes off.
func TestCalendarReview2_SkipPauseRemoveInProgressEndsNow(t *testing.T) {
	for name, change := range map[string]string{
		"skip":   `dom:skip('swiatlo', '2026-10-20')`,
		"pause":  `dom:pause('2026-10-20')`,
		"remove": `dom:remove('swiatlo')`,
	} {
		t.Run(name, func(t *testing.T) {
			got := runCal2OK(t, "2026-10-20T17:00:00+02:00", `
local dom = Calendar.new('dom'):build()
dom:add({ id = 'swiatlo', date = '2026-10-20', time = '18:00', duration = '4h' })
calLog2(dom)
EventBus:getShared():setTimeout(function() `+change+` end, 2 * 3600 * 1000)`, `[{"advance": 25200}]`)
			expectLines(t, got, "10-20 17:00:00 ready", "10-20 18:00:00 start swiatlo@2026-10-20", "10-20 19:00:00 end swiatlo@2026-10-20")
		})
	}
}

func TestCalendarReview2_PauseByTags(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('szkola', { every = 'day', time = '10:01', tags = { 'dzieci', 'rano' } })
  :event('praca', { every = 'day', time = '10:02', tags = 'dorosli' })
  :event('bez', { every = 'day', time = '10:03' })
  :build()
calLog2(dom)
dom:pause('2026-10-20', { tags = 'rano' })
dom:pause('2026-10-21..2026-10-21')
dom:on('ready', function()
  local p = dom:paused()
  print('CAL paused ' .. #p .. ' ' .. p[1].from .. ' ' .. p[1].tags[1] .. ' ' .. tostring(p[2].tags))
  local u = {}
  for _, e in ipairs(dom:upcoming(2)) do u[#u + 1] = e.id .. (e.paused and '-' or '+') end
  print('CAL upcoming ' .. table.concat(u, ','))
end)
EventBus:getShared():setTimeout(function() dom:resume(); print('CAL resumed ' .. #dom:paused()) end, 23 * 3600 * 1000)`,
		`[{"advance": 86400}, {"advance": 600}]`)
	expectLines(t, got,
		"10-20 10:00:00 ready",
		"paused 2 2026-10-20 rano nil",
		"upcoming szkola-,praca+,bez+,szkola-,praca-,bez-",
		"10-20 10:02:00 start praca@2026-10-20", "10-20 10:03:00 start bez@2026-10-20",
		"resumed 0",
		"10-21 10:01:00 start szkola@2026-10-21",
		"10-21 10:02:00 start praca@2026-10-21", "10-21 10:03:00 start bez@2026-10-21",
	)
}

// ---------- 5. long run ----------

func TestCalendarReview2_LongRunOneTimerAndTheSpringChange(t *testing.T) {
	var steps []string
	for i := 0; i < 40; i++ {
		steps = append(steps, `{"advance": 864000}`)
	}
	got := runCal2OK(t, "2026-10-21T12:00:00+02:00", timerCount+`
Calendar.CHECK_S = 6 * 3600   -- 400 days of minute checks exceed the sandbox's callback budget
local dom = Calendar.new('dom'):holidays('pl'):location(52.23, 21.01)
  :event('czas-letni', { every = 'year', month = 3, on = 'last su', time = '02:00' })
  :event('rolety', { every = 'day', time = 'sunset' })
  :schedule('s', { values = { 'x', 'y' }, default = 'x', { every = 'workday', hours = '06:00-22:00', value = 'y' } })
  :build()
dom:on('start', 'czas-letni', function(e) print('CAL ' .. datetime.format(datetime.now(), '2006-01-02 15:04:05 MST') .. ' start ' .. e.date) end)
cnt = { rolety = 0, change = 0 }
dom:on('start', 'rolety', function() cnt.rolety = cnt.rolety + 1 end)
dom:on('change', function() cnt.change = cnt.change + 1 end)
-- timeouts are capped at 24 h: count the days with a chain
local day = { n = 0 }
day.step = function()
  day.n = day.n + 1
  if day.n < 399 then EventBus:getShared():setTimeout(day.step, 86400 * 1000); return end
  print('CAL timers ' .. tc.n .. ' max<=3 ' .. tostring(tc.max <= 3) .. ' rolety ' .. cnt.rolety .. ' change>500 ' .. tostring(cnt.change > 500))
end
EventBus:getShared():setTimeout(day.step, 86400 * 1000)`, "["+strings.Join(steps, ",")+"]")
	// 2027-03-28 02:00 does not exist in Warsaw: it fires at 03:00 CEST, once
	expectLines(t, got, "2027-03-28 03:00:00 CEST start 2027-03-28", "timers 1 max<=3 true rolety 399 change>500 true")
}

// ---------- 6. clock trust ----------

func TestCalendarReview2_BeforeTheClockIsTrusted(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
clock = { ok = false }
__go_calendar_clock = function() if clock.ok then return true end return false, 'not synchronised' end
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '10:00', duration = '1h', catchup = true })
  :schedule('s', { values = { 'x', 'y' }, default = 'x', { every = 'day', hours = '09:00-12:00', value = 'y' } })
  :build()
calLog2(dom)
local function show(tag)
  local i = dom:info()
  print('CAL ' .. tag .. ' ' .. tostring(dom:value('s')) .. ' ' .. tostring(dom:isActive('a')) .. ' ' ..
    type(dom:nextChange('s')) .. ' ' .. tostring(i.ready) .. ' ' .. tostring(i.waiting) .. ' ' .. tostring(i.schedules[1].value))
end
show('before')
EventBus:getShared():setTimeout(function() show('waiting') end, 60 * 1000)
EventBus:getShared():setTimeout(function() clock.ok = true end, 120 * 1000)
EventBus:getShared():setTimeout(function() clock.ok = false; show('after') end, 300 * 1000)`, `[{"advance": 3700}]`)
	expectLines(t, got,
		"before nil nil nil false nil nil",
		"waiting nil nil nil false not synchronised nil",
		"10-20 10:02:00 change y nil", "10-20 10:02:00 start a@2026-10-20 catchup", "10-20 10:02:00 ready",
		// once ready, a clock that loses sync again does not stop the calendar
		"after y true table true nil y",
		"10-20 11:00:00 end a@2026-10-20",
	)
}

// ---------- 7. queries ----------

func TestCalendarReview2_QueriesWithTagsAndDSTSegments(t *testing.T) {
	got := runCal2OK(t, "2026-10-24T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :event('a', { every = 'day', time = '12:00', tags = { 'x', 'y' } })
  :event('b', { every = 'day', time = '13:00', tags = 'y' })
  :event('c', { every = 'day', time = '09:00', duration = '2h' })
  :schedule('s', { values = { 'n', 'k' }, default = 'n', { every = 'day', hours = '01:00-04:00', value = 'k' } })
  :build()
local function ids(l) local o = {} for _, e in ipairs(l) do o[#o+1] = e.id .. '@' .. e.date:sub(9) end return table.concat(o, ',') end
local function segs(date)
  local o = {}
  for _, s in ipairs(dom:segments('s', date)) do o[#o + 1] = s.value .. ':' .. ((s.to - s.from) / 3600) end
  return table.concat(o, ' ')
end
dom:on('ready', function()
  print('CAL today ' .. ids(dom:today()) .. ' | ' .. ids(dom:today({ tags = 'x' })))
  print('CAL upcoming ' .. ids(dom:upcoming({ tags = { 'x', 'y' } })))
  print('CAL between ' .. ids(dom:between('2026-10-25', '2026-10-25', { tags = 'y' })))
  print('CAL autumn ' .. segs('2026-10-25'))
  print('CAL spring ' .. segs('2027-03-28'))
  print('CAL plain ' .. segs('2026-10-26'))
end)`, `[{"advance": 1}]`)
	expectLines(t, got,
		"today c@24,a@24,b@24 | a@24",
		"upcoming a@24,b@24,a@25,b@25,a@26,b@26,a@27,b@27,a@28,b@28,a@29,b@29,a@30,b@30",
		"between a@25,b@25",
		"autumn n:1 k:4 n:20",
		"spring n:1 k:2 n:20",
		"plain n:1 k:3 n:20",
	)
}

func TestCalendarReview2_NextChangeSkipsWindowsWithTheSameValue(t *testing.T) {
	got := runCal2OK(t, "2026-10-20T10:00:00+02:00", `
local dom = Calendar.new('dom')
  :schedule('t', { values = { 'droga', 'tania', 'szczyt' }, default = 'droga',
     { every = 'day', hours = '11:00-13:00', value = 'tania' },
     { every = 'day', hours = '13:00-15:00', value = 'tania' },
     { every = 'day', hours = '15:00-16:00', value = 'droga' },
     { every = 'day', hours = '16:30-17:00', value = 'szczyt' } })
  :build()
local function hm(ts) return datetime.format(datetime.fromTimestamp(ts), '15:04') end
dom:on('ready', function()
  local n = dom:nextChange('t'); print('CAL ' .. n.value .. ' ' .. hm(n.at))
end)
EventBus:getShared():setTimeout(function()
  local n = dom:nextChange('t'); print('CAL ' .. dom:value('t') .. ' -> ' .. n.value .. ' ' .. hm(n.at))
  EventBus:getShared():setTimeout(function()
    local m = dom:nextChange('t'); print('CAL ' .. dom:value('t') .. ' -> ' .. m.value .. ' ' .. hm(m.at))
  end, 3.5 * 3600 * 1000)
  local i = dom:info()
  print('CAL info ' .. i.id .. ' ' .. tostring(i.ready) .. ' ' .. i.schedules[1].id .. '=' .. i.schedules[1].value .. ' ' .. #i.events .. ' ' .. #i.pauses)
end, 2 * 3600 * 1000)`, `[{"advance": 20000}]`)
	// 13:00 (tania to tania) and 16:00 (droga window to the default droga) are not changes
	expectLines(t, got, "tania 11:00", "tania -> droga 15:00", "info dom true t=tania 0 0", "droga -> szczyt 16:30")
}
