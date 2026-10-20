package lua

import (
	"fmt"
	"math"
	"time"

	"github.com/Domktorymysli/vclu/pkg/calendar"
	lua "github.com/yuin/gopher-lua"
)

// Go side of runtime/core/Calendar.lua: compiles rules and expands them into occurrences.
// Times cross the boundary as Unix seconds; the wall clock is the datetime zone.

func registerCalendarFunctions(L *lua.LState) {
	L.SetGlobal("__go_calendar_rule", L.NewFunction(calendarRule))
	L.SetGlobal("__go_calendar_between", L.NewFunction(calendarBetween))
	L.SetGlobal("__go_calendar_next", L.NewFunction(calendarNext))
	L.SetGlobal("__go_calendar_schedule", L.NewFunction(calendarSchedule))
	L.SetGlobal("__go_calendar_day", L.NewFunction(calendarDay))
	L.SetGlobal("__go_calendar_clock", L.NewFunction(calendarClock))
	L.SetGlobal("__go_calendar_sun", L.NewFunction(calendarSun))
	L.SetGlobal("__go_calendar_sun_position", L.NewFunction(calendarSunPosition))
	L.SetGlobal("__go_calendar_add_days", L.NewFunction(calendarAddDays))
}

// __go_calendar_add_days(timestamp, n) -> the same wall-clock time n days away (DST aware).
func calendarAddDays(L *lua.LState) int {
	t := unixSec(float64(L.CheckNumber(1))).In(calendarZone(L))
	n := L.CheckInt(2)
	if n < -366 || n > 366 {
		L.ArgError(2, "at most 366 days")
	}
	L.Push(lua.LNumber(t.AddDate(0, 0, n).Unix()))
	return 1
}

func calendarZone(L *lua.LState) *time.Location {
	if loc := datetimeZone(L); loc != nil {
		return loc
	}
	return time.Local
}

func calendarHolidays(L *lua.LState, v lua.LValue) calendar.Holidays {
	s, ok := v.(lua.LString)
	if !ok || s == "" {
		return nil
	}
	h, err := calendar.HolidaysFor(string(s))
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
	return h
}

func luaStrings(v lua.LValue) ([]string, error) {
	switch t := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LString:
		return []string{string(t)}, nil
	case *lua.LTable:
		var out []string
		var err error
		t.ForEach(func(_, item lua.LValue) {
			if s, ok := item.(lua.LString); ok {
				out = append(out, string(s))
			} else if err == nil {
				err = fmt.Errorf("expected strings, got %s", item.Type())
			}
		})
		return out, err
	}
	return nil, fmt.Errorf("expected a string or a list of strings, got %s", v.Type())
}

func luaInt(t *lua.LTable, key string) (int, error) {
	switch v := t.RawGetString(key).(type) {
	case *lua.LNilType:
		return 0, nil
	case lua.LNumber:
		if float64(v) != math.Trunc(float64(v)) || math.Abs(float64(v)) > 1e6 {
			return 0, fmt.Errorf("%s must be a whole number up to a million", key)
		}
		return int(v), nil
	default:
		return 0, fmt.Errorf("%s must be a number, got %s", key, v.Type())
	}
}

func luaString(t *lua.LTable, key string) (string, error) {
	switch v := t.RawGetString(key).(type) {
	case *lua.LNilType:
		return "", nil
	case lua.LString:
		return string(v), nil
	default:
		return "", fmt.Errorf("%s must be a string, got %s", key, v.Type())
	}
}

// luaDuration reads "6h" or a number of seconds.
func luaDuration(v lua.LValue, key string) (time.Duration, error) {
	switch t := v.(type) {
	case *lua.LNilType:
		return 0, nil
	case lua.LNumber:
		if f := math.Abs(float64(t)); f != f || f > calendar.MaxDuration.Seconds() {
			return 0, fmt.Errorf("%s must be at most 366 days, got %v", key, float64(t))
		}
		return time.Duration(float64(t) * float64(time.Second)), nil
	case lua.LString:
		d, err := calendar.ParseDuration(string(t))
		if err != nil {
			return 0, fmt.Errorf("%s: %w", key, err)
		}
		return d, nil
	}
	return 0, fmt.Errorf("%s must be a string like '6h' or seconds, got %s", key, v.Type())
}

