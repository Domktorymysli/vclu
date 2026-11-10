package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// The boundaries of the API clients and the remote viewers, one per test: who may not pass
// where, whatever they send.

// fullPanel is the panel's own router (NewServer: its auth and CSRF middleware and every
// route), with auth on and one client whose profiles reach the gate.
func fullPanel(t *testing.T) (*Server, http.Handler, string, *lua.LuaEngine) {
	t.Helper()
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	dir := t.TempDir()
	s := NewServer(0, "../../web", dir, &config.CLUDeviceConfig{}, &config.CLUKeys{}, engine)
	t.Cleanup(func() {
		if s.live != nil {
			s.live.shutdown()
		}
	})
	gin.SetMode(gin.TestMode)
	if err := config.SaveAuth(dir, "admin", "password1"); err != nil {
		t.Fatal(err)
	}
	// a client made as the panel would, without its router
	_, r := clientsSiteOn(t, s, engine)
	_, token := newClient(t, r, `{"name":"Zegarek","profiles":["brama"]}`)
	return s, s.router, token, engine
}

// clientsSiteOn is clientsSite on an existing server.
func clientsSiteOn(t *testing.T, s *Server, engine *lua.LuaEngine) (*Server, *gin.Engine) {
	t.Helper()
	site, _ := clientsSite(t)
	if _, err := engine.Execute(`
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU2", "DOU2")
		registerObject("CLU1.DOU2", sw)
		expose(sw, "switch", { path = "brama.glowna" })
		_:sync("CLU1.DOU2", 0)
	`); err != nil {
		t.Fatal(err)
	}
	s.accessManager = site.accessManager
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("auth_method", "session") })
	r.POST("/api/clients", s.handleAPIClientCreate)
	return s, r
}

// A client's token is not a login to the panel: every panel route but the device API answers
// it as it answers nobody, and the device API takes nothing but a client's token.
func TestClientTokenIsNotAPanelLogin(t *testing.T) {
	_, panel, token, _ := fullPanel(t)
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/clients"}, {"POST", "/api/clients"}, {"POST", "/api/clu/execute"},
		{"GET", "/api/access/profiles"}, {"GET", "/api/mcp/bots"}, {"POST", "/api/settings/generate-api-key"},
	} {
		if res := call(panel, p.method, p.path, token, `{}`); res.code != http.StatusUnauthorized {
			t.Errorf("%s %s with a client's token: %d", p.method, p.path, res.code)
		}
	}
	if res := call(panel, "GET", "/api/v1/devices", token, ""); res.code != http.StatusOK || len(devicePaths(res)) != 1 {
		t.Errorf("the device API with the token: %d %v", res.code, res.body)
	}
	// "/api/v1/" passes the panel's auth: anything there must be the device API's
	for _, p := range []string{"/api/v1/devices", "/api/v1/nothing", "/api/v1/../clients", "/api/v1//devices"} {
		res := call(panel, "GET", p, "", "")
		if res.code != http.StatusUnauthorized && res.code != http.StatusNotFound && res.code != http.StatusMovedPermanently {
			t.Errorf("%s without a token: %d", p, res.code)
		}
	}
	for _, r := range panel.(*gin.Engine).Routes() {
		if strings.HasPrefix(r.Path, "/api/v1/") && !strings.HasPrefix(r.Handler, "github.com/Domktorymysli/vclu/pkg/web.(*Server).handleClient") {
			t.Errorf("a panel route under /api/v1/ outside the device API: %s %s (%s)", r.Method, r.Path, r.Handler)
		}
	}
}

// Through the tunnel only the cloud's people come, never a client: its routes are not there,
// and a client's token there is no one.
func TestTunnelHasNoClients(t *testing.T) {
	s, r := clientsSite(t)
	_, token := newClient(t, r, `{"name":"Zegarek","profiles":["brama"],"roles":["gosia"]}`)
	tunnel := s.TunnelHandler()
	for _, p := range []string{"/api/v1/devices", "/api/dashboard/gosia"} {
		if res := call(tunnel, "GET", p, token, ""); res.code != http.StatusNotFound {
			t.Errorf("%s with a client's token through the tunnel: %d", p, res.code)
		}
	}
	res := call(tunnel, "GET", "/api/v1/devices", token, "", proto.HeaderActor, "cloud:1", proto.HeaderRole, "owner")
	if res.code != http.StatusNotFound {
		t.Errorf("the device API through the tunnel as an owner: %d", res.code)
	}
}

