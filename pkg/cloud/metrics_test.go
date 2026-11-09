package cloud

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"

	"github.com/Domktorymysli/vclu/pkg/metrics"
)

// fakeMetrics has one-minute buckets per path, as the collector would make them.
type fakeMetrics struct {
	mu      sync.Mutex
	series  []metrics.CloudSeries
	buckets map[string][][5]float64
}

func (f *fakeMetrics) CloudSeries() []metrics.CloudSeries {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.series)
}

func (f *fakeMetrics) Buckets(path string, since, until int64) [][5]float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out [][5]float64
	for _, b := range f.buckets[path] {
		if int64(b[0]) >= since && int64(b[0]) < until {
			out = append(out, b)
		}
	}
	return out
}

func (f *fakeMetrics) add(path string, at time.Time, v float64) {
	f.mu.Lock()
	f.buckets[path] = append(f.buckets[path], [5]float64{float64(at.Unix()), v, v, v, 1})
	f.mu.Unlock()
}

func readMetrics(t *testing.T, conn *fakeConn) proto.Metrics {
	t.Helper()
	var m proto.Metrics
	if err := conn.read(t, proto.TypeMetrics).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func points(m proto.Metrics) map[string]int {
	out := map[string]int{}
	for _, s := range m.Series {
		out[s.Path] = len(s.Points)
	}
	return out
}

// Statistics: only when welcome enables them, the backlog from where the cloud acknowledged,
// again after rate_limited, and never a series over the limit or with a path the cloud refuses.
func TestMetricsSentFromTheCursor(t *testing.T) {
	min0 := time.Now().Truncate(time.Minute)
	src := &fakeMetrics{
		series:  []metrics.CloudSeries{{Path: "a", Unit: "°C", Kind: "gauge"}, {Path: "c", Kind: "counter"}, {Path: "zła ścieżka"}},
		buckets: map[string][][5]float64{},
	}
	for i := 3; i >= 1; i-- {
		src.add("a", min0.Add(-time.Duration(i)*time.Minute), float64(i))
		src.add("c", min0.Add(-time.Duration(i)*time.Minute), 100)
	}
	src.add("a", min0, 9) // the minute now is in: not over yet

	f := newFakeCloud(t)
	f.metrics = &proto.MetricsLimits{Series: 1, BucketS: 60, MaxAgeS: 7 * 86400}
	var ahead atomic.Int64 // the test's clock runs ahead of the wall clock
	c, conn := connectWith(t, f, func(c *Client) {
		c.clock = func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) }
		c.opts.Metrics = src
		c.opts.MetricsFirst, c.opts.MetricsEvery = 10*time.Millisecond, 100*time.Millisecond
	})
	if !slices.Contains(conn.hello.Caps, proto.CapMetrics) {
		t.Fatalf("hello caps = %v", conn.hello.Caps)
	}

	m := readMetrics(t, conn)
	if p := points(m); len(p) != 2 || p["a"] != 3 || p["c"] != 3 || m.Series[0].Unit != "°C" || m.Series[1].Kind != "counter" {
		t.Fatalf("first batch = %+v", m)
	}
	proto.Write(conn.ctrl, proto.MetricsAck{Type: proto.TypeMetricsAck, ID: m.ID, Error: proto.MetricsRateLimited, RetryAfterS: 1})
	m2 := readMetrics(t, conn)
	if p := points(m2); p["a"] != 3 || p["c"] != 3 || m2.ID == m.ID {
		t.Fatalf("after rate_limited = %+v", m2)
	}
	proto.Write(conn.ctrl, proto.MetricsAck{Type: proto.TypeMetricsAck, ID: m2.ID, Stored: 3,
		Rejected: []proto.MetricRejected{{Path: "c", Reason: proto.MetricOverLimit}}})

	eventually(t, "status after the ack", func() bool { return c.Metrics().Stored == 3 })
	st := c.Metrics()
	if !st.On || st.Limit != 1 || st.Series != 3 || !slices.Equal(st.OverLimit, []string{"c"}) || !slices.Equal(st.Invalid, []string{"zła ścieżka"}) {
		t.Errorf("status = %+v", st)
	}
	if cur := c.loadMetricsCursor(42); cur.Sent["a"] != min0.Add(-time.Minute).Unix() || cur.Sent["c"] != 0 {
		t.Errorf("cursor = %+v", cur)
	}
	if cur := c.loadMetricsCursor(43); len(cur.Sent) != 0 {
		t.Errorf("another home's cursor = %+v", cur)
	}

	// The next round has only the minute since, and nothing of c on this connection.
	src.add("a", min0.Add(time.Minute), 5)
	ahead.Store(int64(min0.Add(2*time.Minute + time.Second).Sub(time.Now())))
	m3 := readMetrics(t, conn)
	if p := points(m3); len(p) != 1 || p["a"] != 2 {
		t.Errorf("next round = %+v", m3)
	}
	if b := m3.Series[0].Points[0][0]; int64(b) != min0.Unix() {
		t.Errorf("next round starts at %v, want %v", b, min0.Unix())
	}
}

