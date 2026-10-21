package calendar

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Second review of the rule engine: holidays against a list kept apart from the code,
// month and year edges, series bounds, schedules and the sky cache under -race.

var oslo = mustZone("Europe/Oslo")

const tromsoLat, tromsoLon = 69.65, 18.96

func mustCompile(t *testing.T, s Spec, hol Holidays) *Rule {
	t.Helper()
	r, err := Compile(s, hol)
	if err != nil {
		t.Fatalf("Compile(%+v): %v", s, err)
	}
	return r
}

func dates(t *testing.T, s Spec, hol Holidays, from, to string) string {
	t.Helper()
	var out []string
	for _, o := range mustCompile(t, s, hol).Between(day(from), day(to), warsaw, false) {
		out = append(out, o.Date.String())
	}
	return strings.Join(out, " ")
}

func TestReview2_PolishHolidays2024to2035(t *testing.T) {
	// Easter Sunday, from published tables
	easters := map[int]string{2024: "03-31", 2025: "04-20", 2026: "04-05", 2027: "03-28", 2028: "04-16", 2029: "04-01",
		2030: "04-21", 2031: "04-13", 2032: "03-28", 2033: "04-17", 2034: "04-09", 2035: "03-25"}
	for y := 2024; y <= 2035; y++ {
		e, err := time.Parse("2006-01-02", fmt.Sprintf("%d-%s", y, easters[y]))
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{}
		for _, md := range []string{"01-01", "01-06", "05-01", "05-03", "08-15", "11-01", "11-11", "12-25", "12-26"} {
			want[fmt.Sprintf("%d-%s", y, md)] = "fixed"
		}
		if y >= 2025 {
			want[fmt.Sprintf("%d-12-24", y)] = "Wigilia"
		}
		for _, n := range []int{0, 1, 49, 60} {
			want[e.AddDate(0, 0, n).Format("2006-01-02")] = "Easter+" + fmt.Sprint(n)
		}
		var got, exp []string
		for d := MakeDate(y, 1, 1); d < MakeDate(y+1, 1, 1); d++ {
			if name := polishHoliday(d); name != "" {
				got = append(got, d.String())
				if HolidayName("pl", d) != name {
					t.Errorf("%s: HolidayName differs", d)
				}
			}
		}
		for k := range want {
			exp = append(exp, k)
		}
		sort.Strings(exp)
		if strings.Join(got, " ") != strings.Join(exp, " ") {
			t.Errorf("%d:\n got %v\nwant %v", y, got, exp)
		}
		if got := polishHoliday(MakeDate(y, e.Month(), e.Day()+60)); got != "Boże Ciało" {
			t.Errorf("%d: Easter+60 is %q", y, got)
		}
		if got := polishHoliday(MakeDate(y, e.Month(), e.Day()+49)); got != "Zielone Świątki" {
			t.Errorf("%d: Easter+49 is %q", y, got)
		}
	}
	if HolidayName("de", MakeDate(2026, 1, 1)) != "" {
		t.Error("an unknown country has no holiday names")
	}
	if _, err := HolidaysFor("de"); err == nil {
		t.Error("unknown holidays accepted")
	}
}

func TestReview2_WorkdayAroundWeekendHolidays(t *testing.T) {
	// 2027-05-01 Saturday, 05-03 Monday (Konstytucja): a Saturday holiday moves nothing
	got := dates(t, Spec{Every: []string{"workday"}}, polish, "2027-04-29", "2027-05-06")
	if got != "2027-04-29 2027-04-30 2027-05-04 2027-05-05" {
		t.Fatalf("got %s", got)
	}
	// 2026-08-15 Saturday: Friday 14th stays a workday
	if got := dates(t, Spec{Every: []string{"workday"}}, polish, "2026-08-14", "2026-08-18"); got != "2026-08-14 2026-08-17" {
		t.Fatalf("got %s", got)
	}
	// 'holiday' matches weekend holidays too
	if got := dates(t, Spec{Every: []string{"holiday"}}, polish, "2026-08-01", "2026-09-01"); got != "2026-08-15" {
		t.Fatalf("holiday: got %s", got)
	}
}

