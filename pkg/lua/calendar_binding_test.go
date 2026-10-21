package lua

import (
	"errors"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// The Go side of Calendar.lua as Lua calls it: a bad rule table gives an error naming the
// option, never a Go panic, and the other __go_calendar_* functions refuse bad arguments.

func calendarState(t *testing.T) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	RegisterParserFunctions(L)
	SetClock(L, fixedClock{time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)})
	if err := L.DoString(`datetime.setTimezone("Europe/Warsaw")`); err != nil {
		t.Fatal(err)
	}
	return L
}

// calRun runs src; a Go panic inside a binding fails the test, a Lua error is returned.
func calRun(t *testing.T, L *lua.LState, src string) error {
	t.Helper()
	err := L.DoString(src)
	var api *lua.ApiError
	if errors.As(err, &api) && api.Type == lua.ApiErrorPanic {
		t.Fatalf("%s: Go panic: %v", src, err)
	}
	return err
}

// ruleErr compiles spec (Lua source) with holidays and a location; "" when it compiles.
func ruleErr(t *testing.T, L *lua.LState, spec string) string {
	t.Helper()
	if err := calRun(t, L, `_r, _e = __go_calendar_rule(`+spec+`, 'pl', 52.23, 21.01)`); err != nil {
		t.Fatalf("%s raised instead of returning the error: %v", spec, err)
	}
	if e := L.GetGlobal("_e"); e != lua.LNil {
		if L.GetGlobal("_r") != lua.LNil {
			t.Fatalf("%s: both a rule and an error", spec)
		}
		return e.String()
	}
	if _, ok := L.GetGlobal("_r").(*lua.LUserData); !ok {
		t.Fatalf("%s: no rule and no error", spec)
	}
	return ""
}

