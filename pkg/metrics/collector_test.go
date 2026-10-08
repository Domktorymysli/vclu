package metrics

import (
	"testing"
	"time"
)

func TestRegisterAndList(t *testing.T) {
	c := NewCollector(t.TempDir())

	c.Register("temp:outside", "°C", 10)
	c.Register("humidity", "%", 60)

	list := c.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(list))
	}
}

func TestRegisterIdempotent(t *testing.T) {
	c := NewCollector(t.TempDir())

	c.Register("temp", "°C", 10)
	c.Ingest([]CollectResult{{Path: "temp", Value: 21.5}})

	// Re-register with different unit — keeps samples
	c.Register("temp", "°F", 30)

	list := c.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(list))
	}
	if list[0].Unit != "°F" {
		t.Errorf("expected unit °F, got %s", list[0].Unit)
	}
	if list[0].Samples != 1 {
		t.Errorf("expected 1 sample after re-register, got %d", list[0].Samples)
	}
}

func TestMaxMetrics(t *testing.T) {
	c := NewCollector(t.TempDir(), WithMaxMetrics(2))

	c.Register("a", "", 10)
	c.Register("b", "", 10)
	c.Register("c", "", 10) // should be silently dropped

	if len(c.List()) != 2 {
		t.Fatalf("expected 2 metrics (max), got %d", len(c.List()))
	}
}

func TestDueMetrics(t *testing.T) {
	c := NewCollector(t.TempDir())

	c.Register("fast", "", 1)
	c.Register("slow", "", 3600)

	// Both should be due initially (lastPoll is zero)
	due := c.DueMetrics()
	if len(due) != 2 {
		t.Fatalf("expected 2 due metrics initially, got %d", len(due))
	}

	// Ingest fast metric
	c.Ingest([]CollectResult{{Path: "fast", Value: 1.0}})

	// Fast should not be due yet (just ingested)
	due = c.DueMetrics()
	found := false
	for _, p := range due {
		if p == "fast" {
			found = true
		}
	}
	if found {
		t.Error("fast metric should not be due immediately after ingest")
	}
}

func TestIngestAndQuery(t *testing.T) {
	c := NewCollector(t.TempDir(), WithRingSize(100))

	c.Register("temp", "°C", 10)

	c.Ingest([]CollectResult{{Path: "temp", Value: 20.0}})
	c.Ingest([]CollectResult{{Path: "temp", Value: 21.0}})
	c.Ingest([]CollectResult{{Path: "temp", Value: 22.0}})

	since := time.Now().Add(-1 * time.Hour).Unix()
	samples := c.Query("temp", since)
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(samples))
	}
	if samples[0].Value != 20.0 {
		t.Errorf("expected first value 20.0, got %f", samples[0].Value)
	}
}

func TestQueryUnknownMetric(t *testing.T) {
	c := NewCollector(t.TempDir())

	samples := c.Query("nonexistent", 0)
	if samples != nil {
		t.Errorf("expected nil for unknown metric, got %v", samples)
	}
}

func TestRingOverwrite(t *testing.T) {
	c := NewCollector(t.TempDir(), WithRingSize(3))
	c.Register("x", "", 1)

	for i := 0; i < 5; i++ {
		c.Ingest([]CollectResult{{Path: "x", Value: float64(i)}})
	}

	samples := c.Query("x", 0)
	if len(samples) != 3 {
		t.Fatalf("expected 3 samples (ring size), got %d", len(samples))
	}
	// Should have values 2, 3, 4 (oldest overwritten)
	if samples[0].Value != 2.0 {
		t.Errorf("expected oldest value 2.0, got %f", samples[0].Value)
	}
}

func TestIngestUnknownPath(t *testing.T) {
	c := NewCollector(t.TempDir())

	// Should not panic
	c.Ingest([]CollectResult{{Path: "nonexistent", Value: 1.0}})
}

func TestIntervalMinimum(t *testing.T) {
	c := NewCollector(t.TempDir())

	c.Register("test", "", 0) // 0 should become 60

	c.mu.RLock()
	def := c.metrics["test"]
	c.mu.RUnlock()

	if def.Interval != 60 {
		t.Errorf("expected interval 60 for input 0, got %d", def.Interval)
	}
}