func TestReview2_OnWorkdayAtMonthEdges(t *testing.T) {
	cases := []struct{ on, from, to, want string }{
		// 2027-01-31 Sunday
		{"last workday", "2027-01-01", "2027-02-01", "2027-01-29"},
		// 2059-03-31 is Easter Monday, 03-29/30 a weekend
		{"last workday", "2059-03-01", "2059-04-01", "2059-03-28"},
		// 2029-04-01 Easter, 04-02 Easter Monday
		{"first workday", "2029-04-01", "2029-05-01", "2029-04-03"},
		// 2027-01-01 Friday holiday, then a weekend
		{"first workday", "2027-01-01", "2027-02-01", "2027-01-04"},
		// five Fridays in October 2026 (2, 9, 16, 23, 30), four in February 2026
		{"fourth fr", "2026-10-01", "2026-11-01", "2026-10-23"},
		{"last fr", "2026-10-01", "2026-11-01", "2026-10-30"},
		{"fourth fr", "2026-02-01", "2026-03-01", "2026-02-27"},
		{"last fr", "2026-02-01", "2026-03-01", "2026-02-27"},
	}
	for _, c := range cases {
		if got := dates(t, Spec{Every: []string{"month"}, On: c.on}, polish, c.from, c.to); got != c.want {
			t.Errorf("%s %s: got %s, want %s", c.on, c.from, got, c.want)
		}
	}
	for _, on := range []string{"second workday", "fifth fr", "last", "first mo tu"} {
		if _, err := Compile(Spec{Every: []string{"month"}, On: on}, nil); err == nil || !strings.Contains(err.Error(), "on") {
			t.Errorf("on %q: %v", on, err)
		}
	}
}

func TestReview2_DayOfMonthEdges(t *testing.T) {
	// day = 31 skips the months without it (not clamped to the last day)
	if got := dates(t, Spec{Every: []string{"month"}, Day: 31}, nil, "2026-01-01", "2027-01-01"); got != "2026-01-31 2026-03-31 2026-05-31 2026-07-31 2026-08-31 2026-10-31 2026-12-31" {
		t.Fatalf("day 31: %s", got)
	}
	if got := dates(t, Spec{Every: []string{"month"}, Day: 30}, nil, "2028-02-01", "2028-03-01"); got != "" {
		t.Fatalf("day 30 in February: %s", got)
	}
	// day = -1 in February of a leap year and of a common one
	if got := dates(t, Spec{Every: []string{"month"}, Day: -1}, nil, "2027-02-01", "2029-03-01"); !strings.Contains(got, "2027-02-28") || !strings.Contains(got, "2028-02-29") || strings.Contains(got, "2028-02-28") {
		t.Fatalf("day -1: %s", got)
	}
	// yearly 02-29: only in leap years
	if got := dates(t, Spec{Every: []string{"year"}, Date: "02-29"}, nil, "2026-01-01", "2033-01-01"); got != "2028-02-29 2032-02-29" {
		t.Fatalf("02-29: %s", got)
	}
	// every second month from 31 January: a 31st only in the months that have one
	if got := dates(t, Spec{Every: []string{"month"}, Months: 2, Day: 31, From: "2026-01-31"}, nil, "2026-01-01", "2027-01-01"); got != "2026-01-31 2026-03-31 2026-05-31 2026-07-31" {
		t.Fatalf("months 2: %s", got)
	}
	for _, d := range []int{-2, 32} {
		if _, err := Compile(Spec{Every: []string{"month"}, Day: d}, nil); err == nil {
			t.Errorf("day %d accepted", d)
		}
	}
}

// A yearly date that no year has is accepted today and never fires.
func TestReview2_YearlyImpossibleDateRefused(t *testing.T) {
	for _, d := range []string{"02-30", "02-31", "04-31", "06-31", "09-31", "11-31"} {
		if _, err := Compile(Spec{Every: []string{"year"}, Date: d}, nil); err == nil {
			t.Errorf("date %q accepted: no year has it", d)
		}
	}
}

