package calendar

import (
	"math"
	"time"
)

// Sun times and position by the NOAA approximation (fractional year, equation of time,
// declination), as in the sun-position plugin; within a minute or two. Computed in UTC,
// so the local time follows the zone and its DST.

const (
	zenithSunrise = 90.833 // refraction and the sun's radius below the horizon
	zenithCivil   = 96     // civil twilight: the sun 6° below the horizon
	deg           = math.Pi / 180
)

// Sun holds the sun's times on a day. Rise and Set are zero in a polar night (Polar < 0) or a
// polar day (Polar > 0); Dawn and Dusk are zero when the sun does not go 6° below the horizon
// or does not come up to it.
type Sun struct {
	Rise, Set, Noon time.Time
	Dawn, Dusk      time.Time
	Polar           int
}

// fractional year (radians), equation of time (minutes) and declination (radians) at t
func solar(t time.Time) (eqTime, decl float64) {
	t = t.UTC()
	days := 365.0
	if time.Date(t.Year(), 12, 31, 0, 0, 0, 0, time.UTC).YearDay() == 366 {
		days = 366
	}
	gamma := 2 * math.Pi / days * (float64(t.YearDay()-1) + (float64(t.Hour())-12)/24)
	eqTime = 229.18 * (0.000075 + 0.001868*math.Cos(gamma) - 0.032077*math.Sin(gamma) -
		0.014615*math.Cos(2*gamma) - 0.040849*math.Sin(2*gamma))
	decl = 0.006918 - 0.399912*math.Cos(gamma) + 0.070257*math.Sin(gamma) -
		0.006758*math.Cos(2*gamma) + 0.000907*math.Sin(2*gamma) -
		0.002697*math.Cos(3*gamma) + 0.00148*math.Sin(3*gamma)
	return eqTime, decl
}

// SunOn returns the sun's times on date d at lat, lon (degrees, east positive).
func SunOn(d Date, lat, lon float64) Sun {
	y, m, dd := d.Civil()
	midnight := time.Date(y, m, dd, 0, 0, 0, 0, time.UTC)
	eqTime, decl := solar(midnight.Add(12 * time.Hour))
	noonMin := 720 - 4*lon - eqTime // minutes after 00:00 UTC
	at := func(min float64) time.Time {
		return midnight.Add(time.Duration(min * float64(time.Minute))).Round(time.Second)
	}
	// hour angle (degrees) at which the sun is at zenith z; ok false if it never is
	hourAngle := func(z float64) (float64, int) {
		c := math.Cos(z*deg)/(math.Cos(lat*deg)*math.Cos(decl)) - math.Tan(lat*deg)*math.Tan(decl)
		switch {
		case c > 1:
			return 0, -1
		case c < -1:
			return 0, 1
		}
		return math.Acos(c) / deg, 0
	}
	s := Sun{Noon: at(noonMin)}
	if ha, polar := hourAngle(zenithSunrise); polar == 0 {
		s.Rise, s.Set = at(noonMin-4*ha), at(noonMin+4*ha)
	} else {
		s.Polar = polar
	}
	if ha, polar := hourAngle(zenithCivil); polar == 0 {
		s.Dawn, s.Dusk = at(noonMin-4*ha), at(noonMin+4*ha)
	}
	return s
}

// SunPosition returns the sun's azimuth (degrees from north, clockwise: 90 east, 180 south)
// and elevation (degrees above the horizon) at t.
func SunPosition(t time.Time, lat, lon float64) (azimuth, elevation float64) {
	eqTime, decl := solar(t)
	u := t.UTC()
	minutes := float64(u.Hour()*60+u.Minute()) + float64(u.Second())/60
	ha := ((minutes+eqTime+4*lon)/4 - 180) * deg // hour angle, 0 at solar noon
	sinEl := math.Sin(lat*deg)*math.Sin(decl) + math.Cos(lat*deg)*math.Cos(decl)*math.Cos(ha)
	elevation = math.Asin(math.Max(-1, math.Min(1, sinEl))) / deg
	azimuth = math.Atan2(math.Sin(ha), math.Cos(ha)*math.Sin(lat*deg)-math.Tan(decl)*math.Cos(lat*deg))/deg + 180
	return math.Mod(azimuth+360, 360), elevation
}

// sunRange is a part of the sky: azimuth from..to (clockwise, may pass north) and elevation.
type sunRange struct {
	azFrom, azTo float64
	elFrom, elTo float64
}

func (r sunRange) contains(az, el float64) bool {
	if el < r.elFrom || el > r.elTo {
		return false
	}
	if r.azFrom <= r.azTo {
		return az >= r.azFrom && az <= r.azTo
	}
	return az >= r.azFrom || az <= r.azTo
}

// sunInRange returns the first span of day d (local midnight to midnight) during which the
// sun stands in r: sampled every 2 minutes, edges refined to a few seconds.
func sunInRange(d Date, loc *time.Location, lat, lon float64, r sunRange) (time.Time, time.Time, bool) {
	from, to := d.At(0, loc), (d+1).At(0, loc)
	in := func(t time.Time) bool {
		az, el := SunPosition(t, lat, lon)
		return r.contains(az, el)
	}
	// edge between a (in == want) and b (in != want)
	edge := func(a, b time.Time, want bool) time.Time {
		for b.Sub(a) > 5*time.Second {
			mid := a.Add(b.Sub(a) / 2)
			if in(mid) == want {
				a = mid
			} else {
				b = mid
			}
		}
		return b.Truncate(time.Second)
	}
	const step = 2 * time.Minute
	var start time.Time
	prev, prevIn := from, in(from)
	if prevIn {
		start = from
	}
	for t := from.Add(step); !t.After(to); t = t.Add(step) {
		cur := in(t)
		switch {
		case cur && !prevIn && start.IsZero():
			start = edge(prev, t, false)
		case !cur && prevIn && !start.IsZero():
			return start, edge(prev, t, true), true
		}
		prev, prevIn = t, cur
	}
	if !start.IsZero() {
		return start, to, true
	}
	return time.Time{}, time.Time{}, false
}
