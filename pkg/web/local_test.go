package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cloud/proto"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const install12 = "0123456789abcdef0123456789abcdef"

func members(version int64, role string, installs ...string) proto.Members {
	m := proto.Member{User: "cloud:12", Name: "Anna", Role: role}
	for _, in := range installs {
		m.Apps = append(m.Apps, proto.App{Install: in, Client: proto.ClientApp})
	}
	return proto.Members{Type: proto.TypeMembers, Version: version, Members: []proto.Member{m}}
}

func newLocal(t *testing.T, dir string) *LocalAccess {
	t.Helper()
	la, cfg, err := NewLocalAccess(dir, 9443)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Certificates) != 1 || !strings.HasPrefix(la.Pin(), "sha256/") {
		t.Fatalf("certificate %v, pin %q", cfg.Certificates, la.Pin())
	}
	la.addresses = func(port int) []string { return []string{"192.168.1.20:9443"} }
	return la
}

// A token works while the cloud's members list names its person and app, with the role from
// there; it survives a restart, and the certificate (so the pin) stays the same.
func TestLocalAccessTokens(t *testing.T) {
	dir := t.TempDir()
	la := newLocal(t, dir)
	issued := 0
	la.OnIssue = func() { issued++ }

	if _, err := la.Issue("cloud:12", "not-an-install"); err == nil {
		t.Error("issued for a bad installation")
	}
	if _, err := la.Issue("anna", install12); err == nil {
		t.Error("issued for a bad actor")
	}
	g, err := la.Issue("cloud:12", install12)
	if err != nil || !strings.HasPrefix(g.Token, "vclu_local_") || g.Pin != la.Pin() || g.Addresses[0] != "192.168.1.20:9443" ||
		g.ExpiresAt < time.Now().Add(29*24*time.Hour).Unix() || issued != 1 {
		t.Fatalf("grant %+v, %v (issued %d)", g, err, issued)
	}
	if _, _, _, ok := la.Viewer(g.Token); ok {
		t.Error("a token works before the members list names its app")
	}
	la.SetMembers(members(5, "member", install12))
	if actor, role, _, ok := la.Viewer(g.Token); !ok || actor != "cloud:12" || role != "member" {
		t.Errorf("viewer %s %s %v", actor, role, ok)
	}
	la.SetMembers(members(6, "admin", install12))
	if _, role, _, _ := la.Viewer(g.Token); role != "admin" {
		t.Errorf("role after a change = %s", role)
	}
	la.SetMembers(members(4, "member")) // older: ignored
	if _, role, _, ok := la.Viewer(g.Token); !ok || role != "admin" {
		t.Errorf("an older list applied: %s %v", role, ok)
	}
	if _, _, _, ok := la.Viewer("vclu_local_guess"); ok {
		t.Error("an unknown token works")
	}

	// a restart keeps the tokens, the list and the certificate
	again := newLocal(t, dir)
	if again.Pin() != la.Pin() {
		t.Error("the pin changed across a restart")
	}
	if _, role, _, ok := again.Viewer(g.Token); !ok || role != "admin" {
		t.Errorf("after a restart: %s %v", role, ok)
	}

	// a new token for the same app replaces the old one
	g2, _ := again.Issue("cloud:12", install12)
	if _, _, _, ok := again.Viewer(g.Token); ok {
		t.Error("the replaced token still works")
	}
	if _, _, _, ok := again.Viewer(g2.Token); !ok {
		t.Error("the new token does not work")
	}

	// expiry
	again.now = func() time.Time { return time.Now().Add(LocalTokenTTL + time.Minute) }
	if _, _, _, ok := again.Viewer(g2.Token); ok {
		t.Error("an expired token works")
	}
	again.now = time.Now

	// signed out or removed in the cloud: the next list leaves the app out
	again.SetMembers(members(7, "admin"))
	if _, _, _, ok := again.Viewer(g2.Token); ok {
		t.Error("a token works after its app left the list")
	}
	again.SetMembers(members(8, "admin", install12))
	if _, _, _, ok := again.Viewer(g2.Token); ok {
		t.Error("a forgotten token came back with the app listed again")
	}
}

