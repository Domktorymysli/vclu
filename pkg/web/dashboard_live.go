package web

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// Dashboard values pushed over a WebSocket, GET /api/dashboard/:id/ws, instead of polled from
// /values. The same auth as /values; JSON text messages. The server sends:
//
//   - {"type":"values","values":{...}} first: what /values answers.
//   - {"type":"patch","values":{...},"removed":[...]} after a change: the widgets whose entry
//     changed, and those gone from the answer (a hidden chip, a getValue that gave nil).
//   - {"type":"definition"} after a Lua reload: the client fetches the definition again; a
//     "values" follows.
//   - {"type":"error","error":"..."} while getValues fails; a "values" follows when it works.
//   - {"type":"pong","id":n} at once for each {"type":"ping","id":n} from the client, the only
//     message it sends: the page measures the round trip, and a missing pong tells it the
//     connection is dead (a browser does not see WebSocket pings).
//
// One feed per dashboard (per key of dashboardValues) reads getValues for all its sockets:
// after a state change or an action, and every poll interval for what StateBus does not see
// (the time, an HTTP answer kept in a variable). After a quiet spell a change is read within
// liveMinGap; while changes keep coming the wait doubles up to liveMaxGap, so a noisy sensor
// costs at most a read a second. A dashboard nobody watches is not read.

const (
	liveMinGap     = 250 * time.Millisecond
	liveMaxGap     = time.Second
	liveDefaultGap = 2 * time.Second
	livePingEvery  = 30 * time.Second
	livePongWait   = 75 * time.Second
	liveWriteWait  = 10 * time.Second
	liveMaxSockets = 64
	liveMaxPerIP   = 8
	liveSendBuffer = 16
	liveMsgCredits = 30 // messages a client may send each liveMsgWindow (a page pings every few s)
	liveMsgWindow  = 10 * time.Second
)

// liveAuthEvery: how often an open socket checks its auth again (tests shorten it).
var liveAuthEvery = 15 * time.Second

// Close codes besides the standard ones: the viewer may no longer see the dashboard (a PIN
// changed, a session ended), or it is gone. The client falls back to /values, which says why.
const (
	liveCloseDenied = 4401
	liveCloseGone   = 4404
)

var liveUpgrader = websocket.Upgrader{
	ReadBufferSize:  512,
	WriteBufferSize: 4096,
	CheckOrigin:     liveSameOrigin,
}

// liveSameOrigin is gorilla's default check, also used before a feed starts: a page of another
// site cannot open a socket with this site's cookies (CSRF protection does not cover GET).
func liveSameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && strings.EqualFold(u.Host, r.Host)
}

// liveFeeds are the feeds of the dashboards watched now.
type liveFeeds struct {
	mu      sync.Mutex
	feeds   map[string]*liveFeed
	sockets int
	perIP   map[string]int
	closing chan struct{} // closed on shutdown
	closed  bool
	reload  chan struct{} // closed and replaced on every Lua reload: sockets check their auth
}

func newLiveFeeds() *liveFeeds {
	return &liveFeeds{feeds: map[string]*liveFeed{}, perIP: map[string]int{}, closing: make(chan struct{}),
		reload: make(chan struct{})}
}

// reloads is closed at the next Lua reload.
func (lf *liveFeeds) reloads() <-chan struct{} {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	return lf.reload
}

type liveFeed struct {
	dash  string
	read  func() (string, error)
	every time.Duration
	wake  chan struct{} // read again soon; buffered 1
	stop  chan struct{}
	fresh atomic.Bool // the next answer goes out as "values" (after a reload)

	mu   sync.Mutex
	subs map[*liveSub]struct{}
	full []byte // the last "values" message, for a socket that joins
}

type liveSub struct {
	ip   string
	send chan []byte
	gone chan struct{} // closed when the feed drops the socket: it does not keep up
}

