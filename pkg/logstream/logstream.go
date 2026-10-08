package logstream

import (
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single log entry
type LogEntry struct {
	ID      int64     `json:"id"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`  // info, warning, error
	Source  string    `json:"source"` // MQTT, HomeKit, BROADCAST, etc.
	Message string    `json:"message"`
}

// LogStream captures logs and broadcasts to SSE clients
type LogStream struct {
	buffer    []LogEntry
	bufferMax int
	bufferIdx int
	count     int // actual number of entries in buffer
	nextID    int64
	mu        sync.RWMutex

	original io.Writer

	clients   map[chan LogEntry]struct{}
	clientsMu sync.RWMutex

	sources   map[string]struct{}
	sourcesMu sync.RWMutex
}

// Regex to parse log lines and extract source
var sourceRegex = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} \[([^\]]+)\]`)
var timestampRegex = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\s*`)

// NewLogStream creates a new log stream
func NewLogStream(original io.Writer, bufferSize int) *LogStream {
	return &LogStream{
		buffer:    make([]LogEntry, bufferSize),
		bufferMax: bufferSize,
		bufferIdx: 0,
		count:     0,
		nextID:    1,
		original:  original,
		clients:   make(map[chan LogEntry]struct{}),
		sources:   make(map[string]struct{}),
	}
}

// Write implements io.Writer - captures log output
func (ls *LogStream) Write(p []byte) (n int, err error) {
	// Write to original output first
	n, err = ls.original.Write(p)

	// Parse and broadcast
	line := strings.TrimSpace(string(p))
	if line == "" {
		return n, err
	}

	entry := ls.parseLine(line)

	// Add to ring buffer
	ls.mu.Lock()
	entry.ID = ls.nextID
	ls.nextID++
	ls.buffer[ls.bufferIdx] = entry
	ls.bufferIdx = (ls.bufferIdx + 1) % ls.bufferMax
	if ls.count < ls.bufferMax {
		ls.count++
	}
	ls.mu.Unlock()

	// Track source
	if entry.Source != "" {
		ls.sourcesMu.Lock()
		ls.sources[entry.Source] = struct{}{}
		ls.sourcesMu.Unlock()
	}

	// Broadcast to SSE clients (non-blocking)
	ls.clientsMu.RLock()
	for ch := range ls.clients {
		select {
		case ch <- entry:
		default:
			// Channel full, skip this client
		}
	}
	ls.clientsMu.RUnlock()

	return n, err
}

// parseLine parses a log line into a LogEntry
func (ls *LogStream) parseLine(line string) LogEntry {
	entry := LogEntry{
		Time:    time.Now(),
		Level:   "info",
		Message: line,
	}

	// Try to extract source from [SOURCE] prefix
	matches := sourceRegex.FindStringSubmatch(line)
	if len(matches) > 1 {
		entry.Source = matches[1]
		// Remove timestamp and source from message
		entry.Message = sourceRegex.ReplaceAllString(line, "")
		entry.Message = strings.TrimSpace(entry.Message)
	} else {
		// Remove just the timestamp
		entry.Message = timestampRegex.ReplaceAllString(line, "")
		entry.Message = strings.TrimSpace(entry.Message)
	}

	// Determine level heuristically
	lineLower := strings.ToLower(line)
	if strings.Contains(lineLower, "error") || strings.Contains(lineLower, "failed") ||
		strings.Contains(lineLower, "fatal") {
		entry.Level = "error"
	} else if strings.Contains(lineLower, "warning") || strings.Contains(lineLower, "warn") {
		entry.Level = "warning"
	}

	return entry
}

// GetHistory returns buffered log entries (oldest first)
func (ls *LogStream) GetHistory() []LogEntry {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	if ls.count == 0 {
		return nil
	}

	result := make([]LogEntry, ls.count)

	if ls.count < ls.bufferMax {
		// Buffer not full yet, entries start at 0
		copy(result, ls.buffer[:ls.count])
	} else {
		// Buffer is full, wrap around
		// Oldest entries start at bufferIdx
		firstPart := ls.buffer[ls.bufferIdx:]
		secondPart := ls.buffer[:ls.bufferIdx]
		copy(result, firstPart)
		copy(result[len(firstPart):], secondPart)
	}

	return result
}

// Subscribe returns a channel for receiving new logs
func (ls *LogStream) Subscribe() chan LogEntry {
	ch := make(chan LogEntry, 100) // Buffer to prevent blocking

	ls.clientsMu.Lock()
	ls.clients[ch] = struct{}{}
	ls.clientsMu.Unlock()

	return ch
}

// Unsubscribe removes a client channel
func (ls *LogStream) Unsubscribe(ch chan LogEntry) {
	ls.clientsMu.Lock()
	delete(ls.clients, ch)
	ls.clientsMu.Unlock()
	close(ch)
}

// GetSources returns list of unique sources seen
func (ls *LogStream) GetSources() []string {
	ls.sourcesMu.RLock()
	defer ls.sourcesMu.RUnlock()

	result := make([]string, 0, len(ls.sources))
	for source := range ls.sources {
		result = append(result, source)
	}
	return result
}

// ClientCount returns the number of connected SSE clients
func (ls *LogStream) ClientCount() int {
	ls.clientsMu.RLock()
	defer ls.clientsMu.RUnlock()
	return len(ls.clients)
}

// EntryCount returns the number of entries in the buffer
func (ls *LogStream) EntryCount() int {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.count
}
