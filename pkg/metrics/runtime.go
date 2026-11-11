package metrics

import (
	"context"
	"runtime"
	"time"
)

// Go runtime metrics for dashboards, sampled like the memory and goroutine charts on Stats (#196).
const (
	MetricMemoryMB   = "runtime:memory_mb"
	MetricGoroutines = "runtime:goroutines"
	runtimeInterval  = time.Minute
)

// RunRuntime samples the heap in use (MB) and the goroutines every minute until ctx ends.
func (c *Collector) RunRuntime(ctx context.Context) {
	c.RegisterGo(MetricMemoryMB, "MB", int(runtimeInterval/time.Second))
	c.RegisterGo(MetricGoroutines, "", int(runtimeInterval/time.Second))
	t := time.NewTicker(runtimeInterval)
	defer t.Stop()
	for {
		c.sampleRuntime()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Collector) sampleRuntime() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	c.Ingest([]CollectResult{
		{Path: MetricMemoryMB, Value: float64(ms.Alloc) / 1024 / 1024},
		{Path: MetricGoroutines, Value: float64(runtime.NumGoroutine())},
	})
}
