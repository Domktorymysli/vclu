package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type sink struct {
	mu  sync.Mutex
	got []Delivery
}

func (s *sink) deliver(d Delivery) { s.mu.Lock(); s.got = append(s.got, d); s.mu.Unlock() }
func (s *sink) list() []Delivery {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Delivery(nil), s.got...)
}

func newTestStore(t *testing.T, dir string) (*Store, *clock, *sink) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	s := New(Options{Path: Path(dir), ConfigDir: dir, Now: c.now, Debounce: time.Hour, Logf: t.Logf})
	k := &sink{}
	s.Subscribe(k.deliver)
	t.Cleanup(func() { _ = s.Close() })
	return s, c, k
}

func login(ip string) Entry {
	return Entry{Kind: KindEvent, Source: SourceLogin, Level: LevelWarning, Key: "login:" + ip,
		Msg: MsgLogin, Params: map[string]string{"ip": ip}, Link: "/settings"}
}

func update(v string) Entry {
	return Entry{Kind: KindState, Source: SourceUpdate, Key: "update:" + v, Msg: MsgUpdate, Params: map[string]string{"version": v}}
}

// An unread event with the same key is counted, not repeated; once read a new one comes.
func TestEventsMergeUntilRead(t *testing.T) {
	s, c, k := newTestStore(t, t.TempDir())
	s.Add(login("10.0.0.5"))
	c.add(time.Minute)
	s.Add(login("10.0.0.5"))
	s.Add(login("10.0.0.6"))
	list, unread, worst := s.List()
	if len(list) != 2 || unread != 2 || worst != LevelWarning {
		t.Fatalf("list %+v unread %d worst %q", list, unread, worst)
	}
	if e := list[1]; e.Key != "login:10.0.0.5" || e.Count != 2 || !e.Last.After(e.First) || e.Title != "Failed login to the panel" || e.Body != "From 10.0.0.5." || e.Params["ip"] != "10.0.0.5" {
		t.Fatalf("merged %+v", e)
	}
	if len(k.list()) != 2 {
		t.Fatalf("a merge was delivered: %+v", k.list())
	}
	s.MarkRead([]int64{list[1].ID}, false)
	s.Add(login("10.0.0.5"))
	list, unread, _ = s.List()
	if len(list) != 3 || unread != 2 || list[0].Count != 1 || list[0].Key != "login:10.0.0.5" {
		t.Fatalf("after read %+v", list)
	}
	// counts of 10 and 100 are delivered again
	for i := 0; i < 99; i++ {
		s.Add(login("10.0.0.6"))
	}
	var counts []int
	for _, d := range k.list() {
		if d.Key == "login:10.0.0.6" {
			counts = append(counts, d.Count)
		}
	}
	if fmt.Sprint(counts) != "[1 10 100]" {
		t.Errorf("deliveries of 10.0.0.6: %v", counts)
	}
}

// A state is one entry per key whether read or not; once read it does not come back unread.
func TestStateStaysReadAndResolves(t *testing.T) {
	s, _, k := newTestStore(t, t.TempDir())
	s.Add(update("1.13.0"))
	list, _, _ := s.List()
	s.MarkRead(nil, true)
	s.Add(update("1.13.0"))
	after, unread, _ := s.List()
	if len(after) != 1 || unread != 0 || after[0].ID != list[0].ID || after[0].Title != "vclu 1.13.0 is available" {
		t.Fatalf("state again: %+v unread %d", after, unread)
	}
	s.Resolve(func(e Entry) bool { return strings.HasPrefix(e.Key, "update:") })
	if list, _, _ := s.List(); len(list) != 0 {
		t.Fatalf("resolved state stays: %+v", list)
	}
	got := k.list()
	if len(got) != 2 || got[0].Resolved || !got[1].Resolved || got[1].Key != "update:1.13.0" {
		t.Errorf("deliveries %+v", got)
	}
	// events are not resolved
	s.Add(login("1.2.3.4"))
	s.Resolve(func(Entry) bool { return true })
	if list, _, _ := s.List(); len(list) != 1 {
		t.Errorf("Resolve removed an event")
	}
}