func TestCalendarBinding_BadRuleTables(t *testing.T) {
	L := calendarState(t)
	cases := []struct{ spec, want string }{
		// numbers
		{`{ every = 'month', day = 1.5 }`, "day must be a whole number"},
		{`{ every = 'tu', weeks = 2.7, from = '2026-01-06' }`, "weeks must be a whole number"},
		{`{ every = 'month', day = 0 }`, "needs day"},
		{`{ every = 'month', day = -2 }`, "day -2"},
		{`{ every = 'month', day = 32 }`, "day 32"},
		{`{ every = 'month', day = 1e300 }`, "day must be a whole number"},
		{`{ every = 'month', day = -1e300 }`, "day must be a whole number"},
		{`{ every = 'month', day = 0/0 }`, "day must be a whole number"},
		{`{ every = 'month', day = 1/0 }`, "day must be a whole number"},
		{`{ every = 'month', day = '2' }`, "day must be a number, got string"},
		{`{ every = 'month', day = true }`, "day must be a number, got boolean"},
		{`{ every = 'month', day = { 2 } }`, "day must be a number, got table"},
		{`{ every = 'tu', weeks = -1, from = '2026-01-06' }`, "at least 1"},
		{`{ every = 'tu', weeks = 1e7, from = '2026-01-06' }`, "weeks must be a whole number"},
		{`{ every = 'month', day = 1, months = 2.5, from = '2026-01-01' }`, "months must be a whole number"},
		{`{ every = 'year', date = '01-01', years = '2', from = '2026-01-01' }`, "years must be a number"},
		{`{ every = 'day', times = -1, from = '2026-01-01' }`, "times must not be negative"},
		{`{ every = 'day', times = 1.5, from = '2026-01-01' }`, "times must be a whole number"},
		{`{ every = 'day', times = 3 }`, "times needs from"},
		{`{ every = 'year', month = 13, on = 'last su' }`, "month = 1..12"},
		{`{ every = 'year', month = 2.5, on = 'last su' }`, "month must be a whole number"},
		{`{ every = 'tu', weeks = 2, months = 2, from = '2026-01-06' }`, "only one of weeks"},
		// every
		{`{ every = {} }`, "give every"},
		{`{ every = { 'mo', 5 } }`, "every: expected strings, got number"},
		{`{ every = { { 'mo' } } }`, "every: expected strings, got table"},
		{`{ every = 5 }`, "every: expected a string or a list of strings, got number"},
		{`{ every = true }`, "every: expected a string or a list of strings"},
		{`{ every = 'monday' }`, `every "monday"`},
		{`{ every = { 'mo', 'month' } }`, `every "month"`},
		// dates, except, moves
		{`{ dates = {} }`, "give every"},
		{`{ dates = { '2026-01-01', 5 } }`, "dates: expected strings, got number"},
		{`{ dates = { '2026-13-01' } }`, "dates: date"},
		{`{ dates = { '2026-02-30' } }`, "dates: date"},
		{`{ dates = { '2026-01-01' }, every = 'day' }`, "drop every"},
		{`{ dates = 20260101 }`, "dates: expected a string or a list"},
		{`{ every = 'day', except = 5 }`, "except: expected a string or a list"},
		{`{ every = 'day', except = { 5 } }`, "except: expected strings"},
		{`{ every = 'day', except = { 'jutro' } }`, "except: date"},
		{`{ every = 'day', except = '2026-01-05..2026-01-01' }`, "except: range"},
		{`{ every = 'day', moves = 'x' }`, "moves must be a table, got string"},
		{`{ every = 'day', moves = { '2026-01-01' } }`, "moves: expected"},
		{`{ every = 'day', moves = { ['2026-01-01'] = 5 } }`, "moves: expected"},
		{`{ every = 'day', moves = { ['2026-01-01'] = 'jutro' } }`, "moves: date"},
		{`{ every = 'day', moves = { ['2026-01-01'] = { '2026-01-02' } } }`, "moves: expected"},
		// sun
		{`{ every = 'day', sun = 5 }`, "sun expects"},
		{`{ every = 'day', sun = {} }`, "sun: azimuth"},
		{`{ every = 'day', sun = { azimuth = { 1 } } }`, "sun: azimuth"},
		{`{ every = 'day', sun = { azimuth = 90 } }`, "sun: azimuth"},
		{`{ every = 'day', sun = { azimuth = { 1, '2' } } }`, "sun: azimuth"},
		{`{ every = 'day', sun = { azimuth = { 1, 2, 3 } } }`, "sun: azimuth"},
		{`{ every = 'day', sun = { azimuth = { -10, 180 } } }`, "sun azimuth"},
		{`{ every = 'day', sun = { azimuth = { 90, 400 } } }`, "sun azimuth"},
		{`{ every = 'day', sun = { azimuth = { 0/0, 180 } } }`, "NaN"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, elevation = '10' } }`, "sun: elevation"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, elevation = { 10 } } }`, "sun: elevation"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, elevation = true } }`, "sun: elevation"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, elevation = 95 } }`, "sun elevation"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, elevation = { 30, 10 } } }`, "sun elevation"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, elevation = 0/0 } }`, "NaN"},
		{`{ every = 'day', sun = { azimuth = { 90, 180 }, height = 10 } }`, `unknown option "height"`},
		{`{ every = 'day', sun = { azimuth = { 90, 180 } }, time = '10:00' }`, "drop time"},
		// strings and durations
		{`{ every = 'day', time = 10 }`, "time must be a string, got number"},
		{`{ every = 'day', time = '25:00' }`, "time"},
		{`{ every = 'day', time = 'sunset+400d' }`, "at most 366 days"},
		{`{ every = 'day', time = '10:00', ['until'] = true }`, "until must be a string"},
		{`{ every = 'day', from = 20260101 }`, "from must be a string"},
		{`{ every = 'day', from = '2026-02-30' }`, "from: date"},
		{`{ every = 'day', from = '2026-02-01', to = '2026-01-01' }`, "to is before from"},
		{`{ every = 'month', on = 5 }`, "on must be a string"},
		{`{ every = 'month', on = 'fifth fr' }`, `on "fifth fr"`},
		{`{ every = 'month', on = 'second workday' }`, "workday takes first or last"},
		{`{ every = 'year', date = '13-01' }`, "MM-DD"},
		{`{ every = 'year', date = 314 }`, "date must be a string"},
		{`{ date = '2026-01-01', from = '2026-01-01' }`, "belong to a repeating event"},
		{`{ every = 'day', time = '10:00', duration = 0/0 }`, "duration must be at most 366 days"},
		{`{ every = 'day', time = '10:00', duration = 1/0 }`, "duration must be at most 366 days"},
		{`{ every = 'day', time = '10:00', duration = -5 }`, "must not be negative"},
		{`{ every = 'day', time = '10:00', duration = {} }`, "duration must be a string like"},
		{`{ every = 'day', time = '10:00', duration = '5x' }`, "duration"},
		{`{ every = 'day', time = '10:00', duration = 367 * 86400 }`, "at most 366 days"},
	}
	for _, c := range cases {
		if got := ruleErr(t, L, c.spec); got == "" || !strings.Contains(got, c.want) {
			t.Errorf("%s:\n got %q\nwant it to contain %q", c.spec, got, c.want)
		}
	}
}