// On the LAN a member's token is not a client's and a client's is not a member's.
func TestLANTokensDoNotMix(t *testing.T) {
	s, r := clientsSite(t)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	g, err := s.local.Issue("cloud:12", install12)
	if err != nil {
		t.Fatal(err)
	}
	s.local.SetMembers(members(1, "owner", install12))
	lan := s.LocalHandler()
	_, token := newClient(t, r, `{"name":"Zegarek","profiles":["brama"]}`)

	if res := call(lan, "GET", "/api/v1/devices", g.Token, ""); res.code != http.StatusUnauthorized {
		t.Errorf("the device API with a member's token: %d", res.code)
	}
	if res := call(lan, "GET", "/api/dashboard/home", g.Token, ""); res.code != http.StatusOK {
		t.Errorf("a member's dashboard: %d", res.code)
	}
	if res := call(lan, "GET", "/api/dashboard/home", token, ""); res.code != http.StatusNotFound {
		t.Errorf("the members' dashboard with a client's token: %d", res.code)
	}
	// a token with a member's prefix and a client's tail, and the other way round
	for _, tok := range []string{"vclu_local_" + strings.TrimPrefix(token, clientTokenPrefix),
		clientTokenPrefix + strings.TrimPrefix(g.Token, "vclu_local_")} {
		if res := call(lan, "GET", "/api/v1/devices", tok, ""); res.code != http.StatusUnauthorized {
			t.Errorf("mixed token %.16s…: %d", tok, res.code)
		}
	}
	// the cloud's headers on the LAN name nobody
	res := call(lan, "GET", "/api/dashboard/home", "", "", proto.HeaderActor, "cloud:12", proto.HeaderRole, "owner")
	if res.code != http.StatusUnauthorized {
		t.Errorf("the cloud's headers on the LAN: %d", res.code)
	}
}

// What a client sends never reaches Lua as code: paths, actions and PINs with quotes, newlines
// and long brackets are only strings.
func TestClientInputIsNotLua(t *testing.T) {
	s, r := clientsSite(t)
	_, token := newClient(t, r, `{"name":"Zegarek","profiles":["wszystko"]}`)
	engine := s.luaExecutor
	evil := []string{`x"); PWNED = true; --`, "x\\\"); PWNED = true; --", "x\n PWNED = true", `x]]; PWNED = true; --[[`, "x\x00\"); PWNED = true --"}
	for _, e := range evil {
		q := jsonQuote(e)
		call(r, "POST", "/api/v1/cmd", token, `{"path":`+q+`,"action":"on"}`)
		call(r, "POST", "/api/v1/cmd", token, `{"path":"brama.glowna","action":`+q+`}`)
		call(r, "POST", "/api/v1/cmd", token, `{"path":"sejf","action":"on","pin":`+q+`}`)
		call(r, "POST", "/api/v1/states", token, `{"paths":[`+q+`]}`)
	}
	call(r, "POST", "/api/v1/cmd", token, `{"path":"brama.glowna","action":"set","value":1e308}`)
	// the same requests do reach Lua: the gate opens
	if res := call(r, "POST", "/api/v1/cmd", token, `{"path":"brama.glowna","action":"on"}`); res.code != http.StatusOK {
		t.Fatalf("a plain command: %d %v", res.code, res.body)
	}
	if out, _ := engine.Execute(`tostring(PWNED)`); out != "nil" {
		t.Fatalf("a client's input ran as Lua: PWNED = %s", out)
	}
}

func jsonQuote(s string) string { return mustJSON(s) }

// A client's access is only what its profiles give: a profile deleted after the client was
// made gives nothing, a device readonly in its expose options stays readonly with a full
// profile, and without the access manager nothing at all.
func TestClientAccessEdges(t *testing.T) {
	s, r := clientsSite(t)
	_, gone := newClient(t, r, `{"name":"Stary","profiles":["usuniety"]}`)
	if got := devicePaths(call(r, "GET", "/api/v1/devices", gone, "")); len(got) != 0 {
		t.Errorf("a profile that does not exist: %v", got)
	}
	if res := call(r, "POST", "/api/v1/cmd", gone, `{"path":"brama.glowna","action":"on"}`); res.code != http.StatusNotFound {
		t.Errorf("a command with a profile that does not exist: %d", res.code)
	}

	if _, err := s.luaExecutor.Execute(`
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU9", "DOU9")
		registerObject("CLU1.DOU9", sw)
		expose(sw, "switch", { path = "czujnik", readonly = true })
		local c = RemoteSwitch:new({ executeNoReply = function() end }, "DOU8", "DOU8")
		registerObject("CLU1.DOU8", c)
		expose(c, "switch", { path = "piec" }):remote({ confirm = true })
	`); err != nil {
		t.Fatal(err)
	}
	_, full := newClient(t, r, `{"name":"Pelny","profiles":["wszystko"]}`)
	if got := devicePaths(call(r, "GET", "/api/v1/devices", full, "")); got["czujnik"] != "readonly" || got["piec"] != "confirm" {
		t.Errorf("devices' own access: %v", got)
	}
	if res := call(r, "POST", "/api/v1/cmd", full, `{"path":"czujnik","action":"on"}`); res.code != http.StatusForbidden {
		t.Errorf("a readonly device with a full profile: %d %v", res.code, res.body)
	}
	if res := call(r, "POST", "/api/v1/cmd", full, `{"path":"piec","action":"on"}`); res.code != http.StatusConflict || res.body["reason"] != "confirm" {
		t.Errorf("a device needing confirmation: %d %v", res.code, res.body)
	}
	if res := call(r, "POST", "/api/v1/cmd", full, `{"path":"piec","action":"on","confirmed":true}`); res.code != http.StatusOK {
		t.Errorf("confirmed: %d %v", res.code, res.body)
	}

	s.accessManager = nil
	if got := devicePaths(call(r, "GET", "/api/v1/devices", full, "")); len(got) != 0 {
		t.Errorf("without the access manager: %v", got)
	}
}

