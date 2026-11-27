package lua

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// internetServer: a test server the HTTP manager treats as on the internet, reached by
// any name in names (resolved without DNS).
func internetServer(t *testing.T, hm *HTTPManager, names ...string) (port string, srv *httptest.Server) {
	t.Helper()
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("to"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)
	_, port, _ = net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	hm.isLocal = func(netip.Addr) bool { return false }
	hm.ownAddr = func(netip.Addr) bool { return false }
	hm.testResolve = map[string]string{}
	for _, n := range names {
		hm.testResolve[n] = "127.0.0.1"
	}
	return port, srv
}

func TestHTTPManagerInternetHosts(t *testing.T) {
	hm := NewHTTPManager()
	port, _ := internetServer(t, hm, "api.example.com", "other.example.com", "a.tuyaeu.com",
		"tuyaeu.com", "eviltuyaeu.com", "tuyaeu.com.evil.net", "a.tuyaeu.com.evil.net")
	u := func(host, path string) string { return "http://" + net.JoinHostPort(host, port) + path }
	get := func(id, rawURL string) error {
		status, _, _, err := hm.Request(id, "GET", rawURL, nil, "", 5000, true, true)
		if err == nil && status != 200 {
			t.Errorf("%s: status %d", rawURL, status)
		}
		return err
	}
	refused := func(err error, host string) bool {
		return err != nil && strings.Contains(err.Error(), host+" is not on the host list of @x/foo (permissions.internet)")
	}

	// no list (a module, an API 1 plugin): any host
	if err := get("@x/foo", u("other.example.com", "/")); err != nil {
		t.Errorf("unrestricted: %v", err)
	}

	// API 2 without internet: none
	hm.SetInternet("@x/foo", plugins.InternetHosts{})
	if err := get("@x/foo", u("api.example.com", "/")); !refused(err, "api.example.com") {
		t.Errorf("no internet: %v", err)
	}

	hm.SetInternet("@x/foo", plugins.InternetHosts{"api.example.com", "*.tuyaeu.com"})
	for _, ok := range []string{
		u("api.example.com", "/"), u("API.Example.COM.", "/"), u("a.tuyaeu.com", "/"),
		u("api.example.com", "/?to="+url.QueryEscape(u("a.tuyaeu.com", "/"))),
	} {
		if err := get("@x/foo", ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, c := range [][2]string{
		{"other.example.com", u("other.example.com", "/")},
		{"tuyaeu.com", u("tuyaeu.com", "/")},
		{"eviltuyaeu.com", u("eviltuyaeu.com", "/")},
		{"tuyaeu.com.evil.net", u("tuyaeu.com.evil.net", "/")},
		{"a.tuyaeu.com.evil.net", u("a.tuyaeu.com.evil.net", "/")},
		{"127.0.0.1", u("127.0.0.1", "/")},
		{"2606:4700::1111", u("2606:4700::1111", "/")},
		{"16843009", u("16843009", "/")}, // 1.1.1.1 as a number: a name, not on the list
		// the host after the user info is the one connected to
		{"other.example.com", "http://api.example.com@" + net.JoinHostPort("other.example.com", port) + "/"},
	} {
		if err := get("@x/foo", c[1]); !refused(err, c[0]) {
			t.Errorf("%s: want refusal of %s, got %v", c[1], c[0], err)
		}
	}
	// a redirect from a listed host to another one, or to an IP address
	for _, to := range []string{u("other.example.com", "/"), u("127.0.0.1", "/")} {
		err := get("@x/foo", u("api.example.com", "/?to="+url.QueryEscape(to)))
		if err == nil || !strings.Contains(err.Error(), "is not on the host list") {
			t.Errorf("redirect to %s: %v", to, err)
		}
	}
	// without following redirects the redirect itself is the answer
	status, _, headers, err := hm.Request("@x/foo", "GET", u("api.example.com", "/?to="+url.QueryEscape(u("other.example.com", "/"))), nil, "", 5000, true, false)
	if err != nil || status != http.StatusFound || !strings.Contains(headers["Location"], "other.example.com") {
		t.Errorf("no redirects: %d %v %v", status, headers, err)
	}

	// an IP address on the list: that address only
	hm.SetInternet("@x/foo", plugins.InternetHosts{"127.0.0.1"})
	if err := get("@x/foo", u("127.0.0.1", "/")); err != nil {
		t.Errorf("listed IP: %v", err)
	}
	if err := get("@x/foo", u("api.example.com", "/")); !refused(err, "api.example.com") {
		t.Errorf("a name resolving to a listed IP: %v", err)
	}

	hm.SetInternet("@x/foo", plugins.InternetHosts{"*"})
	if err := get("@x/foo", u("tuyaeu.com.evil.net", "/")); err != nil {
		t.Errorf("*: %v", err)
	}
}

// The LAN boundary holds on its own: a listed name resolving to a local address needs
// "lan"; a plugin with "lan" reaches local addresses without any internet host.
func TestHTTPManagerInternetAndLAN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	hm := NewHTTPManager()
	hm.ownAddr = func(netip.Addr) bool { return false }
	hm.testResolve = map[string]string{"api.example.com": "127.0.0.1"}
	listed := "http://" + net.JoinHostPort("api.example.com", port) + "/"

	hm.SetLANAllowed("@x/foo", false)
	hm.SetInternet("@x/foo", plugins.InternetHosts{"api.example.com"})
	if _, _, _, err := hm.Request("@x/foo", "GET", listed, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), `needs "lan"`) {
		t.Errorf("listed name on the LAN without lan: %v", err)
	}
	if _, _, _, err := hm.Request("@x/foo", "GET", srv.URL, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), `needs "lan"`) {
		t.Errorf("local IP without lan: %v", err)
	}

	hm.SetLANAllowed("@x/foo", true)
	hm.SetInternet("@x/foo", plugins.InternetHosts{})
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	for _, u := range []string{srv.URL, byName} {
		if status, _, _, err := hm.Request("@x/foo", "GET", u, nil, "", 5000, true, true); err != nil || status != 200 {
			t.Errorf("%s with lan, no internet: %d %v", u, status, err)
		}
	}
	// a public name is not looked up, even if a house DNS would give a local address
	if _, _, _, err := hm.Request("@x/foo", "GET", listed, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), "not on the host list") {
		t.Errorf("public name with lan, no internet: %v", err)
	}
}

