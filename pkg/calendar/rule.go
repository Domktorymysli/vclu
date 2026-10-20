package calendar

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Spec is a recurrence as written in Lua (:event and :schedule fields).
type Spec struct {
	Every    []string          // "day", "workday", "weekend", "holiday", "month", "year" or weekdays ("mo".."su"); empty: one-off
	Weeks    int               // every n-th week (needs From)
	Months   int               // every n-th month (needs From)
	Years    int               // every n-th year (needs From)
	Day      int               // day of the month, -1 = the last
	Month    int               // month of a yearly "on" (every = "year", on = "last su", month = 10)
	On       string            // "first mo", "last fr", "last workday" (every = "month", or "year" with month)
	Date     string            // one-off "YYYY-MM-DD" or "YYYY-MM-DD..YYYY-MM-DD"; yearly "MM-DD"
	Dates    []string          // a list of days (a schedule published as dates, e.g. waste collection)
	From     string            // first day of the series
	To       string            // last day of the series
	Times    int               // number of occurrences (needs From)
	Time     string            // "HH:MM", "sunrise", "sunset-30m"; empty: all day
	Duration time.Duration     // length of a timed occurrence; 0: none
	Until    string            // end "HH:MM" or "sunrise+15m" (windows; at or before the start ends the next day)
	Lat, Lon float64           // where the sun is reckoned (degrees, east positive)
	Sky      *Sky              // the span of each day when the sun stands in this part of the sky
	Located  bool              // Lat and Lon are set
	Except   []string          // dates or ranges without an occurrence
	Moves    map[string]string // date of an occurrence -> the date it moves to
}

// Sky is a part of the sky: azimuth From..To in degrees from north, clockwise (it may pass north,
// 300..60), and elevation Low..High above the horizon.
type Sky struct {
	AzimuthFrom, AzimuthTo float64
	ElevationLow           float64
	ElevationHigh          float64
}

// Occurrence is one instance of a rule.
type Occurrence struct {
	Date   Date // the day it falls on (after a move)
	Start  time.Time
	End    time.Time // zero when the occurrence has no length
	AllDay bool
}

type kind int

const (
	kindOnce kind = iota
	kindDays      // filtered days: weekday mask, workday, weekend, holiday
	kindMonth
	kindYear
	kindDates
)

type dayFilter int

const (
	filterMask dayFilter = iota
	filterWorkday
	filterWeekend
	filterHoliday
)

// Rule is a compiled Spec.
type Rule struct {
	kind     kind
	filter   dayFilter
	mask     uint8 // bit per time.Weekday
	interval int
	day      int
	nth      int // 1..4, -1 = last
	nthDay   int // time.Weekday, or -1 = a workday
	month    time.Month
	yearOn   bool // every = "year" with on: the n-th weekday or workday of month
	mday     int
	onceFrom Date
	onceTo   Date
	dates    map[Date]bool
	from     Date
	hasFrom  bool
	to       Date
	hasTo    bool
	times    int
	time     int // seconds after midnight, -1 = all day
	until    int // -1 = none
	timeSun  sunEvent
	timeOff  time.Duration
	untilSun sunEvent
	untilOff time.Duration
	lat, lon float64
	sky      *sunRange
	skyMu    sync.Mutex
	skyDays  map[skyKey]skySpan // sunInRange is a few hundred samples: computed once per day
	duration time.Duration
	except   [][2]Date
	moves    map[Date]Date
	movedTo  map[Date]bool
	maxMove  int
	holidays Holidays
}

var weekdays = map[string]time.Weekday{
	"su": time.Sunday, "mo": time.Monday, "tu": time.Tuesday, "we": time.Wednesday,
	"th": time.Thursday, "fr": time.Friday, "sa": time.Saturday,
}

var ordinals = map[string]int{"first": 1, "second": 2, "third": 3, "fourth": 4, "last": -1}

