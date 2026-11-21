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
	var skip atomic.Bool
	ls.AddHook(LogHook(s, skip.Load))
	l := log.New(ls, "", log.Ldate|log.Ltime)
	l.Printf("Warning: Failed to load translations: x")
	l.Printf("[LUA] ERROR: loaded 1/2 user module(s) — 1 FAILED")
	l.Printf("[AUTH] 10.0.0.5: 5 failed logins in a row, locked for 30s")
	l.Printf("[ERROR] [AUTH] something")
	l.Printf("[ERROR] [NOTIFY] Notifications.on handler: boom")
	l.Printf("[WARN] [Modbus] slow")
	l.Printf("[ERROR] [Modbus] timeout after 3000 ms on 192.168.0.9")
	l.Printf("[ERROR] [Modbus] timeout after 5000 ms on 192.168.0.9")
	skip.Store(true)
	l.Printf("[ERROR] [Telegram] while a handler runs")
	skip.Store(false)
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
		if !b.Delivering() {
			t.Error("not delivering during the call")
		}
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