func TestCalendarBinding_GoodRuleTables(t *testing.T) {
	L := calendarState(t)
	for _, spec := range []string{
		`{ dates = '2026-01-01' }`,
		`{ every = 'day', moves = {} }`,
		`{ every = 'day', except = {} }`,
		`{ every = 'day', except = '2026-01-01' }`,
		`{ every = 'MO' }`,
		`{ every = 'month', day = 2.0 }`,
		`{ every = 'month', day = -1 }`,
		`{ every = 'day', time = '10:00', duration = 0.5 }`,
		`{ every = 'day', sun = { azimuth = { 300, 60 }, elevation = { -5, 10 } } }`,
		`{ every = 'day', time = 'dusk', ['until'] = 'dawn+15m' }`,
	} {
		if got := ruleErr(t, L, spec); got != "" {
			t.Errorf("%s: %s", spec, got)
		}
	}
}

func TestCalendarBinding_RuleArguments(t *testing.T) {
	L := calendarState(t)
	// a sun time needs both coordinates
	if err := calRun(t, L, `_r, _e = __go_calendar_rule({ every = 'day', time = 'sunset' }, nil, 52.2)`); err != nil {
		t.Fatal(err)
	}
	if e := L.GetGlobal("_e").String(); !strings.Contains(e, "location") {
		t.Errorf("lat without lon: %s", e)
	}
	if err := calRun(t, L, `_r, _e = __go_calendar_rule({ every = 'day' }, 'de')`); err != nil {
		t.Fatal(err)
	}
	if e := L.GetGlobal("_e").String(); !strings.Contains(e, "unknown holidays") {
		t.Errorf("holidays de: %s", e)
	}
	if err := calRun(t, L, `__go_calendar_rule(5)`); err == nil || !strings.Contains(err.Error(), "table expected") {
		t.Errorf("a number as the spec: %v", err)
	}
}

// luaStrings walks the whole table: a string under a key passes as a list item.
func TestReview2_ListWithKeysRefused(t *testing.T) {
	L := calendarState(t)
	for _, spec := range []string{
		`{ every = { 'mo', x = 'fr' } }`,
		`{ dates = { '2026-01-01', note = '2026-02-01' } }`,
		`{ every = 'day', except = { '2026-01-01', [5] = '2026-01-02' } }`,
	} {
		if got := ruleErr(t, L, spec); got == "" {
			t.Errorf("%s accepted", spec)
		}
	}
	// what it does now: { 'mo', x = 'fr' } fires on Fridays too
	if calRun(t, L, `local r = __go_calendar_rule({ every = { 'mo', x = 'fr' } })
		_n = #__go_calendar_between(r, os.time{ year = 2026, month = 10, day = 19, hour = 12 }, os.time{ year = 2026, month = 10, day = 26, hour = 12 })`) == nil {
		if n := L.GetGlobal("_n").String(); n != "1" {
			t.Errorf("{ 'mo', x = 'fr' } gives %s occurrences a week", n)
		}
	}
}

// luaInt returns 0 for nil and for 0, and Compile reads 0 as "not given": weeks = 0 is every week.
func TestReview2_ZeroIntervalRefused(t *testing.T) {
	L := calendarState(t)
	for _, spec := range []string{
		`{ every = 'tu', weeks = 0, from = '2026-01-06' }`,
		`{ every = 'month', day = 1, months = 0, from = '2026-01-01' }`,
		`{ every = 'year', date = '01-01', years = 0, from = '2026-01-01' }`,
		`{ every = 'year', date = '01-01', month = 0 }`,
	} {
		if got := ruleErr(t, L, spec); got == "" {
			t.Errorf("%s accepted", spec)
		}
	}
}

// calendarHolidays and calendarRule ignore a holidays value that is not a string, so
// Builder:holidays(5) or holidays(true) passes its check and the calendar has no holidays.
func TestReview2_HolidaysNonStringRefused(t *testing.T) {
	L := calendarState(t)
	for _, v := range []string{"5", "true", "{ 'pl' }"} {
		if err := calRun(t, L, `__go_calendar_day('2026-01-01', `+v+`)`); err == nil {
			t.Errorf("holidays %s accepted by __go_calendar_day", v)
		}
	}
}

