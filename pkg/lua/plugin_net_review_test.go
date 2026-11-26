package lua

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

func localServer(t *testing.T) (string, int) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(srv.Close)
	return srv.URL, srv.Listener.Addr().(*net.TCPAddr).Port
}

// test: a plugin's HTTP with lan does not reach vclu's own panel port on this machine;
// other local ports and instances without a policy still do.
func TestReviewHTTPOwnPortRefused(t *testing.T) {
	panelURL, panelPort := localServer(t)
	otherURL, _ := localServer(t)
	hm := NewHTTPManager()
	hm.SetOwnPorts(func() []int { return []int{panelPort} })
	hm.SetLAN("@x/api2", true, nil, nil)
	hm.SetInternet("@x/api2", plugins.InternetHosts{})
	byName := strings.Replace(panelURL, "127.0.0.1", "localhost", 1)
	for _, u := range []string{panelURL, byName} {
		if _, _, _, err := hm.Request("@x/api2", "GET", u, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), "an address of this vclu") {
			t.Errorf("%s: %v", u, err)
		}
	}
	if status, _, _, err := hm.Request("@x/api2", "GET", otherURL, nil, "", 5000, true, true); err != nil || status != 200 {
		t.Errorf("another local port: %d %v", status, err)
	}
	// an API 1 plugin (lan in full, any internet host) is refused too
	hm.SetLAN("@x/api1", true, nil, nil)
	if _, _, _, err := hm.Request("@x/api1", "GET", panelURL, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), "an address of this vclu") {
		t.Errorf("api 1: %v", err)
	}
	// a module: as before
	if status, _, _, err := hm.Request("module", "GET", panelURL, nil, "", 5000, true, true); err != nil || status != 200 {
		t.Errorf("no policy: %d %v", status, err)
	}

	p := hm.policy("@x/api2")
	for _, a := range []string{"0.0.0.0", "::", "::1", "::ffff:127.0.0.1", "127.0.0.2"} {
		if err := p.allowsPort(netip.MustParseAddr(a), strconv.Itoa(panelPort)); err == nil {
			t.Errorf("%s: allowed", a)
		}
	}
	if err := p.allowsPort(netip.MustParseAddr("192.0.2.1"), strconv.Itoa(panelPort)); err != nil {
		t.Errorf("another host on that port: %v", err)
	}
}

// test: a plugin's WebSocket does not reach the panel port either, also where its own
// addresses are otherwise reachable.
func TestReviewWSOwnPortRefused(t *testing.T) {
	port := wsServer(t, echo)
	engine := sandboxEngine(t, "@t/wsown", `{api = 2, lan = true, lanPorts = {`+port+`}}`)
	loopbackAllowed(engine)
	engine.httpManager.ownAddr = ownAddr // this machine again, on the panel port only
	n, _ := strconv.Atoi(port)
	engine.SetOwnTCPPorts(func() []int { return []int{n} })
	engine.StartTimers()
	if _, e := runPlugin(t, engine, "@t/wsown", `
		local p = Plugin:new("w")
		p.r = ""
		local ws, err = p:websocket("ws://127.0.0.1:`+port+`/")
		if not ws then p.r = "refused:" .. err return end
		ws:onOpen(function() p.r = "open" end)
		ws:onClose(function(code, reason) p.r = "close:" .. reason end)
	`); e != "nil" {
		t.Fatal(e)
	}
	expr := `Plugin._lookup("@t/wsown").r`
	waitPlugin(t, engine, expr+` ~= "" and (`+expr+`):find("an address of this vclu", 1, true) ~= nil`, "true")
}

