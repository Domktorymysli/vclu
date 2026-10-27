package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// The Access page and its API know remote access (#39), and a change there reaches the
// Lua runtime at once.
func remoteAccessServer(t *testing.T) (*lua.LuaEngine, *gin.Engine) {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	dir := t.TempDir()
	mgr := accesscontrol.NewManager(dir)
	if err := mgr.Load(); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine, accessManager: mgr, serverDir: dir}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/access", s.handleAPIAccessList)
	r.POST("/api/access", s.handleAPIAccessSet)
	r.GET("/api/access/integrations", s.handleAPIAccessIntegrations)
	r.GET("/api/access/profiles", s.handleAPIAccessProfiles)
	r.POST("/api/access/profiles", s.handleAPIAccessProfileSave)
	r.POST("/api/access/assignments", s.handleAPIAccessAssignmentsSave)
	if _, err := engine.Execute(`
		local lamp = VirtualSwitch:new("lamp")
		registerObject("v.lamp", lamp)
		expose(lamp, "switch", { path = "v.lamp", name = "Lamp" })
	`); err != nil {
		t.Fatal(err)
	}
	s.syncAccessControlToLua()
	return engine, r
}

func serve(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func remoteVisible(t *testing.T, engine *lua.LuaEngine) string {
	t.Helper()
	out, err := engine.Execute(`(function()
		local o = {}
		for i, e in ipairs(RemoteAccess.objects()) do o[i] = e.path .. "=" .. e.access end
		return table.concat(o, ",")
	end)()`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAccessAPI_ListsRemoteIntegration(t *testing.T) {
	_, r := remoteAccessServer(t)
	w := serve(r, http.MethodGet, "/api/access/integrations", "")
	if !strings.Contains(w.Body.String(), `"id":"remote"`) {
		t.Errorf("integrations without remote: %s", w.Body)
	}

	w = serve(r, http.MethodGet, "/api/access", "")
	var res struct {
		Objects []struct {
			Path   string            `json:"path"`
			Access map[string]string `json:"access"`
		} `json:"objects"`
		Integrations []string `json:"integrations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("%v: %s", err, w.Body)
	}
	found := false
	for _, o := range res.Objects {
		if o.Path == "v.lamp" {
			found = true
			if o.Access["remote"] != "hidden" || o.Access["mqtt"] != "full" {
				t.Errorf("v.lamp access = %v, want remote hidden, mqtt full", o.Access)
			}
		}
	}
	if !found {
		t.Errorf("v.lamp not listed: %s", w.Body)
	}
	if !strings.Contains(strings.Join(res.Integrations, ","), "remote") {
		t.Errorf("integrations = %v, want remote among them", res.Integrations)
	}

	w = serve(r, http.MethodGet, "/api/access/profiles", "")
	if !strings.Contains(w.Body.String(), `"name":"no-access"`) {
		t.Errorf("no-access profile not listed: %s", w.Body)
	}
}

func TestAccessAPI_SetRemoteObjectAppliesLive(t *testing.T) {
	engine, r := remoteAccessServer(t)
	if got := remoteVisible(t, engine); got != "" {
		t.Fatalf("remote sees %q before any change", got)
	}

	w := serve(r, http.MethodPost, "/api/access", `{"path":"v.lamp","integration":"remote","access":"readonly"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := remoteVisible(t, engine); got != "v.lamp=readonly" {
		t.Errorf("after setting readonly remote sees %q", got)
	}
}

func TestAccessAPI_AssignProfileToRemote(t *testing.T) {
	engine, r := remoteAccessServer(t)
	if w := serve(r, http.MethodPost, "/api/access/profiles", `{"name":"outside","default":"hidden","objects":{"v.lamp":"full"}}`); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := serve(r, http.MethodPost, "/api/access/assignments", `{"assignments":{"remote":["outside"]}}`); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := remoteVisible(t, engine); got != "v.lamp=full" {
		t.Errorf("after assigning remote sees %q", got)
	}
	if w := serve(r, http.MethodPost, "/api/access/assignments", `{"assignments":{"remote":["no-access"]}}`); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := remoteVisible(t, engine); got != "" {
		t.Errorf("after closing remote sees %q", got)
	}
}

func TestAccessAPI_RejectsUnknownIntegration(t *testing.T) {
	_, r := remoteAccessServer(t)
	if w := serve(r, http.MethodPost, "/api/access", `{"path":"v.lamp","integration":"telnet","access":"full"}`); w.Code != http.StatusBadRequest {
		t.Errorf("set for an unknown integration: %d %s", w.Code, w.Body)
	}
	if w := serve(r, http.MethodPost, "/api/access/assignments", `{"assignments":{"telnet":["full-access"]}}`); w.Code != http.StatusBadRequest {
		t.Errorf("assignment for an unknown integration: %d %s", w.Code, w.Body)
	}
}

// A bot on the user's own no-access follows it to its new name at startup (#143 review).
func TestAccessRename_BotProfilesFollow(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "access_control.yaml"), []byte("profiles:\n  no-access:\n    default: full\n    objects:\n      CLU.BOILER: hidden\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := config.UpdateMasterConfig(dir, func(c *config.VCLUConfig) error {
		c.MCP = &config.VCLUMCPConfig{Bots: []config.MCPBot{
			{ID: "a", Name: "A", Profiles: []string{"no-access"}},
			{ID: "b", Name: "B", Profiles: []string{"full-access"}},
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	mgr := accesscontrol.NewManager(dir)
	if err := mgr.Load(); err != nil {
		t.Fatal(err)
	}
	applyProfileRenames(dir, mgr)

	cfg, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, b := cfg.MCP.Bots[0].Profiles, cfg.MCP.Bots[1].Profiles
	if strings.Join(a, ",") != "no-access-user" || strings.Join(b, ",") != "full-access" {
		t.Fatalf("bot profiles after startup: a=%v b=%v", a, b)
	}
	if got := mgr.ResolveAccess(a, "CLU.LAMP"); got != accesscontrol.AccessFull {
		t.Errorf("bot a CLU.LAMP = %q, want full", got)
	}
	if got := mgr.ResolveAccess(a, "CLU.BOILER"); got != accesscontrol.AccessHidden {
		t.Errorf("bot a CLU.BOILER = %q, want hidden", got)
	}
}
