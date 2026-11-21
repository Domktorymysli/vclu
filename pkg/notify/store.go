// Package notify keeps the panel's notifications (the bell): states that last until they are
// resolved (a new version, a module that does not load) and events (a failed login, a lock).
// Entries are kept in workspace/notifications.json; the sources turned off and the keys muted
// are the "notifications" section of .vclu.json.
package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// Kinds of entries.
const (
	KindState = "state" // one per key, removed when resolved
	KindEvent = "event" // merged while unread, a new entry once read
)

// Levels, worst last.
const (
	LevelInfo    = "info"
	LevelWarning = "warning"
	LevelError   = "error"
)

// Sources, in the order the settings list them.
const (
	SourceUpdate = "update"
	SourcePlugin = "plugin"
	SourceModule = "module"
	SourceLog    = "log"
	SourceLogin  = "login"
	SourceLock   = "lock"
)

// Sources are the ids the settings take.
var Sources = []string{SourceUpdate, SourcePlugin, SourceModule, SourceLog, SourceLogin, SourceLock}

// FileName is the store's file in the data directory's workspace/.
const FileName = "notifications.json"

// Path is where the store of the data directory dir is kept.
func Path(dir string) string { return filepath.Join(dir, "workspace", FileName) }

const (
	DefaultMax      = 200
	DefaultDebounce = 2 * time.Second
	maxText         = 300
	readExpiry      = 14 * 24 * time.Hour
	perHour         = 30 // new events of a source an hour
	logPerMinute    = 20
)