// Through the tunnel the cloud gets a grant for the person it names; on the LAN the token opens
// exactly what the tunnel serves to that role, and nothing without it.
func TestLocalHandler(t *testing.T) {
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter()}
	gin.SetMode(gin.TestMode)
	tunnel, lanApp := s.TunnelHandler(), s.LocalHandler()

	if r := do(tunnel, http.MethodPost, proto.PathLocalToken, "member", ""); r.code != http.StatusNotFound {
		t.Errorf("token with local access off = %d", r.code)
	}
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	if r := do(tunnel, http.MethodPost, proto.PathLocalToken, "", ""); r.code != http.StatusNotFound {
		t.Errorf("token without the cloud's headers = %d", r.code)
	}
	if r := do(tunnel, http.MethodPost, proto.PathLocalToken, "member", ""); r.code != http.StatusBadRequest {
		t.Errorf("token without an installation = %d", r.code)
	}
	req := httptest.NewRequest(http.MethodPost, proto.PathLocalToken, nil)
	req.Header.Set(proto.HeaderActor, "cloud:12")
	req.Header.Set(proto.HeaderRole, "member")
	req.Header.Set(proto.HeaderInstall, install12)
	w := httptest.NewRecorder()
	tunnel.ServeHTTP(w, req)
	var g proto.LocalGrant
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &g) != nil || g.Token == "" {
		t.Fatalf("grant = %d %s", w.Code, w.Body)
	}
	s.local.SetMembers(members(1, "member", install12))

	lan := func(method, path, token, body string) tunnelResp {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set(proto.HeaderActor, "cloud:1") // believed only through the tunnel
		req.Header.Set(proto.HeaderRole, "owner")
		w := httptest.NewRecorder()
		lanApp.ServeHTTP(w, req)
		out := tunnelResp{code: w.Code, raw: w.Body.String(), hdr: w.Header()}
		json.Unmarshal(w.Body.Bytes(), &out.body)
		return out
	}
	if r := lan(http.MethodGet, "/api/local/ping", g.Token, ""); r.code != http.StatusOK || r.body["role"] != "member" {
		t.Errorf("ping = %d %s", r.code, r.raw)
	}
	for _, tok := range []string{"", "vclu_local_nope", "cloud:1"} {
		if r := lan(http.MethodGet, "/api/dashboard/home", tok, ""); r.code != http.StatusUnauthorized {
			t.Errorf("dashboard with %q = %d", tok, r.code)
		}
	}
	def := lan(http.MethodGet, "/api/dashboard/home", g.Token, "")
	if def.code != http.StatusOK || widgetOf(def.body, "secret") != nil || widgetOf(def.body, "lamp")["disabled"] != false {
		t.Errorf("home on the LAN = %d %s", def.code, def.raw)
	}
	if r := lan(http.MethodGet, "/api/dashboard/local", g.Token, ""); r.code != http.StatusNotFound {
		t.Errorf("a dashboard without :remote() = %d", r.code)
	}
	if r := lan(http.MethodGet, "/_cloud/local-token", g.Token, ""); r.code != http.StatusNotFound {
		t.Errorf("issuing on the LAN = %d", r.code)
	}

	if r := lan(http.MethodPost, "/api/dashboard/home/action", g.Token, `{"widgetId":"lamp","action":"toggle","value":true}`); r.code != http.StatusOK || r.body["success"] != true {
		t.Errorf("an action on the LAN = %d %s", r.code, r.raw)
	}
	if v, _ := engine.Execute(`return st.actor .. ' ' .. st.origin`); v != "cloud:12 remote" {
		t.Errorf("recorded as %q", v)
	}
	s.local.SetMembers(members(3, "owner"))
	if r := lan(http.MethodGet, "/api/dashboard/home", g.Token, ""); r.code != http.StatusUnauthorized {
		t.Errorf("after the app left the members list = %d", r.code)
	}
}

// The history says who made a change through the cloud: the name from the members list.
func TestHistoryActorNames(t *testing.T) {
	s := &Server{}
	if got := s.actorNames([]history.Entry{{Actor: "cloud:12"}}); got != nil {
		t.Errorf("without local access: %v", got)
	}
	s.local = newLocal(t, t.TempDir())
	s.local.SetMembers(members(1, "member"))
	got := s.actorNames([]history.Entry{{Actor: "cloud:12"}, {Actor: "cloud:99"}, {Actor: "user:admin"}, {Actor: "cloud:12"}})
	if len(got) != 1 || got["cloud:12"] != "Anna" {
		t.Errorf("names = %v, want cloud:12 Anna only", got)
	}
}

