package mcp

import (
	"strings"
	"testing"
	"time"

	vclulua "github.com/Domktorymysli/vclu/pkg/lua"
	lua "github.com/yuin/gopher-lua"
)

var clockStart = time.Date(2026, 10, 4, 21, 0, 0, 0, time.UTC)

func newClockState(t *testing.T) (*lua.LState, *sandboxTimers) {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	vclulua.RegisterParserFunctions(L)
	st := newSandboxTimers(&sandboxClock{now: clockStart})
	if err := st.install(L); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(`_log = {} function log(s) table.insert(_log, s) end`); err != nil {
		t.Fatal(err)
	}
	return L, st
}

func advance(t *testing.T, L *lua.LState, st *sandboxTimers, d time.Duration) {
	t.Helper()
	if err := st.advance(L, d, func(err error) { t.Errorf("callback error: %v", err) }); err != nil {
		t.Fatal(err)
	}
}

func logOf(L *lua.LState) string {
	var out []string
	L.GetGlobal("_log").(*lua.LTable).ForEach(func(_, v lua.LValue) { out = append(out, v.String()) })
	return strings.Join(out, ",")
}

func TestSandboxTimers_OrderAndWindow(t *testing.T) {
	L, st := newClockState(t)
	if err := L.DoString(`
		__go_setTimeout(function() log("b") end, 2000)
		__go_setTimeout(function() log("a") end, 1000)
		__go_setTimeout(function() log("a2") end, 1000)
		__go_setTimeout(function() log("late") end, 5000)
	`); err != nil {
		t.Fatal(err)
	}
	advance(t, L, st, 999*time.Millisecond)
	if got := logOf(L); got != "" {
		t.Fatalf("fired before deadline: %s", got)
	}
	advance(t, L, st, 2*time.Second)
	if got := logOf(L); got != "a,a2,b" {
		t.Fatalf("order = %s, want a,a2,b", got)
	}
	if !st.clock.now.Equal(clockStart.Add(2999 * time.Millisecond)) {
		t.Fatalf("clock = %v", st.clock.now)
	}
}

func TestSandboxTimers_IntervalCatchUpAndClear(t *testing.T) {
	L, st := newClockState(t)
	if err := L.DoString(`
		local n = 0
		_id = __go_setInterval(function()
			n = n + 1
			log("tick" .. n)
			if n == 3 then __go_clearTimer(_id) end
		end, 1000)
	`); err != nil {
		t.Fatal(err)
	}
	advance(t, L, st, 10*time.Second)
	if got := logOf(L); got != "tick1,tick2,tick3" {
		t.Fatalf("got %s", got)
	}
}

func TestSandboxTimers_CallbackSchedulesWithinWindow(t *testing.T) {
	L, st := newClockState(t)
	if err := L.DoString(`
		__go_setTimeout(function()
			log("first@" .. os.time())
			__go_setTimeout(function() log("second@" .. os.time()) end, 500)
			__go_setTimeout(function() log("outside") end, 5000)
		end, 1000)
	`); err != nil {
		t.Fatal(err)
	}
	advance(t, L, st, 2*time.Second)
	base := clockStart.Unix()
	want := "first@" + itoa(base+1) + ",second@" + itoa(base+1)
	if got := logOf(L); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestSandboxTimers_RunawayLoopStops(t *testing.T) {
	L, st := newClockState(t)
	if err := L.DoString(`
		local function again() __go_setTimeout(again, 1) end
		again()
	`); err != nil {
		t.Fatal(err)
	}
	err := st.advance(L, time.Hour, func(error) {})
	if err == nil || !strings.Contains(err.Error(), "runaway") {
		t.Fatalf("want runaway error, got %v", err)
	}
}

func TestSandboxTimers_CallbackErrorReported(t *testing.T) {
	L, st := newClockState(t)
	if err := L.DoString(`
		__go_setTimeout(function() error("boom") end, 10)
		__go_setTimeout(function() log("after") end, 20)
	`); err != nil {
		t.Fatal(err)
	}
	var errs []error
	if err := st.advance(L, time.Second, func(e error) { errs = append(errs, e) }); err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "boom") {
		t.Fatalf("errors = %v", errs)
	}
	if logOf(L) != "after" {
		t.Fatal("a failing callback must not stop later timers")
	}
}

func TestSandboxClock_OSAndDatetime(t *testing.T) {
	L, st := newClockState(t)
	advance(t, L, st, 90*time.Minute)
	if err := L.DoString(`
		datetime.setTimezone("Europe/Warsaw")
		_now = os.time()
		_ms = __go_time_ms()
		_hour = datetime.now().hour
		_explicit = os.time({ year = 2020, month = 1, day = 1, hour = 0 })
		_date = os.date("!%Y-%m-%d %H:%M", 0)
		_dateNow = os.date("!%H:%M")
	`); err != nil {
		t.Fatal(err)
	}
	want := clockStart.Add(90 * time.Minute)
	if got := L.GetGlobal("_now").String(); got != itoa(want.Unix()) {
		t.Errorf("os.time() = %s, want %d", got, want.Unix())
	}
	if got := L.GetGlobal("_ms").String(); got != itoa(want.UnixMilli()) {
		t.Errorf("__go_time_ms() = %s", got)
	}
	// 22:30 UTC on 4 Oct is 00:30 CEST
	if got := L.GetGlobal("_hour").String(); got != "0" {
		t.Errorf("datetime.now().hour = %s, want 0", got)
	}
	if got := L.GetGlobal("_explicit").String(); got == itoa(want.Unix()) {
		t.Error("os.time(table) must convert the table, not return the virtual now")
	}
	if got := L.GetGlobal("_date").String(); got != "1970-01-01 00:00" {
		t.Errorf("os.date with timestamp = %s", got)
	}
	if got := L.GetGlobal("_dateNow").String(); got != "22:30" {
		t.Errorf("os.date without timestamp = %s, want 22:30", got)
	}
}

func itoa(n int64) string { return lua.LNumber(n).String() }

func TestSandboxClock_OSDateTable(t *testing.T) {
	L, _ := newClockState(t)
	if err := L.DoString(`_t = os.date("!*t")`); err != nil {
		t.Fatal(err)
	}
	tbl := L.GetGlobal("_t").(*lua.LTable)
	if h := tbl.RawGetString("hour").String(); h != "21" {
		t.Errorf(`os.date("!*t").hour = %s, want 21`, h)
	}
}

func TestSandboxTimers_ProductionLimits(t *testing.T) {
	L, _ := newClockState(t)
	if err := L.DoString(`
		for i = 1, 1000 do __go_setTimeout(function() end, 100000000) end
		_rejected = __go_setTimeout(function() end, 10)
	`); err != nil {
		t.Fatal(err)
	}
	if got := L.GetGlobal("_rejected").String(); got != "-1" {
		t.Errorf("timer 1001 = %s, want -1 like production", got)
	}

	L2, st2 := newClockState(t)
	if err := L2.DoString(`__go_setTimeout(function() log("fired@" .. os.time()) end, 48 * 3600 * 1000)`); err != nil {
		t.Fatal(err)
	}
	advance(t, L2, st2, 25*time.Hour)
	if got := logOf(L2); got != "fired@"+itoa(clockStart.Add(24*time.Hour).Unix()) {
		t.Errorf("48 h timeout must be clamped to 24 h like production, got %q", got)
	}
}
