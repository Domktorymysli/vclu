// Package calendar expands the recurrence rules of the Lua Calendar (pkg/lua/runtime/core/Calendar.lua)
// into occurrences. It has no state: the Lua side keeps the calendars and asks for occurrences
// in a time window.
package calendar

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Date is a civil date counted in days since 1970-01-01.
type Date int

// MakeDate returns the Date of y-m-d; out-of-range days normalise like time.Date.
func MakeDate(y int, m time.Month, d int) Date {
	return Date(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// DateOf returns the civil date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return MakeDate(y, m, d)
}

// Civil returns the year, month and day of d.
func (d Date) Civil() (int, time.Month, int) {
	return time.Unix(int64(d)*86400, 0).UTC().Date()
}

// Weekday of d (Sunday = 0).
func (d Date) Weekday() time.Weekday {
	return time.Weekday((int(d)%7 + 7 + 4) % 7) // 1970-01-01 was a Thursday
}

func (d Date) String() string {
	y, m, dd := d.Civil()
	return fmt.Sprintf("%04d-%02d-%02d", y, m, dd)
}

// At returns the moment of the wall clock time sec (seconds after midnight) on d in loc.
// A time skipped by a DST change (02:30 on the spring change) gives the moment the clock
// jumps (03:00), so the occurrence is not lost.
func (d Date) At(sec int, loc *time.Location) time.Time {
	y, m, dd := d.Civil()
	h, mi, s := sec/3600, sec/60%60, sec%60
	t := time.Date(y, m, dd, h, mi, s, 0, loc)
	if t.Hour() != h || t.Minute() != mi {
		start, _ := t.ZoneBounds()
		if !start.IsZero() && DateOf(start.In(loc)) == d {
			return start
		}
	}
	return t
}

// ParseDate parses "YYYY-MM-DD".
func ParseDate(s string) (Date, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("date %q: expected YYYY-MM-DD", s)
	}
	return DateOf(t), nil
}

// ParseRange parses "YYYY-MM-DD" or "YYYY-MM-DD..YYYY-MM-DD" (both ends included).
func ParseRange(s string) (Date, Date, error) {
	a, b, ok := strings.Cut(s, "..")
	from, err := ParseDate(a)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return from, from, nil
	}
	to, err := ParseDate(b)
	if err != nil {
		return 0, 0, err
	}
	if to < from {
		return 0, 0, fmt.Errorf("range %q ends before it starts", s)
	}
	return from, to, nil
}

// ParseTime parses "H:MM", "HH:MM" or "HH:MM:SS" into seconds after midnight.
func ParseTime(s string) (int, error) {
	bad := fmt.Errorf("time %q: expected HH:MM", s)
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, bad
	}
	limits := []int{23, 59, 59}
	sec := 0
	for i, p := range parts {
		if (i == 0 && (len(p) < 1 || len(p) > 2)) || (i > 0 && len(p) != 2) {
			return 0, bad
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > limits[i] {
			return 0, bad
		}
		sec += n * []int{3600, 60, 1}[i]
	}
	return sec, nil
}

// MaxDuration bounds durations and offsets: longer ones are mistakes and make expansion slow.
const MaxDuration = 366 * 24 * time.Hour

// ParseDuration parses "90m", "6h", "1h30m", "2d", "30s", with an optional sign ("-12h"),
// at most MaxDuration.
func ParseDuration(s string) (time.Duration, error) {
	str := strings.TrimSpace(s)
	sign := time.Duration(1)
	if strings.HasPrefix(str, "-") {
		sign, str = -1, str[1:]
	} else if strings.HasPrefix(str, "+") {
		str = str[1:]
	}
	if str == "" {
		return 0, fmt.Errorf("duration %q is empty", s)
	}
	var total time.Duration
	for str != "" {
		i := 0
		for i < len(str) && str[i] >= '0' && str[i] <= '9' {
			i++
		}
		if i == 0 || i == len(str) {
			return 0, fmt.Errorf("duration %q: expected e.g. 30m, 6h, 1d", s)
		}
		n, err := strconv.Atoi(str[:i])
		if err != nil || n > int(MaxDuration/time.Second) {
			return 0, fmt.Errorf("duration %q: at most 366 days", s)
		}
		var unit time.Duration
		switch str[i] {
		case 's':
			unit = time.Second
		case 'm':
			unit = time.Minute
		case 'h':
			unit = time.Hour
		case 'd':
			unit = 24 * time.Hour
		default:
			return 0, fmt.Errorf("duration %q: unknown unit %q", s, str[i])
		}
		if time.Duration(n) > MaxDuration/unit {
			return 0, fmt.Errorf("duration %q: at most 366 days", s)
		}
		total += time.Duration(n) * unit
		if total > MaxDuration {
			return 0, fmt.Errorf("duration %q: at most 366 days", s)
		}
		str = str[i+1:]
	}
	return sign * total, nil
}

type sunEvent int

const (
	noSun sunEvent = iota
	sunRise
	sunSet
	sunDawn
	sunDusk
)

// parseClock parses "HH:MM" or a sun event with an optional offset: "sunrise", "sunset-30m",
// "sunrise+1h15m", "dawn", "dusk" (civil twilight).
func parseClock(s string) (int, sunEvent, time.Duration, error) {
	for name, ev := range map[string]sunEvent{"sunrise": sunRise, "sunset": sunSet, "dawn": sunDawn, "dusk": sunDusk} {
		if rest, ok := strings.CutPrefix(s, name); ok {
			if rest == "" {
				return 0, ev, 0, nil
			}
			if rest[0] != '+' && rest[0] != '-' {
				break
			}
			off, err := ParseDuration(rest)
			if err != nil {
				return 0, 0, 0, fmt.Errorf("time %q: %w", s, err)
			}
			if off < -12*time.Hour || off > 12*time.Hour { // occurrences are looked for within a day
				return 0, 0, 0, fmt.Errorf("time %q: the offset from the sun is at most 12h", s)
			}
			return 0, ev, off, nil
		}
	}
	sec, err := ParseTime(s)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("time %q: expected HH:MM, sunrise, sunset, dawn or dusk (with an offset like -30m)", s)
	}
	return sec, noSun, 0, nil
}
