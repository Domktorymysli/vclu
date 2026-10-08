package lua

import (
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestClock_DrivesDatetimeAndTimeMs(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	RegisterParserFunctions(L)
	at := time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC)
	SetClock(L, fixedClock{at})

	if err := L.DoString(`
		datetime.setTimezone("UTC")
		local n = datetime.now()
		_got = string.format("%d-%d-%d %d:%d:%d", n.year, n.month, n.day, n.hour, n.minute, n.second)
		_ts = datetime.format({ month = 1, day = 2 }, "2006")
	`); err != nil {
		t.Fatal(err)
	}
	if got := L.GetGlobal("_got").String(); got != "2031-3-4 5:6:7" {
		t.Errorf("datetime.now() = %s", got)
	}
	if got := L.GetGlobal("_ts").String(); got != "2031" {
		t.Errorf("default year = %s, want the clock's year", got)
	}
}

func TestClock_DefaultIsSystemTime(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if d := time.Since(clockNow(L)); d < 0 || d > time.Second {
		t.Errorf("clockNow without SetClock is %v off system time", d)
	}
}

func TestClock_TimeMsAndTimers(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	at := time.Date(2031, 3, 4, 5, 6, 7, 0, time.UTC)
	SetClock(engine.state, fixedClock{at})
	got, err := engine.Execute(`tostring(__go_time_ms())`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1930367167000" {
		t.Errorf("__go_time_ms() = %s, want %d", got, at.UnixMilli())
	}

	tm := NewTimerManager()
	tm.clock = fixedClock{at}
	id := tm.SetTimeout(lua.LNil, 1500)
	if next := tm.timers[id].NextFire; !next.Equal(at.Add(1500 * time.Millisecond)) {
		t.Errorf("NextFire = %v, want clock + 1.5s", next)
	}
}
