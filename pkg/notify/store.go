// Package notify keeps the panel's notifications (the bell): states that last until they are
// resolved (a new version, a module that does not load) and events (a failed login, a lock).
// Entries are kept in workspace/notifications.json; the sources switched and the keys muted
// are the "notifications" section of .vclu.json (settings.go).
package notify

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

// FileName is the store's file in the data directory's workspace/.
const FileName = "notifications.json"

// fileVersion is the format of the file.
const fileVersion = 1

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

// Entry is one notification as GET /api/notifications gives it. Msg and Params are the stable
// fields for machines; Title and Body are English, made from them; Detail is raw text that
// has no translation (a module's error, a log line).
type Entry struct {
	ID     int64             `json:"id"`
	Kind   string            `json:"kind"`
	Source string            `json:"source"`
	Level  string            `json:"level"`
	Key    string            `json:"key"`
	Title  string            `json:"title"`
	Body   string            `json:"body"`
	Detail string            `json:"detail,omitempty"`
	Msg    string            `json:"msg"`
	Params map[string]string `json:"params"`
	Link   string            `json:"link"`
	Count  int               `json:"count"`
	First  time.Time         `json:"first"`
	Last   time.Time         `json:"last"`
	Read   bool              `json:"read"`
	// Renew makes Add of a state already there unread again, and delivered
	Renew bool `json:"-"`
}

// Delivery is what subscribers get: a new entry, a renewed state, a count reaching 10 or
// 100, or a resolved state.
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
	cfgMu   sync.Mutex // one settings write at a time

	mu      sync.Mutex
	entries []*Entry
	nextID  int64
	sources map[string]bool // what the user switched; the rest keep their default
	muted   map[string]mute
	limits  map[string]*window
	timer   *time.Timer
	dirty   bool
	gen     int64 // counts changes; a write clears dirty only if none came meanwhile
	closed  bool
	subs    map[int]func(Delivery)
	nextSub int
}

type window struct {
	hour, minute     time.Time
	inHour, inMinute int
}

type file struct {
	Version int      `json:"version"`
	NextID  int64    `json:"next_id"`
	Entries []*Entry `json:"entries"`
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
	s := &Store{opts: opts, nextID: 1, limits: map[string]*window{}, subs: map[int]func(Delivery){}}
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
	if err == nil && f.Version > fileVersion {
		err = errors.New("written by a newer vclu")
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
}

// Subscribe calls fn with each Delivery, outside the store's lock; fn must not block. The
// func returned stops it.
func (s *Store) Subscribe(fn func(Delivery)) func() {
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	s.subs[id] = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
	}
}

// Enabled is whether source id adds entries now.
func (s *Store) Enabled(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled(id)
}

// enabled: an unknown source adds nothing; call it under mu.
func (s *Store) enabled(id string) bool {
	info, ok := Lookup(id)
	if !ok {
		return false
	}
	if on, set := s.sources[id]; set {
		return on
	}
	return info.DefaultOn
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
	if s.closed || !s.enabled(e.Source) || s.isMuted(e.Key, now) {
		s.mu.Unlock()
		return
	}
	s.expire(now)
	prepare(&e)
	switch {
	case e.Kind == KindState:
		if old := s.find(e.Key, true); old != nil {
			old.Level, old.Title, old.Body, old.Detail, old.Msg, old.Params, old.Link = e.Level, e.Title, e.Body, e.Detail, e.Msg, e.Params, e.Link
			if e.Renew {
				old.Read, old.Last = false, now
				out = append(out, Delivery{Entry: copyEntry(old)})
			}
		} else {
			out = append(out, s.insert(e, now))
		}
	default:
		e.Kind = KindEvent
		if old := s.find(e.Key, false); old != nil {
			old.Count++
			old.Last = now
			old.Body, old.Detail, old.Params, old.Level = e.Body, e.Detail, e.Params, e.Level
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
	subs := s.subscribers()
	s.mu.Unlock()
	deliver(subs, out)
}

// Resolve removes the states match picks, telling the subscribers.
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
	subs := s.subscribers()
	s.mu.Unlock()
	deliver(subs, out)
}

// State is the state with key.
func (s *Store) State(key string) (Entry, bool) {
	if s == nil {
		return Entry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.find(key, true); e != nil {
		return copyEntry(e), true
	}
	return Entry{}, false
}

// subscribers is a snapshot of them; call it under mu.
func (s *Store) subscribers() []func(Delivery) {
	out := make([]func(Delivery), 0, len(s.subs))
	for _, fn := range s.subs {
		out = append(out, fn)
	}
	return out
}

func deliver(subs []func(Delivery), out []Delivery) {
	for _, d := range out {
		for _, fn := range subs {
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
	b, err := json.MarshalIndent(file{Version: fileVersion, NextID: s.nextID, Entries: s.entries}, "", " ")
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
	e.ID, e.First, e.Last, e.Count, e.Read, e.Renew = s.nextID, now, now, 1, false, false
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
	e.Title, e.Body, e.Detail = clip(e.Title), clip(e.Body), clip(e.Detail)
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
