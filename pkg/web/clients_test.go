package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// clientsSite is a vCLU with two exposed switches (salon.lampa, brama.glowna), the profiles
// "brama" (the gate only, full) and "podglad" (everything read only), the test dashboards and a
// panel router with the device API and the clients' admin endpoints.
func clientsSite(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards + `
		for _, o in ipairs({ { "DOU1", "salon.lampa" }, { "DOU2", "brama.glowna" } }) do
			local sw = RemoteSwitch:new({ executeNoReply = function() end }, o[1], o[1])
			registerObject("CLU1." .. o[1], sw)
			expose(sw, "switch", { path = o[2] })
		end
	`); err != nil {
		t.Fatal(err)
	}
	am := accesscontrol.NewManager(t.TempDir())
	if err := am.SaveProfile("brama", accesscontrol.Profile{Default: accesscontrol.AccessHidden,
		Objects: map[string]accesscontrol.AccessLevel{"brama.glowna": accesscontrol.AccessFull}}); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveProfile("podglad", accesscontrol.Profile{Default: accesscontrol.AccessReadonly}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, accessManager: am,
		calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if m := c.GetHeader("X-Test-Auth"); m != "" {
			c.Set("auth_method", m)
		}
	})
	s.clientRoutes(r)
	r.GET("/api/clients", s.handleAPIClients)
	r.POST("/api/clients", s.handleAPIClientCreate)
	r.PUT("/api/clients/:id", s.handleAPIClientUpdate)
	r.DELETE("/api/clients/:id", s.handleAPIClientDelete)
	r.POST("/api/clients/:id/regenerate", s.handleAPIClientRegenerate)
	return s, r
}

type clientResp struct {
	code int
	body map[string]any
}

func call(h http.Handler, method, path, token, body string, header ...string) clientResp {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return clientResp{w.Code, m}
}

// newClient makes a client through the panel and returns its id and token.
func newClient(t *testing.T, r http.Handler, body string) (id, token string) {
	t.Helper()
	res := call(r, "POST", "/api/clients", "", body, "X-Test-Auth", "session")
	if res.code != 200 {
		t.Fatalf("create %s: %d %v", body, res.code, res.body)
	}
	token, _ = res.body["token"].(string)
	id, _ = res.body["client"].(map[string]any)["id"].(string)
	if !strings.HasPrefix(token, clientTokenPrefix) || id == "" {
		t.Fatalf("create: %v", res.body)
	}
	return id, token
}

func devicePaths(res clientResp) map[string]string {
	out := map[string]string{}
	list, _ := res.body["devices"].([]any)
	for _, d := range list {
		m := d.(map[string]any)
		out[m["path"].(string)], _ = m["access"].(string)
	}
	return out
}

