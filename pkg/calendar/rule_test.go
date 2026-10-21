package calendar

import (
	"strings"
	"testing"
	"time"
)

var warsaw = mustZone("Europe/Warsaw")

var polish = func(d Date) bool { return polishHoliday(d) != "" }

func mustZone(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

func day(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, warsaw)
	if err != nil {
		panic(err)
	}
	return t
}

// starts lists the occurrences between from and to (dates) as "YYYY-MM-DD HH:MM".
func starts(t *testing.T, s Spec, hol Holidays, from, to string) string {
	t.Helper()
	r, err := Compile(s, hol)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var out []string
	for _, o := range r.Between(day(from), day(to), warsaw, false) {
		out = append(out, o.Start.In(warsaw).Format("2006-01-02 15:04"))
	}
	return strings.Join(out, ", ")
}

func TestEveryOtherTuesday(t *testing.T) {
	got := starts(t, Spec{Every: []string{"tu"}, Weeks: 2, From: "2026-10-27", Time: "06:30"}, nil, "2026-10-20", "2026-12-01")
	want := "2026-10-27 06:30, 2026-11-10 06:30, 2026-11-24 06:30"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestWorkdaySkipsPolishHolidays(t *testing.T) {
	// 2026-11-11 (Wednesday) is Independence Day
	got := starts(t, Spec{Every: []string{"workday"}, Time: "08:00"}, polish, "2026-11-09", "2026-11-14")
	want := "2026-11-09 08:00, 2026-11-10 08:00, 2026-11-12 08:00, 2026-11-13 08:00"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// without holidays a workday is Monday to Friday
	if got := starts(t, Spec{Every: []string{"workday"}, Time: "08:00"}, nil, "2026-11-11", "2026-11-12"); got != "2026-11-11 08:00" {
		t.Fatalf("no holidays: got %s", got)
	}
}

func TestPolishHolidays(t *testing.T) {
	for _, d := range []string{"2026-04-05", "2026-04-06", "2026-05-24", "2026-06-04", "2026-12-24", "2027-03-28", "2027-05-27", "2026-01-06"} {
		dd, _ := ParseDate(d)
		if !polish(dd) {
			t.Errorf("%s should be a holiday", d)
		}
	}
	for _, d := range []string{"2026-04-07", "2026-12-23", "2024-12-24", "2026-11-12"} {
		dd, _ := ParseDate(d)
		if polish(dd) {
			t.Errorf("%s should not be a holiday", d)
		}
	}
}

func TestHolidayNamesAndNamedays(t *testing.T) {
	for d, want := range map[string]string{"2026-11-11": "Święto Niepodległości", "2026-04-06": "Poniedziałek Wielkanocny",
		"2026-06-04": "Boże Ciało", "2026-12-24": "Wigilia", "2024-12-24": "", "2026-10-20": ""} {
		dd, _ := ParseDate(d)
		if got := HolidayName("pl", dd); got != want {
			t.Errorf("%s: %q, want %q", d, got, want)
		}
	}
	if HolidayName("de", MakeDate(2026, 1, 1)) != "" || Namedays("de", MakeDate(2026, 1, 1)) != nil {
		t.Error("unknown country must give nothing")
	}
	days := 0
	for d := MakeDate(2028, 1, 1); d < MakeDate(2029, 1, 1); d++ { // a leap year: every day has names
		if len(Namedays("pl", d)) == 0 {
			t.Errorf("%s: no name days", d)
		}
		days++
	}
	has := func(d Date, name string) bool {
		for _, n := range Namedays("pl", d) {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		m, d int
		name string
	}{{10, 20, "Irena"}, {12, 6, "Mikołaj"}, {12, 24, "Ewa"}, {12, 24, "Adam"}, {11, 11, "Marcin"}, {3, 19, "Józef"}, {6, 24, "Jan"}, {2, 29, "Roman"}} {
		if !has(MakeDate(2028, time.Month(c.m), c.d), c.name) {
			t.Errorf("%02d-%02d: no %s in %v", c.m, c.d, c.name, Namedays("pl", MakeDate(2028, time.Month(c.m), c.d)))
		}
	}
}

func TestExceptAndMoves(t *testing.T) {
	s := Spec{Every: []string{"tu"}, Time: "06:30",
		Except: []string{"2026-11-03"},
		Moves:  map[string]string{"2026-11-10": "2026-11-12"}}
	got := starts(t, s, nil, "2026-11-01", "2026-11-20")
	want := "2026-11-12 06:30, 2026-11-17 06:30"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
	// a move out of the window and into it
	if got := starts(t, s, nil, "2026-11-11", "2026-11-13"); got != "2026-11-12 06:30" {
		t.Fatalf("moved into window: got %s", got)
	}
	exc := Spec{Every: []string{"workday"}, Time: "08:00", Except: []string{"2026-12-22..2027-01-06"}}
	if got := starts(t, exc, polish, "2026-12-21", "2027-01-09"); got != "2026-12-21 08:00, 2027-01-07 08:00, 2027-01-08 08:00" {
		t.Fatalf("range except: got %s", got)
	}
}

func TestMonthly(t *testing.T) {
	cases := []struct {
		s    Spec
		want string
	}{
		{Spec{Every: []string{"month"}, Day: 10}, "2026-10-10 00:00, 2026-11-10 00:00, 2026-12-10 00:00"},
		{Spec{Every: []string{"month"}, Day: -1}, "2026-10-31 00:00, 2026-11-30 00:00, 2026-12-31 00:00"},
		{Spec{Every: []string{"month"}, On: "first mo", Time: "18:00"}, "2026-10-05 18:00, 2026-11-02 18:00, 2026-12-07 18:00"},
		{Spec{Every: []string{"month"}, On: "last fr"}, "2026-10-30 00:00, 2026-11-27 00:00, 2026-12-25 00:00"},
		// last workday of December 2026: 31st is Thursday
		{Spec{Every: []string{"month"}, On: "last workday"}, "2026-10-30 00:00, 2026-11-30 00:00, 2026-12-31 00:00"},
		{Spec{Every: []string{"month"}, Months: 2, From: "2026-10-01", Day: 15}, "2026-10-15 00:00, 2026-12-15 00:00"},
		{Spec{Every: []string{"month"}, Day: 31}, "2026-10-31 00:00, 2026-12-31 00:00"},
	}
	for _, c := range cases {
		if got := starts(t, c.s, polish, "2026-10-01", "2027-01-01"); got != c.want {
			t.Errorf("%+v:\ngot  %s\nwant %s", c.s, got, c.want)
		}
	}
	// first workday of January 2027 skips New Year and the weekend
	if got := starts(t, Spec{Every: []string{"month"}, On: "first workday"}, polish, "2027-01-01", "2027-01-31"); got != "2027-01-04 00:00" {
		t.Errorf("first workday: got %s", got)
	}
}

func TestYearlyAndSeriesBounds(t *testing.T) {
	if got := starts(t, Spec{Every: []string{"year"}, Date: "03-14"}, nil, "2026-01-01", "2028-12-31"); got != "2026-03-14 00:00, 2027-03-14 00:00, 2028-03-14 00:00" {
		t.Errorf("yearly: got %s", got)
	}
	if got := starts(t, Spec{Every: []string{"year"}, Date: "02-29"}, nil, "2026-01-01", "2029-01-01"); got != "2028-02-29 00:00" {
		t.Errorf("02-29: got %s", got)
	}
	kurs := Spec{Every: []string{"th"}, From: "2026-10-01", Times: 3, Time: "17:00"}
	if got := starts(t, kurs, nil, "2026-10-09", "2026-12-31"); got != "2026-10-15 17:00" {
		t.Errorf("times: got %s", got)
	}
	bounded := Spec{Every: []string{"mo", "we"}, From: "2026-10-14", To: "2026-10-21", Time: "07:00"}
	if got := starts(t, bounded, nil, "2026-10-01", "2026-12-31"); got != "2026-10-14 07:00, 2026-10-19 07:00, 2026-10-21 07:00" {
		t.Errorf("from/to: got %s", got)
	}
}

func TestYearlyOn(t *testing.T) {
	// clock changes: last Sunday of March and of October
	if got := starts(t, Spec{Every: []string{"year"}, Month: 10, On: "last su", Time: "03:00"}, nil, "2026-01-01", "2028-12-31"); got != "2026-10-25 03:00, 2027-10-31 03:00, 2028-10-29 03:00" {
		t.Errorf("last su of October: got %s", got)
	}
	if got := starts(t, Spec{Every: []string{"year"}, Month: 6, On: "first sa"}, nil, "2026-01-01", "2027-12-31"); got != "2026-06-06 00:00, 2027-06-05 00:00" {
		t.Errorf("first sa of June: got %s", got)
	}
	if got := starts(t, Spec{Every: []string{"year"}, Month: 12, On: "last workday"}, polish, "2026-01-01", "2028-01-01"); got != "2026-12-31 00:00, 2027-12-31 00:00" {
		t.Errorf("last workday of December: got %s", got)
	}
	for _, bad := range []Spec{
		{Every: []string{"year"}, On: "last su"},
		{Every: []string{"year"}, Month: 13, On: "last su"},
		{Every: []string{"year"}, Month: 3, Date: "03-01"},
		{Every: []string{"year"}, Month: 3, On: "last su", Date: "03-01"},
		{Every: []string{"day"}, Month: 3},
	} {
		if _, err := Compile(bad, nil); err == nil {
			t.Errorf("%+v should fail", bad)
		}
	}
}

func TestDatesList(t *testing.T) {
	s := Spec{Dates: []string{"2026-11-02", "2026-12-14", "2026-10-05"}, Time: "06:00",
		Moves: map[string]string{"2026-12-14": "2026-12-12"}}
	if got := starts(t, s, nil, "2026-10-01", "2027-01-01"); got != "2026-10-05 06:00, 2026-11-02 06:00, 2026-12-12 06:00" {
		t.Errorf("dates: got %s", got)
	}
	if got := starts(t, Spec{Dates: []string{"2026-10-05"}, To: "2026-10-01"}, nil, "2026-10-01", "2026-11-01"); got != "" {
		t.Errorf("dates after to: got %s", got)
	}
	for _, bad := range []Spec{{Dates: []string{"5.10"}}, {Dates: []string{"2026-10-05"}, Every: []string{"mo"}}, {Dates: []string{"2026-10-05"}, Times: 2, From: "2026-01-01"}} {
		if _, err := Compile(bad, nil); err == nil {
			t.Errorf("%+v should fail", bad)
		}
	}
}

func TestOneOffAndAllDay(t *testing.T) {
	r, err := Compile(Spec{Date: "2026-11-14..2026-11-16"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	occ := r.Between(day("2026-11-15"), day("2026-11-16"), warsaw, true)
	if len(occ) != 1 || !occ[0].AllDay || !occ[0].Start.Equal(day("2026-11-14")) || !occ[0].End.Equal(day("2026-11-17")) {
		t.Fatalf("range one-off: %+v", occ)
	}
	if got := r.Between(day("2026-11-15"), day("2026-11-16"), warsaw, false); len(got) != 0 {
		t.Fatalf("not overlapping: %+v", got)
	}
	timed := Spec{Date: "2026-11-04", Time: "15:30", Duration: time.Hour}
	r, _ = Compile(timed, nil)
	o := r.Between(day("2026-11-01"), day("2026-11-30"), warsaw, false)
	if len(o) != 1 || o[0].End.Sub(o[0].Start) != time.Hour {
		t.Fatalf("timed one-off: %+v", o)
	}
}

func TestDST(t *testing.T) {
	// 2027-03-28: clocks jump from 02:00 to 03:00 in Warsaw
	r, _ := Compile(Spec{Every: []string{"day"}, Time: "02:30"}, nil)
	o := r.Between(day("2027-03-28"), day("2027-03-29"), warsaw, false)
	if len(o) != 1 || o[0].Start.In(warsaw).Format("15:04") != "03:00" {
		t.Fatalf("skipped time: %+v", o)
	}
	// the window 22:00-06:00 over the autumn change lasts 9 h, ends at 06:00 wall clock
	w, _ := Compile(Spec{Every: []string{"day"}, Time: "22:00", Until: "06:00"}, nil)
	o = w.Between(day("2026-10-24"), day("2026-10-25"), warsaw, false)
	if len(o) != 1 || o[0].End.In(warsaw).Format("01-02 15:04") != "10-25 06:00" || o[0].End.Sub(o[0].Start) != 9*time.Hour {
		t.Fatalf("window over DST: %+v", o)
	}
}

func TestOverlapping(t *testing.T) {
	r, _ := Compile(Spec{Every: []string{"workday"}, Time: "08:00", Duration: 6 * time.Hour}, nil)
	now := day("2026-10-20").Add(10 * time.Hour)
	o := r.Between(now, now.Add(time.Second), warsaw, true)
	if len(o) != 1 || o[0].Start.In(warsaw).Format("15:04") != "08:00" {
		t.Fatalf("active at 10:00: %+v", o)
	}
}

func TestNext(t *testing.T) {
	r, _ := Compile(Spec{Every: []string{"year"}, Date: "03-14"}, nil)
	o, ok := r.Next(day("2026-10-20"), 400*24*time.Hour, warsaw)
	if !ok || o.Date.String() != "2027-03-14" {
		t.Fatalf("next: %v %+v", ok, o)
	}
	r, _ = Compile(Spec{Date: "2026-01-01"}, nil)
	if _, ok := r.Next(day("2026-10-20"), 400*24*time.Hour, warsaw); ok {
		t.Fatal("a past one-off has no next")
	}
}

func TestCompileErrors(t *testing.T) {
	for _, c := range []struct {
		s    Spec
		want string
	}{
		{Spec{}, "give every"},
		{Spec{Every: []string{"tue"}}, "every \"tue\""},
		{Spec{Every: []string{"tu"}, Weeks: 2}, "needs from"},
		{Spec{Every: []string{"month"}}, "needs day"},
		{Spec{Every: []string{"month"}, On: "first monday"}, "on \"first monday\""},
		{Spec{Every: []string{"year"}, Date: "2026-03-14"}, "MM-DD"},
		{Spec{Every: []string{"day"}, Time: "25:00"}, "time"},
		{Spec{Every: []string{"day"}, Months: 2, From: "2026-01-01"}, "months needs"},
		{Spec{Date: "2026-11-14..2026-11-10"}, "ends before"},
		{Spec{Date: "2026-11-14..2026-11-16", Time: "10:00"}, "drop time"},
		{Spec{Every: []string{"day"}, Except: []string{"jutro"}}, "except"},
		{Spec{Every: []string{"day"}, Day: 3}, "need every = 'month'"},
	} {
		_, err := Compile(c.s, nil)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: err %v, want %q", c.s, err, c.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	for s, want := range map[string]time.Duration{"90m": 90 * time.Minute, "-12h": -12 * time.Hour, "1h30m": 90 * time.Minute, "2d": 48 * time.Hour, "+30s": 30 * time.Second} {
		if got, err := ParseDuration(s); err != nil || got != want {
			t.Errorf("%s: %v %v", s, got, err)
		}
	}
	for _, s := range []string{"", "12", "h", "5x", "-"} {
		if _, err := ParseDuration(s); err == nil {
			t.Errorf("%q should fail", s)
		}
	}
}

func TestSchedule(t *testing.T) {
	mk := func(s Spec) *Rule {
		r, err := Compile(s, polish)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// G12w tariff: cheap 13-15 and 22-06 on workdays, all day on weekends and holidays
	s := &Schedule{Windows: []*Rule{
		mk(Spec{Every: []string{"workday"}, Time: "13:00", Until: "15:00"}),
		mk(Spec{Every: []string{"day"}, Time: "22:00", Until: "06:00"}),
		mk(Spec{Every: []string{"weekend"}}),
		mk(Spec{Every: []string{"holiday"}}),
	}}
	at := func(d string, hhmm string) time.Time {
		sec, _ := ParseTime(hhmm)
		dd, _ := ParseDate(d)
		return dd.At(sec, warsaw)
	}
	for _, c := range []struct {
		d, t string
		want int
	}{
		{"2026-10-20", "12:59", -1}, {"2026-10-20", "13:00", 0}, {"2026-10-20", "15:00", -1},
		{"2026-10-20", "23:00", 1}, {"2026-10-21", "05:59", 1}, {"2026-10-24", "12:00", 2},
		{"2026-10-24", "23:00", 2}, {"2026-11-11", "10:00", 3},
	} {
		if got := s.At(at(c.d, c.t), warsaw); got != c.want {
			t.Errorf("%s %s: got %d want %d", c.d, c.t, got, c.want)
		}
	}
	next, v, ok := s.NextChange(at("2026-10-20", "10:00"), 8*24*time.Hour, warsaw)
	if !ok || v != 0 || !next.Equal(at("2026-10-20", "13:00")) {
		t.Fatalf("next change: %v %d %v", ok, v, next)
	}
	// Friday 22:00 night window then the weekend: same "cheap" value but another window
	next, v, _ = s.NextChange(at("2026-10-23", "23:00"), 8*24*time.Hour, warsaw)
	if v != 2 || !next.Equal(at("2026-10-24", "00:00")) {
		t.Fatalf("into the weekend: %d %v", v, next)
	}
}

func TestClockTrusted(t *testing.T) {
	if ok, _ := ClockTrusted(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)); ok {
		t.Fatal("1970 is not a set clock")
	}
}

func TestOneOffLongerThanThreeDaysStaysActive(t *testing.T) {
	r, err := Compile(Spec{Date: "2026-11-14", Time: "18:00", Duration: 5 * 24 * time.Hour}, nil)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, warsaw)
	if got := r.Between(at, at.Add(time.Second), warsaw, true); len(got) != 1 {
		t.Errorf("going on at 11-18 12:00: %d occurrences, want 1", len(got))
	}
	end := time.Date(2026, 11, 19, 18, 0, 0, 0, warsaw)
	if got := r.Between(end.Add(-time.Minute), end.Add(time.Minute), warsaw, true); len(got) != 1 || !got[0].End.Equal(end) {
		t.Errorf("around its end: %+v", got)
	}
}

// windows lists a daily window's occurrences from..to as "MM-DD HH:MM+duration".
func windows(t *testing.T, from, until, a, b string) string {
	t.Helper()
	r, err := Compile(Spec{Every: []string{"day"}, Time: from, Until: until}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, o := range r.Between(day(a), day(b), warsaw, false) {
		out = append(out, o.Start.In(warsaw).Format("01-02 15:04")+"+"+o.End.Sub(o.Start).String())
	}
	return strings.Join(out, ", ")
}

func TestSpringGapWindowIsEmptyThatDay(t *testing.T) {
	// 2026-03-29: 02:00 CET jumps to 03:00 CEST; a window inside the gap does not happen that day
	for _, c := range []struct{ from, until, want string }{
		{"02:00", "03:00", "03-28 02:00+1h0m0s, 03-30 02:00+1h0m0s"},
		{"02:15", "02:45", "03-28 02:15+30m0s, 03-30 02:15+30m0s"},
		{"01:30", "02:30", "03-28 01:30+1h0m0s, 03-29 01:30+30m0s, 03-30 01:30+1h0m0s"},
		{"22:00", "02:30", "03-28 22:00+4h0m0s, 03-29 22:00+4h30m0s, 03-30 22:00+4h30m0s"},
	} {
		if got := windows(t, c.from, c.until, "2026-03-28", "2026-03-31"); got != c.want {
			t.Errorf("%s-%s:\n got %s\nwant %s", c.from, c.until, got, c.want)
		}
	}
	r, _ := Compile(Spec{Every: []string{"day"}, Time: "02:00", Until: "03:00"}, nil)
	s := &Schedule{Windows: []*Rule{r}}
	if got := s.At(time.Date(2026, 3, 29, 12, 0, 0, 0, warsaw), warsaw); got != -1 {
		t.Errorf("window in effect at noon of the change: %d", got)
	}
}

func TestAutumnRepeatedHourWindows(t *testing.T) {
	// 2026-10-25: 03:00 CEST goes back to 02:00 CET; the repeated hour counts once
	for _, c := range []struct{ from, until, want string }{
		{"02:00", "03:00", "10-24 02:00+1h0m0s, 10-25 02:00+1h0m0s, 10-26 02:00+1h0m0s"},
		{"01:30", "02:30", "10-24 01:30+1h0m0s, 10-25 01:30+2h0m0s, 10-26 01:30+1h0m0s"},
		{"02:30", "02:15", "10-24 02:30+24h45m0s, 10-25 02:30+23h45m0s, 10-26 02:30+23h45m0s"},
	} {
		if got := windows(t, c.from, c.until, "2026-10-24", "2026-10-27"); got != c.want {
			t.Errorf("%s-%s:\n got %s\nwant %s", c.from, c.until, got, c.want)
		}
	}
}

func TestDurationAtMost366Days(t *testing.T) {
	if d, err := ParseDuration("366d"); err != nil || d != MaxDuration {
		t.Errorf("366d: %v %v", d, err)
	}
	for _, s := range []string{"367d", "8784h1s", "99999999999999999999d", "9223372036s", "200d200d", "213504d", "106752d", "2562048h"} {
		if _, err := ParseDuration(s); err == nil || !strings.Contains(err.Error(), "366 days") {
			t.Errorf("%s: %v, want an error about 366 days", s, err)
		}
	}
	if _, err := Compile(Spec{Date: "2026-11-14", Time: "18:00", Duration: MaxDuration + time.Second}, nil); err == nil {
		t.Error("a duration over 366 days compiled")
	}
}
