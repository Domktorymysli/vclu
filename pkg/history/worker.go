package history

import (
	"fmt"
	"log"
	"time"
)

// Start runs the background writer: pending entries go to disk every
// FlushInterval, followed by rotation. Without disk it does nothing.
func (s *Store) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disk == nil || s.stop != nil {
		return
	}
	s.stop, s.workerDone = make(chan struct{}), make(chan struct{})
	go s.worker(s.cfg.FlushInterval, s.stop, s.workerDone)
}

func (s *Store) worker(interval time.Duration, stop, done chan struct{}) {
	defer close(done)
	s.Flush()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.Flush()
		}
	}
}

// Close stops the writer and writes what is pending; call after the
// producers (Lua, web, MCP) have stopped. Waiting for a writer stuck on the
// disk and the final write are both bounded by timeout.
func (s *Store) Close(timeout time.Duration) error {
	done := make(chan error, 1)
	go func() {
		s.stopWorker()
		done <- s.Flush()
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("history: final write timed out after %v", timeout)
	}
}

func (s *Store) stopWorker() {
	s.mu.Lock()
	stop, done := s.stop, s.workerDone
	s.stop, s.workerDone = nil, nil
	s.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

// Flush writes pending entries now and rotates old files. The batch is taken
// under the lock and written outside it; a failed batch goes back in front
// of newer entries (the oldest are dropped past maxPending).
func (s *Store) Flush() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	if s.disk == nil {
		s.mu.Unlock()
		return nil
	}
	disk, batch := s.disk, s.pending
	s.pending = nil
	cfg := s.cfg
	s.mu.Unlock()

	var err error
	var skipped int
	if len(batch) > 0 {
		skipped, err = disk.appendBatch(batch)
	}
	removed, over := disk.rotate(s.now(), cfg.RetentionDays, int64(cfg.MaxDiskMB)*1024*1024)
	if skipped > 0 || removed > 0 {
		log.Printf("[history] write: %d skipped (over 64 KiB), %d old files removed", skipped, removed)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped += int64(skipped)
	s.overLimit = over
	if err != nil {
		merged := append(batch, s.pending...)
		if extra := len(merged) - maxPending; extra > 0 {
			merged = merged[extra:]
			s.lost += int64(extra)
		}
		s.pending = merged
		s.lastFlushError = err.Error()
		return err
	}
	s.lastFlushAt = s.now().UnixMilli()
	s.lastFlushError = ""
	return nil
}

// ClearMemory empties the in-memory rings; files on disk stay.
func (s *Store) ClearMemory() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rings = map[string]*ring{}
	s.order = nil
	s.total, s.tombstones = 0, 0
}

// ClearDisk deletes the history files and what is waiting to be written;
// memory stays.
func (s *Store) ClearDisk() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	disk := s.disk
	s.pending = nil
	s.mu.Unlock()
	if disk == nil {
		return nil
	}
	return disk.clear()
}

// Config returns the active configuration.
func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// Apply changes limits on the fly: smaller rings drop their oldest entries,
// a shorter retention rotates at the next write, a new interval restarts the
// writer. Turning disk on or off, or moving it, needs a restart.
func (s *Store) Apply(cfg Config) {
	cfg = normalize(cfg)
	s.mu.Lock()
	cfg.DiskEnabled, cfg.Dir = s.cfg.DiskEnabled, s.cfg.Dir
	restart := s.stop != nil && cfg.FlushInterval != s.cfg.FlushInterval
	s.cfg = cfg
	for _, r := range s.rings {
		for len(r.entries) > cfg.MaxEntriesPerObject {
			s.removeOldestOf(r)
			s.dropped++
			s.tombstones++
		}
	}
	for s.total > cfg.MaxEntriesTotal {
		s.removeGlobalOldest()
		s.dropped++
	}
	if s.tombstones > s.total {
		s.compact()
	}
	s.expire(s.now().UnixMilli())
	s.mu.Unlock()
	if restart {
		s.stopWorker()
		s.Start()
	}
}