// join adds a socket from ip to the feed for key, starting it; false when there are too many
// sockets, from that address or at all, or the server is stopping.
func (lf *liveFeeds) join(ip, dash, key string, every time.Duration, read func() (string, error)) (*liveSub, bool) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if lf.closed || lf.sockets >= liveMaxSockets || lf.perIP[ip] >= liveMaxPerIP {
		return nil, false
	}
	f := lf.feeds[key]
	if f == nil {
		if every <= 0 {
			every = liveDefaultGap
		}
		f = &liveFeed{dash: dash, read: read, every: max(every, time.Second), wake: make(chan struct{}, 1),
			stop: make(chan struct{}), subs: map[*liveSub]struct{}{}}
		f.wake <- struct{}{}
		lf.feeds[key] = f
		go f.run()
	}
	sub := &liveSub{ip: ip, send: make(chan []byte, liveSendBuffer), gone: make(chan struct{})}
	f.mu.Lock()
	f.subs[sub] = struct{}{}
	if f.full != nil {
		sub.send <- f.full
	}
	f.mu.Unlock()
	lf.sockets++
	lf.perIP[ip]++
	return sub, true
}

// leave removes a socket; the feed stops with its last one.
func (lf *liveFeeds) leave(key string, sub *liveSub) {
	lf.mu.Lock()
	defer lf.mu.Unlock()
	lf.sockets--
	if lf.perIP[sub.ip]--; lf.perIP[sub.ip] <= 0 {
		delete(lf.perIP, sub.ip)
	}
	f := lf.feeds[key]
	if f == nil {
		return
	}
	f.mu.Lock()
	delete(f.subs, sub)
	empty := len(f.subs) == 0
	f.mu.Unlock()
	if empty {
		close(f.stop)
		delete(lf.feeds, key)
	}
}

// changed wakes every feed: a state changed. Called from the Lua loop, so it never waits.
func (lf *liveFeeds) changed() {
	lf.each(func(f *liveFeed) { f.poke() })
}

// poke wakes the feeds of dashboard dash: an action ran there.
func (lf *liveFeeds) poke(dash string) {
	lf.each(func(f *liveFeed) {
		if f.dash == dash {
			f.poke()
		}
	})
}

// reloaded tells every socket to fetch the definition again; full values follow.
func (lf *liveFeeds) reloaded() {
	if lf == nil {
		return
	}
	lf.mu.Lock()
	close(lf.reload)
	lf.reload = make(chan struct{})
	lf.mu.Unlock()
	msg := liveMessage(map[string]any{"type": "definition"})
	lf.each(func(f *liveFeed) {
		f.fresh.Store(true)
		f.forget()
		f.broadcast(msg, nil)
		f.poke()
	})
}

// shutdown closes every socket (http.Server.Shutdown does not wait for hijacked connections).
func (lf *liveFeeds) shutdown() {
	if lf == nil {
		return
	}
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if !lf.closed {
		lf.closed = true
		close(lf.closing)
	}
}

func (lf *liveFeeds) each(fn func(*liveFeed)) {
	if lf == nil {
		return
	}
	lf.mu.Lock()
	defer lf.mu.Unlock()
	for _, f := range lf.feeds {
		fn(f)
	}
}

func (f *liveFeed) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *liveFeed) run() {
	tick := time.NewTicker(f.every)
	defer tick.Stop()
	var last map[string]json.RawMessage
	var failed string
	var lastRead time.Time
	var gap time.Duration
	for {
		woken := false
		select {
		case <-f.stop:
			return
		case <-f.wake:
			woken = true
		case <-tick.C:
		}
		if woken {
			gap = liveGap(gap, time.Since(lastRead), f.every)
		}
		if d := gap - time.Since(lastRead); woken && d > 0 {
			select {
			case <-f.stop:
				return
			case <-time.After(d):
			}
		}
		select { // a change during the wait is in this read
		case <-f.wake:
		default:
		}
		lastRead = time.Now()
		if f.fresh.Swap(false) {
			last = nil
		}

		values, err := f.values()
		if f.fresh.Load() {
			// a reload came during the read: these may be the old values
			f.poke()
			continue
		}
		if err != nil {
			if failed != err.Error() {
				failed = err.Error()
				// kept as the message for a socket that joins while it lasts
				msg := liveMessage(map[string]any{"type": "error", "error": liveErrorText(f.dash, err)})
				f.broadcast(msg, msg)
			}
			last = nil
			continue
		}
		failed = ""
		full := liveMessage(map[string]any{"type": "values", "values": values})
		if last == nil {
			f.broadcast(full, full)
		} else if msg := livePatch(last, values); msg != nil {
			f.broadcast(msg, full)
		}
		last = values
	}
}