// A client expires at its second, not after; one made without an expiry never does.
func TestClientExpiry(t *testing.T) {
	s, r := clientsSite(t)
	at := time.Now().Add(2 * time.Second).Unix()
	_, token := newClient(t, r, `{"name":"Gosc","profiles":["podglad"],"expiresAt":`+jsonInt(at)+`}`)
	if _, ok := s.apiClient(token); !ok {
		t.Fatal("not working before its expiry")
	}
	time.Sleep(time.Until(time.Unix(at, 0)) + 50*time.Millisecond)
	if _, ok := s.apiClient(token); ok {
		t.Error("still working at its expiry")
	}
	if res := call(r, "POST", "/api/clients", "", `{"name":"x","expiresAt":-1}`, "X-Test-Auth", "session"); res.code != http.StatusBadRequest {
		t.Errorf("a negative expiry: %d", res.code)
	}
}

// The PIN's tries are counted per device whoever guesses: a client guessing locks it for
// the cloud's people as well, and the right PIN waits until the lock ends.
func TestClientPinTries(t *testing.T) {
	_, r := clientsSite(t)
	_, token := newClient(t, r, `{"name":"Zegarek","profiles":["brama"]}`)
	for i := 0; i < 5; i++ {
		call(r, "POST", "/api/v1/cmd", token, `{"path":"sejf","action":"on","pin":"0000"}`)
	}
	if res := call(r, "POST", "/api/v1/cmd", token, `{"path":"sejf","action":"on","pin":"0420"}`); res.code != http.StatusTooManyRequests || res.body["reason"] != "rate_limited" {
		t.Errorf("the right PIN after five wrong ones: %d %v", res.code, res.body)
	}
}

// Through the tunnel with a members list, someone it does not name gets no dashboard and no
// camera, but the static files the pages need.
func TestTunnelStranger(t *testing.T) {
	s, _ := clientsSite(t)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	s.local.SetMembers(members(1, "owner", install12))
	tunnel := s.TunnelHandler()
	for _, p := range []string{"/d/home", "/api/dashboard/home", "/api/camera/front/snapshot"} {
		if res := call(tunnel, "GET", p, "", "", proto.HeaderActor, "cloud:99", proto.HeaderRole, "owner"); res.code != http.StatusNotFound {
			t.Errorf("%s for someone the list does not name: %d", p, res.code)
		}
	}
	if res := call(tunnel, "GET", "/api/dashboard/home", "", "", proto.HeaderActor, "cloud:12", proto.HeaderRole, "owner"); res.code != http.StatusOK {
		t.Errorf("a member: %d", res.code)
	}
}

// The panel's API key manages no client, whatever the method; the list never shows a token
// or its hash.
func TestClientsPanelOnly(t *testing.T) {
	_, r := clientsSite(t)
	id, token := newClient(t, r, `{"name":"CLI","profiles":["podglad"]}`)
	for _, p := range []struct{ method, path, body string }{
		{"PUT", "/api/clients/" + id, `{"name":"x"}`}, {"DELETE", "/api/clients/" + id, ""},
		{"POST", "/api/clients/" + id + "/regenerate", ""},
	} {
		if res := call(r, p.method, p.path, "", p.body, "X-Test-Auth", "apikey"); res.code != http.StatusForbidden {
			t.Errorf("%s %s with the API key: %d", p.method, p.path, res.code)
		}
	}
	res := call(r, "GET", "/api/clients", "", "", "X-Test-Auth", "session")
	if b := mustJSON(res.body); strings.Contains(b, token) || strings.Contains(b, config.HashAPIKey(token)) || strings.Contains(strings.ToLower(b), "hash") {
		t.Errorf("the list shows the token: %s", b)
	}
}

