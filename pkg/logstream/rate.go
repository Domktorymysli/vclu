package logstream

import (
	"io"
	"sync"
	"time"
)

// countingWriter counts the bytes written to w per minute, over the last hour.
type countingWriter struct {
	w   io.Writer
	now func() time.Time

	mu     sync.Mutex
	bytes  [60]int64
	minute [60]int64 // unix minute of each slot
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	if n > 0 {
		m := c.now().Unix() / 60
		i := m % 60
		c.mu.Lock()
		if c.minute[i] != m {
			c.minute[i], c.bytes[i] = m, 0
		}
		c.bytes[i] += int64(n)
		c.mu.Unlock()
	}
	return n, err
}

// lastHour sums the current minute and the 59 before it.
func (c *countingWriter) lastHour() int64 {
	m := c.now().Unix() / 60
	var sum int64
	c.mu.Lock()
	for i := range c.bytes {
		if age := m - c.minute[i]; age >= 0 && age < 60 {
			sum += c.bytes[i]
		}
	}
	c.mu.Unlock()
	return sum
}

// BytesLastHour is what reached the original writer (stdout) in the last hour.
func (ls *LogStream) BytesLastHour() int64 {
	return ls.written.lastHour()
}
