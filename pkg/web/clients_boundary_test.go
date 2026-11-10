package web

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
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
