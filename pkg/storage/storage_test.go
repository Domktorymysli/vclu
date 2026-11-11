package storage

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/metrics"
)

const GiB = 1 << 30

func TestThresholds_Boundaries(t *testing.T) {
	big := int64(100 * GiB) // 10 % and 3 % are larger than 500 MB and 100 MB
	low, critical := Thresholds(big)
	if low != big/10 || critical != big*3/100 {
		t.Fatalf("100 GB: low %d critical %d", low, critical)
	}
	small := int64(2 * GiB) // 10 % is 205 MB: 500 MB wins; 3 % is 61 MB: 100 MB wins
	if low, critical := Thresholds(small); low != 500*MiB || critical != 100*MiB {
		t.Fatalf("2 GB: low %d critical %d", low, critical)
	}
	cases := []struct {
		name        string
		total, free int64
		want        Level
	}{
		{"11 %", big, big * 11 / 100, LevelOK},
		{"exactly 10 %", big, big / 10, LevelOK},
		{"just under 10 %", big, big/10 - 1, LevelLow},
		{"8 %", big, big * 8 / 100, LevelLow},
		{"exactly 3 %", big, big * 3 / 100, LevelLow},
		{"2 %", big, big * 2 / 100, LevelCritical},
		{"small disk 20 % but under 500 MB", small, 400 * MiB, LevelLow},
		{"small disk at 500 MB", small, 500 * MiB, LevelOK},
		{"small disk under 100 MB", small, 99 * MiB, LevelCritical},
		{"unknown size", 0, 0, LevelOK},
	}
	for _, c := range cases {
		if got := LevelOf(c.total, c.free); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestDaysToFull(t *testing.T) {
	if d, ok := DaysToFull(1000*MiB, 100*MiB); !ok || d != 10 {
		t.Errorf("100 MB/day, 1 GB free: %d %v, want 10", d, ok)
	}
	if d, ok := DaysToFull(GiB, 100*MiB); !ok || d != 10 {
		t.Errorf("100 MB/day, 1 GiB free: %d %v, want 10", d, ok)
	}
	if _, ok := DaysToFull(GiB, 0); ok {
		t.Error("no growth: no days")
	}
	if d, ok := DaysToFull(89*MiB, MiB); !ok || d != 89 {
		t.Errorf("89 days: %d %v", d, ok)
	}
	if _, ok := DaysToFull(90*MiB, MiB); ok {
		t.Error("90 days and more are not reported")
	}
}

// fakeFS answers statfs with the free space set by the test.
type fakeFS struct {
	mu          sync.Mutex
	total, free int64
}

func (f *fakeFS) set(total, free int64) {
	f.mu.Lock()
	f.total, f.free = total, free
	f.mu.Unlock()
}

func (f *fakeFS) stat(string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total, f.free, nil
}

func writeFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), n), 0o600); err != nil {
		t.Fatal(err)
	}
}

// dataDir lays out a data directory as vCLU does, with the sizes in KiB.
func dataDir(t *testing.T) (data, updates string) {
	t.Helper()
	data = t.TempDir()
	writeFile(t, filepath.Join(data, "clu/history/2026-11-08.jsonl"), 800<<10)
	writeFile(t, filepath.Join(data, "clu/history/2026-11-09.jsonl"), 400<<10)
	writeFile(t, filepath.Join(data, "clu/metrics/a.b/2026-11-09.ts"), 300<<10)
	writeFile(t, filepath.Join(data, "clu/plugins/@vclu/weather/init.lua"), 200<<10)
	writeFile(t, filepath.Join(data, "clu/workspace/backups/x.lua.bak"), 100<<10)
	writeFile(t, filepath.Join(data, "backup/vclu-backup.zip"), 100<<10)
	writeFile(t, filepath.Join(data, "clu/user.lua"), 50<<10)
	updates = t.TempDir()
	writeFile(t, filepath.Join(updates, "backup-1.9.0-20261101.tar.gz"), 500<<10)
	writeFile(t, filepath.Join(updates, "vclu"), 900<<10) // the binary is not a backup
	return data, updates
}

func sizes(s Snapshot) map[string]int64 {
	m := map[string]int64{}
	for _, p := range s.Parts {
		m[p.Name] = p.Bytes
	}
	return m
}