// test: without lan, local addresses in every form are refused, also with internet "*".
func TestReviewHTTPLocalAddressForms(t *testing.T) {
	_, port := localServer(t)
	hm := NewHTTPManager()
	hm.SetLAN("@x/f", false, nil, nil)
	hm.SetInternet("@x/f", plugins.InternetHosts{"*"})
	p := strconv.Itoa(port)
	for _, u := range []string{
		"http://127.0.0.1:" + p, "http://localhost:" + p, "http://0.0.0.0:" + p, "http://[::ffff:127.0.0.1]:" + p,
		"http://[::1]:" + p, "http://127.0.0.2:" + p, "http://LOCALHOST.:" + p,
	} {
		if status, _, _, err := hm.Request("@x/f", "GET", u, nil, "", 3000, true, true); err == nil {
			t.Errorf("%s: %d", u, status)
		}
	}
}

// test: lan and its hosts are set together through the sandbox.
func TestReviewHTTPLANHostsFromSandbox(t *testing.T) {
	url, _ := localServer(t)
	engine := sandboxEngine(t, "@t/n", `{api = 2, lan = true, limits = {lanHosts = {"192.168.9.0/24"}}}`)
	if _, _, _, err := engine.httpManager.Request("@t/n", "GET", url, nil, "", 3000, true, true); err == nil || !strings.Contains(err.Error(), "not among the hosts") {
		t.Errorf("narrowed: %v", err)
	}
	pol := engine.httpManager.policy("@t/n")
	if pol.noLAN || !pol.narrowed || len(pol.lanHosts) != 1 {
		t.Errorf("policy %+v", pol)
	}
}

// test: udp targets in other forms: mapped broadcast, a host network's broadcast, port 0,
// this machine, IPv6 and names.
func TestReviewUDPTargetForms(t *testing.T) {
	pio := NewPluginIO()
	pio.SetGrant("@t/u", pluginIOGrant{udp: true, udpPorts: []int{7000}, broadcastPorts: []int{7000}})
	id, err := pio.OpenDeviceUDP("@t/u", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pio.CloseAll()
	cases := []struct {
		ip      string
		port    int
		refusal string
	}{
		{"::ffff:255.255.255.255", 7001, "permissions.udp.broadcast"},
		{"192.168.1.10", 0, "out of range"},
		{"192.168.1.10", 65536, "out of range"},
		{"0.0.0.0", 7000, "this vCLU"},
		{"127.0.0.2", 7000, "this vCLU"},
		{"::1", 7000, "IPv4"},
		{"fe80::1", 7000, "IPv4"},
		{"localhost", 7000, "IPv4"},
		{"239.255.255.250", 7000, "multicast"},
		{"192.168.1.10", 1234, "CLU port"},
	}
	if b, ok := hostBroadcast(); ok {
		cases = append(cases, struct {
			ip      string
			port    int
			refusal string
		}{b, 7001, "permissions.udp.broadcast"})
	}
	for _, c := range cases {
		if err := pio.SendUDP(id, "@t/u", c.ip, c.port, []byte("x")); err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Errorf("%s:%d: %v, want %q", c.ip, c.port, err, c.refusal)
		}
	}
	if _, err := pio.OpenDeviceUDP("@t/u", -1); err == nil {
		t.Error("bind port -1")
	}
}

// hostBroadcast: the broadcast address of an IPv4 network this machine is on.
func hostBroadcast() (string, bool) {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil || n.IP.IsLoopback() || len(n.Mask) != 4 {
			continue
		}
		if ones, _ := n.Mask.Size(); ones >= 31 {
			continue
		}
		ip := n.IP.To4()
		var b [4]byte
		for i := range b {
			b[i] = ip[i] | ^n.Mask[i]
		}
		return netip.AddrFrom4(b).String(), true
	}
	return "", false
}