// fmt.Sscanf("%d-%d") takes "1-1-1" as January 1 and "02-5x" as February 5.
func TestReview2_YearlyDateStrictFormat(t *testing.T) {
	for _, d := range []string{"1-1-1", "02-5x", "+1-05", " 1-05", "1-5-x"} {
		if _, err := Compile(Spec{Every: []string{"year"}, Date: d}, nil); err == nil {
			t.Errorf("date %q accepted as MM-DD", d)
		}
	}
}

func TestReview2_EveryOtherWeekOverYearAndDST(t *testing.T) {
	s := Spec{Every: []string{"mo"}, Weeks: 2, From: "2026-10-19", Time: "07:00"}
	got := starts(t, s, nil, "2026-10-01", "2027-04-10")
	want := "2026-10-19 07:00, 2026-11-02 07:00, 2026-11-16 07:00, 2026-11-30 07:00, 2026-12-14 07:00, 2026-12-28 07:00, " +
		"2027-01-11 07:00, 2027-01-25 07:00, 2027-02-08 07:00, 2027-02-22 07:00, 2027-03-08 07:00, 2027-03-22 07:00, 2027-04-05 07:00"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// weeks count from the Monday of from's week: a from on Wednesday keeps that week's Friday
	if got := dates(t, Spec{Every: []string{"mo", "fr"}, Weeks: 2, From: "2026-10-21"}, nil, "2026-10-19", "2026-11-10"); got != "2026-10-23 2026-11-02 2026-11-06" {
		t.Fatalf("mo+fr: %s", got)
	}
}

func TestReview2_TimesCountsExceptedDays(t *testing.T) {
	// an excepted day uses up one of times: three days from 11-01 without 11-02 is two
	s := Spec{Every: []string{"day"}, From: "2026-11-01", Times: 3, Except: []string{"2026-11-02"}}
	if got := dates(t, s, nil, "2026-10-01", "2026-12-01"); got != "2026-11-01 2026-11-03" {
		t.Fatalf("got %s", got)
	}
	// a moved occurrence keeps its place in the count
	s = Spec{Every: []string{"day"}, From: "2026-11-01", Times: 2, Moves: map[string]string{"2026-11-02": "2026-11-20"}}
	if got := dates(t, s, nil, "2026-10-01", "2026-12-01"); got != "2026-11-01 2026-11-20" {
		t.Fatalf("moved: %s", got)
	}
}

func TestReview2_MovesAndExceptAndBounds(t *testing.T) {
	// except names days of the series: the target of a move is not one, so it is refused
	s := Spec{Every: []string{"tu"}, Except: []string{"2026-11-12"}, Moves: map[string]string{"2026-11-10": "2026-11-12"}}
	if _, err := Compile(s, nil); err == nil || !strings.Contains(err.Error(), "except 2026-11-12: not a day of this series") {
		t.Fatalf("except on a move target: %v", err)
	}
	// an excepted original is not moved
	s = Spec{Every: []string{"tu"}, Except: []string{"2026-11-10"}, Moves: map[string]string{"2026-11-10": "2026-11-12"}}
	if got := dates(t, s, nil, "2026-11-09", "2026-11-14"); got != "" {
		t.Fatalf("excepted original: %s", got)
	}
	// a move out of from..to happens
	s = Spec{Every: []string{"tu"}, From: "2026-11-01", To: "2026-11-30", Moves: map[string]string{"2026-11-24": "2026-12-05"}}
	if got := dates(t, s, nil, "2026-11-01", "2026-12-31"); got != "2026-11-03 2026-11-10 2026-11-17 2026-12-05" {
		t.Fatalf("moves and bounds: %s", got)
	}
	// a move of a day outside from..to or not in the series, or onto another occurrence, is a typo
	for moves, want := range map[string]string{
		"2026-12-01>2026-11-28": "moves 2026-12-01: not a day of this series",
		"2026-11-11>2026-11-27": "moves 2026-11-11: not a day of this series",
		"2026-11-10>2026-11-17": "moves 2026-11-10: 2026-11-17 already has one",
	} {
		a, b, _ := strings.Cut(moves, ">")
		s = Spec{Every: []string{"tu"}, From: "2026-11-01", To: "2026-11-30", Moves: map[string]string{a: b}}
		if _, err := Compile(s, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", moves, err, want)
		}
	}
	// two occurrences may swap days
	s = Spec{Every: []string{"tu", "th"}, Moves: map[string]string{"2026-11-10": "2026-11-12", "2026-11-12": "2026-11-10"}}
	if got := dates(t, s, nil, "2026-11-09", "2026-11-13"); got != "2026-11-10 2026-11-12" {
		t.Fatalf("swap: %s", got)
	}
}

