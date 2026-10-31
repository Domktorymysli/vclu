package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// A dashboard watched over the WebSocket: a temperature read by a function, a virtual switch
// that changes through StateBus, a chip the function hides.
const liveDashboard = `
	STATE = { reads = 0, temp = 20, chip = 'x', tt = 1 }
	SW = VirtualSwitch:new("sw")
	_registry["virt.sw"] = SW
	Dashboard.new('d'):poll(60)
		:header()
			:chip('c', 'circle', function() return STATE.chip end)
		:section('S', 'i')
			:status('temp', 'T', nil, function() STATE.reads = STATE.reads + 1; return STATE.temp end)
			:toggle('sw', 'Sw', nil, function() return SW:getValue() == 1 end,
				function(v) _:sync("virt.sw", v and 1 or 0) end)
			:latency('lat', 'Ping')
		:build()
	Dashboard.new('p'):auth('pin'):pin('1234'):section('S', 'i'):build()
	-- nothing but a chip that may be hidden: getValues gives [] (Lua's empty table)
	Dashboard.new('e'):header():chip('c', 'circle', function() return STATE.echip end):build()
	-- read every second; STATE.tt changes without StateBus
	Dashboard.new('t'):poll(1):section('S', 'i'):status('tt', 'T', nil, function() return STATE.tt end):build()
`

type liveSite struct {
	s      *Server
	engine *lua.LuaEngine
	ts     *httptest.Server
}

func newLiveSite(t *testing.T) *liveSite {
	t.Helper()
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	if _, err := engine.Execute(liveDashboard); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, live: newLiveFeeds()}
	// as cmd/vclu wires it: every state change wakes the live dashboards
	engine.SetClientPushHandler(s.NotifyStateChanged)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id/ws", s.handleAPIDashboardWS)
	r.GET("/api/dashboard/:id/values", s.handleAPIDashboardValues)
	r.POST("/api/dashboard/:id/action", s.handleAPIDashboardAction)
	ts := httptest.NewServer(r)
	t.Cleanup(func() {
		s.live.shutdown()
		ts.Close()
		engine.Close()
	})
	return &liveSite{s: s, engine: engine, ts: ts}
}