// test: a socket or a port whose permission the new grant drops closes at once.
func TestReviewHandlesClosedWhenGrantDrops(t *testing.T) {
	pio := NewPluginIO()
	pio.SetGrant("@t/g", pluginIOGrant{udp: true, udpPorts: []int{7000}, grentonRaw: true})
	raw, err := pio.OpenUDP("@t/g", 0)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := pio.OpenDeviceUDP("@t/g", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pio.CloseAll()
	pio.SetGrant("@t/g", pluginIOGrant{udp: true, udpPorts: []int{7000}})
	if err := pio.SendUDP(raw, "@t/g", "192.168.1.10", 1234, []byte("x")); err == nil {
		t.Error("grenton_raw socket sends after the grant dropped it")
	}
	if _, err := pio.handle(dev, "@t/g"); err != nil {
		t.Errorf("udp socket closed though udp stays: %v", err)
	}
	pio.SetGrant("@t/g", pluginIOGrant{})
	if n := openHandles(pio); n != 0 {
		t.Errorf("%d handles open without a grant", n)
	}
}

// test: a raw socket checks grenton_raw at each send, as a udp one checks udp.
func TestReviewRawSendChecksGrant(t *testing.T) {
	pio := NewPluginIO()
	pio.SetGrant("@t/r", pluginIOGrant{grentonRaw: true})
	raw, err := pio.OpenUDP("@t/r", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pio.CloseAll()
	pio.mu.Lock()
	pio.grants["@t/r"] = pluginIOGrant{}
	pio.mu.Unlock()
	if err := pio.SendUDP(raw, "@t/r", "192.168.1.10", 1234, []byte("x")); err == nil || !strings.Contains(err.Error(), `"clu"`) {
		t.Errorf("send without grenton_raw: %v", err)
	}
}

// test: tcp refuses ports of other permissions even when a grant lists them, port 0,
// this machine, broadcast and multicast, and vclu's broker in a mapped form.
func TestReviewTCPTargetForms(t *testing.T) {
	pio := NewPluginIO()
	pio.SetGrant("@t/t", pluginIOGrant{tcp: true, tcpPorts: []int{23, 502, 1883, 1234, 69, 8883, 9000}})
	pio.SetTCPTaken(func() map[netip.AddrPort]string {
		return map[netip.AddrPort]string{netip.MustParseAddrPort("10.0.0.5:9000"): "mqtt"}
	})
	defer pio.CloseAll()
	for _, c := range []struct {
		host    string
		port    int
		refusal string
	}{
		{"192.168.1.10", 502, "modbus"},
		{"192.168.1.10", 1883, "mqtt"},
		{"192.168.1.10", 8883, "mqtt"},
		{"192.168.1.10", 1234, "clu"},
		{"192.168.1.10", 69, "tftp"},
		{"192.168.1.10", 0, "out of range"},
		{"192.168.1.10", 24, "not in plugin.json"},
		{"0.0.0.0", 23, "this vCLU"},
		{"127.0.0.1", 23, "this vCLU"},
		{"255.255.255.255", 23, "broadcast"},
		{"224.0.0.1", 23, "multicast"},
		{"8.8.8.8", 23, "not on the local network"},
		{"::1", 23, "IPv4"},
		{"localhost", 23, "IPv4"},
		{"::ffff:10.0.0.5", 9000, "vclu uses it"},
	} {
		if _, err := pio.OpenTCP("@t/t", c.host, c.port, 0); err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Errorf("%s:%d: %v, want %q", c.host, c.port, err, c.refusal)
		}
	}
	if n := openHandles(pio); n != 0 {
		t.Errorf("%d handles left by refusals", n)
	}
}

// test: a handle opened by one plugin is not usable under another plugin's id.
func TestReviewHandlesBelongToTheirPlugin(t *testing.T) {
	pio := NewPluginIO()
	pio.SetGrant("@t/a", pluginIOGrant{udp: true, udpPorts: []int{7000}})
	id, err := pio.OpenDeviceUDP("@t/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pio.CloseAll()
	if err := pio.SendUDP(id, "@t/b", "192.168.1.10", 7000, []byte("x")); err == nil {
		t.Error("another plugin's id sends on the socket")
	}
	if err := pio.SendTCP(id, "@t/b", []byte("x")); err == nil {
		t.Error("tcp send on a udp handle of another plugin")
	}
	if err := pio.SendWS(id, "@t/b", []byte("x"), false); err == nil {
		t.Error("ws send on another plugin's handle")
	}
}
