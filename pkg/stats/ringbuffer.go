package stats

import (
	"encoding/json"
	"sync"
	"time"
)

// RingBuffer is a generic circular buffer with fixed capacity
type RingBuffer[T any] struct {
	mu       sync.RWMutex
	data     []T
	head     int // next write position
	count    int // current number of elements
	capacity int
}

// NewRingBuffer creates a new ring buffer with given capacity
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	return &RingBuffer[T]{
		data:     make([]T, capacity),
		capacity: capacity,
	}
}

// Push adds an element to the buffer, overwriting oldest if full
func (rb *RingBuffer[T]) Push(item T) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.data[rb.head] = item
	rb.head = (rb.head + 1) % rb.capacity

	if rb.count < rb.capacity {
		rb.count++
	}
}

// GetAll returns all elements in order (oldest first)
func (rb *RingBuffer[T]) GetAll() []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	result := make([]T, rb.count)
	if rb.count == 0 {
		return result
	}

	// Start from oldest element
	start := 0
	if rb.count == rb.capacity {
		start = rb.head // head points to oldest when full
	}

	for i := 0; i < rb.count; i++ {
		idx := (start + i) % rb.capacity
		result[i] = rb.data[idx]
	}

	return result
}

// GetLast returns the last n elements (most recent)
func (rb *RingBuffer[T]) GetLast(n int) []T {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if n > rb.count {
		n = rb.count
	}
	if n == 0 {
		return []T{}
	}

	result := make([]T, n)

	// Start from (head - n) position
	start := (rb.head - n + rb.capacity) % rb.capacity

	for i := 0; i < n; i++ {
		idx := (start + i) % rb.capacity
		result[i] = rb.data[idx]
	}

	return result
}

// Len returns current number of elements
func (rb *RingBuffer[T]) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.count
}

// Cap returns buffer capacity
func (rb *RingBuffer[T]) Cap() int {
	return rb.capacity
}

// Clear removes all elements
func (rb *RingBuffer[T]) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.count = 0
}

// =============================================================================
// TimeSeries - ring buffer with timestamps
// =============================================================================

// DataPoint represents a single time series data point
type DataPoint struct {
	Timestamp time.Time `json:"t"`
	Value     float64   `json:"v"`
}

// TimeSeries is a ring buffer specifically for time-series data
type TimeSeries struct {
	buffer   *RingBuffer[DataPoint]
	name     string
	unit     string
	interval time.Duration
}

// NewTimeSeries creates a new time series with given capacity
// interval is the expected sampling interval (for display purposes)
func NewTimeSeries(name, unit string, capacity int, interval time.Duration) *TimeSeries {
	return &TimeSeries{
		buffer:   NewRingBuffer[DataPoint](capacity),
		name:     name,
		unit:     unit,
		interval: interval,
	}
}

// Record adds a new data point with current timestamp
func (ts *TimeSeries) Record(value float64) {
	ts.buffer.Push(DataPoint{
		Timestamp: time.Now(),
		Value:     value,
	})
}

// RecordAt adds a new data point with specific timestamp
func (ts *TimeSeries) RecordAt(timestamp time.Time, value float64) {
	ts.buffer.Push(DataPoint{
		Timestamp: timestamp,
		Value:     value,
	})
}

// GetAll returns all data points (oldest first)
func (ts *TimeSeries) GetAll() []DataPoint {
	return ts.buffer.GetAll()
}

// GetLast returns the last n data points
func (ts *TimeSeries) GetLast(n int) []DataPoint {
	return ts.buffer.GetLast(n)
}

// GetSince returns all data points since given time
func (ts *TimeSeries) GetSince(since time.Time) []DataPoint {
	all := ts.buffer.GetAll()
	result := make([]DataPoint, 0, len(all))

	for _, dp := range all {
		if dp.Timestamp.After(since) || dp.Timestamp.Equal(since) {
			result = append(result, dp)
		}
	}

	return result
}

// Latest returns the most recent data point, or zero value if empty
func (ts *TimeSeries) Latest() (DataPoint, bool) {
	last := ts.buffer.GetLast(1)
	if len(last) == 0 {
		return DataPoint{}, false
	}
	return last[0], true
}

// Stats returns basic statistics
func (ts *TimeSeries) Stats() TimeSeriesStats {
	all := ts.buffer.GetAll()
	if len(all) == 0 {
		return TimeSeriesStats{Name: ts.name, Unit: ts.unit}
	}

	var sum, min, max float64
	min = all[0].Value
	max = all[0].Value

	for _, dp := range all {
		sum += dp.Value
		if dp.Value < min {
			min = dp.Value
		}
		if dp.Value > max {
			max = dp.Value
		}
	}

	return TimeSeriesStats{
		Name:    ts.name,
		Unit:    ts.unit,
		Count:   len(all),
		Min:     min,
		Max:     max,
		Avg:     sum / float64(len(all)),
		Latest:  all[len(all)-1].Value,
		FirstAt: all[0].Timestamp,
		LastAt:  all[len(all)-1].Timestamp,
	}
}

// Len returns number of data points
func (ts *TimeSeries) Len() int {
	return ts.buffer.Len()
}

// Name returns series name
func (ts *TimeSeries) Name() string {
	return ts.name
}

// MarshalJSON implements json.Marshaler for API responses
func (ts *TimeSeries) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name     string      `json:"name"`
		Unit     string      `json:"unit"`
		Interval string      `json:"interval"`
		Count    int         `json:"count"`
		Data     []DataPoint `json:"data"`
	}{
		Name:     ts.name,
		Unit:     ts.unit,
		Interval: ts.interval.String(),
		Count:    ts.buffer.Len(),
		Data:     ts.buffer.GetAll(),
	})
}

// TimeSeriesStats holds computed statistics for a time series
type TimeSeriesStats struct {
	Name    string    `json:"name"`
	Unit    string    `json:"unit"`
	Count   int       `json:"count"`
	Min     float64   `json:"min"`
	Max     float64   `json:"max"`
	Avg     float64   `json:"avg"`
	Latest  float64   `json:"latest"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}
