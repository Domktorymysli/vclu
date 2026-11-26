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

// Review of #206 over HTTP: a plugin's dashboard and its actions under the panel's auth.

const reviewAppsPlugin = `
local p = Plugin:new("zakupy", {})
local items = { { id = 1, label = "Mleko" } }
p:action("add", {description = "Dodaj", params = {item = {type = "string", required = true}}}, function(params)
	items[#items + 1] = { id = #items + 1, label = params.item }
	return #items
end)
p:dashboard("lista"):section("L")
	:list("ls", "L", function() return items end, { onAdd = function(text) items[#items + 1] = { id = #items + 1, label = text } end })
	:build()
`

func reviewAppsServer(t *testing.T) (*Server, *lua.LuaEngine, string) {
	t.Helper()
	dashCache.Invalidate()
	saved := actionLimiter
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1000, burst: 1000}
	t.Cleanup(func() { actionLimiter = saved })
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`PluginSandbox:create("@jan/zakupy", {dashboards = {lista = {name = "Zakupy"}}})
		local _, err = PluginSandbox:execute("@jan/zakupy", [==[` + reviewAppsPlugin + `]==])
		assert(err == nil, err)`); err != nil {
		t.Fatal(err)
	}
	s := NewServer(0, "../../web", t.TempDir(), &config.CLUDeviceConfig{}, &config.CLUKeys{}, engine)
	t.Cleanup(func() {
		if s.live != nil {
			s.live.shutdown()
		}
	})
	gin.SetMode(gin.TestMode)
	return s, engine, newAuthWithAPIKey(t, s)
}

func reviewReq(s *Server, method, path, body string, cookie *http.Cookie, csrf string) (int, string) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	s.router.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

// The plugin's dashboard is the panel's until a module lets it out: no login, no list.
func TestReviewPluginDashboardNeedsSession(t *testing.T) {
	s, engine, _ := reviewAppsServer(t)
	add := `{"widgetId":"ls","action":"add","value":"Masło"}`
	if code, _ := reviewReq(s, http.MethodGet, "/api/dashboard/zakupy_lista", "", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("definition without a session: %d", code)
	}
	if code, _ := reviewReq(s, http.MethodPost, "/api/dashboard/zakupy_lista/action", add, nil, ""); code != http.StatusUnauthorized {
		t.Errorf("action without a session: %d", code)
	}
	sess := &http.Cookie{Name: sessionCookieName, Value: s.sessions.create()}
	if code, body := reviewReq(s, http.MethodPost, "/api/dashboard/zakupy_lista/action", add, sess, ""); code != http.StatusOK || !strings.Contains(body, `"total":2`) {
		t.Errorf("action with a session: %d %s", code, body)
	}

	// a module's grant opens it; "off" closes it for everyone
	if _, err := engine.Execute(`Plugin.getPlugin("@jan/zakupy"):dashboardAccess("lista", { auth = "public" })`); err != nil {
		t.Fatal(err)
	}
	if code, _ := reviewReq(s, http.MethodGet, "/api/dashboard/zakupy_lista", "", nil, ""); code != http.StatusOK {
		t.Errorf("public: %d", code)
	}
	if _, err := engine.Execute(`Plugin.getPlugin("@jan/zakupy"):dashboardAccess("lista", { auth = "off" })`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []*http.Cookie{nil, sess} {
		if code, _ := reviewReq(s, http.MethodPost, "/api/dashboard/zakupy_lista/action", add, c, ""); code != http.StatusNotFound {
			t.Errorf("off, session %v: %d", c != nil, code)
		}
	}
}

// An action's body is bounded before it is read.
func TestReviewDashboardActionBodyCap(t *testing.T) {
	s, engine, _ := reviewAppsServer(t)
	if _, err := engine.Execute(`Plugin.getPlugin("@jan/zakupy"):dashboardAccess("lista", { auth = "public" })`); err != nil {
		t.Fatal(err)
	}
	big := `{"widgetId":"ls","action":"add","value":"` + strings.Repeat("x", 70<<10) + `"}`
	if code, body := reviewReq(s, http.MethodPost, "/api/dashboard/zakupy_lista/action", big, nil, ""); code != http.StatusBadRequest {
		t.Errorf("70 KiB: %d %.80s", code, body)
	}
	long := `{"widgetId":"ls","action":"add","value":"` + strings.Repeat("x", 201) + `"}`
	if code, body := reviewReq(s, http.MethodPost, "/api/dashboard/zakupy_lista/action", long, nil, ""); code != http.StatusOK || !strings.Contains(body, "longer than 200") {
		t.Errorf("201 characters: %d %s", code, body)
	}
}

// A plugin's action from the panel's session needs its CSRF token, as any other change.
func TestReviewPluginActionCSRF(t *testing.T) {
	s, _, key := reviewAppsServer(t)
	path := "/api/plugins/%40jan%2Fzakupy/actions/add"
	token := s.sessions.create()
	sess := &http.Cookie{Name: sessionCookieName, Value: token}
	if code, _ := reviewReq(s, http.MethodPost, path, `{"item":"a"}`, sess, ""); code != http.StatusForbidden {
		t.Errorf("session without CSRF: %d", code)
	}
	if code, body := reviewReq(s, http.MethodPost, path, `{"item":"a"}`, sess, s.sessions.getCSRF(token)); code != http.StatusOK {
		t.Errorf("session with CSRF: %d %s", code, body)
	}
	if code, _ := reviewReq(s, http.MethodPost, path, `{"item":"a"}`, &http.Cookie{Name: sessionCookieName, Value: "forged"}, "x"); code != http.StatusUnauthorized {
		t.Errorf("forged session: %d", code)
	}
	// a body that is not an object, too big, or an unknown plugin
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`["a"]`))
	req.Header.Set("X-API-Key", key)
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("array body: %d", w.Code)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/plugins/%40jan%2Finny/actions/add", strings.NewReader(`{}`))
	req.Header.Set("X-API-Key", key)
	s.router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown plugin: %d", w.Code)
	}
}