// A move years away (a typo in the year) widens every expansion of the rule by that many days.
func TestReview2_MoveFarAwayRefused(t *testing.T) {
	s := Spec{Every: []string{"tu"}, Moves: map[string]string{"2026-11-10": "0026-11-12"}}
	r, err := Compile(s, nil)
	if err == nil {
		start := time.Now()
		r.Between(day("2026-11-01"), day("2026-11-02"), warsaw, true)
		t.Errorf("a move of %d days accepted; one Between took %s", r.maxMove, time.Since(start))
	}
}

func TestReview2_NextWithoutFutureIsQuick(t *testing.T) {
	now := day("2026-10-21")
	for _, s := range []Spec{
		{Every: []string{"day"}, From: "2020-01-01", To: "2025-12-31"},
		{Every: []string{"day"}, From: "2026-01-01", Times: 5},
		{Dates: []string{"2025-01-07", "2026-10-20"}},
		{Every: []string{"year"}, Date: "02-29", From: "2026-01-01", To: "2027-12-31"},
		{Date: "2026-10-20", Time: "10:00"},
	} {
		r := mustCompile(t, s, nil)
		start := time.Now()
		if o, ok := r.Next(now, 3*366*24*time.Hour, warsaw); ok {
			t.Errorf("%+v: next %+v", s, o)
		}
		if el := time.Since(start); el > 200*time.Millisecond {
			t.Errorf("%+v: Next took %s", s, el)
		}
	}
	// beyond the horizon: none; just inside: found
	r := mustCompile(t, Spec{Date: "2027-10-21"}, nil)
	if _, ok := r.Next(now, 300*24*time.Hour, warsaw); ok {
		t.Error("found beyond the horizon")
	}
	if o, ok := r.Next(now, 366*24*time.Hour, warsaw); !ok || o.Date.String() != "2027-10-21" {
		t.Errorf("inside the horizon: %v %+v", ok, o)
	}
	// an occurrence starting exactly at t is not next
	r = mustCompile(t, Spec{Every: []string{"day"}, Time: "10:00"}, nil)
	if o, ok := r.Next(now.Add(10*time.Hour), 48*time.Hour, warsaw); !ok || o.Date.String() != "2026-10-22" {
		t.Errorf("at t: %v %+v", ok, o)
	}
}

func TestReview2_BetweenEmptyAndReversed(t *testing.T) {
	r := mustCompile(t, Spec{Every: []string{"day"}}, nil)
	if o := r.Between(day("2026-10-21"), day("2026-10-21"), warsaw, true); o != nil {
		t.Errorf("empty window: %+v", o)
	}
	if o := r.Between(day("2026-10-22"), day("2026-10-21"), warsaw, true); o != nil {
		t.Errorf("reversed window: %+v", o)
	}
}