func TestMonitor_PartsMatchDu(t *testing.T) {
	data, updates := dataDir(t)
	fs := &fakeFS{}
	fs.set(100*GiB, 50*GiB)
	m := New(Options{DataDir: data, UpdateBackups: filepath.Join(updates, "backup-*.tar.gz"), StatFS: fs.stat})
	m.Refresh()
	s := m.Snapshot()
	if !s.Ready {
		t.Fatal("not ready after a walk")
	}
	got := sizes(s)
	names := []string{}
	for _, p := range s.Parts {
		names = append(names, p.Name)
	}
	if strings.Join(names, ",") != "history,metrics,backups,plugins,other" {
		t.Fatalf("parts %v", names)
	}
	// at least the bytes written, plus directories and block rounding
	for name, min := range map[string]int64{PartHistory: 1200 << 10, PartMetrics: 300 << 10, PartPlugins: 200 << 10,
		PartBackups: 700 << 10, PartOther: 50 << 10} {
		if got[name] < min {
			t.Errorf("%s: %d, want at least %d", name, got[name], min)
		}
	}
	if got[PartOther] > 200<<10 {
		t.Errorf("other %d: the binary or a part leaked into it", got[PartOther])
	}
	if s.Largest != PartHistory {
		t.Errorf("largest %q", s.Largest)
	}

	du, err := exec.LookPath("du")
	if err != nil {
		t.Skip("no du")
	}
	duKiB := func(path string) int64 {
		out, err := exec.Command(du, "-sk", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
		return n << 10
	}
	for name, dir := range map[string]string{PartHistory: "clu/history", PartMetrics: "clu/metrics", PartPlugins: "clu/plugins"} {
		want := duKiB(filepath.Join(data, dir))
		if diff := got[name] - want; diff*100 > want || -diff*100 > want {
			t.Errorf("%s: %d, du says %d", name, got[name], want)
		}
	}
	var all int64
	for _, p := range s.Parts {
		all += p.Bytes
	}
	if want := duKiB(data) + duKiB(filepath.Join(updates, "backup-1.9.0-20261101.tar.gz")); all-want > want/100 || want-all > want/100 {
		t.Errorf("sum %d, du says %d", all, want)
	}
}

func TestMonitor_DataDirSymlink(t *testing.T) {
	data, _ := dataDir(t)
	link := filepath.Join(t.TempDir(), "data")
	if err := os.Symlink(data, link); err != nil {
		t.Skip(err)
	}
	fs := &fakeFS{}
	fs.set(100*GiB, 50*GiB)
	m := New(Options{DataDir: link, StatFS: fs.stat})
	m.Refresh()
	if got := sizes(m.Snapshot())[PartHistory]; got < 1200<<10 {
		t.Fatalf("history through a symlinked data dir: %d", got)
	}
}

func TestMonitor_SizesCachedUntilNextWalk(t *testing.T) {
	data, _ := dataDir(t)
	fs := &fakeFS{}
	fs.set(100*GiB, 50*GiB)
	m := New(Options{DataDir: data, StatFS: fs.stat})
	if s := m.Snapshot(); s.Ready || len(s.Parts) != 0 {
		t.Fatalf("ready before a walk: %+v", s)
	}
	m.Refresh()
	before := sizes(m.Snapshot())[PartHistory]
	writeFile(t, filepath.Join(data, "clu/history/2026-11-07.jsonl"), 1<<20)
	for i := 0; i < 10; i++ {
		m.Snapshot()
	}
	fs.set(100*GiB, 40*GiB)
	s := m.RefreshFree()
	if got := sizes(s)[PartHistory]; got != before {
		t.Fatalf("sizes changed without a walk: %d -> %d", before, got)
	}
	if s.FileSystem.FreeBytes != 40*GiB {
		t.Errorf("free space not read again: %d", s.FileSystem.FreeBytes)
	}
	if n := m.walks.Load(); n != 1 {
		t.Fatalf("%d walks, want 1", n)
	}
	m.Refresh()
	if got := sizes(m.Snapshot())[PartHistory]; got < before+1<<20 {
		t.Errorf("after the next walk: %d", got)
	}
}

func TestMonitor_LevelGrowthAndMetrics(t *testing.T) {
	fs := &fakeFS{}
	fs.set(100*GiB, 8*GiB)
	mc := metrics.NewCollector(t.TempDir())
	var mu sync.Mutex
	var levels []Level
	hist, logs := int64(100*MiB), int64(0)
	m := New(Options{
		DataDir: t.TempDir(), StatFS: fs.stat, Metrics: mc,
		HistoryLast24h:  func() int64 { return hist },
		LogBytesPerHour: func() int64 { return logs },
		OnLevel:         func(l Level) { mu.Lock(); levels = append(levels, l); mu.Unlock() },
	})
	m.Refresh()
	s := m.Snapshot()
	if s.Level != LevelLow || s.FileSystem.FreePercent != 8 {
		t.Fatalf("8 %% free: %s %.1f", s.Level, s.FileSystem.FreePercent)
	}
	if s.FileSystem.LowBelowBytes != 10*GiB || s.FileSystem.CriticalBelowBytes != 3*GiB {
		t.Errorf("thresholds %+v", s.FileSystem)
	}
	if s.Growth.DaysToFull == nil || *s.Growth.DaysToFull != 81 {
		t.Errorf("8 GiB at 100 MiB/day: %v", s.Growth.DaysToFull)
	}

	fs.set(100*GiB, GiB)
	if s = m.RefreshFree(); s.Level != LevelCritical || s.Growth.DaysToFull == nil || *s.Growth.DaysToFull != 10 {
		t.Fatalf("1 GiB at 100 MiB/day: %s %v", s.Level, s.Growth.DaysToFull)
	}
	logs = MiB // 24 MiB a day more
	if s = m.RefreshFree(); s.Growth.BytesPerDay != 124*MiB || *s.Growth.DaysToFull != 8 {
		t.Errorf("with logs: %d/day, %v days", s.Growth.BytesPerDay, *s.Growth.DaysToFull)
	}
	hist, logs = 0, 0
	if s = m.RefreshFree(); s.Growth.DaysToFull != nil {
		t.Errorf("no growth: %d days", *s.Growth.DaysToFull)
	}
	fs.set(100*GiB, 20*GiB)
	if s = m.RefreshFree(); s.Level != LevelOK {
		t.Errorf("20 %%: %s", s.Level)
	}
	mu.Lock()
	if strings.Join(levelNames(levels), ",") != "low,critical,critical,critical,ok" {
		t.Errorf("OnLevel %v", levels)
	}
	mu.Unlock()

	latest := map[string]float64{}
	for _, info := range mc.List() {
		latest[info.Path] = info.Latest
	}
	if latest[MetricDiskFree] != 8*GiB {
		t.Errorf("metrics after the walk: %v", latest)
	}
	for _, p := range []string{MetricDiskFree, MetricHistory, MetricLogPerHour} {
		if _, ok := latest[p]; !ok {
			t.Errorf("%s not registered", p)
		}
	}
	if due := mc.DueMetrics(); len(due) != 0 {
		t.Errorf("Lua would be asked for %v", due)
	}
}

func levelNames(ls []Level) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = string(l)
	}
	return out
}

