package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const tunnelDashboards = `
st = { lamp = false, gate = 0 }
Dashboard.new('home'):name('Dom'):remote()
  :section('S', 'house')
  :toggle('lamp', 'Lampa', 'lamp', function() return st.lamp end,
      function(v) st.lamp = v; st.actor = StateContext.current().actor; st.origin = StateContext.current().origin end)
  :status('secret', 'Sekret', 'key', function() return 42 end, nil, { remote = false })
  :button('gate', 'Brama', 'door-open', function() st.gate = st.gate + 1 end, { remote = { confirm = true } })
  :toggle('ro', 'Tylko odczyt', 'eye', function() return false end, function() st.ro = true end, { remote = 'readonly' })
  :build()
Dashboard.new('alarm'):name('Alarm'):remote({ pin = true }):roles('owner'):pin('1234')
  :section('S', 'shield'):image('cam', 'Kamera', Camera.snapshotUrl('front')):build()
Dashboard.new('local'):name('Tylko w domu'):section('S', 'house'):build()
Dashboard.new('gosia'):name('Gosia'):remote():roles('gosia'):section('S', 'heart'):build()
`

type tunnelResp struct {
	code int
	body map[string]any
	raw  string
	hdr  http.Header
}

// newTunnel returns the tunnel handler over the dashboards above, and the LAN routes for comparison.
func newTunnel(t *testing.T) (tunnel, lan http.Handler, engine *lua.LuaEngine) {
	t.Helper()
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	pinLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1, burst: 5}
	engine = lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id", s.handleAPIDashboardDef)
	r.GET("/api/dashboard/:id/values", s.handleAPIDashboardValues)
	return s.TunnelHandler(), r, engine
}

