package metrics

import (
	"context"
	"testing"
	"time"
)

func TestRunRuntime_SamplesAtStartAndNeverDue(t *testing.T) {
	c := NewCollector(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.RunRuntime(ctx); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for len(c.Query(MetricGoroutines, 0)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	for _, m := range c.List() {
		switch m.Path {
		case MetricMemoryMB:
			if m.Latest <= 0 || m.Unit != "MB" {
				t.Errorf("memory listed as %+v", m)
			}
		case MetricGoroutines:
			if m.Latest < 1 {
				t.Errorf("goroutines listed as %+v", m)
			}
		}
	}
	if len(c.Query(MetricMemoryMB, 0)) == 0 || len(c.Query(MetricGoroutines, 0)) == 0 {
		t.Fatal("no sample at start")
	}
	if due := c.DueMetrics(); len(due) != 0 {
		t.Errorf("due %v: Go writes these itself", due)
	}
}