func TestMonitor_StatFSError(t *testing.T) {
	m := New(Options{DataDir: t.TempDir(), StatFS: func(string) (int64, int64, error) {
		return 0, 0, os.ErrPermission
	}, HistoryLast24h: func() int64 { return MiB }})
	m.Refresh()
	s := m.Snapshot()
	if s.Level != LevelOK || s.FileSystem.Error == "" || s.Growth.DaysToFull != nil {
		t.Errorf("unreadable free space: %+v", s)
	}
}

// free space that cannot be read never says critical: history would be cut for nothing
func TestMonitor_UnknownFreeSpaceIsNotCritical(t *testing.T) {
	var total, free int64 = 100 * GiB, GiB
	var fail error
	var last Level
	m := New(Options{DataDir: t.TempDir(), OnLevel: func(l Level) { last = l },
		StatFS: func(string) (int64, int64, error) { return total, free, fail }})
	m.Refresh()
	if last != LevelCritical {
		t.Fatalf("1 %%: %s", last)
	}
	fail = os.ErrPermission
	if m.RefreshFree(); last != LevelOK {
		t.Errorf("statfs error after critical: %s", last)
	}
	fail, total, free = nil, 0, 0
	if s := m.RefreshFree(); last != LevelOK || s.Level != LevelOK {
		t.Errorf("zero-sized file system: %s", last)
	}
}

func TestMonitor_RunMeasuresAtStartOffTheCaller(t *testing.T) {
	fs := &fakeFS{}
	fs.set(100*GiB, 50*GiB)
	m := New(Options{DataDir: t.TempDir(), StatFS: fs.stat})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for !m.Snapshot().Ready {
		if time.Now().After(deadline) {
			t.Fatal("no measurement at start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestStatFS_Real(t *testing.T) {
	total, free, err := statFS(t.TempDir())
	if err != nil {
		t.Skip(err)
	}
	if total <= 0 || free < 0 || free > total {
		t.Errorf("total %d free %d", total, free)
	}
}
