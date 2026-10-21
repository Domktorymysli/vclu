package calendar

import (
	"fmt"
	"time"
)

// Holidays reports whether a date is a public holiday.
type Holidays func(Date) bool

// HolidaysFor returns the public holidays of a country ("pl").
func HolidaysFor(country string) (Holidays, error) {
	switch country {
	case "pl":
		return func(d Date) bool { return polishHoliday(d) != "" }, nil
	}
	return nil, fmt.Errorf("unknown holidays %q (known: pl)", country)
}

// HolidayName returns the name of the public holiday on d, or "".
func HolidayName(country string, d Date) string {
	if country == "pl" {
		return polishHoliday(d)
	}
	return ""
}

// polishHoliday: public holidays (days free from work) in Poland.
func polishHoliday(d Date) string {
	y, m, dd := d.Civil()
	switch {
	case m == time.January && dd == 1:
		return "Nowy Rok"
	case m == time.January && dd == 6:
		return "Trzech Króli"
	case m == time.May && dd == 1:
		return "Święto Pracy"
	case m == time.May && dd == 3:
		return "Święto Konstytucji 3 Maja"
	case m == time.August && dd == 15:
		return "Wniebowzięcie NMP"
	case m == time.November && dd == 1:
		return "Wszystkich Świętych"
	case m == time.November && dd == 11:
		return "Święto Niepodległości"
	case m == time.December && dd == 24 && y >= 2025: // Christmas Eve free since 2025
		return "Wigilia"
	case m == time.December && dd == 25:
		return "Boże Narodzenie"
	case m == time.December && dd == 26:
		return "Drugi dzień Bożego Narodzenia"
	}
	switch e := easter(y); d {
	case e:
		return "Wielkanoc"
	case e + 1:
		return "Poniedziałek Wielkanocny"
	case e + 49:
		return "Zielone Świątki"
	case e + 60:
		return "Boże Ciało"
	}
	return ""
}

// easter returns Easter Sunday of year y (Gregorian, anonymous algorithm).
func easter(y int) Date {
	a := y % 19
	b, c := y/100, y%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return MakeDate(y, time.Month(month), day)
}