func (ls *liveSite) dial(t *testing.T, id string, header ...http.Header) *websocket.Conn {
	t.Helper()
	var h http.Header
	if len(header) > 0 {
		h = header[0]
	}
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ls.ts.URL, "http")+"/api/dashboard/"+id+"/ws", h)
	if err != nil {
		t.Fatalf("dial: %v (%v)", err, resp)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func (ls *liveSite) lua(t *testing.T, script string) string {
	t.Helper()
	res, err := ls.engine.Execute(script)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

type liveMsg struct {
	Type    string                     `json:"type"`
	ID      any                        `json:"id"`
	Values  map[string]json.RawMessage `json:"values"`
	Removed []string                   `json:"removed"`
	Error   string                     `json:"error"`
}

func next(t *testing.T, conn *websocket.Conn) liveMsg {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var m liveMsg
	if err := conn.ReadJSON(&m); err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

func keys(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func entry(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	return e
}

// Contract: the socket starts with what /values answers, then sends only what changed (a state
// change through StateBus, an action, a value gone), and asks for the definition after a reload.
func TestDashboardLiveValues(t *testing.T) {
	ls := newLiveSite(t)
	conn := ls.dial(t, "d")

	first := next(t, conn)
	if first.Type != "values" {
		t.Fatalf("first message %+v, want values", first)
	}
	w := httptest.NewRecorder()
	ls.ts.Config.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/d/values", nil))
	var polled map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &polled); err != nil {
		t.Fatal(err)
	}
	for k, v := range polled {
		if !reflect.DeepEqual(entry(t, v), entry(t, first.Values[k])) {
			t.Errorf("%s: socket %s, /values %s", k, first.Values[k], v)
		}
	}
	if len(polled) != len(first.Values) {
		t.Errorf("socket has %v, /values %v", keys(first.Values), keys(polled))
	}

	// a state change through StateBus: only the switch
	ls.lua(t, `_:sync("virt.sw", 1)`)
	m := next(t, conn)
	if m.Type != "patch" || len(m.Values) != 1 || entry(t, m.Values["sw"])["value"] != true || len(m.Removed) != 0 {
		t.Fatalf("after the switch: %+v", m)
	}

	// an action wakes the dashboard at once
	req := httptest.NewRequest(http.MethodPost, "/api/dashboard/d/action", strings.NewReader(`{"widgetId":"sw","action":"toggle"}`))
	ls.ts.Config.Handler.ServeHTTP(httptest.NewRecorder(), req)
	if m := next(t, conn); m.Type != "patch" || entry(t, m.Values["sw"])["value"] != false {
		t.Fatalf("after the action: %+v", m)
	}

	// a value StateBus does not see, and a chip hidden: read on the next wake
	ls.lua(t, `STATE.temp = 21.5; STATE.chip = nil`)
	ls.s.NotifyStateChanged()
	m = next(t, conn)
	if m.Type != "patch" || entry(t, m.Values["temp"])["value"] != 21.5 || !reflect.DeepEqual(m.Removed, []string{"c"}) {
		t.Fatalf("after temp and chip: %+v", m)
	}

	// a reload: fetch the definition, then the whole state again
	ls.s.DashboardsReloaded()
	if m := next(t, conn); m.Type != "definition" {
		t.Fatalf("after a reload: %+v, want definition", m)
	}
	if m := next(t, conn); m.Type != "values" || entry(t, m.Values["temp"])["value"] != 21.5 {
		t.Fatalf("after the definition: %+v, want values", m)
	}
}

// A dashboard with no values, or none left, is an empty state, not an error: Lua's JSON gives an
// empty table as [].
func TestDashboardLiveNoValues(t *testing.T) {
	ls := newLiveSite(t)
	conn := ls.dial(t, "e")
	if m := next(t, conn); m.Type != "values" || len(m.Values) != 0 {
		t.Fatalf("first message %+v, want empty values", m)
	}
	ls.lua(t, `STATE.echip = 'y'`)
	ls.s.NotifyStateChanged()
	if m := next(t, conn); m.Type != "patch" || len(m.Values) != 1 {
		t.Fatalf("chip shown: %+v", m)
	}
	ls.lua(t, `STATE.echip = nil`)
	ls.s.NotifyStateChanged()
	if m := next(t, conn); m.Type != "patch" || !reflect.DeepEqual(m.Removed, []string{"c"}) {
		t.Fatalf("last chip hidden: %+v, want it removed", m)
	}
}

// A socket that joins while getValues fails gets the error at once (it stops polling when it
// opens, so silence would leave the page as it was); viewers never see a Lua error's text.
func TestDashboardLiveErrorForLateSocket(t *testing.T) {
	lf := newLiveFeeds()
	defer lf.shutdown()
	read := func() (string, error) { return "", errors.New("user.lua:12: attempt to index a nil value") }
	a, _ := lf.join("1.1.1.1", "d", "d", time.Minute, read)
	defer lf.leave("d", a)
	first := readSub(t, a)
	b, _ := lf.join("1.1.1.2", "d", "d", time.Minute, read)
	defer lf.leave("d", b)
	if late := readSub(t, b); !reflect.DeepEqual(first, late) || late.Type != "error" || late.Error != "dashboard unavailable" {
		t.Fatalf("first %+v, late %+v, want the same error without the Lua text", first, late)
	}
}

func readSub(t *testing.T, sub *liveSub) liveMsg {
	t.Helper()
	select {
	case b := <-sub.send:
		var m liveMsg
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no message in 5 s")
	}
	return liveMsg{}
}

// Changes coming without a pause are read ever less often, up to a read a second, whatever their
// rate; after a quiet poll interval the next change is read quickly again.
func TestDashboardLiveGap(t *testing.T) {
	every := 2 * time.Second
	gap := time.Duration(0)
	var got []time.Duration
	for range 5 {
		gap = liveGap(gap, 100*time.Millisecond, every)
		got = append(got, gap)
	}
	want := []time.Duration{liveMinGap, 2 * liveMinGap, liveMaxGap, liveMaxGap, liveMaxGap}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("gaps under changes %v, want %v", got, want)
	}
	if g := liveGap(liveMaxGap, every, every); g != liveMinGap {
		t.Errorf("after a quiet spell %v, want %v", g, liveMinGap)
	}
}

// Every socket of a dashboard shares one read: two viewers cost what one does, and a dashboard
// nobody watches is not read at all.
func TestDashboardLiveSharedRead(t *testing.T) {
	ls := newLiveSite(t)
	a := ls.dial(t, "d")
	next(t, a)
	b := ls.dial(t, "d")
	if m := next(t, b); m.Type != "values" {
		t.Fatalf("second socket: %+v, want values", m)
	}
	before := ls.lua(t, `return tostring(STATE.reads)`)
	ls.lua(t, `STATE.temp = 30`)
	ls.s.NotifyStateChanged()
	next(t, a)
	next(t, b)
	if after := ls.lua(t, `return tostring(STATE.reads)`); after != plusOne(before) {
		t.Errorf("reads %s -> %s, want one read for two sockets", before, after)
	}

	a.Close()
	b.Close()
	waitFor(t, func() bool {
		ls.s.live.mu.Lock()
		defer ls.s.live.mu.Unlock()
		return len(ls.s.live.feeds) == 0 && ls.s.live.sockets == 0
	})
	before = ls.lua(t, `return tostring(STATE.reads)`)
	ls.lua(t, `_:sync("virt.sw", 1)`)
	time.Sleep(2 * liveMinGap)
	if after := ls.lua(t, `return tostring(STATE.reads)`); after != before {
		t.Errorf("a dashboard nobody watches was read: %s -> %s", before, after)
	}
}

// The socket has the auth of /values: a PIN dashboard without its cookie is refused before the
// upgrade, an unknown one is not found.
func TestDashboardLiveAuth(t *testing.T) {
	ls := newLiveSite(t)
	// with no login configured a PIN page is open anyway
	hash, _ := config.HashPassword("secret")
	if err := writeTestConfig(ls.s.serverDir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	url := "ws" + strings.TrimPrefix(ls.ts.URL, "http") + "/api/dashboard/"
	for id, want := range map[string]int{"p": http.StatusUnauthorized, "nope": http.StatusNotFound} {
		_, resp, err := websocket.DefaultDialer.Dial(url+id+"/ws", nil)
		if err == nil || resp == nil || resp.StatusCode != want {
			t.Errorf("%s: %v %v, want %d", id, err, resp, want)
		}
	}
	// another site's page cannot open it with this site's cookies, and leaves no feed
	_, resp, err := websocket.DefaultDialer.Dial(url+"d/ws", http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin: %v %v, want 403", err, resp)
	}
	ls.s.live.mu.Lock()
	n := len(ls.s.live.feeds)
	ls.s.live.mu.Unlock()
	if n != 0 {
		t.Errorf("%d feeds after refused sockets", n)
	}

	// with its PIN cookie it opens; a reload that changes the PIN closes it at once
	cookie := &http.Cookie{Name: "vclu_dash_p", Value: ls.s.makeDashPinCookie("p", "1234", time.Now().Add(time.Hour).Unix())}
	conn := ls.dial(t, "p", http.Header{"Cookie": {cookie.String()}})
	next(t, conn)
	ls.lua(t, `Dashboard.new('p'):auth('pin'):pin('9999'):section('S', 'i'):build()`)
	ls.s.DashboardsReloaded()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, liveCloseDenied) {
				t.Fatalf("after the PIN changed: %v, want close %d", err, liveCloseDenied)
			}
			break
		}
	}
}