// With lan and without internet, only house names reach DNS: a query for any other name
// would tell a server outside the house whatever the plugin put in it.
func TestHTTPManagerLANNamesBeforeDNS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	hm := NewHTTPManager()
	hm.ownAddr = func(netip.Addr) bool { return false }
	house := []string{"shelly-1a2b", "nas.local", "Drukarka.LAN", "router.home", "pc.home.arpa", "hub.internal", "box.localdomain", "app.localhost", "nas.local."}
	public := []string{"6b6579.example.com", "x.lan.example.com", "pc.home.arpa.example.net", "evil.locals"}
	hm.testResolve = map[string]string{}
	for _, n := range append(append([]string{}, house...), public...) {
		hm.testResolve[strings.ToLower(strings.TrimSuffix(n, "."))] = "127.0.0.1"
	}
	hm.SetLANAllowed("@x/foo", true)
	hm.SetInternet("@x/foo", plugins.InternetHosts{"api.example.com"})

	for _, n := range house {
		u := "http://" + net.JoinHostPort(n, port) + "/"
		if status, _, _, err := hm.Request("@x/foo", "GET", u, nil, "", 5000, true, true); err != nil || status != 200 {
			t.Errorf("house name %s: %d %v", n, status, err)
		}
	}
	for _, n := range public {
		u := "http://" + net.JoinHostPort(n, port) + "/"
		if _, _, _, err := hm.Request("@x/foo", "GET", u, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), "not on the host list") {
			t.Errorf("public name %s: %v", n, err)
		}
	}
	for _, n := range []string{"data\u3002attacker\u3002com", "attacker.com\u200b.local"} {
		if localName(n) {
			t.Errorf("localName(%q): a name outside ASCII", n)
		}
	}
	if localName("") || localName(".") {
		t.Errorf("an empty name is not a house name")
	}
}

