package metrics

import (
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/stats"
)

// Sample is a single metric data point (unix seconds + value).
type Sample struct {
	Timestamp int64   `json:"t"`
	Value     float64 `json:"v"`
}

// MetricDef holds a registered metric's metadata, ring buffer, and pending writes.
type MetricDef struct {
	Path     string
	Unit     string
	Interval int // seconds between samples
	ring     *stats.RingBuffer[Sample]
	lastPoll time.Time
	writeBuf []Sample // pending samples for disk flush
}

// MetricInfo is returned by List() for API consumers.
type MetricInfo struct {
	Path     string  `json:"path"`
	Unit     string  `json:"unit"`
	Interval int     `json:"interval"`
	Samples  int     `json:"samples"`
	Latest   float64 `json:"latest,omitempty"`
}

// CollectResult is one entry in the batch returned from Lua collection.
type CollectResult struct {
	Path  string  `json:"p"`
	Value float64 `json:"v"`
}

// Collector manages metric registration, ring-buffer storage, and disk persistence.
type Collector struct {
	mu         sync.RWMutex
	metrics    map[string]*MetricDef
	dataDir    string
	ringSize   int
	flushInt   time.Duration
	retainDays int
	maxMetrics int
	stopChan   chan struct{}
	wg         sync.WaitGroup
}

// Option configures a Collector.
type Option func(*Collector)

// WithRingSize sets the per-metric ring buffer capacity.
func WithRingSize(n int) Option {
	return func(c *Collector) { c.ringSize = n }
}

// WithFlushInterval sets how often samples are flushed to disk.
func WithFlushInterval(d time.Duration) Option {
	return func(c *Collector) { c.flushInt = d }
}

// DefaultRetainDays is how many days of daily files a collector keeps unless told otherwise.
const DefaultRetainDays = 7

// MaxPoints bounds the samples one raw query answers with (Downsample).
const MaxPoints = 2000

// WithRetainDays sets how many days of daily files to keep (1-366; other values keep the default).
func WithRetainDays(d int) Option {
	return func(c *Collector) {
		if d >= 1 && d <= 366 {
			c.retainDays = d
		}
	}
}

// RetainDays is how many days of samples the collector keeps on disk.
func (c *Collector) RetainDays() int { return c.retainDays }

// Retention is the oldest moment the collector still has files for, as of now.
func (c *Collector) Retention(now time.Time) time.Time {
	return now.AddDate(0, 0, -c.retainDays)
}

// QueryRange returns the samples of the last d, at most the retention back and at most
// MaxPoints of them.
func (c *Collector) QueryRange(path string, d time.Duration) []Sample {
	now := time.Now()
	since := now.Add(-d)
	if r := c.Retention(now); since.Before(r) {
		since = r
	}
	return Downsample(c.Query(path, since.Unix()), MaxPoints)
}

// Downsample averages runs of samples down to at most max points, each at its run's first time.
func Downsample(samples []Sample, max int) []Sample {
	if max < 1 || len(samples) <= max {
		return samples
	}
	out := make([]Sample, 0, max)
	for i := 0; i < max; i++ {
		lo, hi := i*len(samples)/max, (i+1)*len(samples)/max
		var sum float64
		for _, s := range samples[lo:hi] {
			sum += s.Value
		}
		out = append(out, Sample{Timestamp: samples[lo].Timestamp, Value: sum / float64(hi-lo)})
	}
	return out
}

// WithMaxMetrics sets the maximum number of registered metrics.
func WithMaxMetrics(n int) Option {
	return func(c *Collector) { c.maxMetrics = n }
}