// A level a profile file should not have (edited by hand) reaches nothing; states answer
// only readable paths and refuse bad requests.
func TestClientLevelsAndStates(t *testing.T) {
	s, r := clientsSite(t)
	if err := s.accessManager.SaveProfile("dziwny", accesscontrol.Profile{Default: `full"); PWNED = true; --`}); err != nil {
		t.Fatal(err)
	}
	_, odd := newClient(t, r, `{"name":"Dziwny","profiles":["dziwny"]}`)
	if got := devicePaths(call(r, "GET", "/api/v1/devices", odd, "")); len(got) != 0 {
		t.Errorf("devices with an unknown level: %v", got)
	}
	if res := call(r, "POST", "/api/v1/cmd", odd, `{"path":"brama.glowna","action":"on"}`); res.code != http.StatusNotFound {
		t.Errorf("a command with an unknown level: %d", res.code)
	}
	if out, _ := s.luaExecutor.Execute(`tostring(PWNED)`); out != "nil" {
		t.Fatalf("a profile's level ran as Lua")
	}

	_, viewer := newClient(t, r, `{"name":"Tablet","profiles":["podglad"]}`)
	if res := call(r, "POST", "/api/v1/states", viewer, `{"paths":["salon.lampa"]}`); res.code != 200 || len(res.body["states"].([]any)) != 1 {
		t.Errorf("a readonly client reads: %d %v", res.code, res.body)
	}
	many := make([]string, proto.MaxStatePaths+1)
	for i := range many {
		many[i] = "salon.lampa"
	}
	for _, body := range []string{`{"paths":` + mustJSON(many) + `}`, `{"paths":[1]}`, `{"paths":"salon.lampa"}`, `x`} {
		if res := call(r, "POST", "/api/v1/states", viewer, body); res.code != http.StatusBadRequest {
			t.Errorf("states %.40s: %d", body, res.code)
		}
	}
}

// The device API takes only a bearer token: a panel session cookie is no client.
func TestClientAPINotByCookie(t *testing.T) {
	s, panel, _, _ := fullPanel(t)
	session := s.sessions.create()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/cmd", strings.NewReader(`{"path":"brama.glowna","action":"on"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
	w := httptest.NewRecorder()
	panel.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("the device API with the panel's session: %d", w.Code)
	}
}

// A flood of tokens that are no client's is slowed per address, and a real client from
// another address is not.
func TestClientBadTokenFlood(t *testing.T) {
	_, r := clientsSite(t)
	_, token := newClient(t, r, `{"name":"CLI","profiles":["podglad"]}`)
	last := 0
	for i := 0; i < 25; i++ {
		last = call(r, "GET", "/api/v1/devices", "vclu_client_nope", "", "X-Forwarded-For", "10.0.0.9").code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after 25 bad tokens: %d", last)
	}
	if res := call(r, "GET", "/api/v1/devices", token, ""); res.code != http.StatusOK {
		t.Errorf("a client from another address: %d", res.code)
	}
}

// Without a panel password nobody gives clients: anyone on the network could open the panel.
// The list says so, and an existing client can still be deleted.
func TestClientsNeedAPanelPassword(t *testing.T) {
	s, r := clientsSite(t)
	id, _ := newClient(t, r, `{"name":"CLI","profiles":["podglad"]}`)
	if err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error { mc.Auth = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if config.IsAuthEnabled(s.serverDir) {
		t.Fatal("auth still on")
	}
	for _, p := range []struct{ method, path, body string }{
		{"POST", "/api/clients", `{"name":"Intruz","profiles":["wszystko"],"roles":["owner"]}`},
		{"PUT", "/api/clients/" + id, `{"name":"CLI","profiles":["wszystko"]}`},
		{"POST", "/api/clients/" + id + "/regenerate", ""},
	} {
		if res := call(r, p.method, p.path, "", p.body, "X-Test-Auth", "session"); res.code != http.StatusForbidden || res.body["code"] != "auth_required" {
			t.Errorf("%s %s without a panel password: %d %v", p.method, p.path, res.code, res.body)
		}
	}
	if res := call(r, "GET", "/api/clients", "", "", "X-Test-Auth", "session"); res.code != 200 || res.body["authEnabled"] != false {
		t.Errorf("the list without a password: %d %v", res.code, res.body["authEnabled"])
	}
	if res := call(r, "DELETE", "/api/clients/"+id, "", "", "X-Test-Auth", "session"); res.code != 200 {
		t.Errorf("delete without a password: %d", res.code)
	}
}