// Compile checks a Spec and prepares it for expansion. holidays may be nil: then "workday"
// is Monday to Friday and "holiday" never matches.
func Compile(s Spec, holidays Holidays) (*Rule, error) {
	r := &Rule{time: -1, until: -1, holidays: holidays, interval: 1, duration: s.Duration}
	if r.holidays == nil {
		r.holidays = func(Date) bool { return false }
	}
	if s.Time != "" {
		t, sun, off, err := parseClock(s.Time)
		if err != nil {
			return nil, err
		}
		r.time, r.timeSun, r.timeOff = t, sun, off
	}
	if s.Until != "" {
		t, sun, off, err := parseClock(s.Until)
		if err != nil {
			return nil, err
		}
		r.until, r.untilSun, r.untilOff = t, sun, off
	}
	if (r.timeSun != noSun || r.untilSun != noSun || s.Sky != nil) && !s.Located {
		return nil, errors.New("the sun needs the calendar's location (:location(lat, lon))")
	}
	if k := s.Sky; k != nil {
		switch {
		case s.Time != "" || s.Until != "" || s.Duration != 0:
			return nil, errors.New("sun (a part of the sky) sets its own hours: drop time, hours and duration")
		case k.AzimuthFrom < 0 || k.AzimuthFrom > 360 || k.AzimuthTo < 0 || k.AzimuthTo > 360:
			return nil, errors.New("sun azimuth expects degrees 0..360 from north (90 east, 180 south)")
		case k.ElevationLow < -90 || k.ElevationHigh > 90 || k.ElevationLow >= k.ElevationHigh:
			return nil, errors.New("sun elevation expects low < high within -90..90 degrees")
		}
		r.sky = &sunRange{azFrom: k.AzimuthFrom, azTo: k.AzimuthTo, elFrom: k.ElevationLow, elTo: k.ElevationHigh}
		r.time = 0 // a timed occurrence, its span from the sun
	}
	r.lat, r.lon = s.Lat, s.Lon
	if s.Duration < 0 {
		return nil, errors.New("duration must not be negative")
	}
	if s.From != "" {
		d, err := ParseDate(s.From)
		if err != nil {
			return nil, fmt.Errorf("from: %w", err)
		}
		r.from, r.hasFrom = d, true
	}
	if s.To != "" {
		d, err := ParseDate(s.To)
		if err != nil {
			return nil, fmt.Errorf("to: %w", err)
		}
		r.to, r.hasTo = d, true
	}
	if r.hasFrom && r.hasTo && r.to < r.from {
		return nil, errors.New("to is before from")
	}
	if s.Times < 0 {
		return nil, errors.New("times must not be negative")
	}
	r.times = s.Times

	if err := r.compileEvery(s); err != nil {
		return nil, err
	}
	if s.Month != 0 && r.kind != kindYear {
		return nil, errors.New("month goes with every = 'year' and on")
	}

	n := 0
	for _, v := range []int{s.Weeks, s.Months, s.Years} {
		if v != 0 {
			n++
			if v < 1 {
				return nil, errors.New("weeks, months and years must be at least 1")
			}
			r.interval = v
		}
	}
	switch {
	case n > 1:
		return nil, errors.New("give only one of weeks, months, years")
	case s.Weeks != 0 && r.kind != kindDays:
		return nil, errors.New("weeks needs every = a weekday or days of the week")
	case s.Months != 0 && r.kind != kindMonth:
		return nil, errors.New("months needs every = 'month'")
	case r.kind == kindDates && (r.times > 0 || n > 0):
		return nil, errors.New("dates takes no weeks, months, years or times")
	case s.Years != 0 && r.kind != kindYear:
		return nil, errors.New("years needs every = 'year'")
	case r.interval > 1 && !r.hasFrom:
		return nil, errors.New("an interval (weeks, months, years) needs from: the day the series starts")
	case r.times > 0 && !r.hasFrom:
		return nil, errors.New("times needs from: the day the series starts")
	case r.kind == kindOnce && (r.times > 0 || r.hasFrom || r.hasTo):
		return nil, errors.New("from, to and times belong to a repeating event (every)")
	}

	for _, e := range s.Except {
		a, b, err := ParseRange(e)
		if err != nil {
			return nil, fmt.Errorf("except: %w", err)
		}
		r.except = append(r.except, [2]Date{a, b})
	}
	if len(s.Moves) > 0 {
		r.moves, r.movedTo = map[Date]Date{}, map[Date]bool{}
		for k, v := range s.Moves {
			a, err := ParseDate(k)
			if err != nil {
				return nil, fmt.Errorf("moves: %w", err)
			}
			b, err := ParseDate(v)
			if err != nil {
				return nil, fmt.Errorf("moves: %w", err)
			}
			r.moves[a] = b
			r.movedTo[b] = true
			r.maxMove = max(r.maxMove, abs(int(b-a)))
		}
	}
	return r, nil
}

