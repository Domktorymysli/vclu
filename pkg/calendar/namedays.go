package calendar

import (
	_ "embed"
	"strings"
	"sync"
)

//go:embed namedays_pl.txt
var namedaysPL string

var (
	namedaysOnce sync.Once
	namedaysMap  map[string][]string // "MM-DD" -> names, the most common first
)

// Namedays returns the names celebrated on d in a country ("pl"), the most common first.
// The slice is shared: callers must not change it.
func Namedays(country string, d Date) []string {
	if country != "pl" {
		return nil
	}
	namedaysOnce.Do(func() {
		namedaysMap = map[string][]string{}
		for _, line := range strings.Split(namedaysPL, "\n") {
			if line == "" || line[0] == '#' {
				continue
			}
			if day, names, ok := strings.Cut(line, " "); ok {
				namedaysMap[day] = strings.Split(names, ",")
			}
		}
	})
	return namedaysMap[d.String()[5:]]
}
