package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

const actionsPlugin = `
local p = Plugin:new("zakupy", {})
local items = {}
p:action("add", {description = {pl = "Dodaj", en = "Add"}, params = {
	item = {type = "string", required = true}, qty = {type = "number"},
}}, function(params)
	if params.item == "x" then return nil, "no x" end
	items[#items + 1] = params.item
	return {count = #items}
end)
p:dashboard("lista"):section("L"):button("b", "B", "x", function() end):build()
`

// The panel's router with auth on, one plugin running, and its API key.
func actionsPanel(t *testing.T) (http.Handler, string, *lua.LuaEngine) {
	t.Helper()
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`PluginSandbox:create("@jan/zakupy", {dashboards = {lista = {name = "Zakupy"}}})
		local _, err = PluginSandbox:execute("@jan/zakupy", [==[` + actionsPlugin + `]==])
		assert(err == nil, err)`); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := NewServer(0, "../../web", dir, &config.CLUDeviceConfig{}, &config.CLUKeys{}, engine)
	t.Cleanup(func() {
		if s.live != nil {
			s.live.shutdown()
		}
	})
	gin.SetMode(gin.TestMode)
	key := newAuthWithAPIKey(t, s)
	return s.router, key, engine
}

func actionsReq(t *testing.T, r http.Handler, method, path, key, body string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func TestPluginActionsAPI(t *testing.T) {
	r, key, _ := actionsPanel(t)
	path := "/api/plugins/%40jan%2Fzakupy/actions/add"

	// the same auth as the other plugin routes
	if code, _ := actionsReq(t, r, http.MethodGet, "/api/plugins/actions", "", ""); code != http.StatusUnauthorized {
		t.Errorf("list without auth: %d", code)
	}
	if code, _ := actionsReq(t, r, http.MethodPost, path, "", `{"item":"milk"}`); code != http.StatusUnauthorized {
		t.Errorf("call without auth: %d", code)
	}

	code, body := actionsReq(t, r, http.MethodGet, "/api/plugins/actions", key, "")
	var list []map[string]any
	if code != http.StatusOK || json.Unmarshal([]byte(body), &list) != nil || len(list) != 1 {
		t.Fatalf("list: %d %s", code, body)
	}
	if list[0]["plugin"] != "@jan/zakupy" || list[0]["name"] != "add" {
		t.Errorf("list: %v", list[0])
	}
	params, _ := list[0]["params"].(map[string]any)
	if item, _ := params["item"].(map[string]any); item["type"] != "string" || item["required"] != true {
		t.Errorf("params: %v", params)
	}
	if code, body := actionsReq(t, r, http.MethodGet, "/api/plugins/actions?plugin=%40jan%2Fother", key, ""); code != http.StatusOK || body != "[]" {
		t.Errorf("filter: %d %s", code, body)
	}
	if code, _ := actionsReq(t, r, http.MethodGet, "/api/plugins/actions?plugin=Bad%20Id", key, ""); code != http.StatusBadRequest {
		t.Errorf("bad filter: %d", code)
	}

	for _, tc := range []struct {
		path, body string
		code       int
		want       string
	}{
		{path, `{"item":"milk","qty":2}`, http.StatusOK, `{"result":{"count":1`},
		{path, `{"item":"milk","qty":null}`, http.StatusOK, `"count":2`},
		{path, ``, http.StatusBadRequest, `param item is required`},
		{path, `{"item":1}`, http.StatusBadRequest, `param item is a string`},
		{path, `{"item":"a","extra":1}`, http.StatusBadRequest, `unknown param extra`},
		{path, `[1]`, http.StatusBadRequest, `params are a JSON object`},
		{path, `{"item":"x"}`, http.StatusUnprocessableEntity, `{"error":"no x"}`},
		{"/api/plugins/%40jan%2Fzakupy/actions/nope", `{}`, http.StatusNotFound, `no action nope`},
		{"/api/plugins/%40jan%2Fother/actions/add", `{}`, http.StatusNotFound, `has no action`},
		{"/api/plugins/%40jan%2Fzakupy/actions/Bad", `{}`, http.StatusNotFound, `no such action`},
	} {
		code, body := actionsReq(t, r, http.MethodPost, tc.path, key, tc.body)
		if code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s %s: %d %s, want %d %s", tc.path, tc.body, code, body, tc.code, tc.want)
		}
	}
}

// Disabling the plugin (its cleanup) takes the action and the dashboard: 404 for both.
func TestPluginActionsGoneWithPlugin(t *testing.T) {
	r, key, engine := actionsPanel(t)
	// a session dashboard: the page sends a visitor to log in
	if code, _ := actionsReq(t, r, http.MethodGet, "/d/zakupy_lista", "", ""); code != http.StatusFound {
		t.Fatalf("dashboard: %d", code)
	}
	if _, err := engine.Execute(`PluginSandbox:cleanup("@jan/zakupy")`); err != nil {
		t.Fatal(err)
	}
	dashCache.Invalidate()
	if code, body := actionsReq(t, r, http.MethodGet, "/api/plugins/actions", key, ""); code != http.StatusOK || body != "[]" {
		t.Errorf("list after cleanup: %d %s", code, body)
	}
	if code, _ := actionsReq(t, r, http.MethodPost, "/api/plugins/%40jan%2Fzakupy/actions/add", key, `{"item":"a"}`); code != http.StatusNotFound {
		t.Errorf("call after cleanup: %d", code)
	}
	if code, _ := actionsReq(t, r, http.MethodGet, "/d/zakupy_lista", "", ""); code != http.StatusNotFound {
		t.Errorf("dashboard after cleanup: %d", code)
	}
}
