package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

const scheduleDashboards = `
dom = Calendar.new('dom'):clock({ wait = false })
  :schedule('komfort', { values = { 'eco', 'komfort' }, default = 'eco',
     { every = 'mo', hours = '06:00-08:00', value = 'komfort' } }):editable():roles('rodzice')
  :schedule('woda', { values = { 'off', 'on' }, default = 'off',
     { every = 'day', hours = '05:00-05:30', value = 'on' } }):editable()
  :build()
edits = {}
dom:on('edit', function(e) edits[#edits + 1] = e.id .. ' ' .. e.action .. ' v' .. e.version .. ' ' .. tostring(e.actor) end)
Dashboard.new('sch'):remote():section('S', 'clock')
  :schedule('k', 'Komfort', dom, 'komfort')
  :schedule('w', 'Woda', dom, 'woda')
  :schedule('kro', 'Komfort', dom, 'komfort', { remote = 'readonly' })
  :build()
Dashboard.new('pub'):section('S', 'clock'):schedule('k', 'K', dom, 'komfort'):schedule('w', 'W', dom, 'woda'):build()
Dashboard.new('pin'):auth('pin'):pin('1234'):section('S', 'clock'):schedule('k', 'K', dom, 'komfort'):build()
`

func newScheduleServer(t *testing.T) (*Server, *lua.LuaEngine, http.Handler) {
	t.Helper()
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	pinLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1, burst: 5}
	dashPinLocks = newPinLockout()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(scheduleDashboards); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := writeTestConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	lan := gin.New()
	lan.GET("/api/dashboard/:id", s.handleAPIDashboardDef)
	lan.GET("/api/dashboard/:id/schedule", s.handleAPIDashboardSchedule)
	lan.POST("/api/dashboard/:id/action", s.handleAPIDashboardAction)
	lan.POST("/api/dashboard/:id/auth", s.handleAPIDashboardAuth)
	return s, engine, lan
}

func luaString(t *testing.T, engine *lua.LuaEngine, script string) string {
	t.Helper()
	out, err := engine.Execute(script)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Through the cloud: who the schedule's roles name (or the owner) edits, others see the week
// without the editor; the same answer for the flag and for the action.
func TestScheduleEditThroughTunnel(t *testing.T) {
	s, engine, _ := newScheduleServer(t)
	tunnel := s.TunnelHandler()

	editable := func(widget, role string) any {
		r := do(tunnel, "GET", "/api/dashboard/sch/schedule?widget="+widget, role, "")
		if r.code != http.StatusOK {
			t.Fatalf("%s as %s: %d %s", widget, role, r.code, r.raw)
		}
		if _, ok := r.body["roles"]; ok {
			t.Error("the week names the roles")
		}
		return r.body["editable"]
	}
	// no members list: the home roles are not known, so only the owner and admins
	if editable("k", "member") != false || editable("k", "owner") != true || editable("w", "member") != true {
		t.Error("without a members list")
	}
	if editable("kro", "owner") != false {
		t.Error("remote = 'readonly' is editable")
	}

	set := func(role, widget, body string) tunnelResp {
		return do(tunnel, "POST", "/api/dashboard/sch/action", role, `{"widgetId":"`+widget+`","action":"set","value":`+body+`}`)
	}
	week := `{"version":0,"week":{"tu":[{"from":"10:00","to":"24:00","value":"komfort"}]}}`
	if r := set("member", "k", week); r.code != http.StatusForbidden {
		t.Errorf("without the role: %d %s", r.code, r.raw)
	}
	if r := set("owner", "kro", week); r.code != http.StatusForbidden {
		t.Errorf("readonly widget: %d %s", r.code, r.raw)
	}

	s.SetLocalAccess(newLocal(t, t.TempDir()))
	m := members(1, "member")
	m.Members[0].Roles = []string{"rodzice"}
	s.local.SetMembers(m)
	if editable("k", "member") != true {
		t.Error("with the role: not editable")
	}
	body := `{"widgetId":"k","action":"set","actionId":"0b6f6a2e-6b7c-4c1e-9a3e-3f1f1b2a7c11","value":` + week + `}`
	first := do(tunnel, "POST", "/api/dashboard/sch/action", "member", body)
	if first.code != http.StatusOK || first.body["success"] != true {
		t.Fatalf("with the role: %d %s", first.code, first.raw)
	}
	again := do(tunnel, "POST", "/api/dashboard/sch/action", "member", body)
	if again.code != http.StatusOK || again.hdr.Get("X-Vclu-Replay") != "1" {
		t.Errorf("a repeat: %d %s", again.code, again.hdr)
	}
	if got := luaString(t, engine, `return table.concat(edits, '|')`); got != "komfort set v1 cloud:12" {
		t.Errorf("edits %q", got)
	}
	stale := set("member", "k", week)
	if stale.code != http.StatusConflict {
		t.Fatalf("stale version: %d %s", stale.code, stale.raw)
	}
	if w, _ := stale.body["week"].(map[string]any); w["version"] != 1.0 {
		t.Errorf("conflict carries %v", stale.body["week"])
	}
	if r := set("member", "k", `{"version":1,"week":{"tu":[{"from":"10:00","to":"09:00","value":"komfort"}]}}`); r.code != http.StatusBadRequest ||
		!strings.Contains(r.raw, "from must be before to") {
		t.Errorf("bad week: %d %s", r.code, r.raw)
	}
	r := do(tunnel, "POST", "/api/dashboard/sch/action", "member", `{"widgetId":"k","action":"reset","value":{"version":1}}`)
	if r.code != http.StatusOK {
		t.Errorf("reset: %d %s", r.code, r.raw)
	}
	wk := do(tunnel, "GET", "/api/dashboard/sch/schedule?widget=k", "member", "")
	if wk.body["version"] != 2.0 || wk.body["edited"] != false {
		t.Errorf("after reset: %s", wk.raw)
	}
	// a client of the device API reads the week but does not edit
	if got := luaString(t, engine, `return Dashboard.API.executeAction('sch', 'w', 'set', nil, { role = 'member', client = true, valueJSON = '{"version":0,"week":{}}' })`); !strings.Contains(got, `"status":403`) {
		t.Errorf("device API client: %s", got)
	}
}

// On the LAN without a token: a public page edits a schedule naming no roles, never one naming
// roles; with the PIN it can. Refusals carry their HTTP status.
func TestScheduleEditOnTheLAN(t *testing.T) {
	_, _, lan := newScheduleServer(t)
	post := func(dash, widget, value string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/dashboard/"+dash+"/action",
			strings.NewReader(`{"widgetId":"`+widget+`","action":"set","value":`+value+`}`))
		for _, c := range cookies {
			req.AddCookie(c)
		}
		lan.ServeHTTP(w, req)
		return w
	}
	komfort := `{"version":0,"week":{"mo":[{"from":"06:00","to":"09:00","value":"komfort"}]}}`
	if w := post("pub", "k", komfort); w.Code != http.StatusForbidden {
		t.Errorf("public page, schedule with roles: %d %s", w.Code, w.Body)
	}
	if w := post("pub", "w", `{"version":0,"week":{"mo":[{"from":"05:00","to":"06:00","value":"on"}]}}`); w.Code != http.StatusOK {
		t.Errorf("public page, schedule without roles: %d %s", w.Code, w.Body)
	}
	if w := post("pub", "w", `{"week":{}}`); w.Code != http.StatusBadRequest {
		t.Errorf("no version: %d %s", w.Code, w.Body)
	}

	w := httptest.NewRecorder()
	lan.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/pin/auth", strings.NewReader(`{"pin":"1234"}`)))
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "vclu_dash_pin" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("no PIN cookie: %d %s", w.Code, w.Body)
	}
	if w := post("pin", "k", komfort); w.Code != http.StatusUnauthorized {
		t.Errorf("PIN page without the PIN: %d %s", w.Code, w.Body)
	}
	if w := post("pin", "k", komfort, cookie); w.Code != http.StatusOK {
		t.Errorf("PIN page with the PIN: %d %s", w.Code, w.Body)
	}
}

