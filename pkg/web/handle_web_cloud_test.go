package web

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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

	devs := sharedDevices(`[{"path":"b","name":"Światło","area":"Biuro","type":"light","access":"full"},`+
		`{"path":"a","name":"","area":"Biuro","type":"cover","access":"confirm","roles":["gosia"]},`+
		`{"path":"c","name":"Brama","type":"garage_door","access":"pin","roles":["owner"]}]`, members)
	if len(devs) != 3 || devs[0].Name != "Brama" || devs[1].Name != "a" || devs[2].Name != "Światło" {
		t.Fatalf("devices not sorted by area and name, or a nameless one not named by its path: %+v", devs)
	}
	if !slices.Equal(devs[1].Nobody, []string{"gosia"}) || devs[0].Nobody != nil || devs[1].Access != "confirm" {
		t.Errorf("devices = %+v", devs)
	}
	if sharedDevices("", members) != nil || sharedDevices("{", members) != nil {
		t.Error("no devices from nothing")
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
	for _, want := range []string{"Udostępnione dashboardy", "Udostępnione urządzenia", `href="/d/gosia"`, ">domownicy<", "PIN"} {
		if !strings.Contains(body, want) {
			t.Errorf("cloud page lacks %q", want)
		}
	}
}

// The access page shows, read-only, what Lua shares with the cloud: roles, how, or nothing.
func TestAccessListShowsCloudShare(t *testing.T) {
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		expose(VirtualObject:new("v1", { value = 0 }), "light", { name = "Lampa" }):remote()
		expose(VirtualObject:new("v2", { value = 0 }), "cover", { name = "Roleta" }):remote({ confirm = true }):roles('biuro')
		expose(VirtualObject:new("v3", { value = 0 }), "light", { name = "Tylko dom" })
	`); err != nil {
		t.Fatal(err)
	}
	e := newHistoryEnv(t)
	e.s.luaExecutor = engine
	var resp struct {
		Objects []struct {
			Name  string `json:"name"`
			Cloud *struct {
				Access string   `json:"access"`
				Roles  []string `json:"roles"`
			} `json:"cloud"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(e.do(t, "GET", "/api/access", "", "session").Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, o := range resp.Objects {
		if o.Cloud == nil {
			got[o.Name] = "-"
		} else {
			got[o.Name] = o.Cloud.Access + " " + strings.Join(o.Cloud.Roles, ",")
		}
	}
	want := map[string]string{"v1": "full ", "v2": "confirm biuro", "v3": "-"} // the access page names objects by their own names
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s: cloud = %q, want %q (all: %v)", name, got[name], w, got)
		}
	}
}

// The roles card joins who has a role with what Lua opens with it, and keeps a role nobody has.
func TestCloudPageHomeRoles(t *testing.T) {
	members := []proto.Member{
		{User: "cloud:1", Name: "Tytus", Roles: []string{"biuro", "owner"}},
		{User: "cloud:2", Roles: []string{"biuro"}},
	}
	shared := []sharedDashboard{{Name: "Biuro", Roles: []string{"biuro"}}, {Name: "Dom"}}
	devices := []sharedDevice{{Name: "Brama", Roles: []string{"owner", "goscie"}}, {Name: "Lampa", Roles: []string{"biuro"}}}
	got := homeRoles(members, shared, devices)
	if len(got) != 3 || got[0].Name != "biuro" || got[1].Name != "goscie" || got[2].Name != "owner" {
		t.Fatalf("roles = %+v", got)
	}
	if !slices.Equal(got[0].People, []string{"Tytus", "cloud:2"}) || !slices.Equal(got[0].Dashboards, []string{"Biuro"}) || got[0].Devices != 1 {
		t.Errorf("biuro = %+v", got[0])
	}
	if got[1].People != nil || got[1].Devices != 1 {
		t.Errorf("a role only Lua names = %+v", got[1])
	}
}

// The connection card: availability, pings in ms, an SVG with the outage and a failed ping,
// and outages named by kind.
func TestCloudLinkView(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	now := t0.Add(24 * time.Hour)
	v := newCloudLinkView(cloud.LinkReport{
		Since: t0, Availability: 0.9975,
		Samples: []cloud.LinkSample{{At: t0.Add(time.Hour), RTT: 40, OK: true}, {At: t0.Add(2 * time.Hour), RTT: 0}, {At: t0.Add(3 * time.Hour), RTT: 60, OK: true}},
		Outages: []cloud.Outage{
			{From: now.Add(-10 * time.Minute), Reason: "connection lost: EOF"},
			{From: t0.Add(5 * time.Hour), To: t0.Add(7*time.Hour + 5*time.Minute), Reason: "dial tcp: lookup cloud.vclu.pl: no such host"},
			{From: t0.Add(4 * time.Hour), To: t0.Add(4*time.Hour + 20*time.Second), Reason: "something new"},
		},
	}, now)
	if v.Availability != "99.8" || v.Last != "60" || v.Avg != "50" || v.Max != "60" {
		t.Errorf("figures = %q %q %q %q", v.Availability, v.Last, v.Avg, v.Max)
	}
	chart := string(v.Chart)
	for _, want := range []string{`<rect class="down"`, `<line class="fail"`, `<polyline class="rtt"`} {
		if !strings.Contains(chart, want) {
			t.Errorf("chart lacks %s: %s", want, chart)
		}
	}
	o := v.Outages
	if !o[0].StillGoing || o[0].Kind != "lost" || o[0].For != "10 min" {
		t.Errorf("ongoing outage = %+v", o[0])
	}
	if o[1].Kind != "network" || o[1].For != "2 h 5 min" || o[2].Kind != "" || o[2].Raw != "something new" || o[2].For != "<1 min" {
		t.Errorf("outages = %+v", o[1:])
	}
	if v := newCloudLinkView(cloud.LinkReport{Since: t0, Availability: -1}, now); v.Availability != "" || v.Last != "" {
		t.Errorf("nothing measured = %+v", v)
	}
}
