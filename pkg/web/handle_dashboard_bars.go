package web

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// barsSpec is what Dashboard.API.barsSpec says a bars widget aggregates.
type barsSpec struct {
	Path    string `json:"path"`
	Compare string `json:"compare"`
	Bucket  string `json:"bucket"`
	Buckets int    `json:"buckets"`
	Agg     string `json:"agg"`
	Zone    string `json:"zone"`
	Error   string `json:"error"`
	Status  int    `json:"status"`
}

type barsBucket struct {
	T       int64    `json:"t"`
	Date    string   `json:"date"` // YYYY-MM-DD in the house's zone
	Weekday int      `json:"wd"`   // 0 = Sunday
	Hour    int      `json:"h"`
	EndHour int      `json:"eh"` // the local hour it ends (03 after a spring-forward 01)
	V       *float64 `json:"v"`
	C       *float64 `json:"c,omitempty"`
	Partial bool     `json:"partial,omitempty"`
	Old     bool     `json:"old,omitempty"` // starts before the retention: no data is kept for it
}

type barsResult struct {
	Bucket  string       `json:"bucket"`
	Agg     string       `json:"agg"`
	Zone    string       `json:"zone"`
	Kept    int          `json:"kept"` // days of samples the collector keeps
	Buckets []barsBucket `json:"buckets"`
}

// handleAPIDashboardBars returns the buckets of a bars widget, aggregated here, under the
// dashboard's own auth; the widget names its metrics, so a page reads no other.
func (s *Server) handleAPIDashboardBars(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil || s.metricsCollector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metrics not available"})
		return
	}
	if !s.checkDashboardAuth(c, id) {
		return
	}
	widget := c.Query("widget")
	if widget == "" || len(widget) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "widget is required (max 128 chars)"})
		return
	}
	key := id + "\x00" + widget
	if data, ok := dashCache.getBars(key); ok {
		c.Data(http.StatusOK, "application/json", []byte(data))
		return
	}
	status, data := barsFlight.do(key, func() (int, []byte) { return barsLoad(s, id, widget) })
	c.Data(status, "application/json", data)
}

// barsLoad reads the widget's spec and aggregates it; a 200 answer is cached.
var barsLoad = func(s *Server, id, widget string) (int, []byte) {
	fail := func(status int, msg string) (int, []byte) {
		b, _ := json.Marshal(gin.H{"error": msg})
		return status, b
	}
	raw, err := s.luaExecutor.Execute(fmt.Sprintf(`Dashboard.API.barsSpec("%s", "%s")`, luaEscape(id), luaEscape(widget)))
	if err != nil {
		log.Printf("[DASHBOARD] %s bars %s: %v", id, widget, err)
		return fail(http.StatusInternalServerError, "bars unavailable")
	}
	var spec barsSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return fail(http.StatusInternalServerError, "bars unavailable")
	}
	if spec.Status >= 400 && spec.Status < 600 {
		return fail(spec.Status, spec.Error)
	}
	res, err := s.barsSeries(spec, time.Now())
	if err != nil {
		log.Printf("[DASHBOARD] %s bars %s: %v", id, widget, err)
		return fail(http.StatusInternalServerError, "bars unavailable")
	}
	data, _ := json.Marshal(res)
	dashCache.setBars(id+"\x00"+widget, string(data))
	return http.StatusOK, data
}

// inflight merges concurrent loads of one key: the first caller reads, the others wait for it.
type inflight struct {
	mu    sync.Mutex
	calls map[string]*inflightCall
}

type inflightCall struct {
	done   chan struct{}
	status int
	data   []byte
}

var barsFlight = &inflight{calls: map[string]*inflightCall{}}

func (f *inflight) do(key string, load func() (int, []byte)) (int, []byte) {
	f.mu.Lock()
	if call, ok := f.calls[key]; ok {
		f.mu.Unlock()
		<-call.done
		return call.status, call.data
	}
	call := &inflightCall{done: make(chan struct{})}
	f.calls[key] = call
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		delete(f.calls, key)
		f.mu.Unlock()
		close(call.done)
	}()
	call.status, call.data = load()
	return call.status, call.data
}

// barsSeries aggregates the widget's metrics into its buckets, the last one holding now.
func (s *Server) barsSeries(spec barsSpec, now time.Time) (*barsResult, error) {
	if !metrics.Aggregations[spec.Agg] {
		return nil, fmt.Errorf("unknown agg %q", spec.Agg)
	}
	loc := time.Local
	if spec.Zone != "" {
		l, err := time.LoadLocation(spec.Zone)
		if err != nil {
			return nil, err
		}
		loc = l
	}
	edges, err := metrics.BucketEdges(now, loc, spec.Buckets, spec.Bucket == "1d")
	if err != nil {
		return nil, err
	}
	// one bucket earlier: delta's first step comes from the sample before the range
	since := edges[0].Add(-edges[1].Sub(edges[0])).Unix()
	vals := metrics.Aggregate(s.metricsCollector.Query(spec.Path, since), edges, spec.Agg)
	var cmp []*float64
	if spec.Compare != "" {
		cmp = metrics.Aggregate(s.metricsCollector.Query(spec.Compare, since), edges, spec.Agg)
	}
	out := &barsResult{Bucket: spec.Bucket, Agg: spec.Agg, Zone: spec.Zone, Kept: s.metricsCollector.RetainDays(),
		Buckets: make([]barsBucket, len(vals))}
	kept := s.metricsCollector.Retention(now)
	for i, v := range vals {
		t := edges[i].In(loc)
		b := barsBucket{T: t.Unix(), Date: t.Format("2006-01-02"), Weekday: int(t.Weekday()), Hour: t.Hour(),
			EndHour: edges[i+1].In(loc).Hour(), V: round6(v), Old: t.Before(kept)}
		if cmp != nil {
			b.C = round6(cmp[i])
		}
		out.Buckets[i] = b
	}
	out.Buckets[len(vals)-1].Partial = true
	return out, nil
}

// round6 drops the float noise of summed steps (10.40099999999984 -> 10.401).
func round6(v *float64) *float64 {
	if v == nil {
		return nil
	}
	r := math.Round(*v*1e6) / 1e6
	return &r
}
