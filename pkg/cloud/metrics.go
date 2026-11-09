package cloud

import (
	"encoding/json"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"

	"github.com/Domktorymysli/vclu/pkg/metrics"
)

// Statistics (protocol section 10, "Statistics"): the metrics Lua registers with cloud=true go
// to the cloud as one-minute buckets every metricsEvery, from where the last acknowledged batch
// of each ended. That place is kept in metricsFile, so after a restart or an outage the backlog
// follows, as far back as the cloud takes (max_age_s) and the collector still has.

const (
	metricsFile    = "cloud-metrics.json"
	metricsFirst   = 30 * time.Second // after welcome: Lua has registered its metrics by then
	metricsEvery   = 5 * time.Minute
	metricsAckWait = 30 * time.Second
	metricsMaxAge  = 7 * 24 * time.Hour // when welcome names none
	metricsHours   = 24                 // hours of MetricsStatus.Hours
)

// MetricsSource is what vCLU measures: metrics.Collector.
type MetricsSource interface {
	CloudSeries() []metrics.CloudSeries
	Buckets(path string, since, until int64) [][5]float64
}

// MetricsStatus is how sending statistics goes, for the cloud page.
type MetricsStatus struct {
	On        bool      `json:"on"`              // the cloud takes them on this connection
	Limit     int       `json:"limit,omitempty"` // series the home may keep
	Series    int       `json:"series"`          // metrics with cloud=true
	OverLimit []string  `json:"overLimit,omitempty"`
	Invalid   []string  `json:"invalid,omitempty"` // paths the cloud does not take
	Stored    int64     `json:"stored"`            // points the cloud stored since vCLU started
	LastSent  time.Time `json:"lastSent,omitzero"`
	Error     string    `json:"error,omitempty"` // the cloud's last refusal: rate_limited, busy, off

	List   []metrics.CloudSeries `json:"list"`             // the metrics with cloud=true
	Sent   map[string]int64      `json:"sent,omitempty"`   // per path, the start of the newest minute in the cloud
	Points map[string]int64      `json:"points,omitempty"` // per path, points stored since vCLU started
	Hours  []MetricsHour         `json:"hours,omitempty"`  // points stored per hour, oldest first, kept over restarts
}

// MetricsHour is how many points the cloud stored in the hour starting At (unix seconds).
type MetricsHour struct {
	At     int64 `json:"at"`
	Points int64 `json:"points"`
}

// Metrics is how sending statistics goes.
func (c *Client) Metrics() MetricsStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := c.metricsSt
	st.OverLimit, st.Invalid = slices.Clone(st.OverLimit), slices.Clone(st.Invalid)
	st.Sent, st.Points, st.Hours = maps.Clone(st.Sent), maps.Clone(st.Points), slices.Clone(st.Hours)
	if c.opts.Metrics != nil {
		st.List = c.opts.Metrics.CloudSeries()
		st.Series = len(st.List)
	}
	return st
}

func (c *Client) setMetricsStatus(f func(*MetricsStatus)) {
	c.mu.Lock()
	f(&c.metricsSt)
	c.mu.Unlock()
}

// metricsCursor is metricsFile: the start of the last bucket the cloud acknowledged, per path,
// for one home (pairing again starts over).
type metricsCursor struct {
	Home  int64            `json:"home"`
	Sent  map[string]int64 `json:"sent"`
	Hours []MetricsHour    `json:"hours,omitempty"` // for the cloud page's chart
}

func (c *Client) loadMetricsCursor(homeID int64) metricsCursor {
	cur := metricsCursor{Home: homeID, Sent: map[string]int64{}}
	b, err := os.ReadFile(filepath.Join(c.opts.Dir, metricsFile))
	if err != nil {
		return cur
	}
	var on metricsCursor
	if json.Unmarshal(b, &on) == nil && on.Home == homeID && on.Sent != nil {
		cur.Sent, cur.Hours = on.Sent, on.Hours
	}
	return cur
}