func specFromTable(t *lua.LTable) (calendar.Spec, error) {
	var s calendar.Spec
	var err error
	if s.Every, err = luaStrings(t.RawGetString("every")); err != nil {
		return s, fmt.Errorf("every: %w", err)
	}
	for key, dst := range map[string]*int{"weeks": &s.Weeks, "months": &s.Months, "years": &s.Years, "day": &s.Day, "month": &s.Month, "times": &s.Times} {
		if *dst, err = luaInt(t, key); err != nil {
			return s, err
		}
	}
	for key, dst := range map[string]*string{"on": &s.On, "date": &s.Date, "from": &s.From, "to": &s.To, "time": &s.Time, "until": &s.Until} {
		if *dst, err = luaString(t, key); err != nil {
			return s, err
		}
	}
	if s.Duration, err = luaDuration(t.RawGetString("duration"), "duration"); err != nil {
		return s, err
	}
	if s.Sky, err = skyFromLua(t.RawGetString("sun")); err != nil {
		return s, err
	}
	if s.Dates, err = luaStrings(t.RawGetString("dates")); err != nil {
		return s, fmt.Errorf("dates: %w", err)
	}
	if s.Except, err = luaStrings(t.RawGetString("except")); err != nil {
		return s, fmt.Errorf("except: %w", err)
	}
	switch m := t.RawGetString("moves").(type) {
	case *lua.LNilType:
	case *lua.LTable:
		s.Moves = map[string]string{}
		m.ForEach(func(k, v lua.LValue) {
			ks, ok1 := k.(lua.LString)
			vs, ok2 := v.(lua.LString)
			if !ok1 || !ok2 {
				err = fmt.Errorf("moves: expected { ['YYYY-MM-DD'] = 'YYYY-MM-DD' }")
				return
			}
			s.Moves[string(ks)] = string(vs)
		})
		if err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("moves must be a table, got %s", m.Type())
	}
	return s, nil
}

// pair reads {a, b} of numbers.
func pair(v lua.LValue) (float64, float64, bool) {
	t, ok := v.(*lua.LTable)
	if !ok || t.Len() != 2 {
		return 0, 0, false
	}
	a, ok1 := t.RawGetInt(1).(lua.LNumber)
	b, ok2 := t.RawGetInt(2).(lua.LNumber)
	return float64(a), float64(b), ok1 && ok2
}

// skyFromLua reads sun = { azimuth = {from, to}, elevation = low | {low, high} }.
func skyFromLua(v lua.LValue) (*calendar.Sky, error) {
	if v == lua.LNil {
		return nil, nil
	}
	t, ok := v.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("sun expects { azimuth = {from, to}, elevation = low or {low, high} }")
	}
	k := &calendar.Sky{ElevationLow: 0, ElevationHigh: 90}
	var ok2 bool
	if k.AzimuthFrom, k.AzimuthTo, ok2 = pair(t.RawGetString("azimuth")); !ok2 {
		return nil, fmt.Errorf("sun: azimuth expects {from, to} in degrees from north (90 east, 180 south)")
	}
	switch e := t.RawGetString("elevation").(type) {
	case *lua.LNilType:
	case lua.LNumber:
		k.ElevationLow = float64(e)
	default:
		if k.ElevationLow, k.ElevationHigh, ok2 = pair(e); !ok2 {
			return nil, fmt.Errorf("sun: elevation expects a number (the lowest) or {low, high}")
		}
	}
	var bad error
	t.ForEach(func(key, _ lua.LValue) {
		if key.String() != "azimuth" && key.String() != "elevation" && bad == nil {
			bad = fmt.Errorf("sun: unknown option %q (azimuth, elevation)", key.String())
		}
	})
	return k, bad
}

