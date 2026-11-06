package calendar

import (
	"strings"
	"testing"
	"time"
)

func mustDaylight(t *testing.T, from, to string) *Daylight {
	t.Helper()
	dl, err := ParseDaylight(from, to, false)
	if err != nil {
		t.Fatal(err)
	}
	return dl
}

// In a white night (62 N in June) there is no dusk: a day up to dusk lasts until midnight.
func TestDaylightWithoutDusk(t *testing.T) {
	const lat, lon = 62.0, 21.0
	if s := SunOn(MakeDate(2026, 6, 21), lat, lon); s.Polar != 0 || !s.Dusk.IsZero() {
		t.Fatalf("62 N on 21 June: %+v, want a sunset without a dusk", s)
	}
	dl := mustDaylight(t, "sunrise", "dusk")
	late := time.Date(2026, 6, 21, 23, 50, 0, 0, warsaw)
	if !dl.IsDay(late, warsaw, lat, lon) {
		t.Error("23:50 without a dusk: night, want day")
	}
	if dl.IsDay(time.Date(2026, 6, 21, 0, 10, 0, 0, warsaw), warsaw, lat, lon) {
		t.Error("00:10 before sunrise: day, want night")
	}
}

// Fixed hours follow the wall clock across a DST change.
func TestDaylightFixedOnDSTChange(t *testing.T) {
	dl, err := ParseDaylight("06:00", "20:00", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Date{MakeDate(2026, 3, 29), MakeDate(2026, 10, 25)} { // spring, autumn
		y, m, dd := d.Civil()
		for _, c := range []struct {
			h, min int
			want   bool
		}{{5, 59, false}, {6, 0, true}, {19, 59, true}, {20, 0, false}} {
			at := time.Date(y, m, dd, c.h, c.min, 0, 0, warsaw)
			if got := dl.IsDay(at, warsaw, 0, 0); got != c.want {
				t.Errorf("%s: %v, want %v", at, got, c.want)
			}
		}
	}
}

func TestDaylightRefuses(t *testing.T) {
	for _, c := range []struct {
		from, to  string
		fixedOnly bool
		want      string
	}{
		{"sunrise", "sunset", true, `time "sunrise": fallback hours are HH:MM`},
		{"06:00", "dusk-10m", true, `time "dusk-10m"`},
		{"sunrise+", "sunset", false, `time "sunrise+"`},
		{"06:00", "24:00", false, `time "24:00"`},
		{"noon", "sunset", false, `time "noon"`},
	} {
		if _, err := ParseDaylight(c.from, c.to, c.fixedOnly); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s..%s: %v, want %q", c.from, c.to, err, c.want)
		}
	}
}