// IDs and read marks survive a restart; the file is written only after the debounce or on Close.
func TestRestartKeepsIDs(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := newTestStore(t, dir)
	s.Add(login("10.0.0.5"))
	s.Add(update("1.13.0"))
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("written before the debounce: %v", err)
	}
	list, _, _ := s.List()
	s.MarkRead([]int64{list[0].ID}, false)
	s.Clear() // the event goes, the state stays
	s.Add(login("10.0.0.7"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(Path(dir)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file %v %v", fi, err)
	}
	s.Add(login("10.0.0.8")) // closed: nothing
	s2, _, _ := newTestStore(t, dir)
	got, unread, _ := s2.List()
	if len(got) != 2 || unread != 1 || got[0].ID != 3 || got[1].ID != 2 || !got[1].Read {
		t.Fatalf("after restart %+v", got)
	}
	s2.Add(login("10.0.0.9"))
	if got, _, _ := s2.List(); got[0].ID != 4 {
		t.Errorf("next id after restart %d", got[0].ID)
	}
}

func TestCorruptFileStartsEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Dir(Path(dir)), 0o755)
	_ = os.WriteFile(Path(dir), []byte("{not json"), 0o600)
	s, _, _ := newTestStore(t, dir)
	s.Add(login("1.1.1.1"))
	if list, _, _ := s.List(); len(list) != 1 || list[0].ID != 1 {
		t.Fatalf("%+v", list)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// At most 200: the oldest read ones go first.
func TestCapDropsReadFirst(t *testing.T) {
	s, c, _ := newTestStore(t, t.TempDir())
	s.opts.Max = 5
	s.Add(update("1.13.0"))
	for i := 0; i < 3; i++ {
		c.add(time.Second)
		s.Add(login(fmt.Sprint("10.0.0.", i)))
	}
	list, _, _ := s.List()
	s.MarkRead([]int64{list[0].ID}, false) // the newest one
	for i := 3; i < 6; i++ {
		c.add(time.Second)
		s.Add(login(fmt.Sprint("10.0.0.", i)))
	}
	list, _, _ = s.List()
	keys := []string{}
	for _, e := range list {
		keys = append(keys, e.Key)
	}
	if got := strings.Join(keys, " "); got != "login:10.0.0.5 login:10.0.0.4 login:10.0.0.3 login:10.0.0.1 update:1.13.0" {
		t.Errorf("kept %s", got)
	}
}

func TestReadEventsExpire(t *testing.T) {
	s, c, _ := newTestStore(t, t.TempDir())
	s.Add(login("1.1.1.1"))
	s.Add(login("2.2.2.2"))
	s.Add(update("1.13.0"))
	list, _, _ := s.List()
	s.MarkRead([]int64{list[0].ID, list[2].ID}, false)
	s.MarkRead(nil, true)
	c.add(15 * 24 * time.Hour)
	if list, _, _ := s.List(); len(list) != 1 || list[0].Kind != KindState {
		t.Fatalf("after 15 days %+v", list)
	}
}

// 30 new events of a source an hour, then one entry counts the rest; logs 20 a minute.
func TestRateLimit(t *testing.T) {
	s, c, k := newTestStore(t, t.TempDir())
	for i := 0; i < 35; i++ {
		s.Add(login(fmt.Sprint("10.0.", i/250, ".", i%250)))
	}
	list, _, _ := s.List()
	if len(list) != 31 || list[0].Msg != MsgSuppressed || list[0].Count != 5 || list[0].Params["count"] != "5" || list[0].Title != "5 notifications skipped" {
		t.Fatalf("%d entries, first %+v", len(list), list[0])
	}
	if n := len(k.list()); n != 31 {
		t.Errorf("%d deliveries", n)
	}
	s.Add(login("10.0.0.0")) // a merge is not a new one
	c.add(time.Hour)
	s.Add(login("10.9.9.9"))
	if list, _, _ := s.List(); list[0].Key != "login:10.9.9.9" {
		t.Errorf("the next hour still limited: %+v", list[0])
	}

	if err := s.SetSources(map[string]bool{SourceLog: true}); err != nil {
		t.Fatal(err)
	}
	s.Clear()
	for i := 0; i < 25; i++ {
		s.Add(Entry{Kind: KindEvent, Source: SourceLog, Level: LevelError, Key: fmt.Sprint("log:x:", i), Body: "x"})
	}
	list, _, _ = s.List()
	if len(list) != 21 || list[0].Count != 5 {
		t.Fatalf("log: %d entries, first %+v", len(list), list[0])
	}
	c.add(time.Minute)
	s.Add(Entry{Kind: KindEvent, Source: SourceLog, Level: LevelError, Key: "log:y", Body: "y"})
	if list, _, _ := s.List(); list[0].Key != "log:y" {
		t.Errorf("logs still limited the next minute")
	}
}

// Each message has its own hourly window: a flood of PIN locks does not hide a login lock.
func TestRateLimitPerMessage(t *testing.T) {
	s, _, _ := newTestStore(t, t.TempDir())
	for i := 0; i < 40; i++ {
		s.Add(Entry{Kind: KindEvent, Source: SourceLock, Level: LevelWarning, Key: fmt.Sprint("lock:pin:x", i), Msg: MsgLockPIN,
			Params: map[string]string{"dashboard": fmt.Sprint("x", i)}})
	}
	s.Add(Entry{Kind: KindEvent, Source: SourceLock, Level: LevelWarning, Key: "lock:login:10.0.0.5", Msg: MsgLockLogin,
		Params: map[string]string{"ip": "10.0.0.5"}})
	list, _, _ := s.List()
	if list[0].Key != "lock:login:10.0.0.5" {
		t.Fatalf("login lock hidden: %+v", list[0])
	}
	if len(list) != 32 {
		t.Errorf("%d entries, want 30 PIN locks, one skipped entry and the login lock", len(list))
	}
}

// Log is off until turned on; a source turned off and a muted key add nothing.
func TestDisabledAndMuted(t *testing.T) {
	dir := t.TempDir()
	s, c, k := newTestStore(t, dir)
	s.Add(Entry{Kind: KindEvent, Source: SourceLog, Level: LevelError, Key: "log:a", Body: "a"})
	if list, _, _ := s.List(); len(list) != 0 {
		t.Fatal("log is on by default")
	}
	if err := s.SetSources(map[string]bool{SourceLogin: false}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSources(map[string]bool{"nope": true}); err == nil {
		t.Error("unknown source taken")
	}
	s.Add(login("1.1.1.1"))
	s.Add(update("1.13.0"))
	if err := s.Mute("update:1.13.0", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	s.Add(update("1.13.0"))
	if list, _, _ := s.List(); len(list) != 0 {
		t.Fatalf("muted or disabled added: %+v", list)
	}
	set := s.Settings()
	if fmt.Sprint(set.Sources) != "[{update true true} {plugin true true} {module true true} {log false false} {login false true} {lock true true}]" ||
		len(set.Muted) != 1 || set.Muted[0].Title != "vclu 1.13.0 is available" || set.Muted[0].Until != "2026-11-22T12:00:00Z" ||
		set.Muted[0].Msg != MsgUpdate || set.Muted[0].Params["version"] != "1.13.0" {
		t.Fatalf("settings %+v", set)
	}
	mc, _ := config.ReadMasterConfig(dir)
	if mc == nil || mc.Notifications == nil || fmt.Sprint(mc.Notifications.Sources) != "map[login:false]" ||
		fmt.Sprint(mc.Notifications.Muted["update:1.13.0"]) != fmt.Sprint(config.VCLUNotificationMute{Until: "2026-11-22T12:00:00Z",
			Title: "vclu 1.13.0 is available", Msg: MsgUpdate, Params: map[string]string{"version": "1.13.0"}}) {
		t.Fatalf(".vclu.json %+v", mc.Notifications)
	}
	// settings come back after a restart, and an expired mute is dropped on the next write
	s2 := New(Options{ConfigDir: dir, Now: c.now})
	if set := s2.Settings(); len(set.Muted) != 1 || set.Sources[4].Enabled || set.Muted[0].Msg != MsgUpdate {
		t.Fatalf("after restart %+v", set)
	}
	c.add(25 * time.Hour)
	s.Add(update("1.13.0"))
	if err := s.Mute("other", 0); err != nil {
		t.Fatal(err)
	}
	mc, _ = config.ReadMasterConfig(dir)
	if _, ok := mc.Notifications.Muted["update:1.13.0"]; ok || mc.Notifications.Muted["other"].Until != "forever" {
		t.Errorf("muted %+v", mc.Notifications.Muted)
	}
	if err := s.Unmute("other"); err != nil {
		t.Fatal(err)
	}
	if len(s.Settings().Muted) != 0 {
		t.Error("unmute")
	}
	for _, d := range k.list() {
		if d.Source == SourceLogin {
			t.Errorf("a disabled source was delivered")
		}
	}
}

func TestTextsCapped(t *testing.T) {
	s, _, _ := newTestStore(t, t.TempDir())
	s.Add(Entry{Kind: KindState, Source: SourceModule, Level: LevelError, Key: "module:x.lua", Msg: MsgModule,
		Params: map[string]string{"module": "x.lua"}, Detail: strings.Repeat("ż", 1000)})
	list, _, worst := s.List()
	if n := len([]rune(list[0].Detail)); n != maxText || worst != LevelError || list[0].Title != "Module x.lua did not load" {
		t.Errorf("body %d runes, worst %q, title %q", n, worst, list[0].Title)
	}
}

func TestModules(t *testing.T) {
	s, _, k := newTestStore(t, t.TempDir())
	ModuleLoad(s, map[string]string{"a.lua": "modules/a.lua:3: boom", "b.lua": "x"})
	ModuleLoad(s, map[string]string{"b.lua": "y"})
	list, _, _ := s.List()
	if len(list) != 1 || list[0].Key != "module:b.lua" || list[0].Detail != "y" || list[0].Body != "" || list[0].Link != "/editor?file=modules/b.lua" {
		t.Fatalf("%+v", list)
	}
	ModuleLoad(s, nil)
	if list, _, _ := s.List(); len(list) != 0 {
		t.Fatal("a module that loads keeps its state")
	}
	var resolved []string
	for _, d := range k.list() {
		if d.Resolved {
			resolved = append(resolved, d.Key)
		}
	}
	if fmt.Sprint(resolved) != "[module:a.lua module:b.lua]" {
		t.Errorf("resolved %v", resolved)
	}
}

// An error of the modules/ directory itself has its own key and opens the editor.
func TestModulesDirError(t *testing.T) {
	s, _, _ := newTestStore(t, t.TempDir())
	ModuleLoad(s, map[string]string{ModulesDir: "failed to read modules dir"})
	list, _, _ := s.List()
	if len(list) != 1 || list[0].Key != "module:dir" || list[0].Link != "/editor" {
		t.Fatalf("%+v", list)
	}
	ModuleLoad(s, map[string]string{ModulesDir: "again"})
	if list, _, _ = s.List(); len(list) != 1 || list[0].Detail != "again" {
		t.Fatalf("not kept: %+v", list)
	}
	ModuleLoad(s, nil)
	if list, _, _ = s.List(); len(list) != 0 {
		t.Fatal("not resolved")
	}
}

// Concurrent writes never leave an older snapshot on disk.
func TestFlushConcurrent(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := newTestStore(t, dir)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Add(login(fmt.Sprintf("10.0.0.%d", i)))
			if err := s.Flush(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	r := New(Options{Path: Path(dir), Logf: t.Logf})
	if list, _, _ := r.List(); len(list) != 20 {
		t.Errorf("file has %d of 20 entries", len(list))
	}
}

// A failed write keeps the store dirty, so Close tries again.
func TestFlushFailureKeepsDirty(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := newTestStore(t, dir)
	ws := filepath.Join(dir, "workspace")
	if err := os.WriteFile(ws, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Add(login("10.0.0.1"))
	if err := s.Flush(); err == nil {
		t.Fatal("write into a file succeeded")
	}
	_ = os.Remove(ws)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Errorf("Close did not write: %v", err)
	}
}

// Discard writes nothing, also later.
func TestDiscard(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := newTestStore(t, dir)
	s.Add(login("10.0.0.1"))
	s.Discard()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s.Add(login("10.0.0.2"))
	if s.Flush() != nil {
		t.Fatal("flush")
	}
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Errorf("discarded store wrote: %v", err)
	}
}

func TestNilStore(t *testing.T) {
	var s *Store
	s.Add(login("1.1.1.1"))
	s.Resolve(func(Entry) bool { return true })
	ModuleLoad(s, map[string]string{"a.lua": "x"})
	if s.Enabled(SourceUpdate) {
		t.Fatal("nil store enabled")
	}
	if list, n, w := s.List(); len(list) != 0 || n != 0 || w != "" {
		t.Fatal("nil store lists")
	}
	if s.Close() != nil || s.Flush() != nil {
		t.Fatal("nil store")
	}
}

// A renewed state comes back unread and is delivered; a plain Add keeps it read and quiet.
func TestStateRenew(t *testing.T) {
	s, c, k := newTestStore(t, t.TempDir())
	s.Add(update("1.13.0"))
	s.MarkRead(nil, true)
	c.add(time.Minute)
	s.Add(update("1.13.0"))
	if _, unread, _ := s.List(); unread != 0 || len(k.list()) != 1 {
		t.Fatalf("plain Add: unread %d, %d deliveries", unread, len(k.list()))
	}
	e := update("1.13.0")
	e.Renew = true
	s.Add(e)
	list, unread, _ := s.List()
	if unread != 1 || len(list) != 1 || !list[0].Last.After(list[0].First) || len(k.list()) != 2 || k.list()[1].Resolved {
		t.Fatalf("renewed: %+v unread %d deliveries %+v", list, unread, k.list())
	}
}

// Each subscriber gets every delivery, log entries too; one unsubscribed gets nothing more.
func TestSubscribe(t *testing.T) {
	s, _, k := newTestStore(t, t.TempDir())
	other := &sink{}
	stop := s.Subscribe(other.deliver)
	s.Add(login("1.1.1.1"))
	stop()
	s.Add(login("2.2.2.2"))
	if len(k.list()) != 2 || len(other.list()) != 1 {
		t.Errorf("deliveries %d and %d", len(k.list()), len(other.list()))
	}
}

// Turning a source off resolves its states; a source switched by nobody keeps its default,
// so one added later off by default stays off for an installation with other choices saved.
func TestSourcesDefaults(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := newTestStore(t, dir)
	s.Add(update("1.13.0"))
	s.Add(Entry{Kind: KindState, Source: SourcePlugin, Key: "plugin-updates", Msg: MsgPluginUpdates})
	if err := s.SetSources(map[string]bool{SourcePlugin: false, SourceLog: true}); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := s.List(); len(list) != 1 || list[0].Source != SourceUpdate {
		t.Fatalf("after plugin off: %+v", list)
	}
	saved := Sources
	t.Cleanup(func() { Sources = saved })
	Sources = append(append([]SourceInfo(nil), saved...), SourceInfo{ID: "later"}, SourceInfo{ID: "later-on", DefaultOn: true})
	s2 := New(Options{ConfigDir: dir})
	if s2.Enabled("later") || !s2.Enabled("later-on") || !s2.Enabled(SourceLog) || s2.Enabled(SourcePlugin) || !s2.Enabled(SourceUpdate) {
		t.Errorf("settings %+v", s2.Settings().Sources)
	}
	if s2.Enabled("unknown") {
		t.Error("an unknown source adds")
	}
}

// The file carries its format; one from a newer vclu is not read.
func TestFileVersion(t *testing.T) {
	dir := t.TempDir()
	s, _, _ := newTestStore(t, dir)
	s.Add(login("1.1.1.1"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	if !strings.Contains(string(b), `"version": 1`) {
		t.Fatalf("no version: %s", b)
	}
	_ = os.WriteFile(Path(dir), []byte(`{"version": 2, "entries": [{"id": 1, "kind": "event", "source": "login"}]}`), 0o600)
	if list, _, _ := New(Options{Path: Path(dir), Logf: t.Logf}).List(); len(list) != 0 {
		t.Errorf("newer format read: %+v", list)
	}
}

// The doc of Notifications.on lists the sources that reach Lua.
func TestLuaDocListsSources(t *testing.T) {
	b, err := os.ReadFile("../lua/runtime/core/Notifications.lua")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, id := range LuaSources() {
		want = append(want, `"`+id+`"`)
	}
	if list := strings.Join(want, "|"); !strings.Contains(strings.ReplaceAll(string(b), "\n-- ", " "), "source ("+list+")") {
		t.Errorf("Notifications.lua does not list source (%s)", list)
	}
}

// Changes are written after the debounce without Flush, several in the window at once; after
// Close no planned write comes.
func TestDebouncedWrite(t *testing.T) {
	dir := t.TempDir()
	s := New(Options{Path: Path(dir), Debounce: 100 * time.Millisecond, Logf: t.Logf})
	t.Cleanup(func() { _ = s.Close() })
	written := func() int {
		r := New(Options{Path: Path(dir), Logf: t.Logf})
		list, _, _ := r.List()
		return len(list)
	}
	s.Add(login("10.0.0.1"))
	s.mu.Lock()
	timer := s.timer
	s.mu.Unlock()
	s.Add(login("10.0.0.2"))
	s.Add(login("10.0.0.3"))
	s.mu.Lock()
	same := s.timer == timer
	s.mu.Unlock()
	if timer == nil || !same {
		t.Fatal("not one planned write for changes in the window")
	}
	deadline := time.Now().Add(5 * time.Second)
	for written() != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := written(); n != 3 {
		t.Fatalf("%d of 3 entries written", n)
	}
	s.mu.Lock()
	dirty, pending := s.dirty, s.timer != nil
	s.mu.Unlock()
	if dirty || pending {
		t.Errorf("after the write: dirty %v, timer %v", dirty, pending)
	}

	s.Add(login("10.0.0.4"))
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := written(); n != 4 {
		t.Fatalf("Close wrote %d of 4", n)
	}
	if err := os.Remove(Path(dir)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Errorf("written after Close: %v", err)
	}
}

// State gives a copy of the state with the key; events and missing keys are not states.
func TestState(t *testing.T) {
	s, _, _ := newTestStore(t, t.TempDir())
	s.Add(update("1.13.0"))
	s.Add(login("10.0.0.1"))
	e, ok := s.State("update:1.13.0")
	if !ok || e.Params["version"] != "1.13.0" || e.Kind != KindState {
		t.Fatalf("%+v %v", e, ok)
	}
	e.Params["version"] = "x"
	if e, _ := s.State("update:1.13.0"); e.Params["version"] != "1.13.0" {
		t.Error("State is not a copy")
	}
	if _, ok := s.State("login:10.0.0.1"); ok {
		t.Error("an event is a state")
	}
	if _, ok := s.State("nope"); ok {
		t.Error("a missing key")
	}
	if _, ok := (*Store)(nil).State("update:1.13.0"); ok {
		t.Error("nil store")
	}
}

// Without .vclu.json the settings are kept in memory.
func TestSettingsInMemory(t *testing.T) {
	c := &clock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	s := New(Options{Now: c.now, Logf: t.Logf})
	if err := s.SetSources(map[string]bool{SourceLog: true, SourceLogin: false}); err != nil {
		t.Fatal(err)
	}
	if err := s.Mute("a", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.Mute("b", 0); err != nil {
		t.Fatal(err)
	}
	nc := s.section()
	if !nc.Sources[SourceLog] || nc.Sources[SourceLogin] || len(nc.Sources) != 2 {
		t.Errorf("sources %v", nc.Sources)
	}
	if nc.Muted["a"].Until != "2026-11-21T13:00:00Z" || nc.Muted["b"].Until != "forever" || nc.Muted["b"].Title != "b" {
		t.Errorf("muted %v", nc.Muted)
	}
	if !s.Enabled(SourceLog) || s.Enabled(SourceLogin) {
		t.Error("switched sources not applied")
	}
	c.add(2 * time.Hour)
	if err := s.Unmute("b"); err != nil {
		t.Fatal(err)
	}
	if m := s.section().Muted; len(m) != 0 {
		t.Errorf("expired and unmuted kept: %v", m)
	}
}
