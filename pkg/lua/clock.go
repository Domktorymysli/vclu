package lua

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Clock is the time source for timers, datetime and __go_time_ms.
// Production uses RealClock; the MCP sandbox swaps in a virtual one.
type Clock interface {
	Now() time.Time
}

// RealClock reads the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

const clockKey = "__vclu_clock"

// SetClock makes c the time source of Lua functions running in L.
func SetClock(L *lua.LState, c Clock) {
	ud := L.NewUserData()
	ud.Value = c
	L.G.Registry.RawSetString(clockKey, ud)
}

// clockNow returns the time of the clock set for L, or the system time.
func clockNow(L *lua.LState) time.Time {
	if ud, ok := L.G.Registry.RawGetString(clockKey).(*lua.LUserData); ok {
		if c, ok := ud.Value.(Clock); ok {
			return c.Now()
		}
	}
	return time.Now()
}
