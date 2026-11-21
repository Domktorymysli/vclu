package notify

import (
	"errors"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/logstream"
)

// Only lines tagged [ERROR] reach the bell, never [AUTH] (logins come as their own entries)
// nor a word guessed in an untagged line.
func TestLogHook(t *testing.T) {
	s, _, _ := newTestStore(t, t.TempDir())
	if err := s.SetSources(map[string]bool{SourceLog: true}); err != nil {
		t.Fatal(err)
	}
	ls := logstream.NewLogStream(io.Discard, 100)
	ls.AddHook(LogHook(s))
	l := log.New(ls, "", log.Ldate|log.Ltime)
	l.Printf("Warning: Failed to load translations: x")
	l.Printf("[LUA] ERROR: loaded 1/2 user module(s) — 1 FAILED")
	l.Printf("[AUTH] 10.0.0.5: 5 failed logins in a row, locked for 30s")
	l.Printf("[ERROR] [AUTH] something")
	l.Printf("[ERROR] [NOTIFY] Notifications.on handler: boom")
	l.Printf("[WARN] [Modbus] slow")
	l.Printf("[ERROR] [Modbus] timeout after 3000 ms on 192.168.0.9")
	l.Printf("[ERROR] [Modbus] timeout after 5000 ms on 192.168.0.9")
	vl := log.New(ls, "", log.Ldate|log.Ltime|log.Lshortfile)
	vl.Printf("[ERROR] plain error")

	list, _, _ := s.List()
	if len(list) != 2 {
		t.Fatalf("entries %+v", list)
	}
	m := list[1]
	if m.Key != "log:Modbus:timeout after # ms on #.#.#.#" || m.Count != 2 || m.Detail != "timeout after 5000 ms on 192.168.0.9" || m.Body != "" ||
		m.Params["source"] != "Modbus" || m.Title != "Error in the log: Modbus" || m.Link != "/debug" {
		t.Errorf("modbus %+v", m)
	}
	if p := list[0]; p.Key != "log::plain error" || p.Detail != "plain error" {
		t.Errorf("verbose %+v", p)
	}
}

// The logstream entry says which lines carry a level tag; Level keeps guessing by words.
func TestTagLevelExported(t *testing.T) {
	ls := logstream.NewLogStream(io.Discard, 10)
	l := log.New(ls, "", log.Ldate|log.Ltime)
	l.Printf("[ERROR] x")
	l.Printf("Failed to do y")
	h := ls.GetHistory()
	if h[0].TagLevel != "error" || h[1].TagLevel != "" || h[1].Level != "error" {
		t.Errorf("%+v", h)
	}
}

func TestLuaBridge(t *testing.T) {
	got := make(chan string, 4)
	var b *LuaBridge
	b = NewLuaBridge(func(js string) error {
		got <- js
		return errors.New("Lua not ready")
	})
	defer b.Close()
	b.Deliver(Delivery{Entry: Entry{ID: 6, Source: SourceLog, Key: "log:x"}}) // never to Lua
	b.Deliver(Delivery{Entry: Entry{ID: 7, Source: SourceUpdate, Key: "update:1.13.0", Read: true,
		Msg: MsgUpdate, Params: map[string]string{"version": "1.13.0"}}, Resolved: true})
	select {
	case js := <-got:
		if !strings.Contains(js, `"id":7`) || !strings.Contains(js, `"resolved":true`) || !strings.Contains(js, `"key":"update:1.13.0"`) ||
			!strings.Contains(js, `"params":{"version":"1.13.0"}`) || !strings.Contains(js, `"msg":"notify.update.available"`) || strings.Contains(js, `"read"`) {
			t.Errorf("payload %s", js)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("not delivered")
	}
	b.Close()
	b.Deliver(Delivery{}) // closed: dropped, does not block
}

// A full queue drops new deliveries without blocking, also once closed.
func TestLuaBridgeQueueFull(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	b := NewLuaBridge(func(string) error {
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		return nil
	})
	b.Deliver(Delivery{Entry: Entry{ID: 1, Source: SourceUpdate}})
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("not delivered")
	}
	for i := 0; i < 100; i++ { // 64 fit in the queue, the rest are dropped
		b.Deliver(Delivery{Entry: Entry{ID: int64(i + 2), Source: SourceUpdate}})
	}
	if n := b.Dropped(); n != 36 {
		t.Errorf("dropped %d, want 36", n)
	}
	b.Close()
	b.Deliver(Delivery{Entry: Entry{ID: 200, Source: SourceUpdate}})
	close(release)
	time.Sleep(50 * time.Millisecond)
	if n := calls.Load(); n < 1 || n > 65 {
		t.Errorf("%d calls, at most 65 fit", n)
	}
}

// A delivery that cannot be made JSON is skipped; the next one still goes.
func TestLuaBridgeSkipsBadPayload(t *testing.T) {
	got := make(chan string, 4)
	b := NewLuaBridge(func(js string) error { got <- js; return nil })
	defer b.Close()
	b.Deliver(Delivery{Entry: Entry{ID: 1, Source: SourceUpdate, First: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}})
	b.Deliver(Delivery{Entry: Entry{ID: 2, Source: SourceUpdate}})
	select {
	case js := <-got:
		if !strings.Contains(js, `"id":2`) {
			t.Errorf("payload %s", js)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("not delivered")
	}
}

// An error logged elsewhere while a handler runs still reaches the bell.
func TestLogDuringDelivery(t *testing.T) {
	s, _, _ := newTestStore(t, t.TempDir())
	if err := s.SetSources(map[string]bool{SourceLog: true}); err != nil {
		t.Fatal(err)
	}
	ls := logstream.NewLogStream(io.Discard, 100)
	ls.AddHook(LogHook(s))
	l := log.New(ls, "", log.Ldate|log.Ltime)
	inside, release := make(chan struct{}), make(chan struct{})
	b := NewLuaBridge(func(string) error { close(inside); <-release; return nil })
	defer b.Close()
	b.Deliver(Delivery{Entry: Entry{ID: 1, Source: SourceUpdate}})
	<-inside
	l.Printf("[ERROR] [MQTT] broker down")
	close(release)
	if list, _, _ := s.List(); len(list) != 1 || list[0].Params["source"] != "MQTT" {
		t.Errorf("entries %+v", list)
	}
}
