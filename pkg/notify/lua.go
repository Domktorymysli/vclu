package notify

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"
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

// NewLuaBridge starts the goroutine; call gets each delivery as JSON.
func NewLuaBridge(call func(payload string) error) *LuaBridge {
	b := &LuaBridge{call: call, ch: make(chan Delivery, 64), done: make(chan struct{})}
	go b.run()
	return b
}

// Deliver queues d; with the queue full it is dropped. Never blocks.
func (b *LuaBridge) Deliver(d Delivery) {
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
			js, err := json.Marshal(d)
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