// Tokens stop working after a week without the cloud: its members list, roles included, may have
// changed meanwhile. A health report on a working connection counts as hearing from it.
func TestLocalTokensNeedTheCloud(t *testing.T) {
	la := newLocal(t, t.TempDir())
	now := time.Now()
	la.now = func() time.Time { return now }
	g, err := la.Issue("cloud:12", install12)
	if err != nil {
		t.Fatal(err)
	}
	la.SetMembers(members(1, "member", install12))
	now = now.Add(6 * 24 * time.Hour)
	la.CloudSeen()
	now = now.Add(6 * 24 * time.Hour)
	if _, _, _, ok := la.Viewer(g.Token); !ok {
		t.Error("a token stopped with the cloud heard 6 days ago")
	}
	now = now.Add(2 * 24 * time.Hour)
	if _, _, _, ok := la.Viewer(g.Token); ok {
		t.Error("a token works after 8 days without the cloud")
	}
	la.SetMembers(members(2, "member", install12))
	if _, _, _, ok := la.Viewer(g.Token); !ok {
		t.Error("the cloud back: the token works again")
	}
}

// On the LAN vCLU checks a dashboard's :roles() against the person's roles from the members list:
// the cloud, which does it otherwise, is not in the way.
func TestLocalHandlerRoles(t *testing.T) {
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter()}
	gin.SetMode(gin.TestMode)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	g, err := s.local.Issue("cloud:12", install12)
	if err != nil {
		t.Fatal(err)
	}
	lanApp := s.LocalHandler()
	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+g.Token)
		w := httptest.NewRecorder()
		lanApp.ServeHTTP(w, req)
		return w.Code
	}
	s.local.SetMembers(members(1, "member", install12))
	if code := get("/api/dashboard/gosia"); code != http.StatusNotFound {
		t.Errorf("gosia without the role = %d", code)
	}
	if code := get("/d/gosia"); code != http.StatusNotFound {
		t.Errorf("gosia's page without the role = %d", code)
	}
	if code := get("/api/dashboard/home"); code != http.StatusOK {
		t.Errorf("a dashboard naming no roles = %d", code)
	}
	withRole := members(2, "member", install12)
	withRole.Members[0].Roles = []string{"gosia"}
	s.local.SetMembers(withRole)
	if code := get("/api/dashboard/gosia"); code != http.StatusOK {
		t.Errorf("gosia with the role = %d", code)
	}

	// roles from a function: hidden from everyone when it gives none, and back without a reload
	// once the cache is older than remoteRolesFor
	if _, err := engine.Execute(`
		sprzatanie = false
		Dashboard.new('sprzatanie'):remote():roles(function()
			if sprzatanie then return { 'gosia' } end
		end):section('S', 'i'):build()
	`); err != nil {
		t.Fatal(err)
	}
	s.DashboardsReloaded()
	if code := get("/api/dashboard/sprzatanie"); code != http.StatusNotFound {
		t.Errorf("hidden by its roles function = %d", code)
	}
	engine.Execute(`sprzatanie = true`)
	s.remoteRolesMu.Lock()
	s.remoteRolesAt = time.Now().Add(-remoteRolesFor)
	s.remoteRolesMu.Unlock()
	if code := get("/api/dashboard/sprzatanie"); code != http.StatusOK {
		t.Errorf("shown again by its roles function = %d", code)
	}
}