// Each client reaches what its own profiles give it, as the MCP bots do: the gate client
// controls the gate only, the viewer reads everything, a client with no profile gets nothing.
func TestClientDeviceAPI(t *testing.T) {
	_, r := clientsSite(t)
	_, gate := newClient(t, r, `{"name":"Zegarek","profiles":["brama"]}`)
	_, viewer := newClient(t, r, `{"name":"Tablet","profiles":["podglad"]}`)
	_, nothing := newClient(t, r, `{"name":"Pusty"}`)

	if got := devicePaths(call(r, "GET", "/api/v1/client/devices", gate, "")); len(got) != 1 || got["brama.glowna"] != "full" {
		t.Errorf("gate client's devices: %v", got)
	}
	if got := devicePaths(call(r, "GET", "/api/v1/client/devices", viewer, "")); len(got) != 2 || got["salon.lampa"] != "readonly" {
		t.Errorf("viewer's devices: %v", got)
	}
	if res := call(r, "GET", "/api/v1/client/devices", nothing, ""); res.code != 200 || len(devicePaths(res)) != 0 {
		t.Errorf("client with no profile: %d %v", res.code, res.body)
	}

	if res := call(r, "GET", "/api/v1/client/device?path=brama.glowna", gate, ""); res.code != 200 || res.body["access"] != "full" {
		t.Errorf("gate client's gate: %d %v", res.code, res.body)
	}
	if res := call(r, "GET", "/api/v1/client/device?path=salon.lampa", gate, ""); res.code != 404 {
		t.Errorf("gate client's lamp: %d", res.code)
	}

	if res := call(r, "POST", "/api/v1/client/set", gate, `{"path":"brama.glowna","value":true}`); res.code != 200 {
		t.Errorf("gate client opens the gate: %d %v", res.code, res.body)
	}
	if res := call(r, "POST", "/api/v1/client/set", gate, `{"path":"salon.lampa","value":true}`); res.code != 404 {
		t.Errorf("gate client on the lamp: %d", res.code)
	}
	if res := call(r, "POST", "/api/v1/client/set", viewer, `{"path":"salon.lampa","value":true}`); res.code != 403 || res.body["error"] != "forbidden" {
		t.Errorf("viewer sets: %d %v", res.code, res.body)
	}
	if res := call(r, "POST", "/api/v1/client/call", gate, `{"path":"brama.glowna","method":"_private"}`); res.code != 400 {
		t.Errorf("a private method: %d", res.code)
	}
	if res := call(r, "POST", "/api/v1/client/call", gate, `{"path":"brama.glowna","method":"nope"}`); res.code != 422 {
		t.Errorf("an unknown method: %d %v", res.code, res.body)
	}

	for _, tok := range []string{"", "vclu_client_wrong", "vclu_mcp_x"} {
		if res := call(r, "GET", "/api/v1/client/devices", tok, ""); res.code != 401 {
			t.Errorf("token %q: %d", tok, res.code)
		}
	}
}

// A deleted or expired client stops at once, a new token replaces the old one, and only the
// panel's session manages clients, never its API key.
func TestClientLifecycle(t *testing.T) {
	s, r := clientsSite(t)
	id, token := newClient(t, r, `{"name":"CLI","profiles":["podglad"]}`)
	devices := func(tok string) int { return call(r, "GET", "/api/v1/client/devices", tok, "").code }

	if res := call(r, "POST", "/api/clients", "", `{"name":"x"}`, "X-Test-Auth", "apikey"); res.code != 403 {
		t.Errorf("created with the API key: %d", res.code)
	}
	if res := call(r, "GET", "/api/clients", "", "", "X-Test-Auth", "apikey"); res.code != 403 {
		t.Errorf("listed with the API key: %d", res.code)
	}
	for _, bad := range []string{`{"name":""}`, `{"name":"x","roles":["Duże"]}`, `{"name":"x","roles":["a","a"]}`} {
		if res := call(r, "POST", "/api/clients", "", bad, "X-Test-Auth", "session"); res.code != 400 {
			t.Errorf("create %s: %d", bad, res.code)
		}
	}

	res := call(r, "POST", "/api/clients/"+id+"/regenerate", "", "", "X-Test-Auth", "session")
	fresh, _ := res.body["token"].(string)
	if devices(token) != 401 || devices(fresh) != 200 {
		t.Errorf("after a new token: old %d, new %d", devices(token), devices(fresh))
	}

	past := time.Now().Add(-time.Minute).Unix()
	if res := call(r, "PUT", "/api/clients/"+id, "", `{"name":"CLI","profiles":["podglad"],"expiresAt":`+jsonInt(past)+`}`,
		"X-Test-Auth", "session"); res.code != 200 {
		t.Fatalf("update: %d %v", res.code, res.body)
	}
	if devices(fresh) != 401 {
		t.Error("an expired client still works")
	}
	mc, _ := config.ReadMasterConfig(s.serverDir)
	if strings.Contains(mustJSON(mc), fresh) {
		t.Error("the token is in config.json")
	}

	if res := call(r, "DELETE", "/api/clients/"+id, "", "", "X-Test-Auth", "session"); res.code != 200 {
		t.Errorf("delete: %d", res.code)
	}
	if res := call(r, "DELETE", "/api/clients/"+id, "", "", "X-Test-Auth", "session"); res.code != 404 {
		t.Errorf("delete again: %d", res.code)
	}
}

