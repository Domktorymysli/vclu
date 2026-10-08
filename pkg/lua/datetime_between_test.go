package lua

import (
	"fmt"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func betweenState(t *testing.T, at time.Time) *lua.LState {
	t.Helper()
	L := lua.NewState()
	t.Cleanup(L.Close)
	RegisterParserFunctions(L)
	SetClock(L, fixedClock{at})
	if err := L.DoString(`datetime.setTimezone("Europe/Warsaw")`); err != nil {
		t.Fatal(err)
	}
	return L
}

func between(t *testing.T, L *lua.LState, args string) bool {
	t.Helper()
	if err := L.DoString("_r = datetime.between(" + args + ")"); err != nil {
		t.Fatalf("between(%s): %v", args, err)
	}
	return L.GetGlobal("_r") == lua.LTrue
}

func TestDatetimeBetween_Windows(t *testing.T) {
	warsaw, _ := time.LoadLocation("Europe/Warsaw")
	cases := []struct {
		local, from, to string
		want            bool
	}{
		// across midnight, [from, to)
		{"20:59:59", "21:00", "06:00", false},
		{"21:00:00", "21:00", "06:00", true},
		{"23:59:59", "21:00", "06:00", true},
		{"00:00:00", "21:00", "06:00", true},
		{"05:59:59", "21:00", "06:00", true},
		{"06:00:00", "21:00", "06:00", false},
		{"12:00:00", "21:00", "06:00", false},
		// same day
		{"07:59:59", "08:00", "16:00", false},
		{"08:00:00", "08:00", "16:00", true},
		{"15:59:59", "08:00", "16:00", true},
		{"16:00:00", "08:00", "16:00", false},
		// seconds and single-digit hours
		{"07:30:29", "7:30:30", "7:31", false},
		{"07:30:30", "7:30:30", "7:31", true},
		// equal ends: the whole day
		{"03:00:00", "10:00", "10:00", true},
		{"10:00:00", "10:00", "10:00", true},
	}
	for _, c := range cases {
		local, _ := time.ParseInLocation("2006-01-02 15:04:05", "2026-10-07 "+c.local, warsaw)
		L := betweenState(t, local)
		if got := between(t, L, fmt.Sprintf("%q, %q", c.from, c.to)); got != c.want {
			t.Errorf("%s in [%s, %s) = %v, want %v", c.local, c.from, c.to, got, c.want)
		}
	}
}

// The window follows local time across DST: 19:30 UTC is 21:30 CEST on
// 24 Oct (night) but 20:30 CET on 25 Oct, after clocks went back (not night).
func TestDatetimeBetween_DST(t *testing.T) {
	for day, want := range map[int]bool{24: true, 25: false} {
		L := betweenState(t, time.Date(2026, 10, day, 19, 30, 0, 0, time.UTC))
		if got := between(t, L, `"21:00", "06:00"`); got != want {
			t.Errorf("19:30 UTC on %d Oct: %v, want %v", day, got, want)
		}
	}
}

// Wall-clock semantics on transition days in Warsaw: the skipped hour
// 02:00-03:00 on 29 Mar never matches, the repeated one on 25 Oct matches
// for two real hours.
func TestDatetimeBetween_DSTGapAndRepeat(t *testing.T) {
	cases := []struct {
		utc  string
		want bool
	}{
		{"2026-03-29T00:59:59Z", false}, // 01:59:59 CET
		{"2026-03-29T01:00:00Z", false}, // 03:00:00 CEST, 02:xx never exists
		{"2026-03-29T01:30:00Z", false},
		{"2026-10-25T00:00:00Z", true},  // 02:00 CEST
		{"2026-10-25T00:59:59Z", true},  // 02:59:59 CEST
		{"2026-10-25T01:00:00Z", true},  // 02:00 CET, the hour again
		{"2026-10-25T01:59:59Z", true},  // 02:59:59 CET
		{"2026-10-25T02:00:00Z", false}, // 03:00 CET
	}
	for _, c := range cases {
		at, _ := time.Parse(time.RFC3339, c.utc)
		L := betweenState(t, at)
		if got := between(t, L, `"02:00", "03:00"`); got != c.want {
			t.Errorf("%s: %v, want %v", c.utc, got, c.want)
		}
	}
}

func TestDatetimeBetween_ExplicitTime(t *testing.T) {
	L := betweenState(t, time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC))
	night := time.Date(2026, 10, 7, 21, 30, 0, 0, time.UTC).Unix() // 23:30 CEST
	if !between(t, L, fmt.Sprintf(`"21:00", "06:00", %d`, night)) {
		t.Error("timestamp argument ignored")
	}
	if !between(t, L, fmt.Sprintf(`"21:00", "06:00", %d`, night*1000)) {
		t.Error("millisecond timestamp not recognised")
	}
	if !between(t, L, `"21:00", "06:00", datetime.parse("2026-10-07 22:15:00")`) {
		t.Error("datetime table argument ignored")
	}
	if between(t, L, `"21:00", "06:00"`) {
		t.Error("now (12:00 CEST) must be outside the night window")
	}
}

func TestDatetimeBetween_BadArguments(t *testing.T) {
	L := betweenState(t, time.Now())
	for _, args := range []string{`"25:00", "06:00"`, `"21:00", "6"`, `"21:5", "06:00"`, `"ab:cd", "06:00"`,
		`"21:00", "06:00:60"`, `nil, "06:00"`, `"21:00", "06:00", "tomorrow"`,
		`"+7:00", "08:00"`, `"007:00", "08:00"`, `"07:+5", "08:00"`, `"-0:00", "08:00"`, `"24:00", "08:00"`,
		`" 7:00", "08:00"`, `"21:00", "06:00", 0/0`} {
		err := L.DoString("datetime.between(" + args + ")")
		if err == nil || !strings.Contains(err.Error(), "bad argument") {
			t.Errorf("between(%s): err = %v, want bad argument", args, err)
		}
	}
}
