// Package storage measures what vCLU keeps on disk and how fast it grows, and
// says when the data directory's file system is nearly full (#196).
package storage

import (
	"context"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Domktorymysli/vclu/pkg/metrics"
)

const (
	MiB = 1 << 20

	// RefreshInterval is how often the directories are walked; free space is read every minute.
	RefreshInterval = 15 * time.Minute
	freeInterval    = time.Minute

	// DaysToFullMax: a disk full in this many days or later is not reported.
	DaysToFullMax = 90
)

// Metrics the monitor writes after every walk.
const (
	MetricDiskFree    = "vclu:disk_free_bytes"
	MetricHistory     = "vclu:history_bytes"
	MetricLogPerHour  = "vclu:log_bytes_per_hour"
	metricIntervalSec = int(RefreshInterval / time.Second)
)

// Level says how close the file system is to full.
type Level string

const (
	LevelOK       Level = "ok"
	LevelLow      Level = "low"      // below 10 % or 500 MB free
	LevelCritical Level = "critical" // below 3 % or 100 MB free
)

// Parts of the data directory, in the order they are shown.
const (
	PartHistory = "history"
	PartMetrics = "metrics"
	PartBackups = "backups"
	PartPlugins = "plugins"
	PartOther   = "other"
)

var partOrder = []string{PartHistory, PartMetrics, PartBackups, PartPlugins, PartOther}

// Thresholds returns the free space below which the disk is low and critical:
// 10 % or 500 MB and 3 % or 100 MB, whichever is larger.
func Thresholds(total int64) (low, critical int64) {
	return max(total/10, 500*MiB), max(total*3/100, 100*MiB)
}

// LevelOf rates the free space of a file system of size total.
func LevelOf(total, free int64) Level {
	if total <= 0 {
		return LevelOK
	}
	low, critical := Thresholds(total)
	switch {
	case free < critical:
		return LevelCritical
	case free < low:
		return LevelLow
	}
	return LevelOK
}

// DaysToFull is how many whole days free lasts at perDay; ok is false without
// growth or when it is DaysToFullMax days or more.
func DaysToFull(free, perDay int64) (days int, ok bool) {
	if perDay <= 0 {
		return 0, false
	}
	d := max(free, 0) / perDay
	if d >= DaysToFullMax {
		return 0, false
	}
	return int(d), true
}

// FileSystem is the file system of the data directory.
type FileSystem struct {
	Path               string  `json:"path"`
	TotalBytes         int64   `json:"totalBytes"`
	FreeBytes          int64   `json:"freeBytes"`
	FreePercent        float64 `json:"freePercent"`
	LowBelowBytes      int64   `json:"lowBelowBytes"`
	CriticalBelowBytes int64   `json:"criticalBelowBytes"`
	Error              string  `json:"error,omitempty"`
}

// Part is one part of what vCLU keeps, with its size on disk.
type Part struct {
	Name  string `json:"name" enums:"history,metrics,backups,plugins,other"`
	Bytes int64  `json:"bytes"`
}

// Growth is how fast vCLU's data grows.
type Growth struct {
	HistoryLast24hBytes int64 `json:"historyLast24hBytes"`
	LogBytesPerHour     int64 `json:"logBytesPerHour"`
	BytesPerDay         int64 `json:"bytesPerDay"` // history of 24 h + 24 x log per hour
	DaysToFull          *int  `json:"daysToFull"`  // null: no growth, or 90 days or more
}

// Snapshot is the last measurement.
type Snapshot struct {
	Ready      bool       `json:"ready"` // false before the first walk ended
	Level      Level      `json:"level" enums:"ok,low,critical"`
	FileSystem FileSystem `json:"fileSystem"`
	Parts      []Part     `json:"parts"`
	Largest    string     `json:"largest,omitempty"`
	Growth     Growth     `json:"growth"`
	SizesAt    time.Time  `json:"sizesAt"`
	FreeAt     time.Time  `json:"freeAt"`
}

// Options wire a Monitor; every function may be nil.
type Options struct {
	DataDir string
	// UpdateBackups is a glob of the archives deploy/update.sh keeps (next to the binary)
	UpdateBackups   string
	HistoryLast24h  func() int64
	LogBytesPerHour func() int64
	// OnLevel is called after every free-space reading
	OnLevel func(Level)
	Metrics *metrics.Collector
	Now     func() time.Time
	StatFS  func(path string) (total, free int64, err error) // default: the system's
}

// Monitor keeps the last measurement; reads never touch the disk.
type Monitor struct {
	opts      Options
	mu        sync.RWMutex
	snap      Snapshot
	refreshMu sync.Mutex // one walk at a time
	walks     atomic.Int64
}

// New creates a Monitor and registers its metrics; Run measures.
func New(o Options) *Monitor {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.StatFS == nil {
		o.StatFS = statFS
	}
	if mc := o.Metrics; mc != nil {
		mc.RegisterGo(MetricDiskFree, "B", metricIntervalSec)
		mc.RegisterGo(MetricHistory, "B", metricIntervalSec)
		mc.RegisterGo(MetricLogPerHour, "B/h", metricIntervalSec)
	}
	m := &Monitor{opts: o}
	m.snap.Level = LevelOK
	m.snap.FileSystem.Path = o.DataDir
	return m
}