func TestCalendarBinding_RuleHandles(t *testing.T) {
	L := calendarState(t)
	L.SetGlobal("_foreign", L.NewUserData())
	if err := calRun(t, L, `_rule = __go_calendar_rule({ every = 'day', time = '10:00' })`); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		`__go_calendar_next(5, 0)`:                              "userdata expected",
		`__go_calendar_next(_foreign, 0)`:                       "calendar rule expected",
		`__go_calendar_next(_rule, 'x')`:                        "number expected",
		`__go_calendar_between(_foreign, 0, 1)`:                 "calendar rule expected",
		`__go_calendar_between(_rule, 0, 4 * 366 * 86400)`:      "longer than 3 years",
		`__go_calendar_between(_rule, 0, 1/0)`:                  "",
		`__go_calendar_schedule({ _rule, 5 }, 0)`:               "list of calendar rules expected",
		`__go_calendar_schedule({ _rule, _foreign }, 0)`:        "list of calendar rules expected",
		`__go_calendar_schedule(5, 0)`:                          "table expected",
		`__go_calendar_add_days(0, 367)`:                        "at most 366 days",
		`__go_calendar_day('2026-02-30')`:                       "YYYY-MM-DD",
		`__go_calendar_day('2026-01-01', 'de')`:                 "unknown holidays",
		`__go_calendar_sun('2026-01-01', 'x', 0)`:               "number expected",
		`__go_calendar_sun('jutro', 52, 21)`:                    "YYYY-MM-DD",
		`__go_calendar_between(_rule, 0/0, 0/0)`:                "",
		`__go_calendar_between(_rule, 1e15, 1e15 + 86400)`:      "",
		`__go_calendar_between(_rule, -1e12, -1e12 + 86400)`:    "",
		`__go_calendar_next(_rule, 0/0)`:                        "",
		`__go_calendar_schedule({ _rule }, 1792000000, -5)`:     "",
		`__go_calendar_schedule({ _rule }, 1792000000, 0/0)`:    "",
		`__go_calendar_day(0/0)`:                                "",
		`__go_calendar_sun(nil, 0/0, 21)`:                       "",
		`__go_calendar_sun_position(nil, 0/0, 0/0)`:             "",
		`__go_calendar_schedule({}, 1792000000, 8 * 86400)`:     "",
		`__go_calendar_schedule({ _rule, nil, _rule }, 0, 600)`: "list of calendar rules expected",
		`__go_calendar_next(_rule, 1/0)`:                        "",
		`__go_calendar_next(_rule, -1/0)`:                       "",
		`__go_calendar_next(_rule, 1e300)`:                      "",
		`__go_calendar_between(_rule, 1e300, 1e300 + 1)`:        "",
		`__go_calendar_schedule({ _rule }, 1/0, 8 * 86400)`:     "",
		`__go_calendar_schedule({ _rule }, 1792000000, 1e300)`:  "",
		`__go_calendar_day(1/0)`:                                "",
		`__go_calendar_add_days(1/0, 1)`:                        "",
	} {
		start := time.Now()
		err := calRun(t, L, src)
		switch {
		case want == "" && err != nil:
			t.Errorf("%s: %v", src, err)
		case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
			t.Errorf("%s: %v, want %q", src, err, want)
		}
		if el := time.Since(start); el > time.Second {
			t.Errorf("%s took %s", src, el)
		}
	}
}

func TestCalendarBinding_Results(t *testing.T) {
	L := calendarState(t)
	if err := calRun(t, L, `
		local r = __go_calendar_rule({ every = 'day', time = '10:00', duration = '1h' })
		local o = __go_calendar_next(r, 1792000000)   -- 2026-10-14 ~ 21:46 Warsaw
		_next = o.date .. ' ' .. (o.ends - o.at) .. ' ' .. tostring(o.allDay)
		local past = __go_calendar_rule({ every = 'day', from = '2020-01-01', to = '2020-12-31' })
		_past = __go_calendar_next(past, 1792000000)
		local idx, at, after = __go_calendar_schedule({ r }, 1792000000, 86400)
		_sched = idx .. ' ' .. (at - 1792000000 > 0 and 'later' or 'now') .. ' ' .. after
		local d = __go_calendar_day('2026-08-15', 'pl')
		_day = d.weekday .. ' ' .. tostring(d.holiday) .. ' ' .. tostring(d.workday) .. ' ' .. d.holidayName
		local w = __go_calendar_day('2026-08-15')
		_noHol = tostring(w.holiday) .. ' ' .. tostring(w.workday)
		local june = __go_calendar_sun('2026-06-21', 69.65, 18.96)
		local dec = __go_calendar_sun('2026-12-21', 69.65, 18.96)
		_polar = june.polar .. ' ' .. tostring(june.dawn) .. ' ' .. dec.polar .. ' ' .. tostring(dec.dawn ~= nil) .. ' ' .. tostring(dec.sunrise)
	`); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"_next":  "2026-10-15 3600 false",
		"_past":  "nil",
		"_sched": "0 later 1",
		"_day":   "sa true false Wniebowzięcie NMP",
		"_noHol": "false false",
		"_polar": "day nil night true nil",
	} {
		if got := L.GetGlobal(name).String(); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