func (c *Client) saveMetricsCursor(cur metricsCursor) {
	b, _ := json.Marshal(cur)
	tmp := filepath.Join(c.opts.Dir, metricsFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		log.Printf("[Cloud] statistics: %v", err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(c.opts.Dir, metricsFile)); err != nil {
		log.Printf("[Cloud] statistics: %v", err)
	}
}

// sendMetrics runs for one connection with metrics enabled.
func (c *Client) sendMetrics(t *tunnel, homeID int64, lim proto.MetricsLimits, done <-chan struct{}) {
	cur := c.loadMetricsCursor(homeID)
	maxAge := time.Duration(lim.MaxAgeS) * time.Second
	if maxAge <= 0 {
		maxAge = metricsMaxAge
	}
	skip := map[string]string{} // path → why, for this connection: the limit or an invalid path
	c.setMetricsStatus(func(s *MetricsStatus) {
		s.On, s.Limit, s.OverLimit, s.Invalid, s.Error = true, lim.Series, nil, nil, ""
		s.Sent, s.Hours = maps.Clone(cur.Sent), slices.Clone(cur.Hours)
	})
	defer c.setMetricsStatus(func(s *MetricsStatus) { s.On = false })

	wait := c.opts.MetricsFirst
	for {
		select {
		case <-done:
			return
		case <-time.After(wait):
		}
		wait = c.opts.MetricsEvery
		if !clockLooksSet() {
			continue // buckets with a wrong time would be refused, or worse, taken
		}
		for {
			now := c.clock()
			m, last, more := metricsBatch(c.opts.Metrics, cur.Sent, skip, now.Add(-maxAge).Unix()+proto.MetricsBucketSeconds, now.Unix())
			if len(m.Series) == 0 {
				break
			}
			if err := t.send(m); err != nil {
				return
			}
			ack, ok := waitMetricsAck(t, m.ID, done)
			if !ok {
				return
			}
			if ack.ID != m.ID {
				log.Printf("[Cloud] statistics: no answer to batch %s, sending it again later", m.ID)
				break
			}
			if ack.Error == proto.MetricsOff {
				log.Printf("[Cloud] statistics are off for this home")
				c.setMetricsStatus(func(s *MetricsStatus) { s.On, s.Error = false, ack.Error })
				return
			}
			if ack.Error != "" {
				c.setMetricsStatus(func(s *MetricsStatus) { s.Error = ack.Error })
				if ack.RetryAfterS > 0 {
					wait = time.Duration(ack.RetryAfterS) * time.Second
				}
				break
			}
			stored := applyMetricsAck(ack, m, last, cur.Sent, skip)
			cur.Hours = addMetricsHour(cur.Hours, now, int64(ack.Stored))
			c.saveMetricsCursor(cur)
			c.setMetricsStatus(func(s *MetricsStatus) {
				s.Stored += int64(ack.Stored)
				s.Sent, s.Hours = maps.Clone(cur.Sent), slices.Clone(cur.Hours)
				if s.Points == nil {
					s.Points = map[string]int64{}
				}
				for p, n := range stored {
					s.Points[p] += n
				}
				s.LastSent, s.Error = now, ""
				s.OverLimit, s.Invalid = nil, nil
				for p, why := range skip {
					if why == proto.MetricOverLimit {
						s.OverLimit = append(s.OverLimit, p)
					} else {
						s.Invalid = append(s.Invalid, p)
					}
				}
				slices.Sort(s.OverLimit)
				slices.Sort(s.Invalid)
			})
			if !more {
				break
			}
		}
	}
}

// waitMetricsAck waits for the answer to batch id, passing over late answers to earlier ones; an
// empty ack after metricsAckWait. False when the connection ended.
func waitMetricsAck(t *tunnel, id string, done <-chan struct{}) (proto.MetricsAck, bool) {
	timeout := time.After(metricsAckWait)
	for {
		select {
		case <-done:
			return proto.MetricsAck{}, false
		case a := <-t.metricsAck:
			if a.ID == id {
				return a, true
			}
		case <-timeout:
			return proto.MetricsAck{}, true
		}
	}
}

// metricsBatch takes the buckets over since each series' cursor (at least oldest), up to the
// minute now is in, as many as one message takes. last is each series' newest bucket in it;
// more says that some were left for the next one.
func metricsBatch(src MetricsSource, sent map[string]int64, skip map[string]string, oldest, now int64) (m proto.Metrics, last map[string]int64, more bool) {
	m = proto.Metrics{Type: proto.TypeMetrics, ID: randomID()}
	last = map[string]int64{}
	until := now / proto.MetricsBucketSeconds * proto.MetricsBucketSeconds
	room := proto.MaxMetricsPoints
	for _, s := range src.CloudSeries() {
		if _, ok := skip[s.Path]; ok {
			continue
		}
		if !validMetricPath(s.Path) || len(s.Unit) > proto.MaxMetricUnit {
			skip[s.Path] = proto.MetricInvalid
			log.Printf("[Cloud] statistics: metric %.80q is not sent: use up to %d of %s in the path and a short unit", s.Path, proto.MaxMetricPath, proto.MetricPathChars)
			continue
		}
		since := max(sent[s.Path]+proto.MetricsBucketSeconds, oldest)
		pts := src.Buckets(s.Path, since, until)
		if len(pts) == 0 {
			continue
		}
		if room == 0 || len(m.Series) == proto.MaxMetricsSeries {
			return m, last, true
		}
		if len(pts) > room {
			pts, more = pts[:room], true
		}
		room -= len(pts)
		m.Series = append(m.Series, proto.MetricSeries{Path: s.Path, Unit: s.Unit, Kind: s.Kind, Points: pts})
		last[s.Path] = int64(pts[len(pts)-1][0])
	}
	return m, last, more
}

// applyMetricsAck moves the cursors over what the cloud took or will never take; a series over
// the limit stays where it was, to follow once there is room (on a later connection). It returns
// the points stored per path.
func applyMetricsAck(ack proto.MetricsAck, m proto.Metrics, last, sent map[string]int64, skip map[string]string) map[string]int64 {
	stored := map[string]int64{}
	for _, s := range m.Series {
		stored[s.Path] = int64(len(s.Points))
	}
	for _, r := range ack.Rejected {
		switch r.Reason {
		case proto.MetricOverLimit:
			skip[r.Path] = r.Reason
			delete(last, r.Path)
			delete(stored, r.Path)
		case proto.MetricInvalid:
			skip[r.Path] = r.Reason
			delete(stored, r.Path)
		case proto.MetricBadPoints:
			stored[r.Path] = max(stored[r.Path]-int64(r.Points), 0)
		}
	}
	for p, b := range last {
		sent[p] = b
	}
	return stored
}

// addMetricsHour counts n points in the hour now is in and keeps the last metricsHours hours.
func addMetricsHour(hours []MetricsHour, now time.Time, n int64) []MetricsHour {
	at := now.Truncate(time.Hour).Unix()
	if k := len(hours); k > 0 && hours[k-1].At == at {
		hours[k-1].Points += n
	} else {
		hours = append(hours, MetricsHour{At: at, Points: n})
	}
	oldest := at - (metricsHours-1)*3600
	i := 0
	for i < len(hours) && hours[i].At < oldest {
		i++
	}
	return slices.Clone(hours[i:])
}

func validMetricPath(p string) bool {
	if p == "" || len(p) > proto.MaxMetricPath {
		return false
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return false
		}
	}
	return true
}
