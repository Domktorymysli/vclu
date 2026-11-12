package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/cloud"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/metrics"
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

// The access page shows, read-only, what Lua's :remote() says about the cloud (roles, how, or
// kept home); for the rest its own cloud column decides, hidden until opened there.
func TestAccessListShowsCloudShare(t *testing.T) {
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		expose(VirtualObject:new("v1", { value = 0 }), "light", { name = "Lampa" }):remote()
		expose(VirtualObject:new("v2", { value = 0 }), "cover", { name = "Roleta" }):remote({ confirm = true }):roles('biuro')
		expose(VirtualObject:new("v3", { value = 0 }), "light", { name = "Tylko dom" })
		expose(VirtualObject:new("v4", { value = 0 }), "light", { name = "Nigdy" }):remote(false)
	`); err != nil {
		t.Fatal(err)
	}
	e := newHistoryEnv(t)
	e.s.luaExecutor = engine
	e.s.accessManager = accesscontrol.NewManager(t.TempDir())

	type object struct {
		Path     string            `json:"path"`
		Name     string            `json:"name"`
		Access   map[string]string `json:"access"`
		CloudLua json.RawMessage   `json:"cloudLua"`
	}
	list := func() map[string]object {
		t.Helper()
		var resp struct {
			Objects []object `json:"objects"`
		}
		if err := json.Unmarshal(e.do(t, "GET", "/api/access", "", "session").Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		out := map[string]object{}
		for _, o := range resp.Objects {
			out[o.Name] = o // the access page names objects by their own names
		}
		return out
	}
	got := list()
	want := map[string]string{
		"v1": `{"access":"full"}`, "v2": `{"access":"confirm","roles":["biuro"]}`, "v3": ``, "v4": `false`,
	}
	for name, w := range want {
		o := got[name]
		if string(o.CloudLua) != w || o.Access["cloud"] != "hidden" {
			t.Errorf("%s: cloudLua = %s, access.cloud = %q; want %s, hidden", name, o.CloudLua, o.Access["cloud"], w)
		}
	}

	// Opened on the page: v3 reaches the cloud, every member, read-only; v4 stays home
	for name, level := range map[string]string{"v3": "readonly", "v4": "full"} {
		body := fmt.Sprintf(`{"path":%q,"integration":"cloud","access":%q}`, got[name].Path, level)
		if w := e.do(t, "POST", "/api/access", body, "session"); w.Code != http.StatusOK {
			t.Fatalf("set %s: %d %s", name, w.Code, w.Body)
		}
	}
	if a := list()["v3"].Access["cloud"]; a != "readonly" {
		t.Errorf("v3 access.cloud = %q after setting it", a)
	}
	out, err := engine.Execute(`local s = {}
		for _, d in ipairs(ExposedObjects:remoteList()) do s[#s + 1] = d.name .. "=" .. d.access .. "/" .. #(d.roles or {}) end
		return table.concat(s, " ")`)
	if err != nil || out != "Lampa=full/0 Roleta=confirm/1 Tylko dom=readonly/0" {
		t.Errorf("remoteList = %q (%v)", out, err)
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

// The statistics tab: each metric's state, the backlog, and the hours as bars.
func TestCloudMetricsView(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 30, 0, 0, time.UTC)
	v := newCloudMetricsView(cloud.MetricsStatus{
		On: true, Limit: 100, Series: 5,
		List: []metrics.CloudSeries{{Path: "a", Kind: "gauge"}, {Path: "b", Kind: "counter"}, {Path: "c"}, {Path: "d"}, {Path: "e"}},
		Sent: map[string]int64{
			"a": now.Add(-3 * time.Minute).Unix(),
			"b": now.Add(-26 * time.Hour).Unix(),
		},
		OverLimit: []string{"d"}, Invalid: []string{"e"},
		Points: map[string]int64{"a": 120},
		Hours:  []cloud.MetricsHour{{At: now.Add(-30 * time.Hour).Truncate(time.Hour).Unix(), Points: 9}, {At: now.Add(-time.Hour).Truncate(time.Hour).Unix(), Points: 1000}, {At: now.Truncate(time.Hour).Unix(), Points: 250}},
	}, now)
	states := []string{}
	for _, r := range v.Rows {
		states = append(states, r.State)
	}
	if !slices.Equal(states, []string{"ok", "behind", "waiting", "limit", "invalid"}) || v.Rows[1].Behind != "1 d 2 h" || v.Rows[0].Points != 120 {
		t.Errorf("rows = %+v", v.Rows)
	}
	if v.Day != 1250 || v.Behind != 1 {
		t.Errorf("day = %d, behind = %d", v.Day, v.Behind)
	}
	if n := strings.Count(string(v.Chart), `<rect class="bar"`); n != 2 {
		t.Errorf("%d bars, want 2: %s", n, v.Chart)
	}
}

// A paired vCLU's cloud page has the statistics tab, with how to start when no metric has cloud=true.
func TestCloudPageMetricsTab(t *testing.T) {
	e := newHistoryEnv(t)
	if err := config.UpdateMasterConfig(e.dir, func(c *config.VCLUConfig) error {
		c.Cloud = &config.VCLUCloudConfig{URL: "http://127.0.0.1:1", Token: "t", ConnectURL: "ws://127.0.0.1:1/ws", HomeID: 42, HomeName: "Dom", Disabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cc := cloud.New(cloud.Options{Dir: e.dir})
	cc.Start()
	t.Cleanup(func() { cc.Shutdown("test", 0) })
	e.s.SetCloudClient(cc)
	body := e.do(t, "GET", "/cloud?lang=pl", "", "session").Body.String()
	for _, want := range []string{`id="tab-metrics"`, `id="pane-metrics"`, "Statystyki w chmurze", "docs.vclu.pl/docs/cloud/statistics/"} {
		if !strings.Contains(body, want) {
			t.Errorf("cloud page lacks %q", want)
		}
	}
}

// The pairing code is for the person pairing: the API key gets the status without it.
func TestCloudStatusHidesPairingCodeFromAPIKey(t *testing.T) {
	st := cloud.Status{State: cloud.StatePairing, Pairing: &cloud.Pairing{UserCode: "KXQF-7MPA", VerifyURL: "https://cloud/pair", VerifyURLFull: "https://cloud/pair?code=KXQF-7MPA"}}
	if got := statusFor(st, true); got.Pairing.UserCode != "" || got.Pairing.VerifyURLFull != "" || got.Pairing.VerifyURL == "" {
		t.Errorf("API key sees %+v", got.Pairing)
	}
	if got := statusFor(st, false); got.Pairing.UserCode != "KXQF-7MPA" {
		t.Errorf("a session sees %+v", got.Pairing)
	}
	if st.Pairing.UserCode != "KXQF-7MPA" {
		t.Error("the client's own status was changed")
	}
}

// Without a panel password nobody may pair: anyone on the home network could attach the house to
// their own cloud account. The page says so and its button is off.
func TestCloudPairingNeedsPanelPassword(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Cloud: &config.VCLUCloudConfig{URL: "http://127.0.0.1:1"}}); err != nil {
		t.Fatal(err)
	}
	s := NewServer(0, "../../web", dir, nil, nil, nil)
	cc := cloud.New(cloud.Options{Dir: dir})
	cc.Start()
	t.Cleanup(func() { cc.Shutdown("test", 0) })
	s.SetCloudClient(cc)
	do := func(method, url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, httptest.NewRequest(method, url, nil))
		return w
	}
	if w := do("POST", "/api/cloud/pair"); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"auth_required"`) {
		t.Errorf("pair without a password: %d %s", w.Code, w.Body)
	}
	if st := cc.Status(); st.State != cloud.StateOff {
		t.Errorf("pairing started: %s", st.State)
	}
	w := do("GET", "/cloud?lang=pl")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "cloudAuthRequired") || !strings.Contains(w.Body.String(), `id="cloudPairBtn" disabled`) {
		t.Errorf("cloud page without a password: %d", w.Code)
	}
	// Unpairing and cancelling stay open: they only take the house off the cloud.
	if w := do("POST", "/api/cloud/pair/cancel"); w.Code != http.StatusOK {
		t.Errorf("cancel without a password: %d", w.Code)
	}
}