// __go_calendar_rule(spec, holidays, lat, lon) -> rule userdata, or nil and the error of a bad
// spec (returned, not raised: a caught error splits the closures of a module being loaded).
func calendarRule(L *lua.LState) int {
	s, err := specFromTable(L.CheckTable(1))
	if lat, ok := L.Get(3).(lua.LNumber); ok {
		if lon, ok := L.Get(4).(lua.LNumber); ok {
			s.Lat, s.Lon, s.Located = float64(lat), float64(lon), true
		}
	}
	if err == nil {
		var hol calendar.Holidays
		if c, ok := L.Get(2).(lua.LString); ok && c != "" {
			hol, err = calendar.HolidaysFor(string(c))
		}
		var r *calendar.Rule
		if err == nil {
			if r, err = calendar.Compile(s, hol); err == nil {
				ud := L.NewUserData()
				ud.Value = r
				L.Push(ud)
				return 1
			}
		}
	}
	L.Push(lua.LNil)
	L.Push(lua.LString(err.Error()))
	return 2
}

func checkRule(L *lua.LState, n int) *calendar.Rule {
	if r, ok := L.CheckUserData(n).Value.(*calendar.Rule); ok {
		return r
	}
	L.ArgError(n, "calendar rule expected")
	return nil
}

func unixSec(f float64) time.Time {
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9))
}

func occurrenceTable(L *lua.LState, o calendar.Occurrence) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("at", lua.LNumber(o.Start.Unix()))
	if !o.End.IsZero() {
		t.RawSetString("ends", lua.LNumber(o.End.Unix()))
	}
	t.RawSetString("allDay", lua.LBool(o.AllDay))
	t.RawSetString("date", lua.LString(o.Date.String()))
	return t
}

// __go_calendar_between(rule, from, to, overlapping) -> occurrences starting in [from, to)
// (with overlapping also the ones still going on at from).
func calendarBetween(L *lua.LState) int {
	r := checkRule(L, 1)
	from, to := unixSec(float64(L.CheckNumber(2))), unixSec(float64(L.CheckNumber(3)))
	if to.Sub(from) > 3*366*24*time.Hour {
		L.ArgError(3, "window longer than 3 years")
	}
	out := L.NewTable()
	for _, o := range r.Between(from, to, calendarZone(L), L.OptBool(4, false)) {
		out.Append(occurrenceTable(L, o))
	}
	L.Push(out)
	return 1
}

// __go_calendar_next(rule, after) -> the first occurrence starting after it (within 3 years), or nil.
func calendarNext(L *lua.LState) int {
	r := checkRule(L, 1)
	o, ok := r.Next(unixSec(float64(L.CheckNumber(2))), 3*366*24*time.Hour, calendarZone(L))
	if !ok {
		L.Push(lua.LNil)
		return 1
	}
	L.Push(occurrenceTable(L, o))
	return 1
}

// __go_calendar_schedule({rules}, t, horizon) -> index in effect (1-based, 0 = default),
// time of the next change within horizon seconds (or nil), index after it.
func calendarSchedule(L *lua.LState) int {
	list := L.CheckTable(1)
	s := &calendar.Schedule{}
	for i := 1; i <= list.Len(); i++ {
		ud, ok := list.RawGetInt(i).(*lua.LUserData)
		r, ok2 := (*calendar.Rule)(nil), false
		if ok {
			r, ok2 = ud.Value.(*calendar.Rule)
		}
		if !ok2 {
			L.ArgError(1, "list of calendar rules expected")
		}
		s.Windows = append(s.Windows, r)
	}
	t := unixSec(float64(L.CheckNumber(2)))
	horizon := time.Duration(float64(L.OptNumber(3, 60)) * float64(time.Second))
	loc := calendarZone(L)
	L.Push(lua.LNumber(s.At(t, loc) + 1))
	if next, idx, ok := s.NextChange(t, horizon, loc); ok {
		L.Push(lua.LNumber(next.Unix()))
		L.Push(lua.LNumber(idx + 1))
		return 3
	}
	L.Push(lua.LNil)
	L.Push(lua.LNil)
	return 3
}