// A camera on the LAN only for who sees a dashboard showing it: the alarm's camera needs the
// alarm's role (TestTunnelCamera lets every member through, the cloud checking there).
func TestLocalCameraRoles(t *testing.T) {
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter()}
	gin.SetMode(gin.TestMode)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	g, err := s.local.Issue("cloud:12", install12)
	if err != nil {
		t.Fatal(err)
	}
	lanApp := s.LocalHandler()
	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+g.Token)
		w := httptest.NewRecorder()
		lanApp.ServeHTTP(w, req)
		return w.Code
	}
	s.local.SetMembers(members(1, "member", install12))
	if code := get("/api/camera/front/snapshot"); code != http.StatusNotFound {
		t.Errorf("the alarm's camera without the alarm's role = %d", code)
	}
	withRole := members(2, "member", install12)
	withRole.Members[0].Roles = []string{"owner"}
	s.local.SetMembers(withRole)
	// no camera manager in the test: past the check it answers 503
	if code := get("/api/camera/front/snapshot"); code != http.StatusServiceUnavailable {
		t.Errorf("the alarm's camera with the role = %d", code)
	}

	// through the tunnel, with the members list known, the same
	tunnel := s.TunnelHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/camera/front/snapshot", nil)
	req.Header.Set(proto.HeaderActor, "cloud:12")
	req.Header.Set(proto.HeaderRole, "member")
	s.local.SetMembers(members(3, "member", install12))
	w := httptest.NewRecorder()
	tunnel.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("through the tunnel without the role = %d", w.Code)
	}
}

// An open live socket on the LAN ends when its token does: the member signed the phone out.
func TestLocalSocketEndsWithToken(t *testing.T) {
	saved := liveAuthEvery
	liveAuthEvery = 50 * time.Millisecond
	t.Cleanup(func() { liveAuthEvery = saved })
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	g, err := s.local.Issue("cloud:12", install12)
	if err != nil {
		t.Fatal(err)
	}
	withRole := members(1, "member", install12)
	withRole.Members[0].Roles = []string{"gosia"}
	s.local.SetMembers(withRole)
	ts := httptest.NewServer(s.LocalHandler())
	t.Cleanup(ts.Close)
	dial := func(dash string) *websocket.Conn {
		t.Helper()
		h := http.Header{"Authorization": {"Bearer " + g.Token}}
		c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/dashboard/"+dash+"/ws", h)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	closedWith := func(c *websocket.Conn, code int) {
		t.Helper()
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				if !websocket.IsCloseError(err, code) {
					t.Fatalf("closed with %v, want %d", err, code)
				}
				return
			}
		}
	}

	// the role taken away: gosia's dashboard goes
	gosia := dial("gosia")
	s.local.SetMembers(members(2, "member", install12))
	closedWith(gosia, liveCloseGone)

	// signed out: the phone is no longer in the list
	home := dial("home")
	s.local.SetMembers(members(3, "member"))
	closedWith(home, liveCloseDenied)
}

// Unpairing ends the app's local tokens at once, and the next pairing's first members list is
// taken whatever its version (another home counts from 1 again).
func TestLocalAccessRevoke(t *testing.T) {
	la := newLocal(t, t.TempDir())
	la.SetMembers(members(40, "owner", install12))
	g, err := la.Issue("cloud:12", install12)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := la.Viewer(g.Token); !ok {
		t.Fatal("token not working before")
	}
	la.Revoke()
	if _, _, _, ok := la.Viewer(g.Token); ok {
		t.Error("token works after Revoke")
	}
	if _, _, known := la.MemberRoles("cloud:12"); known {
		t.Error("members still known after Revoke")
	}
	la.SetMembers(members(1, "member", install12))
	if _, member, known := la.MemberRoles("cloud:12"); !known || !member {
		t.Error("the new pairing's first list (version 1) was not taken")
	}
}

// History names a member by the cloud's list, and a home's cloud app by its latest command's name
// and its member's; an app vCLU has not heard of yet keeps its member's name.
func TestActorName(t *testing.T) {
	la := newLocal(t, t.TempDir())
	la.SetMembers(members(1, "owner"))
	la.NoteApp("3", "MacBar")
	for actor, want := range map[string]string{
		"cloud:12":         "Anna",
		"app:3@cloud:12":   "MacBar · Anna",
		"app:4@cloud:12":   "Anna",
		"app:3@cloud:99":   "MacBar",
		"cloud:99":         "",
		"agent:x@cloud:12": "",
	} {
		if got := la.ActorName(actor); got != want {
			t.Errorf("%s = %q, want %q", actor, got, want)
		}
	}
	la.NoteApp("3", strings.Repeat("ą", 80))
	if got := la.ActorName("app:3@cloud:99"); len([]rune(got)) != 60 {
		t.Errorf("a long name is kept as %d runes", len([]rune(got)))
	}
}