// Entry is one notification as GET /api/notifications gives it. Title and Body are English;
// Msg and Params let the panel translate them.
type Entry struct {
	ID     int64             `json:"id"`
	Kind   string            `json:"kind"`
	Source string            `json:"source"`
	Level  string            `json:"level"`
	Key    string            `json:"key"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Msg    string            `json:"msg"`
	Params map[string]string `json:"params"`
	Link   string            `json:"link"`
	Count  int               `json:"count"`
	First  time.Time         `json:"first"`
	Last   time.Time         `json:"last"`
	Read   bool              `json:"read"`
	IP     string            `json:"ip,omitempty"`
}

// Delivery is what Notifications.on handlers get: a new entry, a count reaching 10 or 100,
// or a resolved state.
type Delivery struct {
	Entry
	Resolved bool `json:"resolved"`
}

// Options configure a Store; all are optional.
type Options struct {
	Path      string // the file; "" keeps nothing on disk
	ConfigDir string // the directory of .vclu.json; "" keeps the settings in memory
	Max       int    // DefaultMax when 0
	Debounce  time.Duration
	Now       func() time.Time
	Logf      func(format string, args ...any)
}

// Store is safe for concurrent use; a nil *Store adds nothing.
type Store struct {
	opts Options

	flushMu sync.Mutex // one write at a time, so an older snapshot never lands last

	mu        sync.Mutex
	entries   []*Entry
	nextID    int64
	titles    map[string]string // muted key -> its title, for the settings
	disabled  map[string]bool
	muted     map[string]time.Time // zero: forever
	limits    map[string]*window
	timer     *time.Timer
	dirty     bool
	gen       int64 // counts changes; a write clears dirty only if none came meanwhile
	closed    bool
	onDeliver func(Delivery)
}

type window struct {
	hour, minute     time.Time
	inHour, inMinute int
}

type file struct {
	NextID      int64             `json:"next_id"`
	Entries     []*Entry          `json:"entries"`
	MutedTitles map[string]string `json:"muted_titles,omitempty"`
}

// New loads the store; an unreadable file is logged and the store starts empty.
func New(opts Options) *Store {
	if opts.Max <= 0 {
		opts.Max = DefaultMax
	}
	if opts.Debounce <= 0 {
		opts.Debounce = DefaultDebounce
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	s := &Store{opts: opts, nextID: 1, titles: map[string]string{}, limits: map[string]*window{}}
	s.loadConfig()
	if opts.Path != "" {
		s.loadFile()
	}
	return s
}

func (s *Store) loadFile() {
	b, err := os.ReadFile(s.opts.Path)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	var f file
	if err == nil {
		err = json.Unmarshal(b, &f)
	}
	if err != nil {
		s.opts.Logf("[NOTIFY] %s not read, starting empty: %v", s.opts.Path, err)
		return
	}
	for _, e := range f.Entries {
		if e == nil || e.ID <= 0 {
			continue
		}
		if e.Params == nil {
			e.Params = map[string]string{}
		}
		s.entries = append(s.entries, e)
		s.nextID = max(s.nextID, e.ID+1)
	}
	s.nextID = max(s.nextID, f.NextID)
	if f.MutedTitles != nil {
		s.titles = f.MutedTitles
	}
}

func (s *Store) loadConfig() {
	s.disabled = map[string]bool{SourceLog: true}
	s.muted = map[string]time.Time{}
	if s.opts.ConfigDir == "" {
		return
	}
	mc, err := config.ReadMasterConfig(s.opts.ConfigDir)
	if err != nil {
		s.opts.Logf("[NOTIFY] settings not read, using defaults: %v", err)
		return
	}
	if mc == nil || mc.Notifications == nil {
		return
	}
	s.disabled, s.muted = parseConfig(mc.Notifications)
}

func parseConfig(nc *config.VCLUNotificationsConfig) (map[string]bool, map[string]time.Time) {
	disabled := map[string]bool{SourceLog: true}
	if nc.Disabled != nil {
		disabled = map[string]bool{}
		for _, id := range *nc.Disabled {
			disabled[id] = true
		}
	}
	muted := map[string]time.Time{}
	for k, v := range nc.Muted {
		if v == "forever" {
			muted[k] = time.Time{}
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			muted[k] = t
		}
	}
	return disabled, muted
}

// SetOnDeliver sets what gets each Delivery; it is called outside the store's lock and must
// not block. Log entries are never delivered.
func (s *Store) SetOnDeliver(fn func(Delivery)) {
	s.mu.Lock()
	s.onDeliver = fn
	s.mu.Unlock()
}

// Add records e: Kind, Source and Level are required, Key merges. An empty Title or Body is
// made from Msg and Params. It never logs, so a log hook may call it.
func (s *Store) Add(e Entry) {
	if s == nil {
		return
	}
	var out []Delivery
	s.mu.Lock()
	now := s.opts.Now()
	if s.closed || s.disabled[e.Source] || s.isMuted(e.Key, now) {
		s.mu.Unlock()
		return
	}
	s.expire(now)
	prepare(&e)
	switch {
	case e.Kind == KindState:
		if old := s.find(e.Key, true); old != nil {
			old.Level, old.Title, old.Body, old.Msg, old.Params, old.Link, old.IP = e.Level, e.Title, e.Body, e.Msg, e.Params, e.Link, e.IP
		} else {
			out = append(out, s.insert(e, now))
		}
	default:
		e.Kind = KindEvent
		if old := s.find(e.Key, false); old != nil {
			old.Count++
			old.Last = now
			old.Body, old.Params, old.Level, old.IP = e.Body, e.Params, e.Level, e.IP
			if old.Count == 10 || old.Count == 100 {
				out = append(out, Delivery{Entry: copyEntry(old)})
			}
		} else if n, ok := s.allow(e, now); ok {
			out = append(out, s.insert(e, now))
		} else {
			out = append(out, s.suppressed(e, n, now)...)
		}
	}
	s.changed()
	fn := s.onDeliver
	s.mu.Unlock()
	deliver(fn, out)
}

// Resolve removes the states match picks, telling the handlers.
func (s *Store) Resolve(match func(Entry) bool) {
	if s == nil {
		return
	}
	var out []Delivery
	s.mu.Lock()
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.Kind == KindState && match(*e) {
			out = append(out, Delivery{Entry: copyEntry(e), Resolved: true})
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	if len(out) > 0 {
		s.changed()
	}
	fn := s.onDeliver
	s.mu.Unlock()
	deliver(fn, out)
}

func deliver(fn func(Delivery), out []Delivery) {
	if fn == nil {
		return
	}
	for _, d := range out {
		if d.Source != SourceLog {
			fn(d)
		}
	}
}

// List gives the entries, the last changed first, the unread count and the worst unread level.
func (s *Store) List() ([]Entry, int, string) {
	if s == nil {
		return []Entry{}, 0, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.expire(s.opts.Now()) {
		s.changed()
	}
	out := make([]Entry, 0, len(s.entries))
	unread, worst := 0, ""
	for _, e := range s.entries {
		out = append(out, copyEntry(e))
		if !e.Read {
			unread++
			if rank(e.Level) > rank(worst) {
				worst = e.Level
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Last.Equal(out[j].Last) {
			return out[i].Last.After(out[j].Last)
		}
		return out[i].ID > out[j].ID
	})
	return out, unread, worst
}

// MarkRead marks the entries with these ids read; all marks every one.
func (s *Store) MarkRead(ids []int64, all bool) {
	if s == nil {
		return
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for _, e := range s.entries {
		if !e.Read && (all || want[e.ID]) {
			e.Read, changed = true, true
		}
	}
	if changed {
		s.changed()
	}
}

// Clear removes every event; states stay until resolved.
func (s *Store) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.Kind == KindState {
			kept = append(kept, e)
		}
	}
	s.entries = kept
	s.changed()
}

// Mute adds nothing with key for d (0: forever) and removes its entry.
func (s *Store) Mute(key string, d time.Duration) error {
	if key == "" {
		return errors.New("no key")
	}
	s.mu.Lock()
	var until time.Time
	if d > 0 {
		until = s.opts.Now().Add(d).UTC().Truncate(time.Second)
	}
	title := key
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.Key == key {
			title = e.Title
			continue
		}
		kept = append(kept, e)
	}
	s.entries = kept
	s.titles[key] = title
	s.changed()
	s.mu.Unlock()
	return s.saveConfig(func(nc *config.VCLUNotificationsConfig) {
		if nc.Muted == nil {
			nc.Muted = map[string]string{}
		}
		if until.IsZero() {
			nc.Muted[key] = "forever"
		} else {
			nc.Muted[key] = until.Format(time.RFC3339)
		}
	})
}

// Unmute lets key add entries again.
func (s *Store) Unmute(key string) error {
	s.mu.Lock()
	delete(s.titles, key)
	s.changed()
	s.mu.Unlock()
	return s.saveConfig(func(nc *config.VCLUNotificationsConfig) { delete(nc.Muted, key) })
}

// SetDisabled turns off these sources (and on every other one); an unknown id is an error.
func (s *Store) SetDisabled(ids []string) error {
	list := []string{}
	for _, id := range ids {
		known := false
		for _, src := range Sources {
			known = known || id == src
		}
		if !known {
			return fmt.Errorf("unknown source %q", id)
		}
		list = append(list, id)
	}
	return s.saveConfig(func(nc *config.VCLUNotificationsConfig) { nc.Disabled = &list })
}

// Source is a source and whether it adds entries.
type Source struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// Muted is a muted key: until is RFC 3339 or "forever".
type Muted struct {
	Key   string `json:"key"`
	Until string `json:"until"`
	Title string `json:"title"`
}

// Settings is GET /api/notifications/settings.
type Settings struct {
	Sources []Source `json:"sources"`
	Muted   []Muted  `json:"muted"`
}

// Settings gives the sources and the keys muted now.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now()
	out := Settings{Sources: []Source{}, Muted: []Muted{}}
	for _, id := range Sources {
		out.Sources = append(out.Sources, Source{ID: id, Enabled: !s.disabled[id]})
	}
	for k, until := range s.muted {
		if !until.IsZero() && !now.Before(until) {
			continue
		}
		m := Muted{Key: k, Until: "forever", Title: s.titles[k]}
		if !until.IsZero() {
			m.Until = until.UTC().Format(time.RFC3339)
		}
		if m.Title == "" {
			m.Title = k
		}
		out.Muted = append(out.Muted, m)
	}
	sort.Slice(out.Muted, func(i, j int) bool { return out.Muted[i].Key < out.Muted[j].Key })
	return out
}

// saveConfig changes the section through UpdateMasterConfig, dropping expired mutes.
func (s *Store) saveConfig(fn func(*config.VCLUNotificationsConfig)) error {
	if s.opts.ConfigDir == "" {
		s.mu.Lock()
		nc := s.memConfig()
		fn(nc)
		s.disabled, s.muted = parseConfig(nc)
		s.mu.Unlock()
		return nil
	}
	var saved *config.VCLUNotificationsConfig
	err := config.UpdateMasterConfig(s.opts.ConfigDir, func(mc *config.VCLUConfig) error {
		if mc.Notifications == nil {
			mc.Notifications = &config.VCLUNotificationsConfig{}
		}
		fn(mc.Notifications)
		now := s.opts.Now()
		for k, v := range mc.Notifications.Muted {
			if t, err := time.Parse(time.RFC3339, v); v != "forever" && (err != nil || !now.Before(t)) {
				delete(mc.Notifications.Muted, k)
			}
		}
		saved = mc.Notifications
		return nil
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.disabled, s.muted = parseConfig(saved)
	s.mu.Unlock()
	return nil
}

// memConfig is the settings as a section, for a store without .vclu.json.
func (s *Store) memConfig() *config.VCLUNotificationsConfig {
	list := []string{}
	for id := range s.disabled {
		list = append(list, id)
	}
	nc := &config.VCLUNotificationsConfig{Disabled: &list, Muted: map[string]string{}}
	for k, t := range s.muted {
		if t.IsZero() {
			nc.Muted[k] = "forever"
		} else {
			nc.Muted[k] = t.Format(time.RFC3339)
		}
	}
	return nc
}

// Flush writes the file now.
func (s *Store) Flush() error {
	if s == nil || s.opts.Path == "" {
		return nil
	}
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	b, err := json.MarshalIndent(file{NextID: s.nextID, Entries: s.entries, MutedTitles: s.titles}, "", " ")
	gen := s.gen
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if err := writeAtomic(s.opts.Path, b); err != nil {
		return err
	}
	s.mu.Lock()
	if s.gen == gen {
		s.dirty = false
	}
	s.mu.Unlock()
	return nil
}

// Close writes what is pending; afterwards the store adds and writes nothing.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	dirty := s.dirty
	s.mu.Unlock()
	var err error
	if dirty {
		err = s.Flush()
	}
	s.flushMu.Lock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.flushMu.Unlock()
	return err
}

// Discard closes the store without writing: for a data directory just wiped.
func (s *Store) Discard() {
	if s == nil {
		return
	}
	s.flushMu.Lock() // a write under way lands before the caller removes what is left
	s.mu.Lock()
	if s.timer != nil {
		s.timer.Stop()
	}
	s.closed = true
	s.mu.Unlock()
	s.flushMu.Unlock()
}

// changed plans a write after the debounce; call it under mu.
func (s *Store) changed() {
	s.dirty = true
	s.gen++
	if s.opts.Path == "" || s.closed || s.timer != nil {
		return
	}
	s.timer = time.AfterFunc(s.opts.Debounce, func() {
		s.mu.Lock()
		s.timer = nil
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return
		}
		if err := s.Flush(); err != nil {
			s.opts.Logf("[NOTIFY] not saved: %v", err)
		}
	})
}

func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+FileName+".tmp-*") // created 0600
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *Store) isMuted(key string, now time.Time) bool {
	until, ok := s.muted[key]
	return ok && key != "" && (until.IsZero() || now.Before(until))
}

// find is the state with key, or the unread event with it.
func (s *Store) find(key string, state bool) *Entry {
	if key == "" {
		return nil
	}
	for _, e := range s.entries {
		if e.Key != key {
			continue
		}
		if state && e.Kind == KindState || !state && e.Kind == KindEvent && !e.Read {
			return e
		}
	}
	return nil
}

func (s *Store) insert(e Entry, now time.Time) Delivery {
	e.ID, e.First, e.Last, e.Count, e.Read = s.nextID, now, now, 1, false
	s.nextID++
	s.entries = append(s.entries, &e)
	for len(s.entries) > s.opts.Max {
		s.dropOne()
	}
	return Delivery{Entry: copyEntry(&e)}
}

// dropOne removes the oldest read entry, else the oldest event, else the oldest one.
func (s *Store) dropOne() {
	pick := -1
	for _, pass := range []func(*Entry) bool{
		func(e *Entry) bool { return e.Read },
		func(e *Entry) bool { return e.Kind == KindEvent },
		func(*Entry) bool { return true },
	} {
		for i, e := range s.entries {
			if pass(e) && (pick < 0 || e.Last.Before(s.entries[pick].Last)) {
				pick = i
			}
		}
		if pick >= 0 {
			break
		}
	}
	s.entries = append(s.entries[:pick], s.entries[pick+1:]...)
}

// expire removes read events older than 14 days; call it under mu.
func (s *Store) expire(now time.Time) bool {
	kept := s.entries[:0]
	for _, e := range s.entries {
		if e.Kind == KindEvent && e.Read && now.Sub(e.Last) > readExpiry {
			continue
		}
		kept = append(kept, e)
	}
	gone := len(kept) < len(s.entries)
	s.entries = kept
	return gone
}

// limitKey is what the hourly limit counts: each kind of message on its own, so a flood of
// one (locks of made-up dashboards) does not hide another (login locks).
func limitKey(e Entry) string {
	if e.Msg != "" {
		return e.Source + ":" + e.Msg
	}
	return e.Source
}

// allow counts a new event; when over the limit it gives the hour's window start.
func (s *Store) allow(e Entry, now time.Time) (time.Time, bool) {
	key := limitKey(e)
	w := s.limits[key]
	if w == nil {
		w = &window{}
		s.limits[key] = w
	}
	if now.Sub(w.hour) >= time.Hour {
		w.hour, w.inHour = now, 0
	}
	if now.Sub(w.minute) >= time.Minute {
		w.minute, w.inMinute = now, 0
	}
	// logs share one window a minute, whatever the message
	lw := w
	if e.Source == SourceLog {
		if lw = s.limits[SourceLog+":minute"]; lw == nil {
			lw = &window{}
			s.limits[SourceLog+":minute"] = lw
		}
		if now.Sub(lw.minute) >= time.Minute {
			lw.minute, lw.inMinute = now, 0
		}
	}
	if w.inHour >= perHour || e.Source == SourceLog && lw.inMinute >= logPerMinute {
		return w.hour, false
	}
	w.inHour++
	lw.inMinute++
	return w.hour, true
}

// suppressed counts an event over the limit in one entry per message and hour.
func (s *Store) suppressed(e Entry, hour, now time.Time) []Delivery {
	key := "suppressed:" + limitKey(e) + ":" + strconv.FormatInt(hour.Unix(), 10)
	if old := s.find(key, false); old != nil {
		old.Count++
		old.Last = now
		old.Params["count"] = strconv.Itoa(old.Count)
		old.Title = render(old.Msg, old.Params, true)
		return nil
	}
	n := Entry{Kind: KindEvent, Source: e.Source, Level: LevelInfo, Key: key, Msg: MsgSuppressed,
		Params: map[string]string{"count": "1", "source": e.Source}}
	prepare(&n)
	return []Delivery{s.insert(n, now)}
}

func prepare(e *Entry) {
	if e.Params == nil {
		e.Params = map[string]string{}
	}
	if e.Level == "" {
		e.Level = LevelInfo
	}
	if e.Title == "" {
		e.Title = render(e.Msg, e.Params, true)
	}
	if e.Body == "" {
		e.Body = render(e.Msg, e.Params, false)
	}
	e.Title, e.Body = clip(e.Title), clip(e.Body)
}

func clip(s string) string {
	if utf8.RuneCountInString(s) <= maxText {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:maxText-1])) + "…"
}

func rank(level string) int {
	switch level {
	case LevelInfo:
		return 1
	case LevelWarning:
		return 2
	case LevelError:
		return 3
	}
	return 0
}

func copyEntry(e *Entry) Entry {
	c := *e
	c.Params = make(map[string]string, len(e.Params))
	for k, v := range e.Params {
		c.Params[k] = v
	}
	return c
}

// Modules records a module load report: failed maps a file in modules/ to its error, and the
// states of every other module are resolved.
func (s *Store) Modules(failed map[string]string) {
	if s == nil {
		return
	}
	keys := map[string]string{}
	for name := range failed {
		keys[moduleKey(name)] = name
	}
	s.Resolve(func(e Entry) bool {
		_, still := keys[e.Key]
		return strings.HasPrefix(e.Key, "module:") && !still
	})
	names := make([]string, 0, len(failed))
	for n := range failed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		link := "/editor?file=modules/" + url.QueryEscape(name)
		if name == ModulesDir {
			link = "/editor"
		}
		s.Add(Entry{
			Kind: KindState, Source: SourceModule, Level: LevelError, Key: moduleKey(name),
			Msg: MsgModule, Params: map[string]string{"module": name}, Body: failed[name], Link: link,
		})
	}
}

// ModulesDir is the name a load report gives an error of the modules/ directory itself.
const ModulesDir = "modules/"

func moduleKey(name string) string {
	if name == ModulesDir {
		return "module:dir"
	}
	return "module:" + name
}