func (r *Rule) compileEvery(s Spec) error {
	if len(s.Dates) > 0 {
		if len(s.Every) > 0 || s.Date != "" || s.Day != 0 || s.On != "" {
			return errors.New("dates is a list of days: drop every, date, day and on")
		}
		r.kind, r.dates = kindDates, map[Date]bool{}
		for _, v := range s.Dates {
			d, err := ParseDate(v)
			if err != nil {
				return fmt.Errorf("dates: %w", err)
			}
			r.dates[d] = true
		}
		return nil
	}
	if len(s.Every) == 0 {
		if s.Date == "" {
			return errors.New("give every (a repeating event) or date (a one-off)")
		}
		if s.Day != 0 || s.On != "" {
			return errors.New("day and on need every = 'month'")
		}
		a, b, err := ParseRange(s.Date)
		if err != nil {
			return err
		}
		if a != b && r.time >= 0 {
			return errors.New("a range of days is an all-day event: drop time")
		}
		r.kind, r.onceFrom, r.onceTo = kindOnce, a, b
		return nil
	}
	if len(s.Every) == 1 {
		switch e := strings.ToLower(s.Every[0]); e {
		case "day", "workday", "weekend", "holiday":
			r.kind = kindDays
			r.filter = map[string]dayFilter{"day": filterMask, "workday": filterWorkday, "weekend": filterWeekend, "holiday": filterHoliday}[e]
			r.mask = 0x7f
			return r.noMonthFields(s, e)
		case "month":
			r.kind = kindMonth
			if s.Date != "" {
				return errors.New("every = 'month' takes day or on, not date")
			}
			switch {
			case s.Day != 0 && s.On != "":
				return errors.New("give day or on, not both")
			case s.Day != 0:
				if s.Day < -1 || s.Day > 31 {
					return fmt.Errorf("day %d: expected 1..31 or -1 (the last)", s.Day)
				}
				r.day = s.Day
			case s.On != "":
				return r.compileOn(s.On)
			default:
				return errors.New("every = 'month' needs day (10, -1 = last) or on ('first mo')")
			}
			return nil
		case "year":
			r.kind = kindYear
			if s.Day != 0 {
				return errors.New("day needs every = 'month'")
			}
			if s.On != "" {
				if s.Date != "" || s.Month < 1 || s.Month > 12 {
					return errors.New("every = 'year' with on needs month = 1..12 and no date")
				}
				r.month, r.yearOn = time.Month(s.Month), true
				return r.compileOn(s.On)
			}
			if s.Month != 0 {
				return errors.New("month goes with on ('last su'); a fixed day is date = 'MM-DD'")
			}
			var m, d int
			if _, err := fmt.Sscanf(s.Date, "%d-%d", &m, &d); err != nil || len(s.Date) != 5 || m < 1 || m > 12 || d < 1 || d > 31 {
				return fmt.Errorf("every = 'year' needs date = 'MM-DD', or on with month, got %q", s.Date)
			}
			r.month, r.mday = time.Month(m), d
			return nil
		}
	}
	r.kind, r.filter = kindDays, filterMask
	for _, e := range s.Every {
		wd, ok := weekdays[strings.ToLower(e)]
		if !ok {
			return fmt.Errorf("every %q: expected day, workday, weekend, holiday, month, year or a weekday (mo, tu, we, th, fr, sa, su)", e)
		}
		r.mask |= 1 << wd
	}
	return r.noMonthFields(s, "a weekday")
}

func (r *Rule) noMonthFields(s Spec, every string) error {
	if s.Day != 0 || s.On != "" {
		return errors.New("day and on need every = 'month'")
	}
	if s.Date != "" {
		return fmt.Errorf("every = %s takes from/to, not date", every)
	}
	return nil
}

