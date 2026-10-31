package metrics

import (
	"testing"
	"time"
)

func warsaw(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skip("no zoneinfo:", err)
	}
	return loc
}

// Daily buckets start at local midnight; the DST days of 2026 are 23 and 25 hours long.
func TestBucketEdgesDailyDST(t *testing.T) {
	loc := warsaw(t)
	for _, tc := range []struct {
		now   time.Time
		hours []float64
	}{
		{time.Date(2026, 3, 30, 12, 0, 0, 0, loc), []float64{24, 23, 24}},
		{time.Date(2026, 10, 26, 0, 0, 0, 0, loc), []float64{24, 25, 24}},
	} {
		edges, err := BucketEdges(tc.now, loc, 3, true)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range tc.hours {
			if got := edges[i+1].Sub(edges[i]).Hours(); got != want {
				t.Errorf("%v bucket %d: %v h, want %v", tc.now, i, got, want)
			}
			if h, m, _ := edges[i].In(loc).Clock(); h != 0 || m != 0 {
				t.Errorf("edge %v is not midnight", edges[i].In(loc))
			}
		}
		if !edges[2].Before(tc.now) && !edges[2].Equal(tc.now) || !edges[3].After(tc.now) {
			t.Errorf("the last bucket %v..%v does not hold now %v", edges[2], edges[3], tc.now)
		}
	}
}

// Hourly buckets over a fall-back night: 02:00 comes twice and is two buckets; now in the
// second 02:xx lands in the last bucket.
func TestBucketEdgesHourlyFallBack(t *testing.T) {
	loc := warsaw(t)
	// 2026-10-25 01:00 UTC = the second 02:30 in Warsaw (CET)
	now := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	edges, err := BucketEdges(now, loc, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range edges {
		got = append(got, e.In(loc).Format("15:04 MST"))
	}
	want := []string{"00:00 CEST", "01:00 CEST", "02:00 CEST", "02:00 CET", "03:00 CET"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("edges %v, want %v", got, want)
		}
	}
	// the spring-forward day has no 02:00: 23 buckets from midnight to midnight
	spring, _ := BucketEdges(time.Date(2026, 3, 29, 23, 10, 0, 0, loc), loc, 23, false)
	if s := spring[0].In(loc).Format("2006-01-02 15:04"); s != "2026-03-29 00:00" {
		t.Errorf("23 hourly buckets on 29.03 start at %s, want midnight", s)
	}
}

func TestBucketEdgesBounds(t *testing.T) {
	for _, n := range []int{0, MaxBuckets + 1} {
		if _, err := BucketEdges(time.Now(), time.UTC, n, false); err == nil {
			t.Errorf("n=%d accepted", n)
		}
	}
}

func hourEdges(n int) []time.Time {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	edges := make([]time.Time, n+1)
	for i := range edges {
		edges[i] = base.Add(time.Duration(i) * time.Hour)
	}
	return edges
}

func at(h, m int, v float64) Sample {
	return Sample{Timestamp: time.Date(2026, 1, 1, h, m, 0, 0, time.UTC).Unix(), Value: v}
}

func vals(out []*float64) []any {
	r := make([]any, len(out))
	for i, v := range out {
		if v != nil {
			r[i] = *v
		}
	}
	return r
}

func TestAggregate(t *testing.T) {
	samples := []Sample{at(0, 0, 1), at(0, 30, 3), at(2, 10, 5), at(2, 20, 2), at(2, 50, 8)}
	for agg, want := range map[string][]any{
		"sum": {4.0, nil, 15.0},
		"avg": {2.0, nil, 5.0},
		"max": {3.0, nil, 8.0},
		"min": {1.0, nil, 2.0},
	} {
		got := vals(Aggregate(samples, hourEdges(3), agg))
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s = %v, want %v (the empty hour is a gap, not 0)", agg, got, want)
				break
			}
		}
	}
}

// A counter's bucket is its rises: the step from the previous bucket counts when it is at most
// a bucket long, a fall (reset) adds nothing and the rises after it count.
func TestAggregateDelta(t *testing.T) {
	samples := []Sample{
		at(0, 0, 100), at(0, 50, 101.5), // hour 0: 1.5
		at(1, 10, 102), at(1, 20, 3), at(1, 40, 4.25), // hour 1: 0.5 from 00:50, reset, 1.25
		// hour 2: nothing
		at(3, 30, 10), at(3, 45, 11), // hour 3: 1 (the step from 01:40 is longer than a bucket)
		at(4, 0, 11), // hour 4: 0 (the meter stood still)
	}
	got := vals(Aggregate(samples, hourEdges(5), "delta"))
	want := []any{1.5, 1.75, nil, 1.0, 0.0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delta = %v, want %v", got, want)
		}
	}
	// a sample before the first edge gives the first bucket its first step
	before := []Sample{{Timestamp: hourEdges(1)[0].Unix() - 60, Value: 99}, at(0, 10, 100)}
	if got := vals(Aggregate(before, hourEdges(1), "delta")); got[0] != 1.0 {
		t.Errorf("delta with the sample before = %v, want 1", got)
	}
	// only a reset: never negative
	if got := vals(Aggregate([]Sample{at(0, 0, 50), at(0, 30, 0)}, hourEdges(1), "delta")); got[0] != 0.0 {
		t.Errorf("reset only = %v, want 0", got)
	}
}

// A bad reading is not a reset: the count goes on from the value before the dip. A value that
// stays low for resetSamples samples is a reset and the rises after it count.
func TestAggregateDeltaDips(t *testing.T) {
	series := func(vs ...float64) []Sample {
		out := make([]Sample, len(vs))
		for i, v := range vs {
			out[i] = at(0, i*5, v)
		}
		return out
	}
	for name, tc := range map[string]struct {
		samples []Sample
		want    float64
	}{
		"one zero (Modbus timeout)": {series(1000, 1001, 0, 1002, 1003), 3},
		"two zeros":                 {series(1000, 1001, 0, 0, 1002), 2},
		"three zeros, then back":    {series(1000, 1001, 0, 0, 0, 1002), 2},
		"meter replaced":            {series(5000, 5001, 10, 11, 12, 13), 4},
		"reset to 0, counting up":   {series(50, 51, 0, 1, 2, 3), 4},
		"a dip below, not to 0":     {series(1000, 1001, 999.5, 1001.5), 1.5},
	} {
		got := Aggregate(tc.samples, hourEdges(1), "delta")
		if got[0] == nil || *got[0] != tc.want {
			t.Errorf("%s: %v, want %v", name, vals(got), tc.want)
		}
	}
}

func TestAggregateEmpty(t *testing.T) {
	if got := Aggregate(nil, hourEdges(2), "avg"); len(got) != 2 || got[0] != nil || got[1] != nil {
		t.Errorf("no samples = %v, want two gaps", got)
	}
	// samples after the last edge are not counted
	if got := vals(Aggregate([]Sample{at(5, 0, 1)}, hourEdges(2), "sum")); got[0] != nil || got[1] != nil {
		t.Errorf("late sample counted: %v", got)
	}
}
