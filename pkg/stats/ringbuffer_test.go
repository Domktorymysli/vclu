package stats

import (
	"testing"
	"time"
)

func TestRingBuffer_Basic(t *testing.T) {
	rb := NewRingBuffer[int](5)

	// Initially empty
	if rb.Len() != 0 {
		t.Errorf("Expected len 0, got %d", rb.Len())
	}

	// Add items
	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	if rb.Len() != 3 {
		t.Errorf("Expected len 3, got %d", rb.Len())
	}

	// Get all
	all := rb.GetAll()
	if len(all) != 3 || all[0] != 1 || all[1] != 2 || all[2] != 3 {
		t.Errorf("GetAll returned unexpected: %v", all)
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer[int](3)

	// Fill buffer
	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	// Overflow - oldest should be discarded
	rb.Push(4)
	rb.Push(5)

	if rb.Len() != 3 {
		t.Errorf("Expected len 3 (capacity), got %d", rb.Len())
	}

	all := rb.GetAll()
	// Should have 3, 4, 5 (oldest first)
	if len(all) != 3 || all[0] != 3 || all[1] != 4 || all[2] != 5 {
		t.Errorf("Expected [3,4,5], got %v", all)
	}
}

func TestRingBuffer_GetLast(t *testing.T) {
	rb := NewRingBuffer[int](5)

	rb.Push(1)
	rb.Push(2)
	rb.Push(3)
	rb.Push(4)
	rb.Push(5)

	// Get last 3
	last := rb.GetLast(3)
	if len(last) != 3 || last[0] != 3 || last[1] != 4 || last[2] != 5 {
		t.Errorf("Expected [3,4,5], got %v", last)
	}

	// Get last 10 (more than available)
	last = rb.GetLast(10)
	if len(last) != 5 {
		t.Errorf("Expected 5 items, got %d", len(last))
	}
}

func TestRingBuffer_Clear(t *testing.T) {
	rb := NewRingBuffer[int](5)
	rb.Push(1)
	rb.Push(2)
	rb.Push(3)

	rb.Clear()

	if rb.Len() != 0 {
		t.Errorf("Expected len 0 after clear, got %d", rb.Len())
	}
}

func TestTimeSeries_Record(t *testing.T) {
	ts := NewTimeSeries("test", "units", 10, time.Second)

	ts.Record(1.0)
	ts.Record(2.0)
	ts.Record(3.0)

	if ts.Len() != 3 {
		t.Errorf("Expected 3 data points, got %d", ts.Len())
	}

	latest, ok := ts.Latest()
	if !ok || latest.Value != 3.0 {
		t.Errorf("Expected latest 3.0, got %v", latest)
	}
}

func TestTimeSeries_Stats(t *testing.T) {
	ts := NewTimeSeries("test", "units", 10, time.Second)

	ts.Record(10.0)
	ts.Record(20.0)
	ts.Record(30.0)

	stats := ts.Stats()

	if stats.Min != 10.0 {
		t.Errorf("Expected min 10, got %f", stats.Min)
	}
	if stats.Max != 30.0 {
		t.Errorf("Expected max 30, got %f", stats.Max)
	}
	if stats.Avg != 20.0 {
		t.Errorf("Expected avg 20, got %f", stats.Avg)
	}
	if stats.Count != 3 {
		t.Errorf("Expected count 3, got %d", stats.Count)
	}
}

func TestTimeSeries_GetSince(t *testing.T) {
	ts := NewTimeSeries("test", "units", 10, time.Second)

	t1 := time.Now()
	ts.RecordAt(t1, 1.0)

	time.Sleep(10 * time.Millisecond)
	t2 := time.Now()
	ts.RecordAt(t2, 2.0)

	time.Sleep(10 * time.Millisecond)
	t3 := time.Now()
	ts.RecordAt(t3, 3.0)

	// Get since t2
	since := ts.GetSince(t2)
	if len(since) != 2 {
		t.Errorf("Expected 2 data points since t2, got %d", len(since))
	}
}

func TestMetricsCollector_Counters(t *testing.T) {
	mc := NewMetricsCollector()

	// Register counter
	counter := mc.RegisterCounter("test_counter")

	counter.Inc()
	counter.Inc()
	counter.Add(5)

	if counter.Value() != 7 {
		t.Errorf("Expected counter 7, got %d", counter.Value())
	}

	// Access via collector
	mc.Inc("test_counter")
	if counter.Value() != 8 {
		t.Errorf("Expected counter 8, got %d", counter.Value())
	}
}

func TestMetricsCollector_CustomTimeSeries(t *testing.T) {
	mc := NewMetricsCollector()

	// Register custom time series
	ts := mc.RegisterTimeSeries("lua_executions", "count")

	ts.Record(100)
	ts.Record(105)
	ts.Record(110)

	// Retrieve it
	ts2 := mc.GetTimeSeries("lua_executions")
	if ts2 == nil {
		t.Fatal("Failed to get custom time series")
	}

	stats := ts2.Stats()
	if stats.Count != 3 {
		t.Errorf("Expected 3 data points, got %d", stats.Count)
	}
}

func TestMetricsCollector_Snapshot(t *testing.T) {
	mc := NewMetricsCollectorWithConfig(10*time.Millisecond, 100)
	mc.Start()
	defer mc.Stop()

	// Wait for a few samples
	time.Sleep(50 * time.Millisecond)

	snapshot := mc.Snapshot()

	// Should have uptime
	if snapshot.Uptime < 40*time.Millisecond {
		t.Errorf("Expected uptime >= 40ms, got %v", snapshot.Uptime)
	}

	// Should have memory data
	if snapshot.MemoryUsedMB <= 0 {
		t.Errorf("Expected positive memory usage, got %f", snapshot.MemoryUsedMB)
	}

	// Should have goroutines
	if snapshot.Goroutines <= 0 {
		t.Errorf("Expected positive goroutine count, got %d", snapshot.Goroutines)
	}

	t.Logf("Snapshot: uptime=%v, memory=%.2fMB, goroutines=%d",
		snapshot.Uptime, snapshot.MemoryUsedMB, snapshot.Goroutines)
}

func TestMetricsCollector_GetAllTimeSeries(t *testing.T) {
	mc := NewMetricsCollectorWithConfig(10*time.Millisecond, 100)
	mc.Start()
	defer mc.Stop()

	// Register custom
	ts := mc.RegisterTimeSeries("custom_metric", "ops")
	ts.Record(1)
	ts.Record(2)

	// Wait for samples
	time.Sleep(30 * time.Millisecond)

	allTS := mc.GetAllTimeSeries()

	// Should have built-in series
	if _, ok := allTS["memory_used"]; !ok {
		t.Error("Missing memory_used time series")
	}
	if _, ok := allTS["goroutines"]; !ok {
		t.Error("Missing goroutines time series")
	}

	// Should have custom
	if custom, ok := allTS["custom_metric"]; !ok {
		t.Error("Missing custom_metric time series")
	} else if len(custom) != 2 {
		t.Errorf("Expected 2 custom data points, got %d", len(custom))
	}
}

func TestUptimeString(t *testing.T) {
	tests := []struct {
		uptime   time.Duration
		expected string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "05m 00s"},
		{2*time.Hour + 15*time.Minute, "02h 15m"},
		{25*time.Hour + 30*time.Minute, "01d 01h 30m"},
	}

	for _, tc := range tests {
		ms := MetricsSnapshot{Uptime: tc.uptime}
		result := ms.UptimeString()
		if result != tc.expected {
			t.Errorf("UptimeString(%v): expected %q, got %q", tc.uptime, tc.expected, result)
		}
	}
}
