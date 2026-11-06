package calendar

import (
	"fmt"
	"time"
)

// Daylight is the part of each day that counts as day: from..to, each a fixed time or a sun
// event with an offset ("sunrise+30m", "dusk").
type Daylight struct {
	from, to dayBound
}

type dayBound struct {
	sec int
	sun sunEvent
	off time.Duration
}

// ParseDaylight parses the bounds of the day; fixedOnly refuses sun events (fallback hours).
func ParseDaylight(from, to string, fixedOnly bool) (*Daylight, error) {
	var dl Daylight
	for _, b := range []struct {
		s   string
		out *dayBound
	}{{from, &dl.from}, {to, &dl.to}} {
		sec, sun, off, err := parseClock(b.s)
		if err != nil {
			return nil, err
		}
		if fixedOnly && sun != noSun {
			return nil, fmt.Errorf("time %q: fallback hours are HH:MM, without the sun", b.s)
		}
		*b.out = dayBound{sec, sun, off}
	}
	return &dl, nil
}

// IsDay reports whether t falls between from and to of its local day. A polar day or night
// decides alone when a bound follows the sun; from at or after to makes the whole day night.
func (dl *Daylight) IsDay(t time.Time, loc *time.Location, lat, lon float64) bool {
	d := DateOf(t.In(loc))
	var s Sun
	if dl.from.sun != noSun || dl.to.sun != noSun {
		s = SunOn(d, lat, lon)
		if s.Polar != 0 {
			return s.Polar > 0
		}
	}
	start := dl.from.at(d, loc, s, d.At(0, loc))
	end := dl.to.at(d, loc, s, (d+1).At(0, loc))
	return !t.Before(start) && t.Before(end)
}

// at is the bound on d; a dawn or dusk that does not come (a white night) gives edge.
func (b dayBound) at(d Date, loc *time.Location, s Sun, edge time.Time) time.Time {
	var t time.Time
	switch b.sun {
	case noSun:
		return d.At(b.sec, loc)
	case sunRise:
		t = s.Rise
	case sunSet:
		t = s.Set
	case sunDawn:
		t = s.Dawn
	case sunDusk:
		t = s.Dusk
	}
	if t.IsZero() {
		return edge
	}
	return t.Add(b.off)
}