func do(h http.Handler, method, path, role, body string, cookies ...*http.Cookie) tunnelResp {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if role != "" {
		req.Header.Set(proto.HeaderActor, "cloud:12")
		req.Header.Set(proto.HeaderRole, role)
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	out := tunnelResp{code: w.Code, raw: w.Body.String(), hdr: w.Header()}
	json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func widgetOf(def map[string]any, id string) map[string]any {
	ws, _ := def["widgets"].([]any)
	for _, w := range ws {
		if m, _ := w.(map[string]any); m["id"] == id {
			return m
		}
	}
	return nil
}

// Only the dashboards marked :remote() reach the cloud; widgets with remote = false never leave
// the house, and remote = 'readonly' comes without controls. Who may see which dashboard is the
// cloud's (its roles): through the tunnel every viewer gets the same.
func TestTunnelDashboards(t *testing.T) {
	tunnel, lan, _ := newTunnel(t)

	if r := do(tunnel, "GET", "/api/dashboard/home", "", ""); r.code != 404 {
		t.Errorf("without the cloud's headers: %d", r.code)
	}
	if r := do(tunnel, "GET", "/api/dashboard/local", "owner", ""); r.code != 404 {
		t.Errorf("a dashboard without :remote(): %d", r.code)
	}
	for _, p := range []string{"/api/dashboards", "/api/clus", "/dashboards", "/mcp", "/api/lua/execute"} {
		if r := do(tunnel, "GET", p, "owner", ""); r.code != 404 {
			t.Errorf("%s through the tunnel: %d", p, r.code)
		}
	}

	for _, role := range []string{"member", "owner"} {
		m := do(tunnel, "GET", "/api/dashboard/home", role, "")
		if m.code != 200 {
			t.Fatalf("%s: %d %s", role, m.code, m.raw)
		}
		if widgetOf(m.body, "secret") != nil || strings.Contains(m.raw, "Sekret") {
			t.Errorf("remote = false sent: %s", m.raw)
		}
		if w := widgetOf(m.body, "lamp"); w["disabled"] != false || w["readonly"] != nil {
			t.Errorf("lamp for %s: %v", role, w)
		}
		if w := widgetOf(m.body, "ro"); w["disabled"] != true || w["readonly"] != true {
			t.Errorf("remote = 'readonly' for %s: %v", role, w)
		}
		if w := widgetOf(m.body, "gate"); w["confirm"] == nil {
			t.Errorf("confirmed button: %v", w)
		}
		if m.body["readonly"] != nil || m.body["poll"] != float64(5) || m.body["auth"] != "public" {
			t.Errorf("%s's definition: readonly %v poll %v auth %v", role, m.body["readonly"], m.body["poll"], m.body["auth"])
		}
	}

	v := do(tunnel, "GET", "/api/dashboard/home/values", "member", "")
	if _, ok := v.body["secret"]; ok || v.code != 200 {
		t.Errorf("values: %d %s", v.code, v.raw)
	}

	// the LAN router never believes the headers: the house sees everything
	l := do(lan, "GET", "/api/dashboard/home", "member", "")
	if widgetOf(l.body, "secret") == nil || l.body["readonly"] != nil {
		t.Errorf("LAN with the cloud's headers: %s", l.raw)
	}
}

func TestTunnelActions(t *testing.T) {
	tunnel, _, engine := newTunnel(t)
	act := func(role, widget, action, extra string) tunnelResp {
		return do(tunnel, "POST", "/api/dashboard/home/action", role,
			`{"widgetId":"`+widget+`","action":"`+action+`","value":true`+extra+`}`)
	}
	get := func(expr string) string {
		out, err := engine.Execute("return tostring(" + expr + ")")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if r := act("owner", "ro", "toggle", ""); r.code != 403 || get("st.ro") != "nil" {
		t.Errorf("readonly widget: %d %s", r.code, r.raw)
	}
	if r := act("owner", "secret", "tap", ""); r.body["error"] != "Widget not found" {
		t.Errorf("hidden widget: %d %s", r.code, r.raw)
	}
	if r := act("owner", "lamp", "toggle", ""); r.code != 200 || r.body["success"] != true || get("st.lamp") != "true" {
		t.Fatalf("owner: %d %s", r.code, r.raw)
	}
	if get("st.origin") != "remote" || get("st.actor") != "cloud:12" {
		t.Errorf("ran as %s %s, want remote cloud:12", get("st.origin"), get("st.actor"))
	}
	if r := act("admin", "gate", "tap", ""); r.code != 400 || get("st.gate") != "0" {
		t.Errorf("unconfirmed: %d %s", r.code, r.raw)
	}
	if r := act("admin", "gate", "tap", `,"confirmed":true`); r.code != 200 || get("st.gate") != "1" {
		t.Errorf("confirmed: %d %s", r.code, r.raw)
	}
}

// remote{pin = true}: the PIN is asked through the cloud too, and the cookie works only there.
func TestTunnelPin(t *testing.T) {
	tunnel, _, _ := newTunnel(t)
	if r := do(tunnel, "GET", "/api/dashboard/alarm", "owner", ""); r.code != 401 {
		t.Errorf("without PIN: %d %s", r.code, r.raw)
	}
	if r := do(tunnel, "POST", "/api/dashboard/alarm/auth", "owner", `{"pin":"0000"}`); r.code != 401 {
		t.Errorf("wrong PIN: %d", r.code)
	}
	r := do(tunnel, "POST", "/api/dashboard/alarm/auth", "owner", `{"pin":"1234"}`)
	var cookie *http.Cookie
	for _, ck := range (&http.Response{Header: r.hdr}).Cookies() {
		if ck.Name == "vclu_dash_alarm" {
			cookie = ck
		}
	}
	if r.code != 200 || cookie == nil {
		t.Fatalf("right PIN: %d %s", r.code, r.raw)
	}
	if r := do(tunnel, "GET", "/api/dashboard/alarm", "owner", "", cookie); r.code != 200 || r.body["auth"] != "pin" {
		t.Errorf("with the cookie: %d %s", r.code, r.raw)
	}
}

// A camera is served through the cloud only when a remote dashboard shows it.
func TestTunnelCamera(t *testing.T) {
	tunnel, _, _ := newTunnel(t)
	if r := do(tunnel, "GET", "/api/camera/back/snapshot", "owner", ""); r.code != 404 {
		t.Errorf("camera on no dashboard: %d", r.code)
	}
	// no camera manager in the test: past the check it answers 503
	if r := do(tunnel, "GET", "/api/camera/front/snapshot", "member", ""); r.code != 503 {
		t.Errorf("camera of a remote dashboard: %d", r.code)
	}
}

// The live values through the tunnel, and the per-address limit counting people: everyone through
// the cloud comes from one address.
func TestTunnelSocket(t *testing.T) {
	tunnel, _, _ := newTunnel(t)
	ts := httptest.NewServer(tunnel)
	t.Cleanup(ts.Close)
	dial := func(actor, role string) (*websocket.Conn, *http.Response, error) {
		h := http.Header{proto.HeaderActor: {actor}, proto.HeaderRole: {role}}
		c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/dashboard/home/ws", h)
		if c != nil {
			t.Cleanup(func() { c.Close() })
		}
		return c, resp, err
	}
	read := func(c *websocket.Conn) map[string]map[string]any {
		t.Helper()
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		var m struct {
			Type   string                    `json:"type"`
			Values map[string]map[string]any `json:"values"`
		}
		if err := c.ReadJSON(&m); err != nil {
			t.Fatal(err)
		}
		return m.Values
	}

	if _, resp, err := dial("", ""); err == nil || resp.StatusCode != 404 {
		t.Errorf("without the cloud's headers: %v", err)
	}
	member, _, err := dial("cloud:2", "member")
	if err != nil {
		t.Fatal(err)
	}
	mv := read(member)
	if _, ok := mv["secret"]; ok || mv["lamp"]["disabled"] != false {
		t.Errorf("member's values: %v", mv)
	}
	owner, _, err := dial("cloud:1", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if ov := read(owner); ov["lamp"]["disabled"] != false {
		t.Errorf("owner's values: %v", ov)
	}

	// an action through the tunnel reaches both roles' sockets at once
	if r := do(tunnel, "POST", "/api/dashboard/home/action", "owner", `{"widgetId":"lamp","action":"toggle","value":true}`); r.code != 200 {
		t.Fatalf("action: %d %s", r.code, r.raw)
	}
	if p := read(member); p["lamp"]["value"] != true {
		t.Errorf("member's patch: %v", p)
	}
	if p := read(owner); p["lamp"]["value"] != true {
		t.Errorf("owner's patch: %v", p)
	}

	// one person gets liveMaxPerIP sockets; another person still gets theirs
	for i := 1; i < liveMaxPerIP; i++ {
		if _, _, err := dial("cloud:1", "owner"); err != nil {
			t.Fatalf("owner's socket %d: %v", i+1, err)
		}
	}
	if _, resp, err := dial("cloud:1", "owner"); err == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("one person over the limit: %v", err)
	}
	if _, _, err := dial("cloud:3", "member"); err != nil {
		t.Errorf("another person through the same address: %v", err)
	}
}

// With a members list the house checks a dashboard's roles through the tunnel too: the cloud's
// word that someone may open it is not enough.
func TestTunnelDashboardRoles(t *testing.T) {
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	tunnel := s.TunnelHandler()

	// no members list (no LAN access): the cloud's check is the only one
	if r := do(tunnel, "GET", "/api/dashboard/gosia", "member", ""); r.code != http.StatusOK {
		t.Errorf("without a members list = %d", r.code)
	}
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	s.local.SetMembers(members(1, "member"))
	if r := do(tunnel, "GET", "/api/dashboard/gosia", "member", ""); r.code != http.StatusNotFound {
		t.Errorf("without the role = %d", r.code)
	}
	if r := do(tunnel, "GET", "/api/dashboard/home", "member", ""); r.code != http.StatusOK {
		t.Errorf("a dashboard naming no roles = %d", r.code)
	}
	withRole := members(2, "member")
	withRole.Members[0].Roles = []string{"gosia"}
	s.local.SetMembers(withRole)
	if r := do(tunnel, "GET", "/api/dashboard/gosia", "member", ""); r.code != http.StatusOK {
		t.Errorf("with the role = %d", r.code)
	}
	// someone the list does not name
	s.local.SetMembers(proto.Members{Type: proto.TypeMembers, Version: 3})
	if r := do(tunnel, "GET", "/api/dashboard/home", "member", ""); r.code != http.StatusNotFound {
		t.Errorf("not in the members list = %d", r.code)
	}
	if r := do(tunnel, "GET", "/static/css/dashboard.css", "member", ""); r.code == http.StatusNotFound && strings.Contains(r.raw, "Dashboard not found") {
		t.Error("a path that is no dashboard's was checked")
	}
}