// On the LAN listener a client sees the remote dashboards naming one of its roles, never one
// for every member, and uses the device API; history knows it as client:<id>.
func TestClientOnLAN(t *testing.T) {
	s, r := clientsSite(t)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	lanApp := s.LocalHandler()
	id, token := newClient(t, r, `{"name":"Zegarek","profiles":["brama"],"roles":["gosia"]}`)

	if res := call(lanApp, "GET", "/api/dashboard/gosia", token, ""); res.code != 200 {
		t.Errorf("gosia's dashboard with the role: %d", res.code)
	}
	if res := call(lanApp, "GET", "/api/dashboard/home", token, ""); res.code != 404 {
		t.Errorf("a dashboard naming no roles: %d", res.code)
	}
	if res := call(lanApp, "GET", "/api/camera/front/snapshot", token, ""); res.code != 404 {
		t.Errorf("the alarm's camera: %d", res.code)
	}
	if got := devicePaths(call(lanApp, "GET", "/api/v1/client/devices", token, "")); got["brama.glowna"] != "full" {
		t.Errorf("devices on the LAN: %v", got)
	}

	_, noRoles := newClient(t, r, `{"name":"Bez ról","profiles":["brama"]}`)
	if res := call(lanApp, "GET", "/api/dashboard/gosia", noRoles, ""); res.code != 404 {
		t.Errorf("gosia's dashboard without roles: %d", res.code)
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(ctxRemoteActor, clientActor(config.APIClient{ID: id}))
	if origin, actor := s.requestActor(c, ""); origin != "client" || actor != "client:"+id {
		t.Errorf("history: %s %s", origin, actor)
	}
}

func jsonInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// A client's open live socket ends when the client is deleted.
func TestClientSocketEndsWhenDeleted(t *testing.T) {
	saved := liveAuthEvery
	liveAuthEvery = 50 * time.Millisecond
	t.Cleanup(func() { liveAuthEvery = saved })
	s, r := clientsSite(t)
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	ts := httptest.NewServer(s.LocalHandler())
	t.Cleanup(ts.Close)
	id, token := newClient(t, r, `{"name":"Zegarek","roles":["gosia"]}`)

	h := http.Header{"Authorization": {"Bearer " + token}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/dashboard/gosia/ws", h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	call(r, "DELETE", "/api/clients/"+id, "", "", "X-Test-Auth", "session")
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, liveCloseDenied) {
				t.Fatalf("closed with %v, want %d", err, liveCloseDenied)
			}
			return
		}
	}
}

// The clients page renders in both languages, every text translated.
func TestClientsPage(t *testing.T) {
	if err := i18n.Load("../../web/locales"); err != nil {
		t.Fatal(err)
	}
	s := &Server{sessions: newSessionStore()}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tmpl := template.New("").Funcs(template.FuncMap{
		"t":    func(lang, key string) string { return i18n.Get(lang, key) },
		"json": func(v interface{}) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
	})
	r.SetHTMLTemplate(template.Must(tmpl.ParseGlob("../../web/templates/*.html")))
	r.Use(s.languageMiddleware())
	r.GET("/clients", s.handleClientsPage)
	// a missing key renders as nothing (index .t), so look for texts from each part of the page
	texts := map[string][]string{
		"pl": {"<title>Klienci API", "Dodaj klienta", "Profile dostępu", "Skopiuj teraz"},
		"en": {"<title>API clients", "Add client", "Access profiles", "Copy it now"},
	}
	for lang, want := range texts {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/clients?lang="+lang, nil))
		for _, text := range want {
			if w.Code != 200 || !strings.Contains(w.Body.String(), text) {
				t.Errorf("%s: %d, %q missing", lang, w.Code, text)
			}
		}
	}
}
