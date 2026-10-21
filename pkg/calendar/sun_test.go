package calendar

import (
	"strings"
	"testing"
	"time"
)

func near(t *testing.T, what string, got time.Time, want string) {
	t.Helper()
	w, err := time.ParseInLocation("2006-01-02 15:04", want, warsaw)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.Sub(w); d < -3*time.Minute || d > 3*time.Minute {
		t.Errorf("%s: %s, want %s ±3 min", what, got.In(warsaw).Format("2006-01-02 15:04"), want)
	}
}

// Warsaw (52.23 N, 21.01 E), published times rounded to the minute
func TestSunOnWarsaw(t *testing.T) {
	for _, c := range []struct{ day, rise, set string }{
		{"2026-06-21", "04:14", "21:01"},
		{"2026-12-21", "07:43", "15:25"},
	} {
		d, _ := ParseDate(c.day)
		s := SunOn(d, 52.2297, 21.0122)
		near(t, c.day+" rise", s.Rise, c.day+" "+c.rise)
		near(t, c.day+" set", s.Set, c.day+" "+c.set)
	}
	// at the equinox a day is a little over 12 h (refraction), around a solar noon
	// 24 min before 12:00 CET plus the equation of time (+7 min on 20 March)
	s := SunOn(MakeDate(2026, 3, 20), 52.2297, 21.0122)
	if l := s.Set.Sub(s.Rise); l < 12*time.Hour || l > 12*time.Hour+12*time.Minute {
		t.Errorf("equinox day length %s", l)
	}
	near(t, "equinox noon", s.Noon, "2026-03-20 11:43")
}

func TestSunPolar(t *testing.T) {
	if s := SunOn(MakeDate(2026, 12, 21), 69.65, 18.96); s.Polar != -1 || !s.Rise.IsZero() {
		t.Errorf("Tromsø in December: %+v", s)
	}
	if s := SunOn(MakeDate(2026, 6, 21), 69.65, 18.96); s.Polar != 1 {
		t.Errorf("Tromsø in June: %+v", s)
	}
}

