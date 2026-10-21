package calendar

import (
	"sort"
	"time"
)

// Schedule is a list of windows; the value in effect is that of the last window that covers
// the moment, or none (-1: the default).
type Schedule struct {
	Windows []*Rule
}

// At returns the index of the window in effect at t, or -1.
func (s *Schedule) At(t time.Time, loc *time.Location) int {
	idx := -1
	for i, w := range s.Windows {
		for _, o := range w.Between(t, t.Add(time.Second), loc, true) {
			if !o.Start.After(t) && o.End.After(t) {
				idx = i
				break
			}
		}
	}
	return idx
}

// NextChange returns the first moment after t, within horizon, at which the window in effect
// changes, and the index from then.
func (s *Schedule) NextChange(t time.Time, horizon time.Duration, loc *time.Location) (time.Time, int, bool) {
	cur := s.At(t, loc)
	const chunk = 8 * 24 * time.Hour
	for from := t; from.Before(t.Add(horizon)); from = from.Add(chunk) {
		to := from.Add(chunk)
		var edges []time.Time
		for _, w := range s.Windows {
			for _, o := range w.Between(from, to, loc, true) {
				for _, e := range []time.Time{o.Start, o.End} {
					if e.After(t) && !e.Before(from) && e.Before(to) {
						edges = append(edges, e)
					}
				}
			}
		}
		sort.Slice(edges, func(i, j int) bool { return edges[i].Before(edges[j]) })
		for _, e := range edges {
			if v := s.At(e, loc); v != cur {
				return e, v, true
			}
		}
	}
	return time.Time{}, cur, false
}