func TestReview2_ScheduleLastWindowWins(t *testing.T) {
	all := mustCompile(t, Spec{Every: []string{"day"}, Time: "08:00", Until: "20:00"}, nil)
	noon := mustCompile(t, Spec{Every: []string{"workday"}, Time: "12:00", Until: "14:00"}, nil)
	mon, sat := day("2026-10-19").Add(13*time.Hour), day("2026-10-24").Add(13*time.Hour)
	s := &Schedule{Windows: []*Rule{all, noon}}
	if s.At(mon, warsaw) != 1 || s.At(sat, warsaw) != 0 || s.At(day("2026-10-19").Add(21*time.Hour), warsaw) != -1 {
		t.Fatalf("At: %d %d", s.At(mon, warsaw), s.At(sat, warsaw))
	}
	// the window covering all of it listed last hides the other
	s = &Schedule{Windows: []*Rule{noon, all}}
	if s.At(mon, warsaw) != 1 {
		t.Fatalf("reversed: %d", s.At(mon, warsaw))
	}
	if at, idx, ok := s.NextChange(mon, 24*time.Hour, warsaw); !ok || idx != -1 || at.In(warsaw).Format("15:04") != "20:00" {
		t.Fatalf("NextChange: %s %d %v", at, idx, ok)
	}
}

func TestReview2_ScheduleWholeDayAndNoChange(t *testing.T) {
	// hours equal at both ends: a whole day, back to back, so the value never changes
	whole := mustCompile(t, Spec{Every: []string{"day"}, Time: "00:00", Until: "00:00"}, nil)
	s := &Schedule{Windows: []*Rule{whole}}
	for _, at := range []time.Time{day("2026-10-21"), day("2026-10-21").Add(24*time.Hour - time.Second), day("2026-10-25").Add(2*time.Hour + 30*time.Minute)} {
		if s.At(at, warsaw) != 0 {
			t.Errorf("At %s: %d", at, s.At(at, warsaw))
		}
	}
	if at, idx, ok := s.NextChange(day("2026-10-21"), 8*24*time.Hour, warsaw); ok {
		t.Errorf("never changes, got %s %d", at, idx)
	}
	// an all-day weekend window: from Saturday 00:00 to Monday 00:00
	we := &Schedule{Windows: []*Rule{mustCompile(t, Spec{Every: []string{"weekend"}}, nil)}}
	fri := day("2026-10-23")
	if we.At(fri.Add(24*time.Hour-time.Second), warsaw) != -1 || we.At(fri.Add(24*time.Hour), warsaw) != 0 {
		t.Error("weekend start")
	}
	if at, idx, ok := we.NextChange(fri.Add(36*time.Hour), 8*24*time.Hour, warsaw); !ok || idx != -1 || !at.Equal(day("2026-10-26")) {
		t.Errorf("weekend end: %s %d %v", at, idx, ok)
	}
	// a schedule with no windows, or windows that never come, never changes
	past := mustCompile(t, Spec{Dates: []string{"2025-01-01"}}, nil)
	for _, sc := range []*Schedule{{}, {Windows: []*Rule{past}}} {
		if sc.At(fri, warsaw) != -1 {
			t.Error("default expected")
		}
		if _, _, ok := sc.NextChange(fri, 8*24*time.Hour, warsaw); ok {
			t.Error("no change expected")
		}
	}
}

func TestReview2_PolarSunWindows(t *testing.T) {
	at := func(s Spec) Spec { s.Lat, s.Lon, s.Located = tromsoLat, tromsoLon, true; return s }
	night := mustCompile(t, at(Spec{Every: []string{"day"}, Time: "sunset", Until: "sunrise"}), nil)
	dusk := mustCompile(t, at(Spec{Every: []string{"day"}, Time: "dusk"}), nil)
	in := func(r *Rule, from, to string) int {
		a, _ := time.ParseInLocation("2006-01-02", from, oslo)
		b, _ := time.ParseInLocation("2006-01-02", to, oslo)
		return len(r.Between(a, b, oslo, false))
	}
	// polar day: no sunset, no dusk, so no night window and no 'dusk' event
	if n := in(night, "2026-06-15", "2026-06-25"); n != 0 {
		t.Errorf("June night windows: %d", n)
	}
	if n := in(dusk, "2026-06-15", "2026-06-25"); n != 0 {
		t.Errorf("June dusk: %d", n)
	}
	// polar night: no sunset either, so no night window although it is dark all day;
	// civil twilight still comes, so 'dusk' fires every day
	if n := in(night, "2026-12-15", "2026-12-25"); n != 0 {
		t.Errorf("December night windows: %d", n)
	}
	if n := in(dusk, "2026-12-15", "2026-12-25"); n != 10 {
		t.Errorf("December dusk: %d", n)
	}
	s := &Schedule{Windows: []*Rule{night}}
	mid, _ := time.ParseInLocation("2006-01-02 15:04", "2026-12-21 23:00", oslo)
	if s.At(mid, oslo) != -1 {
		t.Error("polar night: the night window is absent, the default applies")
	}
	if _, _, ok := s.NextChange(mid, 8*24*time.Hour, oslo); ok {
		t.Error("polar night: no change within 8 days")
	}
}