// A batch takes at most what one message may carry and says the rest is left.
func TestMetricsBatchLimits(t *testing.T) {
	src := &fakeMetrics{buckets: map[string][][5]float64{}}
	t0 := time.Unix(1_800_000_000/60*60, 0)
	for i := range proto.MaxMetricsSeries + 1 {
		p := "s" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		src.series = append(src.series, metrics.CloudSeries{Path: p})
		for j := range 30 {
			src.add(p, t0.Add(time.Duration(j)*time.Minute), 1)
		}
	}
	m, last, more := metricsBatch(src, map[string]int64{}, map[string]string{}, 0, t0.Add(time.Hour).Unix())
	n := 0
	for _, s := range m.Series {
		n += len(s.Points)
	}
	if n != proto.MaxMetricsPoints || !more || len(last) != len(m.Series) || len(m.Series) > proto.MaxMetricsSeries {
		t.Errorf("%d series, %d points, more %v", len(m.Series), n, more)
	}
}

// Statistics read back: one query to the cloud, then the cache; a refusal makes the next charts
// draw without the cloud for retry_after_s.
func TestMetricsHistoryCached(t *testing.T) {
	f := newFakeCloud(t)
	f.metrics = &proto.MetricsLimits{Series: 10, BucketS: 60, MaxAgeS: 7 * 86400}
	c, conn := connectWith(t, f, func(c *Client) {
		c.opts.Metrics = &fakeMetrics{buckets: map[string][][5]float64{}}
		c.opts.MetricsFirst = time.Hour
	})
	eventually(t, "statistics on", func() bool { return c.Metrics().On })

	type result struct {
		pts   [][5]float64
		until int64
		err   error
	}
	ask := func(since, until int64, step int) chan result {
		ch := make(chan result, 1)
		go func() {
			pts, u, err := c.MetricsHistory(context.Background(), "energia:dom", since, until, step)
			ch <- result{pts, u, err}
		}()
		return ch
	}
	ch := ask(3700, 90000, 1800)
	var q proto.MetricsQuery
	if err := conn.read(t, proto.TypeMetricsQuery).Decode(&q); err != nil {
		t.Fatal(err)
	}
	if q.Path != "energia:dom" || q.Since != 3600 || q.Until != 90000 || q.StepS != 1800 {
		t.Fatalf("query = %+v", q)
	}
	proto.Write(conn.ctrl, proto.MetricsPoints{Type: proto.TypeMetricsPoints, ID: q.ID, StepS: 3600,
		Points: [][5]float64{{3600, 1, 1, 1, 60}, {7200, 2, 2, 2, 60}}})
	if r := <-ch; r.err != nil || r.until != 90000 || len(r.pts) != 1 || r.pts[0][0] != 7200 {
		t.Fatalf("first = %+v", r)
	}

	// Later and narrower: from the cache, up to where it reaches.
	if r := <-ask(7200, 95000, 1800); r.err != nil || r.until != 90000 || len(r.pts) != 1 {
		t.Errorf("cached = %+v", r)
	}

	ch = ask(0, 90000, 3600)
	if err := conn.read(t, proto.TypeMetricsQuery).Decode(&q); err != nil {
		t.Fatal(err)
	}
	proto.Write(conn.ctrl, proto.MetricsPoints{Type: proto.TypeMetricsPoints, ID: q.ID, Points: [][5]float64{}, Error: proto.MetricsRateLimited, RetryAfterS: 60})
	if r := <-ch; !errors.Is(r.err, ErrNoHistory) {
		t.Errorf("rate limited = %+v", r)
	}
	if r := <-ask(0, 90000, 7200); !errors.Is(r.err, ErrNoHistory) {
		t.Errorf("within retry_after_s = %+v", r)
	}
}