// One address cannot take every socket: the ones after liveMaxPerIP are refused.
func TestDashboardLivePerAddress(t *testing.T) {
	ls := newLiveSite(t)
	for range liveMaxPerIP {
		next(t, ls.dial(t, "d"))
	}
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ls.ts.URL, "http")+"/api/dashboard/d/ws", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("socket %d from one address: %v %v, want 503", liveMaxPerIP+1, err, resp)
	}
}

// Stopping the server (Server.Stop) closes the sockets (http.Server.Shutdown leaves hijacked ones open).
func TestDashboardLiveShutdown(t *testing.T) {
	ls := newLiveSite(t)
	conn := ls.dial(t, "d")
	next(t, conn)
	ls.s.Stop()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err := conn.ReadMessage()
	if !websocket.IsCloseError(err, websocket.CloseGoingAway) {
		t.Fatalf("after shutdown: %v, want close 1001", err)
	}
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ls.ts.URL, "http")+"/api/dashboard/d/ws", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a socket after shutdown: %v %v, want 503", err, resp)
	}
}

func plusOne(s string) string {
	var n int
	json.Unmarshal([]byte(s), &n)
	b, _ := json.Marshal(n + 1)
	return string(b)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in 5 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A value no state change shows (the time, a variable) still arrives: read every poll interval.
func TestDashboardLivePollInterval(t *testing.T) {
	ls := newLiveSite(t)
	conn := ls.dial(t, "t")
	next(t, conn)
	ls.lua(t, `STATE.tt = 2`)
	if m := next(t, conn); m.Type != "patch" || entry(t, m.Values["tt"])["value"] != 2.0 {
		t.Fatalf("after the poll interval: %+v", m)
	}
}

// A socket that does not read is dropped once its queue is full, instead of holding the feed or
// growing without end; the others go on.
func TestDashboardLiveSlowSocket(t *testing.T) {
	lf := newLiveFeeds()
	defer lf.shutdown()
	read := func() (string, error) { return `{"a":{"value":1}}`, nil }
	slow, _ := lf.join("1.1.1.1", "d", "d", time.Minute, read)
	fast, _ := lf.join("1.1.1.2", "d", "d", time.Minute, read)
	defer lf.leave("d", fast)
	lf.mu.Lock()
	f := lf.feeds["d"]
	lf.mu.Unlock()
	msg := liveMessage(map[string]any{"type": "patch"})
	for range liveSendBuffer + 1 {
		f.broadcast(msg, nil)
		select { // the fast one keeps up
		case <-fast.send:
		default:
		}
	}
	select {
	case <-slow.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("a socket that does not read was not dropped")
	}
	select {
	case <-fast.gone:
		t.Fatal("the socket that reads was dropped")
	default:
	}
	lf.leave("d", slow)
	lf.mu.Lock()
	sockets, feeds := lf.sockets, len(lf.feeds)
	lf.mu.Unlock()
	if sockets != 1 || feeds != 1 {
		t.Errorf("after the slow one left: %d sockets, %d feeds, want 1 and 1", sockets, feeds)
	}
}

// An open socket checks its auth again: a PIN changed without a reload closes it with the next
// check; a Lua error in the check is not a refusal and keeps it.
func TestDashboardLiveAuthRecheck(t *testing.T) {
	saved := liveAuthEvery
	liveAuthEvery = 50 * time.Millisecond
	t.Cleanup(func() { liveAuthEvery = saved })
	ls := newLiveSite(t)
	hash, _ := config.HashPassword("secret")
	if err := writeTestConfig(ls.s.serverDir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}

	d := ls.dial(t, "d")
	next(t, d)
	ls.lua(t, `SAVED_AUTH = Dashboard.API.getAuth; Dashboard.API.getAuth = function() error('boom') end`)
	time.Sleep(10 * liveAuthEvery)
	ls.lua(t, `Dashboard.API.getAuth = SAVED_AUTH; STATE.temp = 25`)
	ls.s.NotifyStateChanged()
	if m := next(t, d); m.Type != "patch" {
		t.Fatalf("after a Lua error in the auth check: %+v, want the socket still open", m)
	}

	cookie := &http.Cookie{Name: "vclu_dash_p", Value: ls.s.makeDashPinCookie("p", "1234", time.Now().Add(time.Hour).Unix())}
	p := ls.dial(t, "p", http.Header{"Cookie": {cookie.String()}})
	next(t, p)
	ls.lua(t, `Dashboard.new('p'):auth('pin'):pin('9999'):section('S', 'i'):build()`)
	p.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := p.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, liveCloseDenied) {
				t.Fatalf("after the PIN changed: %v, want close %d", err, liveCloseDenied)
			}
			break
		}
	}
}