// An offset over a day moves the start out of the days Between walks: daily windows miss it.
func TestReview2_SunOffsetOverADay(t *testing.T) {
	for _, tm := range []string{"sunset+50h", "sunrise-50h"} {
		s := Spec{Every: []string{"day"}, Time: tm, Lat: 52.23, Lon: 21.01, Located: true}
		r, err := Compile(s, nil)
		if err != nil {
			continue // refused: fine
		}
		for d := day("2026-11-01"); d.Before(day("2026-11-05")); d = d.AddDate(0, 0, 1) {
			if n := len(r.Between(d, d.AddDate(0, 0, 1), warsaw, false)); n != 1 {
				t.Errorf("%s on %s: %d occurrences, want 1 (or refuse the offset)", tm, d.Format("01-02"), n)
			}
		}
	}
}

// Lat and Lon arrive from Lua, where 0/0 passes `lat < -90 or lat > 90`.
func TestReview2_NaNLocationRefused(t *testing.T) {
	for _, s := range []Spec{
		{Every: []string{"day"}, Time: "sunset", Lat: math.NaN(), Lon: 21, Located: true},
		{Every: []string{"day"}, Time: "sunset", Lat: 52, Lon: math.NaN(), Located: true},
		{Every: []string{"day"}, Sky: &Sky{AzimuthFrom: 90, AzimuthTo: 180, ElevationHigh: 90}, Lat: math.NaN(), Lon: 21, Located: true},
	} {
		if _, err := Compile(s, nil); err == nil {
			t.Errorf("location %v, %v accepted", s.Lat, s.Lon)
		}
	}
}

func TestReview2_AllDayDurations(t *testing.T) {
	// a duration without a time was cut to whole days or ignored: refused now
	for _, s := range []Spec{
		{Every: []string{"mo"}, Duration: 36 * time.Hour},
		{Every: []string{"mo"}, Duration: 12 * time.Hour},
		{Date: "2026-10-19", Duration: 72 * time.Hour},
	} {
		if _, err := Compile(s, nil); err == nil || !strings.Contains(err.Error(), "duration needs time") {
			t.Errorf("%+v: %v", s, err)
		}
	}
}

func TestReview2_SkyCacheRace(t *testing.T) {
	r := mustCompile(t, Spec{Every: []string{"day"}, Sky: &Sky{AzimuthFrom: 135, AzimuthTo: 225, ElevationLow: 10, ElevationHigh: 90},
		Lat: 52.23, Lon: 21.01, Located: true}, nil)
	want := r.Between(day("2026-06-01"), day("2026-06-08"), warsaw, false)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 60 {
				// past 400 days the cache is dropped and refilled while others read it
				from := day("2026-01-01").AddDate(0, 0, (g*53+i*7)%500)
				r.Between(from, from.AddDate(0, 0, 3), warsaw, true)
				if got := r.Between(day("2026-06-01"), day("2026-06-08"), warsaw, false); len(got) != len(want) || !got[0].Start.Equal(want[0].Start) {
					t.Errorf("concurrent Between differs: %+v", got)
					return
				}
			}
		}()
	}
	wg.Wait()
}