// NewCollector creates a metrics collector that stores daily files under dataDir.
func NewCollector(dataDir string, opts ...Option) *Collector {
	c := &Collector{
		metrics:    make(map[string]*MetricDef),
		dataDir:    dataDir,
		ringSize:   720, // 2h at 10s intervals
		flushInt:   5 * time.Minute,
		retainDays: DefaultRetainDays,
		maxMetrics: 500,
		stopChan:   make(chan struct{}),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Register adds or updates a metric definition. Idempotent — re-registering
// the same path keeps the existing ring buffer and pending writes.
func (c *Collector) Register(path, unit string, interval int) {
	if interval < 1 {
		interval = 60
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.metrics[path]; exists {
		// Update metadata, keep history
		c.metrics[path].Unit = unit
		c.metrics[path].Interval = interval
		return
	}

	if len(c.metrics) >= c.maxMetrics {
		return // silently drop
	}

	c.metrics[path] = &MetricDef{
		Path:     path,
		Unit:     unit,
		Interval: interval,
		ring:     stats.NewRingBuffer[Sample](c.ringSize),
	}
}

// DueMetrics returns the paths of metrics that are due for polling.
func (c *Collector) DueMetrics() []string {
	now := time.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()

	var due []string
	for path, def := range c.metrics {
		if now.Sub(def.lastPoll) >= time.Duration(def.Interval)*time.Second {
			due = append(due, path)
		}
	}
	return due
}

// Ingest pushes collected results into ring buffers and write buffers.
func (c *Collector) Ingest(results []CollectResult) {
	now := time.Now().Unix()
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, r := range results {
		def, ok := c.metrics[r.Path]
		if !ok {
			continue
		}
		s := Sample{Timestamp: now, Value: r.Value}
		def.ring.Push(s)
		def.writeBuf = append(def.writeBuf, s)
		def.lastPoll = time.Now()
	}
}

// Query returns samples for a metric since the given unix timestamp.
// It reads from the ring buffer first, falling back to disk for older data.
func (c *Collector) Query(path string, since int64) []Sample {
	c.mu.RLock()
	def, ok := c.metrics[path]
	if !ok {
		c.mu.RUnlock()
		return nil
	}

	// Get all from ring buffer
	all := def.ring.GetAll()
	c.mu.RUnlock()

	// Check if ring buffer covers the requested range
	if len(all) > 0 && all[0].Timestamp <= since {
		// Ring buffer has everything we need
		return filterSamples(all, since)
	}

	// Need to read from disk for older data
	diskSamples := readFiles(c.dataDir, path, since)

	// Merge: disk samples first, then ring buffer (dedup by timestamp)
	seen := make(map[int64]bool, len(all))
	var merged []Sample
	for _, s := range diskSamples {
		if s.Timestamp >= since {
			merged = append(merged, s)
			seen[s.Timestamp] = true
		}
	}
	for _, s := range all {
		if s.Timestamp >= since && !seen[s.Timestamp] {
			merged = append(merged, s)
		}
	}
	return merged
}

// List returns info about all registered metrics.
func (c *Collector) List() []MetricInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make([]MetricInfo, 0, len(c.metrics))
	for _, def := range c.metrics {
		info := MetricInfo{
			Path:     def.Path,
			Unit:     def.Unit,
			Interval: def.Interval,
			Samples:  def.ring.Len(),
		}
		last := def.ring.GetLast(1)
		if len(last) > 0 {
			info.Latest = last[0].Value
		}
		out = append(out, info)
	}
	return out
}

// Start begins the background flush loop and rotation.
func (c *Collector) Start() {
	c.wg.Add(1)
	go c.flushLoop()
}

// Stop signals the flush loop to stop, performs a final flush, and waits.
func (c *Collector) Stop() {
	close(c.stopChan)
	c.wg.Wait()
}

// flushLoop periodically flushes write buffers to disk and rotates old files.
func (c *Collector) flushLoop() {
	defer c.wg.Done()

	flushTicker := time.NewTicker(c.flushInt)
	defer flushTicker.Stop()

	rotateTicker := time.NewTicker(1 * time.Hour)
	defer rotateTicker.Stop()

	for {
		select {
		case <-c.stopChan:
			c.flush()
			return
		case <-flushTicker.C:
			c.flush()
		case <-rotateTicker.C:
			rotate(c.dataDir, c.retainDays)
		}
	}
}

// flush snapshots pending writes under lock, then writes to disk outside lock.
func (c *Collector) flush() {
	c.mu.Lock()
	// Snapshot and clear write buffers
	pending := make(map[string][]Sample)
	for path, def := range c.metrics {
		if len(def.writeBuf) > 0 {
			pending[path] = def.writeBuf
			def.writeBuf = nil
		}
	}
	c.mu.Unlock()

	// Write outside lock — no contention with collection
	for path, samples := range pending {
		appendToFile(c.dataDir, path, samples)
	}
}

func filterSamples(samples []Sample, since int64) []Sample {
	var out []Sample
	for _, s := range samples {
		if s.Timestamp >= since {
			out = append(out, s)
		}
	}
	return out
}
