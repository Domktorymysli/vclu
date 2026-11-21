package notify

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// LuaBridge hands deliveries to Lua (Notifications._deliver) from its own goroutine: a log
// hook may run on the Lua loop, which must never wait for itself.
type LuaBridge struct {
	call       func(payload string) error
	ch         chan Delivery
	delivering atomic.Bool
	done       chan struct{}
	once       sync.Once
}

// luaNote is what a handler gets: no read mark, the panel's own.
type luaNote struct {
	ID       int64             `json:"id"`
	Kind     string            `json:"kind"`
	Source   string            `json:"source"`
	Level    string            `json:"level"`
	Key      string            `json:"key"`
	Msg      string            `json:"msg"`
	Params   map[string]string `json:"params"`
	Title    string            `json:"title"`
	Body     string            `json:"body"`
	Detail   string            `json:"detail,omitempty"`
	Link     string            `json:"link"`
	Count    int               `json:"count"`
	First    time.Time         `json:"first"`
	Last     time.Time         `json:"last"`
	Resolved bool              `json:"resolved"`
}

// NewLuaBridge starts the goroutine; call gets each delivery as JSON.
func NewLuaBridge(call func(payload string) error) *LuaBridge {
	b := &LuaBridge{call: call, ch: make(chan Delivery, 64), done: make(chan struct{})}
	go b.run()
	return b
}

// Deliver queues d when its source goes to Lua; with the queue full it is dropped. Never blocks.
func (b *LuaBridge) Deliver(d Delivery) {
	if info, ok := Lookup(d.Source); !ok || !info.ToLua {
		return
	}
	select {
	case <-b.done:
	case b.ch <- d:
	default:
	}
}

// Delivering is whether a handler runs now; LogHook leaves its lines out.
func (b *LuaBridge) Delivering() bool { return b.delivering.Load() }

// Close stops the goroutine; queued deliveries are dropped.
func (b *LuaBridge) Close() { b.once.Do(func() { close(b.done) }) }

func (b *LuaBridge) run() {
	for {
		select {
		case <-b.done:
			return
		case d := <-b.ch:
			js, err := json.Marshal(payload(d))
			if err != nil {
				continue
			}
			b.delivering.Store(true)
			err = b.call(string(js))
			b.delivering.Store(false)
			if err != nil {
				log.Printf("[NOTIFY] delivery to Lua: %v", err)
			}
		}
	}
}

func payload(d Delivery) luaNote {
	e := d.Entry
	return luaNote{ID: e.ID, Kind: e.Kind, Source: e.Source, Level: e.Level, Key: e.Key, Msg: e.Msg, Params: e.Params,
		Title: e.Title, Body: e.Body, Detail: e.Detail, Link: e.Link, Count: e.Count, First: e.First, Last: e.Last,
		Resolved: d.Resolved}
}