// __go_calendar_day(date | timestamp, country) -> {date, weekday ("mo".."su"), holiday,
// holidayName, workday, namedays, starts, ends} for that local day.
func calendarDay(L *lua.LState) int {
	loc := calendarZone(L)
	var d calendar.Date
	switch v := L.Get(1).(type) {
	case lua.LString:
		var err error
		if d, err = calendar.ParseDate(string(v)); err != nil {
			L.ArgError(1, err.Error())
		}
	case lua.LNumber:
		d = calendar.DateOf(unixSec(float64(v)).In(loc))
	default:
		d = calendar.DateOf(clockNow(L).In(loc))
	}
	country, _ := L.Get(2).(lua.LString)
	hol := calendarHolidays(L, L.Get(2))
	holiday := hol != nil && hol(d)
	wd := d.Weekday()
	t := L.NewTable()
	t.RawSetString("date", lua.LString(d.String()))
	t.RawSetString("weekday", lua.LString([]string{"su", "mo", "tu", "we", "th", "fr", "sa"}[wd]))
	t.RawSetString("holiday", lua.LBool(holiday))
	t.RawSetString("workday", lua.LBool(!holiday && wd != time.Saturday && wd != time.Sunday))
	if name := calendar.HolidayName(string(country), d); name != "" {
		t.RawSetString("holidayName", lua.LString(name))
	}
	if names := calendar.Namedays(string(country), d); len(names) > 0 {
		nt := L.CreateTable(len(names), 0)
		for _, n := range names {
			nt.Append(lua.LString(n))
		}
		t.RawSetString("namedays", nt)
	}
	t.RawSetString("starts", lua.LNumber(d.At(0, loc).Unix()))
	t.RawSetString("ends", lua.LNumber((d+1).At(0, loc).Unix()))
	L.Push(t)
	return 1
}

// __go_calendar_clock() -> trusted, reason. A virtual clock (the MCP sandbox) is trusted.
func calendarClock(L *lua.LState) int {
	if ud, ok := L.G.Registry.RawGetString(clockKey).(*lua.LUserData); ok {
		if _, real := ud.Value.(RealClock); !real && ud.Value != nil {
			L.Push(lua.LTrue)
			return 1
		}
	}
	ok, reason := calendar.ClockTrusted(clockNow(L))
	L.Push(lua.LBool(ok))
	if reason != "" {
		L.Push(lua.LString(reason))
		return 2
	}
	return 1
}

// __go_calendar_sun(date | timestamp | nil, lat, lon) -> {sunrise, sunset, noon, dayLength,
// dawn, dusk} or {polar = "day" | "night", noon} for that local day.
func calendarSun(L *lua.LState) int {
	loc := calendarZone(L)
	var d calendar.Date
	switch v := L.Get(1).(type) {
	case lua.LString:
		var err error
		if d, err = calendar.ParseDate(string(v)); err != nil {
			L.ArgError(1, err.Error())
		}
	case lua.LNumber:
		d = calendar.DateOf(unixSec(float64(v)).In(loc))
	default:
		d = calendar.DateOf(clockNow(L).In(loc))
	}
	s := calendar.SunOn(d, float64(L.CheckNumber(2)), float64(L.CheckNumber(3)))
	t := L.NewTable()
	t.RawSetString("date", lua.LString(d.String()))
	t.RawSetString("noon", lua.LNumber(s.Noon.Unix()))
	switch s.Polar {
	case 1:
		t.RawSetString("polar", lua.LString("day"))
	case -1:
		t.RawSetString("polar", lua.LString("night"))
	default:
		t.RawSetString("sunrise", lua.LNumber(s.Rise.Unix()))
		t.RawSetString("sunset", lua.LNumber(s.Set.Unix()))
		t.RawSetString("dayLength", lua.LNumber(s.Set.Sub(s.Rise).Seconds()))
	}
	if !s.Dawn.IsZero() {
		t.RawSetString("dawn", lua.LNumber(s.Dawn.Unix()))
		t.RawSetString("dusk", lua.LNumber(s.Dusk.Unix()))
	}
	L.Push(t)
	return 1
}

// __go_calendar_sun_position(timestamp | nil, lat, lon) -> azimuth, elevation (degrees).
func calendarSunPosition(L *lua.LState) int {
	t := clockNow(L)
	if v, ok := L.Get(1).(lua.LNumber); ok {
		t = unixSec(float64(v))
	}
	az, el := calendar.SunPosition(t, float64(L.CheckNumber(2)), float64(L.CheckNumber(3)))
	L.Push(lua.LNumber(math.Round(az*10) / 10))
	L.Push(lua.LNumber(math.Round(el*10) / 10))
	return 2
}