// values reads getValues, each widget's entry in a canonical form (sorted keys), so an entry
// that did not change compares equal whatever order Lua gave its keys in.
func (f *liveFeed) values() (map[string]json.RawMessage, error) {
	raw, err := f.read()
	if err != nil {
		return nil, err
	}
	var entries map[string]json.RawMessage
	if strings.TrimSpace(raw) == "[]" {
		// Lua's JSON gives an empty table as [] (a dashboard with no values, or none left)
		return map[string]json.RawMessage{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, err
	}
	// {"error": "..."}: the dashboard is gone (as /values answers it)
	var msg string
	if e, ok := entries["error"]; ok && json.Unmarshal(e, &msg) == nil {
		return nil, liveError(msg)
	}
	for k, v := range entries {
		dec := json.NewDecoder(bytes.NewReader(v))
		dec.UseNumber()
		var x any
		if dec.Decode(&x) == nil {
			if b, err := json.Marshal(x); err == nil {
				entries[k] = b
			}
		}
	}
	return entries, nil
}

type liveError string

func (e liveError) Error() string { return string(e) }

// liveErrorText is what viewers see of a failed read: getValues' own error ("Dashboard not
// found"), never a Lua error (paths, a stack), which goes to the log.
func liveErrorText(dash string, err error) string {
	if e, ok := err.(liveError); ok {
		return string(e)
	}
	log.Printf("[DASHBOARD] %s live values: %v", dash, err)
	return "dashboard unavailable"
}

// liveGap is the wait before a read after a change: liveMinGap when the last read was a poll
// interval ago or more, else twice the last wait, up to liveMaxGap.
func liveGap(prev, sinceLast, every time.Duration) time.Duration {
	if sinceLast >= every {
		return liveMinGap
	}
	return min(max(2*prev, liveMinGap), liveMaxGap)
}

// livePatch is the patch from last to now, nil when nothing changed.
func livePatch(last, now map[string]json.RawMessage) []byte {
	changed := map[string]json.RawMessage{}
	for k, v := range now {
		if !bytes.Equal(last[k], v) {
			changed[k] = v
		}
	}
	removed := []string{}
	for k := range last {
		if _, ok := now[k]; !ok {
			removed = append(removed, k)
		}
	}
	if len(changed) == 0 && len(removed) == 0 {
		return nil
	}
	sort.Strings(removed)
	return liveMessage(map[string]any{"type": "patch", "values": changed, "removed": removed})
}

// broadcast sends msg to every socket and keeps full, when given, for those that join later. A
// socket whose queue is full is dropped: it reconnects and starts again from full values.
func (f *liveFeed) broadcast(msg, full []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if full != nil {
		f.full = full
	}
	for sub := range f.subs {
		select {
		case sub.send <- msg:
		default:
			delete(f.subs, sub)
			close(sub.gone)
		}
	}
}

// forget drops the kept values: a socket that joins now waits for the next read.
func (f *liveFeed) forget() {
	f.mu.Lock()
	f.full = nil
	f.mu.Unlock()
}

func liveMessage(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// handleAPIDashboardWS serves a dashboard's values over a WebSocket (see the top of this file).
func (s *Server) handleAPIDashboardWS(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil || s.live == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}
	if !websocket.IsWebSocketUpgrade(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "WebSocket only"})
		return
	}
	if !liveSameOrigin(c.Request) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}
	status, msg, poll := s.dashboardAccess(c, id)
	if status != 0 {
		c.JSON(status, gin.H{"error": msg})
		return
	}
	key, read := s.dashboardValues(c, id)
	sub, ok := s.live.join(limitKey(c), id, key, poll, read)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many live dashboards"})
		return
	}
	defer s.live.leave(key, sub)

	conn, err := liveUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // the upgrader answered
	}
	defer func() { _ = conn.Close() }()
	closeWith := func(code int, text string) {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, text), time.Now().Add(time.Second))
	}

	// the client sends its pings, pongs and the close; a ping is answered by the loop below
	done := make(chan struct{})
	pings := make(chan []byte, 4)
	conn.SetReadLimit(512)
	_ = conn.SetReadDeadline(time.Now().Add(livePongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(livePongWait)) })
	go func() {
		defer close(done)
		credits, refill := liveMsgCredits, time.Now().Add(liveMsgWindow)
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if now := time.Now(); now.After(refill) {
				credits, refill = liveMsgCredits, now.Add(liveMsgWindow)
			}
			if credits--; credits < 0 {
				closeWith(websocket.ClosePolicyViolation, "rate limit exceeded")
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(livePongWait))
			if pong := livePong(b); pong != nil {
				select {
				case pings <- pong:
				default: // pinging faster than it reads answers: those are dropped
				}
			}
		}
	}()

	ping := time.NewTicker(livePingEvery)
	defer ping.Stop()
	auth := time.NewTicker(liveAuthEvery)
	defer auth.Stop()
	reload := s.live.reloads()
	for {
		checkAuth := false
		select {
		case m := <-sub.send:
			_ = conn.SetWriteDeadline(time.Now().Add(liveWriteWait))
			if err := conn.WriteMessage(websocket.TextMessage, m); err != nil {
				return
			}
		case m := <-pings:
			_ = conn.SetWriteDeadline(time.Now().Add(liveWriteWait))
			if err := conn.WriteMessage(websocket.TextMessage, m); err != nil {
				return
			}
		case <-sub.gone:
			closeWith(websocket.CloseTryAgainLater, "too slow")
			return
		case <-done:
			return
		case <-ping.C:
			if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(liveWriteWait)); err != nil {
				return
			}
		case <-auth.C:
			checkAuth = true
		case <-reload:
			reload = s.live.reloads()
			checkAuth = true
		case <-s.live.closing:
			closeWith(websocket.CloseGoingAway, "")
			return
		}
		// a session ended, the PIN cookie expired, or a reload changed the PIN or the auth: a Lua
		// error (500) is not a refusal and keeps the socket
		if checkAuth {
			// a remote viewer's token, members list or roles may have changed too
			status, msg, _ := s.dashboardAccess(c, id)
			if st := viewerRecheck(c, c.Request.URL.Path); st != 0 {
				status, msg = st, "access ended"
			}
			switch status {
			case http.StatusUnauthorized, http.StatusForbidden:
				closeWith(liveCloseDenied, msg)
				return
			case http.StatusNotFound:
				closeWith(liveCloseGone, msg)
				return
			}
		}
	}
}

// livePong is the answer to a client's message: a pong with its id for a ping, else nil.
func livePong(b []byte) []byte {
	var m struct {
		Type string          `json:"type"`
		ID   json.RawMessage `json:"id"`
	}
	if json.Unmarshal(b, &m) != nil || m.Type != "ping" {
		return nil
	}
	var id any
	var n float64
	if json.Unmarshal(m.ID, &n) == nil {
		id = n
	}
	return liveMessage(map[string]any{"type": "pong", "id": id})
}

// NotifyStateChanged reads the watched dashboards again soon. Called for every state change,
// from the Lua loop: it never waits.
func (s *Server) NotifyStateChanged() {
	s.live.changed()
}

// DashboardsReloaded drops the cached dashboards and tells the open ones to fetch their
// definition again; call it after every Lua load.
func (s *Server) DashboardsReloaded() {
	dashCache.Invalidate()
	s.remoteRolesMu.Lock()
	s.remoteRolesCache = nil // :roles() may have changed
	s.remoteRolesMu.Unlock()
	s.live.reloaded()
}