// Run measures now, walks the directories every RefreshInterval and reads the
// free space every minute, until ctx ends. Run it on its own goroutine.
func (m *Monitor) Run(ctx context.Context) {
	m.Refresh()
	full := time.NewTicker(RefreshInterval)
	defer full.Stop()
	free := time.NewTicker(freeInterval)
	defer free.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-full.C:
			m.Refresh()
		case <-free.C:
			m.RefreshFree()
		}
	}
}

// Snapshot returns the last measurement.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.snap
	s.Parts = append([]Part(nil), m.snap.Parts...)
	if g := m.snap.Growth.DaysToFull; g != nil {
		d := *g
		s.Growth.DaysToFull = &d
	}
	return s
}

// Refresh walks the directories, reads the free space and writes the metrics.
func (m *Monitor) Refresh() {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	parts := m.walk()
	m.mu.Lock()
	m.snap.Parts, m.snap.SizesAt, m.snap.Ready = parts, m.opts.Now(), true
	m.snap.Largest = largest(parts)
	m.mu.Unlock()
	s := m.RefreshFree()
	if mc := m.opts.Metrics; mc != nil {
		mc.Ingest([]metrics.CollectResult{
			{Path: MetricDiskFree, Value: float64(s.FileSystem.FreeBytes)},
			{Path: MetricHistory, Value: float64(partBytes(s.Parts, PartHistory))},
			{Path: MetricLogPerHour, Value: float64(s.Growth.LogBytesPerHour)},
		})
	}
}

// RefreshFree reads the free space and the growth, keeping the sizes.
func (m *Monitor) RefreshFree() Snapshot {
	o := m.opts
	fsys := FileSystem{Path: o.DataDir}
	total, free, err := o.StatFS(o.DataDir)
	if err != nil {
		fsys.Error = err.Error()
	} else {
		fsys.TotalBytes, fsys.FreeBytes = total, free
		fsys.LowBelowBytes, fsys.CriticalBelowBytes = Thresholds(total)
		if total > 0 {
			fsys.FreePercent = math.Round(float64(free)*1000/float64(total)) / 10
		}
	}
	var g Growth
	if o.HistoryLast24h != nil {
		g.HistoryLast24hBytes = o.HistoryLast24h()
	}
	if o.LogBytesPerHour != nil {
		g.LogBytesPerHour = o.LogBytesPerHour()
	}
	g.BytesPerDay = g.HistoryLast24hBytes + 24*g.LogBytesPerHour
	if err == nil {
		if d, ok := DaysToFull(free, g.BytesPerDay); ok {
			g.DaysToFull = &d
		}
	}
	level := LevelOf(fsys.TotalBytes, fsys.FreeBytes)

	m.mu.Lock()
	m.snap.FileSystem, m.snap.Growth, m.snap.Level, m.snap.FreeAt = fsys, g, level, o.Now()
	m.mu.Unlock()
	if o.OnLevel != nil {
		o.OnLevel(level)
	}
	return m.Snapshot()
}

// walk sums what every part takes on disk, directories included, as du does.
func (m *Monitor) walk() []Part {
	m.walks.Add(1)
	sizes := map[string]int64{}
	counted := map[string]bool{}
	if m.opts.UpdateBackups != "" {
		names, _ := filepath.Glob(m.opts.UpdateBackups)
		for _, n := range names {
			if fi, err := os.Lstat(n); err == nil && fi.Mode().IsRegular() {
				sizes[PartBackups] += allocated(fi)
				counted[absPath(n)] = true
			}
		}
	}
	root := m.opts.DataDir
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r // WalkDir does not descend into a symlinked root
	}
	if root != "" {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable: left out
			}
			if counted[absPath(path)] {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			sizes[partOf(filepath.ToSlash(rel))] += allocated(fi)
			return nil
		})
	}
	parts := make([]Part, 0, len(partOrder))
	for _, name := range partOrder {
		parts = append(parts, Part{Name: name, Bytes: sizes[name]})
	}
	return parts
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// partOf places a path relative to the data directory (see deploy/update.sh).
func partOf(rel string) string {
	under := func(dir string) bool { return rel == dir || strings.HasPrefix(rel, dir+"/") }
	switch {
	case under("clu/history"):
		return PartHistory
	case under("clu/metrics"):
		return PartMetrics
	case under("clu/plugins"):
		return PartPlugins
	case under("clu/workspace/backups"), under("backup"),
		strings.HasPrefix(rel, "clu/backups"), strings.HasPrefix(rel, "backup-"):
		return PartBackups
	}
	return PartOther
}

func largest(parts []Part) string {
	best := Part{}
	for _, p := range parts {
		if p.Bytes > best.Bytes {
			best = p
		}
	}
	return best.Name
}

func partBytes(parts []Part, name string) int64 {
	for _, p := range parts {
		if p.Name == name {
			return p.Bytes
		}
	}
	return 0
}
