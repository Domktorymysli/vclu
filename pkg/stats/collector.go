package stats

import (
	"runtime"
	"sync"
	"time"
)

// Default configuration
const (
	DefaultSampleInterval = 1 * time.Minute
	DefaultCapacity       = 360 // 6 hours at 1 min intervals
)

// MetricsCollector collects and stores system metrics over time
type MetricsCollector struct {
	mu sync.RWMutex

	// Configuration
	sampleInterval time.Duration
	startTime      time.Time

	// Time series data
	MemoryUsed  *TimeSeries // MB
	Goroutines  *TimeSeries // count
	HeapObjects *TimeSeries // count
	GCPauseMs   *TimeSeries // ms

	// Custom time series (for Lua, protocol, etc.)
	custom map[string]*TimeSeries

	// Counters (cumulative, not time series)
	counters map[string]*Counter

	// Control
	stopChan chan struct{}
	running  bool
}

// Counter is a simple atomic counter
type Counter struct {
	mu    sync.Mutex
	value int64
}

func (c *Counter) Inc() {
	c.mu.Lock()
	c.value++
	c.mu.Unlock()
}

func (c *Counter) Add(n int64) {
	c.mu.Lock()
	c.value += n
	c.mu.Unlock()
}

func (c *Counter) Value() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *Counter) Reset() {
	c.mu.Lock()
	c.value = 0
	c.mu.Unlock()
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector() *MetricsCollector {
	return NewMetricsCollectorWithConfig(DefaultSampleInterval, DefaultCapacity)
}

// NewMetricsCollectorWithConfig creates a collector with custom configuration
func NewMetricsCollectorWithConfig(sampleInterval time.Duration, capacity int) *MetricsCollector {
	return &MetricsCollector{
		sampleInterval: sampleInterval,
		startTime:      time.Now(),

		MemoryUsed:  NewTimeSeries("memory_used", "MB", capacity, sampleInterval),
		Goroutines:  NewTimeSeries("goroutines", "count", capacity, sampleInterval),
		HeapObjects: NewTimeSeries("heap_objects", "count", capacity, sampleInterval),
		GCPauseMs:   NewTimeSeries("gc_pause", "ms", capacity, sampleInterval),

		custom:   make(map[string]*TimeSeries),
		counters: make(map[string]*Counter),
		stopChan: make(chan struct{}),
	}
}

// Start begins collecting metrics at the configured interval
func (mc *MetricsCollector) Start() {
	mc.mu.Lock()
	if mc.running {
		mc.mu.Unlock()
		return
	}
	mc.running = true
	mc.mu.Unlock()

	// Collect initial sample
	mc.collectSample()

	go func() {
		ticker := time.NewTicker(mc.sampleInterval)
		defer ticker.Stop()

		for {
			select {
			case <-mc.stopChan:
				return
			case <-ticker.C:
				mc.collectSample()
			}
		}
	}()
}

// Stop stops the collector
func (mc *MetricsCollector) Stop() {
	mc.mu.Lock()
	if !mc.running {
		mc.mu.Unlock()
		return
	}
	mc.running = false
	mc.mu.Unlock()

	close(mc.stopChan)
}

// collectSample gathers current metrics
func (mc *MetricsCollector) collectSample() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	mc.MemoryUsed.Record(float64(memStats.Alloc) / 1024 / 1024)
	mc.Goroutines.Record(float64(runtime.NumGoroutine()))
	mc.HeapObjects.Record(float64(memStats.HeapObjects))

	// GC pause - use last pause if available
	if memStats.NumGC > 0 {
		// PauseNs is a circular buffer of recent GC pause times
		lastPauseIdx := (memStats.NumGC + 255) % 256
		lastPauseNs := memStats.PauseNs[lastPauseIdx]
		mc.GCPauseMs.Record(float64(lastPauseNs) / 1e6)
	}
}

// RegisterTimeSeries registers a custom time series
func (mc *MetricsCollector) RegisterTimeSeries(name, unit string) *TimeSeries {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if ts, exists := mc.custom[name]; exists {
		return ts
	}

	ts := NewTimeSeries(name, unit, DefaultCapacity, mc.sampleInterval)
	mc.custom[name] = ts
	return ts
}

// GetTimeSeries returns a custom time series by name
func (mc *MetricsCollector) GetTimeSeries(name string) *TimeSeries {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.custom[name]
}

// RegisterCounter registers a named counter
func (mc *MetricsCollector) RegisterCounter(name string) *Counter {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	if c, exists := mc.counters[name]; exists {
		return c
	}

	c := &Counter{}
	mc.counters[name] = c
	return c
}

// GetCounter returns a counter by name
func (mc *MetricsCollector) GetCounter(name string) *Counter {
	mc.mu.RLock()
	defer mc.mu.RUnlock()
	return mc.counters[name]
}