func (r *Rule) compileOn(on string) error {
	f := strings.Fields(strings.ToLower(on))
	if len(f) == 2 {
		if n, ok := ordinals[f[0]]; ok {
			if f[1] == "workday" {
				if n != 1 && n != -1 {
					return fmt.Errorf("on %q: workday takes first or last", on)
				}
				r.nth, r.nthDay = n, -1
				return nil
			}
			if wd, ok := weekdays[f[1]]; ok {
				r.nth, r.nthDay = n, int(wd)
				return nil
			}
		}
	}
	return fmt.Errorf("on %q: expected e.g. 'first mo', 'last fr', 'last workday'", on)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (r *Rule) workday(d Date) bool {
	wd := d.Weekday()
	return wd != time.Saturday && wd != time.Sunday && !r.holidays(d)
}

// matches reports whether the series has an occurrence on its original date d
// (before except, moves and the bounds).
func (r *Rule) matches(d Date) bool {
	switch r.kind {
	case kindDays:
		wd := d.Weekday()
		switch r.filter {
		case filterWorkday:
			if !r.workday(d) {
				return false
			}
		case filterWeekend:
			if wd != time.Saturday && wd != time.Sunday {
				return false
			}
		case filterHoliday:
			if !r.holidays(d) {
				return false
			}
		default:
			if r.mask&(1<<wd) == 0 {
				return false
			}
		}
		if r.interval > 1 {
			weeks := (mondayOf(d) - mondayOf(r.from)) / 7
			return int(weeks)%r.interval == 0
		}
		return true
	case kindMonth:
		y, m, dd := d.Civil()
		if r.interval > 1 {
			fy, fm, _ := r.from.Civil()
			if ((y*12+int(m))-(fy*12+int(fm)))%r.interval != 0 {
				return false
			}
		}
		switch {
		case r.day == -1:
			return d == MakeDate(y, m+1, 0)
		case r.day > 0:
			return dd == r.day // months without that day are skipped
		default:
			return r.nthMatch(d)
		}
	case kindDates:
		return r.dates[d]
	case kindYear:
		y, m, dd := d.Civil()
		if r.interval > 1 {
			fy, _, _ := r.from.Civil()
			if (y-fy)%r.interval != 0 {
				return false
			}
		}
		if r.yearOn {
			return m == r.month && r.nthMatch(d)
		}
		return m == r.month && dd == r.mday // 02-29 only in leap years
	}
	return false
}

// nthMatch: d is the on-day ("first mo", "last workday") of its month.
func (r *Rule) nthMatch(d Date) bool {
	y, m, dd := d.Civil()
	last := MakeDate(y, m+1, 0)
	if r.nthDay == -1 {
		return d == r.nthWorkday(y, m, last)
	}
	if int(d.Weekday()) != r.nthDay {
		return false
	}
	if r.nth == -1 {
		return d+7 > last
	}
	return (dd-1)/7+1 == r.nth
}

func (r *Rule) nthWorkday(y int, m time.Month, last Date) Date {
	if r.nth == -1 {
		d := last
		for !r.workday(d) {
			d--
		}
		return d
	}
	d := MakeDate(y, m, 1)
	for !r.workday(d) {
		d++
	}
	return d
}

func mondayOf(d Date) Date {
	return d - Date((int(d.Weekday())+6)%7)
}

func (r *Rule) excepted(d Date) bool {
	for _, e := range r.except {
		if d >= e[0] && d <= e[1] {
			return true
		}
	}
	return false
}

// moment is the time of day sec (or the sun event with its offset) on d; false when the sun
// does not rise or set that day (polar day or night).
func (r *Rule) moment(d Date, sec int, sun sunEvent, off time.Duration, loc *time.Location) (time.Time, bool) {
	if sun == noSun {
		return d.At(sec, loc), true
	}
	s := SunOn(d, r.lat, r.lon)
	var t time.Time
	switch sun {
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
		return time.Time{}, false
	}
	return t.Add(off), true
}

func (r *Rule) occurrence(orig Date, loc *time.Location) (Occurrence, bool) {
	d := orig
	if nd, ok := r.moves[orig]; ok {
		d = nd
	}
	if r.sky != nil {
		sp := r.skySpan(d, loc)
		if !sp.ok {
			return Occurrence{}, false
		}
		return Occurrence{Date: d, Start: sp.start, End: sp.end}, true
	}
	if r.kind == kindOnce && r.time < 0 {
		return Occurrence{Date: d, Start: d.At(0, loc), End: (d + r.onceTo - r.onceFrom + 1).At(0, loc), AllDay: true}, true
	}
	if r.time < 0 {
		end := d + 1
		if r.duration >= 24*time.Hour {
			end = d + Date(r.duration/(24*time.Hour))
		}
		return Occurrence{Date: d, Start: d.At(0, loc), End: end.At(0, loc), AllDay: true}, true
	}
	start, ok := r.moment(d, r.time, r.timeSun, r.timeOff, loc)
	if !ok {
		return Occurrence{}, false
	}
	o := Occurrence{Date: d, Start: start}
	switch {
	case r.until >= 0:
		end, ok := r.moment(d, r.until, r.untilSun, r.untilOff, loc)
		if ok && !end.After(start) {
			end, ok = r.moment(d+1, r.until, r.untilSun, r.untilOff, loc)
		}
		if !ok {
			return Occurrence{}, false
		}
		o.End = end
	case r.duration > 0:
		o.End = o.Start.Add(r.duration)
	}
	return o, true
}

type skyKey struct {
	d   Date
	loc *time.Location
}

type skySpan struct {
	start, end time.Time
	ok         bool
}

func (r *Rule) skySpan(d Date, loc *time.Location) skySpan {
	r.skyMu.Lock()
	defer r.skyMu.Unlock()
	k := skyKey{d, loc}
	if sp, ok := r.skyDays[k]; ok {
		return sp
	}
	if r.skyDays == nil || len(r.skyDays) > 400 {
		r.skyDays = map[skyKey]skySpan{}
	}
	var sp skySpan
	sp.start, sp.end, sp.ok = sunInRange(d, loc, r.lat, r.lon, *r.sky)
	r.skyDays[k] = sp
	return sp
}

// span is how far (in days) an occurrence can reach past its start day.
func (r *Rule) span() Date {
	if r.kind == kindOnce {
		return r.onceTo - r.onceFrom + 2
	}
	return Date(r.duration/(24*time.Hour)) + 2
}

// Between returns the occurrences that start in [from, to) or, with overlapping, are still
// going on at from. Sorted by start. loc gives the wall clock of the rule's times.
func (r *Rule) Between(from, to time.Time, loc *time.Location, overlapping bool) []Occurrence {
	if !to.After(from) {
		return nil
	}
	first := DateOf(from.In(loc)) - Date(r.maxMove) - 1
	if overlapping {
		first -= r.span()
	}
	last := DateOf(to.In(loc)) + Date(r.maxMove) + 1
	var out []Occurrence
	r.walk(first, last, loc, func(o Occurrence) {
		in := !o.Start.Before(from) && o.Start.Before(to)
		if !in && overlapping && o.Start.Before(from) && o.End.After(from) {
			in = true
		}
		if in {
			out = append(out, o)
		}
	})
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// walk calls fn for each occurrence whose original date lies in [first, last].
func (r *Rule) walk(first, last Date, loc *time.Location, fn func(Occurrence)) {
	if r.kind == kindOnce {
		if r.onceFrom >= first && r.onceFrom <= last && !r.excepted(r.onceFrom) {
			if o, ok := r.occurrence(r.onceFrom, loc); ok {
				fn(o)
			}
		}
		return
	}
	start := first
	count := 0
	if r.hasFrom && (r.times > 0 || start < r.from) {
		start = r.from // times counts from the start of the series
	}
	if r.hasTo && last > r.to {
		last = r.to
	}
	for d := start; d <= last; d++ {
		if !r.matches(d) {
			continue
		}
		count++
		if r.times > 0 && count > r.times {
			return
		}
		if d < first || r.excepted(d) {
			continue
		}
		if o, ok := r.occurrence(d, loc); ok {
			fn(o)
		}
	}
}

// Next returns the first occurrence that starts after t, searching up to horizon ahead.
func (r *Rule) Next(t time.Time, horizon time.Duration, loc *time.Location) (Occurrence, bool) {
	const chunk = 62 * 24 * time.Hour
	for from := t; from.Before(t.Add(horizon)); from = from.Add(chunk) {
		to := from.Add(chunk)
		for _, o := range r.Between(from, to, loc, false) {
			if o.Start.After(t) {
				return o, true
			}
		}
	}
	return Occurrence{}, false
}
