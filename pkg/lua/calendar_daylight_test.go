package lua

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// Contract of cal:isDay(at) / cal:isNight(at) and Builder:daylight (#171), on a fixed clock.

var daylightZone = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		panic(err)
	}
	return loc
}()

// daylightCal builds calendar dom from builder (the chain after Calendar.new('dom')) and runs
// its first check, so that the clock is trusted.
func daylightCal(t *testing.T, builder string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	SetClock(engine.state, fixedClock{time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC)})
	mustExec(t, engine, `datetime.setTimezone("Europe/Warsaw")`)
	mustExec(t, engine, `dom = Calendar.new('dom'):clock({ wait = false })`+builder+`:build() dom:_tick()`)
	return engine
}

// dayAt says "day" or "night" at the local wall clock "YYYY-MM-DD HH:MM", checking that
// isNight is the opposite of isDay.
func dayAt(t *testing.T, engine *LuaEngine, at string) string {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02 15:04", at, daylightZone)
	if err != nil {
		t.Fatal(err)
	}
	return dayAtTs(t, engine, ts.Unix())
}

func dayAtTs(t *testing.T, engine *LuaEngine, ts int64) string {
	t.Helper()
	got := mustExec(t, engine, fmt.Sprintf(`return tostring(dom:isDay(%d)) .. ' ' .. tostring(dom:isNight(%d))`, ts, ts))
	switch got {
	case "true false":
		return "day"
	case "false true":
		return "night"
	}
	t.Fatalf("isDay, isNight at %d: %s", ts, got)
	return ""
}

// sunTime is a field of dom:sun(date) as a timestamp.
func sunTime(t *testing.T, engine *LuaEngine, date, field string) int64 {
	t.Helper()
	var ts int64
	if _, err := fmt.Sscan(mustExec(t, engine, fmt.Sprintf(`return string.format('%%d', dom:sun('%s').%s)`, date, field)), &ts); err != nil {
		t.Fatalf("sun %s %s: %v", date, field, err)
	}
	return ts
}

func expectDay(t *testing.T, engine *LuaEngine, want map[string]string) {
	t.Helper()
	for at, w := range want {
		if got := dayAt(t, engine, at); got != w {
			t.Errorf("%s: %s, want %s", at, got, w)
		}
	}
}

// Brwinów (52.14 N, 20.72 E): sunrise about 07:44 and sunset 15:26 in December (CET),
// 04:15 and 21:03 in June (CEST).
func TestContract_CalendarIsDayBrwinow(t *testing.T) {
	engine := daylightCal(t, `:location(52.14, 20.72)`)
	expectDay(t, engine, map[string]string{
		"2026-12-21 06:44": "night", "2026-12-21 12:00": "day", "2026-12-21 16:26": "night",
		"2026-06-21 03:15": "night", "2026-06-21 12:00": "day", "2026-06-21 22:03": "night",
	})
	// an hour on each side of the sun's own times, in winter and in summer time
	const h = 3600
	for _, date := range []string{"2026-12-21", "2026-06-21"} {
		rise, set := sunTime(t, engine, date, "sunrise"), sunTime(t, engine, date, "sunset")
		for _, c := range []struct {
			ts   int64
			want string
		}{{rise - h, "night"}, {rise, "day"}, {set - 1, "day"}, {set, "night"}, {set + h, "night"}} {
			if got := dayAtTs(t, engine, c.ts); got != c.want {
				t.Errorf("%s at %s: %s, want %s", date, time.Unix(c.ts, 0).In(daylightZone).Format("15:04:05"), got, c.want)
			}
		}
	}
	// without an argument: now, 12:00 in November
	if got := mustExec(t, engine, `return tostring(dom:isDay()) .. ' ' .. tostring(dom:isNight())`); got != "true false" {
		t.Errorf("isDay(), isNight() at noon: %s", got)
	}
}