// Counter convenience method - increments named counter
func (mc *MetricsCollector) Inc(name string) {
	mc.mu.RLock()
	c := mc.counters[name]
	mc.mu.RUnlock()

	if c != nil {
		c.Inc()
	}
}

// Uptime returns how long the collector has been running
func (mc *MetricsCollector) Uptime() time.Duration {
	return time.Since(mc.startTime)
}

// Snapshot returns current metrics snapshot
func (mc *MetricsCollector) Snapshot() MetricsSnapshot {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Collect counter values
	counterValues := make(map[string]int64)
	for name, c := range mc.counters {
		counterValues[name] = c.Value()
	}

	// Collect custom time series stats
	customStats := make(map[string]TimeSeriesStats)
	for name, ts := range mc.custom {
		customStats[name] = ts.Stats()
	}

	return MetricsSnapshot{
		Timestamp: time.Now(),
		Uptime:    mc.Uptime(),

		// Current values
		MemoryUsedMB:  float64(memStats.Alloc) / 1024 / 1024,
		MemoryTotalMB: float64(memStats.Sys) / 1024 / 1024,
		Goroutines:    runtime.NumGoroutine(),
		HeapObjects:   int(memStats.HeapObjects),
		GCRuns:        int(memStats.NumGC),

		// Time series stats
		MemoryStats:     mc.MemoryUsed.Stats(),
		GoroutineStats:  mc.Goroutines.Stats(),
		HeapObjectStats: mc.HeapObjects.Stats(),
		GCPauseStats:    mc.GCPauseMs.Stats(),

		// Custom
		Counters: counterValues,
		Custom:   customStats,
	}
}

// GetAllTimeSeries returns all time series data (for charts)
func (mc *MetricsCollector) GetAllTimeSeries() map[string][]DataPoint {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := map[string][]DataPoint{
		"memory_used":  mc.MemoryUsed.GetAll(),
		"goroutines":   mc.Goroutines.GetAll(),
		"heap_objects": mc.HeapObjects.GetAll(),
		"gc_pause":     mc.GCPauseMs.GetAll(),
	}

	for name, ts := range mc.custom {
		result[name] = ts.GetAll()
	}

	return result
}

// GetTimeSeriesSince returns data points since given time (for incremental updates)
func (mc *MetricsCollector) GetTimeSeriesSince(since time.Time) map[string][]DataPoint {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	result := map[string][]DataPoint{
		"memory_used":  mc.MemoryUsed.GetSince(since),
		"goroutines":   mc.Goroutines.GetSince(since),
		"heap_objects": mc.HeapObjects.GetSince(since),
		"gc_pause":     mc.GCPauseMs.GetSince(since),
	}

	for name, ts := range mc.custom {
		result[name] = ts.GetSince(since)
	}

	return result
}

// MetricsSnapshot is a point-in-time view of all metrics
type MetricsSnapshot struct {
	Timestamp time.Time     `json:"timestamp"`
	Uptime    time.Duration `json:"uptime"`

	// Current values
	MemoryUsedMB  float64 `json:"memory_used_mb"`
	MemoryTotalMB float64 `json:"memory_total_mb"`
	Goroutines    int     `json:"goroutines"`
	HeapObjects   int     `json:"heap_objects"`
	GCRuns        int     `json:"gc_runs"`

	// Time series statistics
	MemoryStats     TimeSeriesStats `json:"memory_stats"`
	GoroutineStats  TimeSeriesStats `json:"goroutine_stats"`
	HeapObjectStats TimeSeriesStats `json:"heap_object_stats"`
	GCPauseStats    TimeSeriesStats `json:"gc_pause_stats"`

	// Counters
	Counters map[string]int64 `json:"counters"`

	// Custom time series stats
	Custom map[string]TimeSeriesStats `json:"custom,omitempty"`
}

// UptimeString returns uptime as human-readable string
func (ms MetricsSnapshot) UptimeString() string {
	d := ms.Uptime
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return formatDuration(days, "d", hours, "h", minutes, "m")
	}
	if hours > 0 {
		return formatDuration(hours, "h", minutes, "m", int(d.Seconds())%60, "s")
	}
	return formatDuration(minutes, "m", int(d.Seconds())%60, "s", 0, "")
}

func formatDuration(v1 int, u1 string, v2 int, u2 string, v3 int, u3 string) string {
	s := ""
	if v1 > 0 {
		s += string(rune('0'+v1/10)) + string(rune('0'+v1%10)) + u1
	}
	if v2 > 0 || s != "" {
		if s != "" {
			s += " "
		}
		s += string(rune('0'+v2/10)) + string(rune('0'+v2%10)) + u2
	}
	if v3 > 0 && u3 != "" {
		if s != "" {
			s += " "
		}
		s += string(rune('0'+v3/10)) + string(rune('0'+v3%10)) + u3
	}
	if s == "" {
		return "0s"
	}
	return s
}