// The page's side (web/static/js/dashboard-live.js), on a virtual clock: pushed values while the
// socket is open, polling while it is not, never both; retries back off; stop means silence.
func TestDashboardLiveClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/dashboard_live_client.js", "../../web/static/js/dashboard-live.js").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Fatalf("%v\n%s", err, out)
	}
}

// The page measures its round trip: every ping gets a pong with its id at once, whatever the feed
// is doing; anything else it sends is ignored. The latency tile is in the definition, measured by
// the page, and has no value.
func TestDashboardLivePing(t *testing.T) {
	ls := newLiveSite(t)
	conn := ls.dial(t, "d")
	first := next(t, conn)
	if _, ok := first.Values["lat"]; ok {
		t.Errorf("the latency tile has a value: %s", first.Values["lat"])
	}
	def := ls.lua(t, `return Dashboard.API.getDefinition('d')`)
	if !strings.Contains(def, `"id":"lat"`) || !strings.Contains(def, `"type":"latency"`) {
		t.Errorf("the latency tile is not in the definition: %s", def)
	}

	for _, m := range []string{`not json`, `{"type":"values"}`, `{"type":"ping","id":"<script>"}`, `{"type":"ping","id":7}`} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(m)); err != nil {
			t.Fatal(err)
		}
	}
	if m := next(t, conn); m.Type != "pong" || m.ID != nil {
		t.Fatalf("pong to a ping with a string id: %+v, want id null", m)
	}
	if m := next(t, conn); m.Type != "pong" || m.ID != 7.0 {
		t.Fatalf("pong: %+v, want id 7", m)
	}
}

func TestDashboardLiveFlood(t *testing.T) {
	ls := newLiveSite(t)
	conn := ls.dial(t, "d")
	next(t, conn)
	for i := 0; i <= liveMsgCredits; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping","id":1}`)); err != nil {
			break
		}
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
				t.Errorf("closed with %v, want 1008", err)
			}
			return
		}
	}
}
