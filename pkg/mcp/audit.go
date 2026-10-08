package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	auditMaxEntries = 200
	auditMaxLines   = 1000
	auditFileName   = "audit.jsonl"
	auditRotateBase = "audit"
	auditRotateExt  = ".jsonl"
)

// AuditLog is a thread-safe ring buffer for Layer 2 audit entries,
// with optional JSONL file persistence and rotation.
type AuditLog struct {
	mu        sync.Mutex
	entries   []AuditEntry
	dir       string // "" = in-memory only
	lineCount int    // lines in active audit.jsonl
}

// NewAuditLog creates a new audit log.
// If dir is non-empty, entries are persisted to JSONL files with rotation.
// If dir is empty, the log is in-memory only (for tests).
func NewAuditLog(dir string) *AuditLog {
	a := &AuditLog{dir: dir}

	if dir != "" {
		a.loadFromFiles()
	}

	return a
}

// Add records an audit entry.
func (a *AuditLog) Add(entry AuditEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if entry.Time.IsZero() {
		entry.Time = time.Now()
	}

	// In-memory ring buffer
	a.entries = append(a.entries, entry)
	if len(a.entries) > auditMaxEntries {
		a.entries = a.entries[len(a.entries)-auditMaxEntries:]
	}

	// Persist to file
	if a.dir != "" {
		a.appendToFile(entry)
		a.lineCount++
		if a.lineCount > auditMaxLines {
			a.rotate()
		}
	}
}

// GetAll returns all in-memory entries (newest first).
func (a *AuditLog) GetAll() []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]AuditEntry, len(a.entries))
	for i, e := range a.entries {
		result[len(a.entries)-1-i] = e
	}
	return result
}

// GetRecent returns the N most recent in-memory entries (newest first).
func (a *AuditLog) GetRecent(n int) []AuditEntry {
	all := a.GetAll()
	if n >= len(all) {
		return all
	}
	return all[:n]
}

// CountRecentErrors returns the number of errors in the last duration.
func (a *AuditLog) CountRecentErrors(window time.Duration) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	cutoff := time.Now().Add(-window)
	count := 0
	for i := len(a.entries) - 1; i >= 0; i-- {
		if a.entries[i].Time.Before(cutoff) {
			break
		}
		if !a.entries[i].Success {
			count++
		}
	}
	return count
}

// ReadAll reads entries from persisted JSONL files (newest first).
// Returns up to limit entries. Falls back to in-memory if no files exist.
func (a *AuditLog) ReadAll(limit int) []AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.dir == "" {
		// In-memory fallback
		result := make([]AuditEntry, len(a.entries))
		for i, e := range a.entries {
			result[len(a.entries)-1-i] = e
		}
		if limit > 0 && len(result) > limit {
			return result[:limit]
		}
		return result
	}

	var all []AuditEntry

	// Read files from newest to oldest: audit.jsonl, audit.1.jsonl, audit.2.jsonl
	files := []string{
		filepath.Join(a.dir, auditFileName),
		filepath.Join(a.dir, fmt.Sprintf("%s.1%s", auditRotateBase, auditRotateExt)),
		filepath.Join(a.dir, fmt.Sprintf("%s.2%s", auditRotateBase, auditRotateExt)),
	}

	for _, fpath := range files {
		entries := a.readJSONLFile(fpath)
		all = append(all, entries...)
		if limit > 0 && len(all) >= limit {
			break
		}
	}

	// Sort newest first (entries within each file are chronological)
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}

	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}

	return all
}

// --- private methods ---

// loadFromFiles loads existing entries from JSONL files into the in-memory buffer
// and counts lines in the active file.
func (a *AuditLog) loadFromFiles() {
	activeFile := filepath.Join(a.dir, auditFileName)

	// Count lines in active file
	data, err := os.ReadFile(activeFile)
	if err == nil {
		a.lineCount = bytes.Count(data, []byte("\n"))
	}

	// Load recent entries into in-memory buffer for GetRecent compatibility
	files := []string{
		filepath.Join(a.dir, fmt.Sprintf("%s.2%s", auditRotateBase, auditRotateExt)),
		filepath.Join(a.dir, fmt.Sprintf("%s.1%s", auditRotateBase, auditRotateExt)),
		activeFile,
	}

	var loaded []AuditEntry
	for _, fpath := range files {
		entries := a.readJSONLFile(fpath)
		loaded = append(loaded, entries...)
	}

	// Keep only last auditMaxEntries
	if len(loaded) > auditMaxEntries {
		loaded = loaded[len(loaded)-auditMaxEntries:]
	}
	a.entries = loaded

	if len(loaded) > 0 {
		log.Printf("[MCP] Loaded %d audit entries from disk (active file: %d lines)", len(loaded), a.lineCount)
	}
}

// readJSONLFile reads and parses a JSONL file, returning entries in chronological order.
func (a *AuditLog) readJSONLFile(path string) []AuditEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []AuditEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}
	return entries
}

// appendToFile appends a single entry as JSONL to the active file.
func (a *AuditLog) appendToFile(entry AuditEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		log.Printf("[MCP] Failed to marshal audit entry: %v", err)
		return
	}
	data = append(data, '\n')

	fpath := filepath.Join(a.dir, auditFileName)
	f, err := os.OpenFile(fpath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[MCP] Failed to open audit file: %v", err)
		return
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		log.Printf("[MCP] Failed to write audit entry: %v", err)
	}
}

// rotate performs file rotation: .2 → delete, .1 → .2, active → .1, reset lineCount.
func (a *AuditLog) rotate() {
	file2 := filepath.Join(a.dir, fmt.Sprintf("%s.2%s", auditRotateBase, auditRotateExt))
	file1 := filepath.Join(a.dir, fmt.Sprintf("%s.1%s", auditRotateBase, auditRotateExt))
	active := filepath.Join(a.dir, auditFileName)

	os.Remove(file2)
	os.Rename(file1, file2)
	os.Rename(active, file1)

	a.lineCount = 0
	log.Printf("[MCP] Audit log rotated")
}