// End to end through the sandbox: the loader's hosts reach the plugin's HTTP; API 1 any.
func TestPluginSandboxInternetHosts(t *testing.T) {
	engine := sandboxEngine(t, "@t/listed", `{api = 2, limits = {internetHosts = {"api.example.com"}}}`)
	port, _ := internetServer(t, engine.httpManager, "api.example.com", "other.example.com")
	engine.StartTimers()
	defer engine.StopTimers()
	for _, create := range []string{
		`PluginSandbox:create("@t/none", {api = 2})`,
		`PluginSandbox:create("@t/old", {api = 1, lan = true, mqtt = true, modbus = true})`,
	} {
		if _, err := engine.Execute(create); err != nil {
			t.Fatal(err)
		}
	}
	// the plugin cannot set its own list or pick another HTTP instance
	if r, e := runPlugin(t, engine, "@t/none", `return type(__go_http_set_internet) .. type(__go_http_async_request) .. type(_G and _G.__go_http_set_internet)`); r != "nilnilnil" {
		t.Errorf("Go HTTP functions in the sandbox: %s %s", r, e)
	}
	other := "http://" + net.JoinHostPort("other.example.com", port) + "/"
	listed := "http://" + net.JoinHostPort("api.example.com", port) + "/"
	cases := map[string]string{"@t/listed": listed, "@t/none": listed, "@t/old": other}
	for id, target := range cases {
		if _, e := runPlugin(t, engine, id, `
			local p = Plugin:new("x")
			p:httpRequest({ method = "GET", url = "`+target+`" }, function(resp, err) p.result = tostring(err) .. "|" .. tostring(resp and resp.status) end)
			p:httpRequest({ method = "GET", url = "`+other+`" }, function(resp, err) p.other = tostring(err) .. "|" end)
		`); e != "nil" {
			t.Fatalf("%s: %s", id, e)
		}
	}
	read := func(id, field string) string {
		v, _ := engine.Execute(`tostring(Plugin._lookup("` + id + `").` + field + `)`)
		return v
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		pumpTimers(engine, 50*time.Millisecond)
		if read("@t/listed", "other") != "nil" && read("@t/none", "result") != "nil" && read("@t/old", "result") != "nil" && read("@t/old", "other") != "nil" {
			break
		}
	}
	if got := read("@t/listed", "result"); got != "nil|200" {
		t.Errorf("listed host: %q", got)
	}
	if got := read("@t/listed", "other"); !strings.Contains(got, "other.example.com is not on the host list of @t/listed (permissions.internet)") {
		t.Errorf("unlisted host: %q", got)
	}
	if got := read("@t/none", "result"); !strings.Contains(got, "api.example.com is not on the host list of @t/none") {
		t.Errorf("API 2 without internet: %q", got)
	}
	if got := read("@t/old", "result"); got != "nil|200" {
		t.Errorf("API 1: %q", got)
	}
}

func TestNumericHost(t *testing.T) {
	for _, h := range []string{"16843009", "127.1", "0x7f.1", "0X7F000001", "1.2.3.4.", "017700000001"} {
		if !numericHost(h) {
			t.Errorf("%s: not seen as a number", h)
		}
	}
	for _, h := range []string{"api.example.com", "1.example", "dev.lan", "x0x7f", "1-2-3-4.nip.io"} {
		if numericHost(h) {
			t.Errorf("%s: seen as a number", h)
		}
	}
}