// The viewer's part of the ctx: nothing for the house; the role, the home roles as escaped JSON
// and client for a remote viewer.
func TestViewerCtxLua(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if got := viewerCtxLua(c, "held = false, "); got != "{ held = false, }" {
		t.Errorf("house: %s", got)
	}
	c.Set(ctxRemoteRole, "member")
	c.Set(ctxRemoteActor, "client:7")
	c.Set(ctxViewerRoles, []string{`a"b`})
	if got := viewerCtxLua(c, ""); got != `{ role = "member", roles = "[\"a\\\"b\"]", client = true, }` {
		t.Errorf("client: %s", got)
	}
}

// A client of the device API with a dashboard's role reads the week through the LAN listener
// but never edits it, also a schedule naming no roles.
func TestScheduleDeviceClientOnTheLAN(t *testing.T) {
	s, r := clientsSite(t)
	if _, err := s.luaExecutor.Execute(`
woda = Calendar.new('woda'):clock({ wait = false })
  :schedule('w', { values = { 'off', 'on' }, default = 'off',
     { every = 'day', hours = '05:00-05:30', value = 'on' } }):editable()
  :build()
Dashboard.new('ogrod'):remote():roles('gosia'):section('S', 'clock'):schedule('w', 'Woda', woda, 'w'):build()
`); err != nil {
		t.Fatal(err)
	}
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	_, token := newClient(t, r, `{"name":"Zegarek","profiles":["brama"],"roles":["gosia"]}`)
	lan := s.LocalHandler()
	wk := call(lan, "GET", "/api/dashboard/ogrod/schedule?widget=w", token, "")
	if wk.code != http.StatusOK || wk.body["editable"] != false {
		t.Fatalf("the week for a client: %d %v", wk.code, wk.body)
	}
	set := call(lan, "POST", "/api/dashboard/ogrod/action", token, `{"widgetId":"w","action":"set","value":{"version":0,"week":{}}}`)
	if set.code != http.StatusForbidden {
		t.Errorf("set by a client: %d %v", set.code, set.body)
	}
	if v := serverLua(t, s, `return tostring(woda:week('w').version)`); v != "0" {
		t.Errorf("version after a client's set: %s", v)
	}
}

func serverLua(t *testing.T, s *Server, script string) string {
	t.Helper()
	out, err := s.luaExecutor.Execute(script)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A public page on the LAN cannot send a body past remoteBodyMax: it is refused before Lua.
func TestDashboardActionBodyBoundedOnTheLAN(t *testing.T) {
	s, _, _ := newScheduleServer(t)
	lan := gin.New()
	lan.POST("/api/dashboard/:id/action", limitBody, s.handleAPIDashboardAction)
	big := `{"widgetId":"w","action":"set","value":{"version":0,"pad":"` + strings.Repeat("x", remoteBodyMax) + `"}}`
	w := httptest.NewRecorder()
	lan.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/pub/action", strings.NewReader(big)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("a body past the limit: %d %s", w.Code, w.Body)
	}
}
