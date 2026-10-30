package metrics

import (
	"fmt"
	"math"
	"time"
)

// MaxBuckets bounds one series: 31 days of hours.
const MaxBuckets = 31 * 24

// Aggregations a bucket can take.
var Aggregations = map[string]bool{"sum": true, "avg": true, "max": true, "min": true, "delta": true}

// BucketEdges returns n+1 edges of n buckets in loc, the last bucket holding now. Daily
// buckets start at local midnight (a DST day is 23 or 25 h long), hourly ones on the hour.
func BucketEdges(now time.Time, loc *time.Location, n int, daily bool) ([]time.Time, error) {
	if n < 1 || n > MaxBuckets {
		return nil, fmt.Errorf("buckets: %d not in 1..%d", n, MaxBuckets)
	}
	t := now.In(loc)
	edges := make([]time.Time, n+1)
	if daily {
		y, m, d := t.Date()
		for i := 0; i <= n; i++ {
			// time.Date normalises the day and gives midnight in loc on any DST day
			edges[i] = time.Date(y, m, d-(n-1)+i, 0, 0, 0, 0, loc)
		}
		return edges, nil
	}
	y, m, d := t.Date()
	cur := time.Date(y, m, d, t.Hour(), 0, 0, 0, loc)
	// a fall-back hour is there twice and time.Date picks one of them: take the one with now
	if cur.After(now) {
		cur = cur.Add(-time.Hour)
	} else if !cur.Add(time.Hour).After(now) {
		cur = cur.Add(time.Hour)
	}
	for i := 0; i <= n; i++ {
		// absolute hours: the repeated hour of a fall-back day is two buckets
		edges[i] = cur.Add(time.Duration(i-(n-1)) * time.Hour)
	}
	return edges, nil
}

// A fall of a counter is a reset (a replaced meter, a counter from 0) only when the value stays
// below its high mark for resetSamples samples in a row or for resetAfter; a shorter dip is a bad
// reading (a Modbus timeout read as 0) and adds nothing.
const (
	resetSamples = 3
	resetAfter   = time.Hour
)

// rise is what sample i added to a counter, from the sample at time from.
type rise struct {
	inc  float64
	from int64
	ok   bool
}

// counterRises gives each sample of a counter its rise over the high mark: after a dip the count
// goes on from the value before it; after a reset, from the first low sample.
func counterRises(samples []Sample) []rise {
	out := make([]rise, len(samples))
	high, at, before := -1, -1, -1 // the high mark, the first low sample, the mark before a reset
	for i, s := range samples {
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			continue
		}
		if high < 0 {
			high = i
			continue
		}
		h := samples[high]
		// back at the mark before a "reset": it was a longer dip, count from that mark
		if before >= 0 && s.Value >= samples[before].Value {
			h = samples[before]
			before = -1
		}
		if s.Value >= h.Value {
			out[i] = rise{inc: s.Value - h.Value, from: h.Timestamp, ok: true}
			high, at = i, -1
			continue
		}
		if at < 0 {
			at = i
		}
		low := 0
		for k := at; k <= i; k++ {
			if !math.IsNaN(samples[k].Value) && !math.IsInf(samples[k].Value, 0) {
				low++
			}
		}
		if low < resetSamples && s.Timestamp-samples[at].Timestamp < int64(resetAfter/time.Second) {
			continue
		}
		// a reset: the rises from the first low sample on count, the fall itself does not
		prev := at
		for k := at + 1; k <= i; k++ {
			v := samples[k].Value
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			if v > samples[prev].Value {
				out[k] = rise{inc: v - samples[prev].Value, from: samples[prev].Timestamp, ok: true}
			}
			prev = k
		}
		before = high
		high, at = i, -1
	}
	return out
}

// Aggregate folds samples (sorted by time) into the buckets between edges; a bucket without
// samples is nil, not zero. avg is the plain mean of the samples and sum their total (it grows
// with the sampling rate). delta sums the rises of a counter (counterRises): a rise goes to the
// bucket of its later sample, one from an earlier bucket only when it is at most a bucket long,
// so a bucket is never negative.
func Aggregate(samples []Sample, edges []time.Time, agg string) []*float64 {
	n := len(edges) - 1
	out := make([]*float64, max(n, 0))
	if n < 1 {
		return out
	}
	var rises []rise
	if agg == "delta" {
		rises = counterRises(samples)
	}
	b := 0
	var acc float64
	var count int
	flush := func() {
		if count == 0 {
			return
		}
		v := acc
		if agg == "avg" {
			v = acc / float64(count)
		}
		out[b] = &v
	}
	for i, s := range samples {
		ts := s.Timestamp
		if math.IsNaN(s.Value) || math.IsInf(s.Value, 0) || ts < edges[0].Unix() {
			continue
		}
		if ts >= edges[n].Unix() {
			break
		}
		for ts >= edges[b+1].Unix() {
			flush()
			b++
			acc, count = 0, 0
		}
		start, end := edges[b].Unix(), edges[b+1].Unix()
		switch agg {
		case "sum", "avg":
			acc += s.Value
		case "max":
			if count == 0 || s.Value > acc {
				acc = s.Value
			}
		case "min":
			if count == 0 || s.Value < acc {
				acc = s.Value
			}
		case "delta":
			if r := rises[i]; r.ok && (r.from >= start || ts-r.from <= end-start) {
				acc += r.inc
			}
		}
		count++
	}
	flush()
	return out
}