func TestContract_CalendarIsDayOffsets(t *testing.T) {
	engine := daylightCal(t, `:location(52.14, 20.72):daylight({ from = 'sunrise+30m', to = 'dusk' })`)
	rise, set := sunTime(t, engine, "2026-11-06", "sunrise"), sunTime(t, engine, "2026-11-06", "sunset")
	dusk := sunTime(t, engine, "2026-11-06", "dusk")
	if dusk <= set+10*60 {
		t.Fatalf("dusk %d not 10 min after sunset %d", dusk, set)
	}
	for _, c := range []struct {
		what string
		ts   int64
		want string
	}{
		{"sunrise+15m", rise + 15*60, "night"},
		{"sunrise+45m", rise + 45*60, "day"},
		{"sunset+10m", set + 10*60, "day"},
		{"dusk-1s", dusk - 1, "day"},
		{"dusk", dusk, "night"},
	} {
		if got := dayAtTs(t, engine, c.ts); got != c.want {
			t.Errorf("%s: %s, want %s", c.what, got, c.want)
		}
	}
	// with the default to = 'sunset' the same moment is night
	plain := daylightCal(t, `:location(52.14, 20.72)`)
	if got := dayAtTs(t, plain, set+10*60); got != "night" {
		t.Errorf("sunset+10m with to = 'sunset': %s", got)
	}
}

func TestContract_CalendarIsDayFallback(t *testing.T) {
	engine := daylightCal(t, ``)
	expectDay(t, engine, map[string]string{
		"2026-11-06 05:59": "night", "2026-11-06 06:00": "day", "2026-11-06 19:59": "day", "2026-11-06 20:00": "night",
		// summer time: still the wall clock
		"2026-07-01 05:59": "night", "2026-07-01 06:00": "day", "2026-07-01 20:00": "night",
	})
	// the sun bounds need a location: without one the fallback decides
	custom := daylightCal(t, `:daylight({ from = 'sunrise', fallback = { from = '07:30', to = '22:00' } })`)
	expectDay(t, custom, map[string]string{
		"2026-11-06 07:00": "night", "2026-11-06 07:30": "day", "2026-11-06 21:00": "day", "2026-11-06 22:00": "night",
	})
	// a fallback given only in part keeps the other default
	half := daylightCal(t, `:daylight({ fallback = { to = '22:00' } })`)
	expectDay(t, half, map[string]string{"2026-11-06 05:00": "night", "2026-11-06 06:00": "day", "2026-11-06 21:00": "day"})
	// with a location the fallback is not used
	located := daylightCal(t, `:location(52.14, 20.72):daylight({ fallback = { from = '00:00', to = '23:59' } })`)
	expectDay(t, located, map[string]string{"2026-12-21 06:00": "night", "2026-12-21 22:00": "night"})
}

// A fixed from and to with a location are wall clock times too.
func TestContract_CalendarIsDayFixedHours(t *testing.T) {
	engine := daylightCal(t, `:location(52.14, 20.72):daylight({ from = '07:00', to = '21:00' })`)
	expectDay(t, engine, map[string]string{
		"2026-12-21 06:59": "night", "2026-12-21 07:00": "day", "2026-12-21 20:59": "day", "2026-12-21 21:00": "night",
		"2026-06-21 06:59": "night", "2026-06-21 20:59": "day", "2026-06-21 21:00": "night",
	})
}

// Svalbard (78 N): polar day and polar night, offsets or not.
func TestContract_CalendarIsDayPolar(t *testing.T) {
	for _, builder := range []string{`:location(78.22, 15.65)`, `:location(78.22, 15.65):daylight({ from = 'sunrise+2h', to = 'sunset-2h' })`} {
		engine := daylightCal(t, builder)
		if got := mustExec(t, engine, `return dom:sun('2026-06-21').polar .. ' ' .. dom:sun('2026-12-21').polar`); got != "day night" {
			t.Fatalf("polar: %s", got)
		}
		expectDay(t, engine, map[string]string{
			"2026-06-21 00:00": "day", "2026-06-21 03:00": "day", "2026-06-21 12:00": "day", "2026-06-21 23:59": "day",
			"2026-12-21 00:00": "night", "2026-12-21 12:00": "night", "2026-12-21 23:59": "night",
		})
	}
}

