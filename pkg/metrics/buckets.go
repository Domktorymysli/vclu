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

// BucketEdges returns n+1 edges of n buckets in loc, the last bucket holding now. bucket is
// "1h" (on the hour), "1d" (from local midnight: a DST day is 23 or 25 h long), "1w" (from
// Monday's midnight) or "1mo" (from the 1st).
func BucketEdges(now time.Time, loc *time.Location, n int, bucket string) ([]time.Time, error) {
	if n < 1 || n > MaxBuckets {
		return nil, fmt.Errorf("buckets: %d not in 1..%d", n, MaxBuckets)
	}
	t := now.In(loc)
	edges := make([]time.Time, n+1)
	y, m, d := t.Date()
	switch bucket {
	case "1d", "1w":
		days := 1
		if bucket == "1w" {
			days = 7
			d -= (int(t.Weekday()) + 6) % 7 // back to Monday
		}
		for i := 0; i <= n; i++ {
			// time.Date normalises the day and gives midnight in loc on any DST day
			edges[i] = time.Date(y, m, d+(i-(n-1))*days, 0, 0, 0, 0, loc)
		}
		return edges, nil
	case "1mo":
		for i := 0; i <= n; i++ {
			edges[i] = time.Date(y, m+time.Month(i-(n-1)), 1, 0, 0, 0, 0, loc)
		}
		return edges, nil
	case "1h":
	default:
		return nil, fmt.Errorf("buckets: unknown bucket %q", bucket)
	}

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

func valid(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// nextValid is the index of the first valid sample after i, or -1.
func nextValid(samples []Sample, i int) int {
	for k := i + 1; k < len(samples); k++ {
		if valid(samples[k].Value) {
			return k
		}
	}
	return -1
}

// counterRises gives each sample of a counter its rise over the high mark: after a dip the count
// goes on from the value before it; after a reset, from the first low sample. A single reading
// above the trend that the next one returns from is a spike (a garbage float) and adds nothing.
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
			// a spike: the next reading is back between the mark and it, so this one adds nothing
			if n := nextValid(samples, i); n >= 0 && samples[n].Value >= h.Value && samples[n].Value < s.Value {
				continue
			}
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
// with the sampling rate). delta is aggregateDelta.
func Aggregate(samples []Sample, edges []time.Time, agg string) []*float64 {
	n := len(edges) - 1
	out := make([]*float64, max(n, 0))
	if n < 1 {
		return out
	}
	if agg == "delta" {
		return aggregateDelta(samples, edges, out)
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
	for _, s := range samples {
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
		}
		count++
	}
	flush()
	return out
}

// aggregateDelta sums the rises of a counter (counterRises), so a bucket is never negative. A
// rise across bucket edges is split between the buckets by time (hourly readings a few seconds
// late still count); one over a gap longer than 1.5 buckets of its later sample (a missed
// reading, the meter offline) is left out. A bucket is nil when it has no sample and no share.
func aggregateDelta(samples []Sample, edges []time.Time, out []*float64) []*float64 {
	n := len(edges) - 1
	first, last := edges[0].Unix(), edges[n].Unix()
	rises := counterRises(samples)
	add := func(b int, v float64) {
		if out[b] == nil {
			out[b] = new(float64)
		}
		*out[b] += v
	}
	b := 0
	for i, s := range samples {
		ts := s.Timestamp
		if !valid(s.Value) || ts < first {
			continue
		}
		if ts >= last {
			break
		}
		for ts >= edges[b+1].Unix() {
			b++
		}
		add(b, 0)
		r := rises[i]
		if !r.ok || r.inc == 0 {
			continue
		}
		gap := ts - r.from
		if gap <= 0 || ts-edges[b].Unix() >= gap {
			add(b, r.inc)
			continue
		}
		if 2*gap > 3*(edges[b+1].Unix()-edges[b].Unix()) {
			continue
		}
		// from the later sample's bucket back to the earlier one; time before the range is not shown
		for k := b; k >= 0 && edges[k+1].Unix() > r.from; k-- {
			if lo, hi := max(edges[k].Unix(), r.from), min(edges[k+1].Unix(), ts); hi > lo {
				add(k, r.inc*float64(hi-lo)/float64(gap))
			}
		}
	}
	return out
}