func TestSunRules(t *testing.T) {
	at := func(s Spec) Spec { s.Lat, s.Lon, s.Located = 52.2297, 21.0122, true; return s }
	r, err := Compile(at(Spec{Every: []string{"day"}, Time: "sunset-30m"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	sun := SunOn(MakeDate(2026, 10, 20), 52.2297, 21.0122)
	next := SunOn(MakeDate(2026, 10, 21), 52.2297, 21.0122)
	o := r.Between(day("2026-10-20"), day("2026-10-21"), warsaw, false)
	if len(o) != 1 || !o[0].Start.Equal(sun.Set.Add(-30*time.Minute)) {
		t.Fatalf("sunset-30m: %+v, sunset %s", o, sun.Set)
	}

	// a night window from sunset to sunrise ends the next morning
	w, _ := Compile(at(Spec{Every: []string{"day"}, Time: "sunset", Until: "sunrise+15m"}), nil)
	o = w.Between(day("2026-10-20"), day("2026-10-21"), warsaw, false)
	if len(o) != 1 {
		t.Fatalf("night window: %+v", o)
	}
	if !o[0].Start.Equal(sun.Set) || !o[0].End.Equal(next.Rise.Add(15*time.Minute)) {
		t.Errorf("night window %s..%s, want %s..%s", o[0].Start, o[0].End, sun.Set, next.Rise.Add(15*time.Minute))
	}

	// a fixed start and a sun end on the same day
	w, _ = Compile(at(Spec{Every: []string{"day"}, Time: "06:00", Until: "sunrise"}), nil)
	o = w.Between(day("2026-12-21"), day("2026-12-22"), warsaw, false)
	near(t, "06:00 to sunrise", o[0].End, "2026-12-21 07:43")

	// no sunset in a polar day: no occurrence
	p := Spec{Every: []string{"day"}, Time: "sunset", Lat: 69.65, Lon: 18.96, Located: true}
	if got := starts(t, p, nil, "2026-06-20", "2026-06-22"); got != "" {
		t.Errorf("polar day: %s", got)
	}

	for _, c := range []struct {
		s    Spec
		want string
	}{
		{Spec{Every: []string{"day"}, Time: "sunset"}, "location"},
		{Spec{Every: []string{"day"}, Time: "06:00", Until: "sunrise"}, "location"},
		{at(Spec{Every: []string{"day"}, Time: "sunset30m"}), "sunrise, sunset"},
		{at(Spec{Every: []string{"day"}, Time: "sunset-30x"}), "unknown unit"},
		{at(Spec{Every: []string{"day"}, Time: "moonrise"}), "sunrise, sunset"},
		{Spec{Every: []string{"day"}, Sky: &Sky{AzimuthFrom: 135, AzimuthTo: 225, ElevationLow: 15, ElevationHigh: 90}}, "location"},
		{at(Spec{Every: []string{"day"}, Time: "10:00", Sky: &Sky{AzimuthFrom: 135, AzimuthTo: 225, ElevationLow: 15, ElevationHigh: 90}}), "drop time"},
		{at(Spec{Every: []string{"day"}, Sky: &Sky{AzimuthFrom: 135, AzimuthTo: 400, ElevationLow: 15, ElevationHigh: 90}}), "azimuth"},
		{at(Spec{Every: []string{"day"}, Sky: &Sky{AzimuthFrom: 135, AzimuthTo: 225, ElevationLow: 50, ElevationHigh: 20}}), "elevation"},
	} {
		if _, err := Compile(c.s, nil); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", c.s.Time, err, c.want)
		}
	}
}

func TestSunPosition(t *testing.T) {
	const lat, lon = 52.2297, 21.0122
	s := SunOn(MakeDate(2026, 3, 20), lat, lon)
	if az, el := SunPosition(s.Noon, lat, lon); az < 178 || az > 182 || el < 36 || el > 39 {
		t.Errorf("equinox noon: az %.1f el %.1f, want ~180 and ~37.8", az, el)
	}
	if az, el := SunPosition(s.Rise, lat, lon); az < 87 || az > 92 || el < -1.5 || el > 0.5 {
		t.Errorf("equinox sunrise: az %.1f el %.1f, want ~89 and ~-0.8", az, el)
	}
	if az, _ := SunPosition(s.Set, lat, lon); az < 268 || az > 273 {
		t.Errorf("equinox sunset: az %.1f, want ~271", az)
	}
	// summer solstice noon: 90 - 52.2 + 23.4 = 61.2
	if _, el := SunPosition(SunOn(MakeDate(2026, 6, 21), lat, lon).Noon, lat, lon); el < 60 || el > 62 {
		t.Errorf("solstice elevation %.1f", el)
	}
	// civil dusk at 52 N: about 35-45 min after sunset in October
	o := SunOn(MakeDate(2026, 10, 20), lat, lon)
	if d := o.Dusk.Sub(o.Set); d < 30*time.Minute || d > 50*time.Minute {
		t.Errorf("dusk %s after sunset", d)
	}
	if _, el := SunPosition(o.Dusk, lat, lon); el < -6.5 || el > -5.5 {
		t.Errorf("dusk elevation %.2f, want -6", el)
	}
	// Tromsø in June: no dusk (white night)
	if tr := SunOn(MakeDate(2026, 6, 21), 69.65, 18.96); !tr.Dusk.IsZero() {
		t.Errorf("Tromsø dusk in June: %s", tr.Dusk)
	}
}

func TestSkyWindow(t *testing.T) {
	const lat, lon = 52.2297, 21.0122
	south := &Sky{AzimuthFrom: 135, AzimuthTo: 225, ElevationLow: 15, ElevationHigh: 90}
	r, err := Compile(Spec{Every: []string{"day"}, Sky: south, Lat: lat, Lon: lon, Located: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	o := r.Between(day("2026-06-21"), day("2026-06-22"), warsaw, false)
	if len(o) != 1 {
		t.Fatalf("south facade in June: %+v", o)
	}
	azS, _ := SunPosition(o[0].Start, lat, lon)
	azE, _ := SunPosition(o[0].End, lat, lon)
	if azS < 134 || azS > 136 || azE < 224 || azE > 226 {
		t.Errorf("June window %s..%s, azimuth %.1f..%.1f, want 135..225",
			o[0].Start.In(warsaw).Format("15:04"), o[0].End.In(warsaw).Format("15:04"), azS, azE)
	}
	// in December the sun stays under 15 degrees: no window
	if o := r.Between(day("2026-12-21"), day("2026-12-22"), warsaw, false); len(o) != 0 {
		t.Errorf("December: %+v", o)
	}
	// a window over north (azimuth 300..60) in June, evening sun low in the north-west
	nw, _ := Compile(Spec{Every: []string{"day"}, Sky: &Sky{AzimuthFrom: 290, AzimuthTo: 60, ElevationLow: 0, ElevationHigh: 20},
		Lat: lat, Lon: lon, Located: true}, nil)
	if o := nw.Between(day("2026-06-21"), day("2026-06-22"), warsaw, false); len(o) != 1 {
		t.Errorf("north-west evening sun: %+v", o)
	}
	// dusk as a time
	dk, _ := Compile(Spec{Every: []string{"day"}, Time: "dusk", Lat: lat, Lon: lon, Located: true}, nil)
	if o := dk.Between(day("2026-10-20"), day("2026-10-21"), warsaw, false); len(o) != 1 || !o[0].Start.Equal(SunOn(MakeDate(2026, 10, 20), lat, lon).Dusk) {
		t.Errorf("dusk: %+v", o)
	}
}
