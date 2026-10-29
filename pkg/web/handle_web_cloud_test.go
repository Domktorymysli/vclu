package web

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/Domktorymysli/vclu/pkg/cloud"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// Whoever enters the pairing code attaches the house to their account, so
// an API key (an MCP bot, a script) can read the status but never pair or
// unpair.
func TestCloudAPI_PairingNeedsSession(t *testing.T) {
	e := newHistoryEnv(t)
	if err := config.UpdateMasterConfig(e.dir, func(c *config.VCLUConfig) error {
		c.Cloud = &config.VCLUCloudConfig{URL: "http://127.0.0.1:1"} // nothing listens
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cc := cloud.New(cloud.Options{Dir: e.dir})
	cc.Start()
	t.Cleanup(func() { cc.Shutdown("test", 0) })
	e.s.SetCloudClient(cc)

	if w := e.do(t, "GET", "/api/cloud/status", "", "apikey"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"off"`) {
		t.Errorf("status with an API key: %d %s", w.Code, w.Body)
	}
	for _, url := range []string{"/api/cloud/pair", "/api/cloud/pair/cancel", "/api/cloud/unpair", "/api/cloud/reconnect"} {
		if w := e.do(t, "POST", url, "", "apikey"); w.Code != http.StatusForbidden {
			t.Errorf("%s with an API key: %d, want 403", url, w.Code)
		}
	}
	// A session may: the request reaches the (unreachable) cloud.
	if w := e.do(t, "POST", "/api/cloud/pair", "", "session"); w.Code != http.StatusBadGateway {
		t.Errorf("pair with a session: %d %s, want 502", w.Code, w.Body)
	}
	w := e.do(t, "GET", "/cloud?lang=pl", "", "session")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Połącz z chmurą") || !strings.Contains(w.Body.String(), "/static/js/cloud.js") {
		t.Errorf("cloud page: %d", w.Code)
	}
}

// The cloud page lists what Lua shares through the cloud and flags a role no member has.
func TestCloudPageListsSharedDashboards(t *testing.T) {
	members := []proto.Member{{User: "cloud:1", Role: "owner", Roles: []string{"owner"}}}
	list := sharedDashboards(`[{"id":"home","name":"Dom","roles":[]},{"id":"gosia","name":"","roles":["gosia","owner"],"pin":true}]`, members)
	if len(list) != 2 || list[0].Name != "Dom" || len(list[0].Roles) != 0 || list[1].Name != "gosia" || !list[1].Pin {
		t.Fatalf("list = %+v", list)
	}
	if !slices.Equal(list[1].Nobody, []string{"gosia"}) {
		t.Errorf("roles nobody has = %v, want [gosia]", list[1].Nobody)
	}
	if got := sharedDashboards(`[{"id":"gosia","roles":["gosia"]}]`, nil); len(got[0].Nobody) != 0 {
		t.Error("a role flagged before the cloud sent any members")
	}
	if sharedDashboards("", members) != nil || sharedDashboards("not json", members) != nil {
		t.Error("no list from nothing")
	}

	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(tunnelDashboards); err != nil {
		t.Fatal(err)
	}
	e := newHistoryEnv(t)
	e.s.luaExecutor = engine
	if err := config.UpdateMasterConfig(e.dir, func(c *config.VCLUConfig) error {
		c.Cloud = &config.VCLUCloudConfig{URL: "http://127.0.0.1:1"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cc := cloud.New(cloud.Options{Dir: e.dir})
	cc.Start()
	t.Cleanup(func() { cc.Shutdown("test", 0) })
	e.s.SetCloudClient(cc)
	body := e.do(t, "GET", "/cloud?lang=pl", "", "session").Body.String()
	for _, want := range []string{"Udostępnione przez chmurę", `href="/d/gosia"`, "wszyscy domownicy", "PIN"} {
		if !strings.Contains(body, want) {
			t.Errorf("cloud page lacks %q", want)
		}
	}
}