// Offsets that put from at or after to: the whole day is night, without an error.
func TestContract_CalendarIsDayEmptyDay(t *testing.T) {
	// a December day in Brwinów is under 8 h
	engine := daylightCal(t, `:location(52.14, 20.72):daylight({ from = 'sunrise+4h', to = 'sunset-4h' })`)
	for _, at := range []string{"00:00", "08:00", "11:35", "12:00", "15:00", "23:59"} {
		if got := dayAt(t, engine, "2026-12-21 "+at); got != "night" {
			t.Errorf("2026-12-21 %s: %s, want night", at, got)
		}
	}
	// a June day is long enough for the same offsets
	if got := dayAt(t, engine, "2026-06-21 12:00"); got != "day" {
		t.Errorf("2026-06-21 12:00: %s, want day", got)
	}
	// equal fixed hours and fallback hours backwards
	for _, builder := range []string{
		`:location(52.14, 20.72):daylight({ from = '12:00', to = '12:00' })`,
		`:daylight({ fallback = { from = '20:00', to = '06:00' } })`,
	} {
		e := daylightCal(t, builder)
		for _, at := range []string{"02:00", "12:00", "22:00"} {
			if got := dayAt(t, e, "2026-11-06 "+at); got != "night" {
				t.Errorf("%s at %s: %s, want night", builder, at, got)
			}
		}
	}
}

func TestContract_CalendarDaylightRefusesBadOptions(t *testing.T) {
	for _, c := range []struct{ opts, want string }{
		{`{ form = 'sunrise' }`, `Calendar dom: daylight: unknown option "form"`},
		{`{ from = 'sunrise+' }`, `Calendar dom: daylight: time "sunrise+"`},
		{`{ to = '25:00' }`, `Calendar dom: daylight: time "25:00"`},
		{`{ to = 'sunset+13h' }`, `at most 12h`},
		{`{ from = 7 }`, `Calendar dom: daylight: from and to expect times`},
		{`{ fallback = { from = 'sunrise' } }`, `Calendar dom: daylight fallback: time "sunrise": fallback hours are HH:MM`},
		{`{ fallback = { from = '6:00', till = '20:00' } }`, `Calendar dom: daylight fallback: unknown option "till"`},
		{`{ fallback = '06:00-20:00' }`, `Calendar dom: daylight: fallback expects`},
		{`'sunrise'`, `Calendar dom: daylight expects a table`},
	} {
		e := NewLuaEngine()
		t.Cleanup(e.Close)
		_, err := e.Execute("local b = Calendar.new('dom')\nb:daylight(" + c.opts + ")\nb:build()")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("daylight(%s): %v, want %q", c.opts, err, c.want)
			continue
		}
		// the error points at the module's line, not into Calendar.lua
		if !strings.Contains(err.Error(), "<string>:2: Calendar dom") {
			t.Errorf("daylight(%s): %v, want the caller's line 2", c.opts, err)
		}
	}
	engine := daylightCal(t, ``)
	if _, err := engine.Execute(`dom:isNight('12:00')`); err == nil || !strings.Contains(err.Error(), "Calendar dom:isNight: expected a timestamp") {
		t.Errorf("isNight('12:00'): %v", err)
	}
}

// Like value(), both are nil until the clock is trusted (before the calendar's first check).
func TestContract_CalendarIsDayNilBeforeClock(t *testing.T) {
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	SetClock(engine.state, fixedClock{time.Date(2026, 11, 6, 11, 0, 0, 0, time.UTC)})
	mustExec(t, engine, `dom = Calendar.new('dom'):location(52.14, 20.72)
		:schedule('pora', { default = 0, { every = 'day', hours = '06:00-07:00', value = 1 } }):build()`)
	if got := mustExec(t, engine, `return tostring(dom:isDay()) .. ' ' .. tostring(dom:isNight(0)) .. ' ' .. tostring(dom:value('pora'))`); got != "nil nil nil" {
		t.Errorf("before the clock: %s, want nil nil nil", got)
	}
	mustExec(t, engine, `dom:_tick()`)
	if got := mustExec(t, engine, `return tostring(dom:isDay()) .. ' ' .. tostring(dom:isNight())`); got != "true false" {
		t.Errorf("after the check: %s", got)
	}
}
