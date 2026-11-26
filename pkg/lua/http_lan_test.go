package lua

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

func TestIsLocalAddr(t *testing.T) {
	local := []string{
		"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.0.9", "169.254.169.254",
		"100.64.1.1", "0.0.0.0", "255.255.255.255", "224.0.0.1",
		"::1", "fe80::1", "fd00::1", "::ffff:192.168.0.1", "64:ff9b::c0a8:0001", "::",
		"fec0::1", "::192.168.0.1", "2002:c0a8:0001::1", "::ffff:0:192.168.0.1",
		"64:ff9b:1::c0a8:1", "198.18.0.1", "192.0.0.1",
	}
	internet := []string{"1.1.1.1", "8.8.8.8", "172.32.0.1", "100.128.0.1", "2606:4700::1111", "2002:0101:0101::1"}
	for _, s := range local {
		if !isLocalAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be local", s)
		}
	}
	for _, s := range internet {
		if isLocalAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should not be local", s)
		}
	}
}

func TestHTTPManagerNoLAN(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	// a name resolving to the LAN is refused as well, after resolution
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	hm := NewHTTPManager()
	hm.ownAddr = func(netip.Addr) bool { return false }
	hm.SetLANAllowed("@t/a", false)
	for _, u := range []string{srv.URL, byName} {
		_, _, _, err := hm.Request("@t/a", "GET", u, nil, "", 5000, true, true)
		if err == nil || !strings.Contains(err.Error(), "lan") {
			t.Errorf("%s: want refusal, got %v", u, err)
		}
	}

	// a module's instance, without restriction, still reaches it
	status, body, _, err := hm.Request("other", "GET", srv.URL, nil, "", 5000, true, true)
	if err != nil || status != 200 || body != "ok" {
		t.Fatalf("instance without restriction: %d %q %v", status, body, err)
	}

	hm.SetLANAllowed("@t/a", true)
	status, _, _, err = hm.Request("@t/a", "GET", srv.URL, nil, "", 5000, true, true)
	if err != nil || status != 200 {
		t.Errorf("after allowing lan: %d %v", status, err)
	}
}

// Addresses of this machine's interfaces and their networks count as the LAN, also
// a global IPv6 one.
func TestOnHostNetwork(t *testing.T) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip(err)
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(n.IP)
		if !ok {
			continue
		}
		if !onHostNetwork(ip) {
			t.Errorf("%s (own interface) not on the host's network", ip)
		}
	}
	if onHostNetwork(netip.MustParseAddr("1.1.1.1")) {
		t.Error("1.1.1.1 on the host's network")
	}
}

// A plugin's HTTP, with lan and on API 1 alike, does not reach this vclu: not by its
// loopback, not by a name resolving to it; a module's does.
func TestHTTPManagerPluginNotToOwnAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	byName := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	hm := NewHTTPManager()
	hm.SetLANAllowed("@t/a", true)
	for _, u := range []string{srv.URL, byName} {
		_, _, _, err := hm.Request("@t/a", "GET", u, nil, "", 5000, true, true)
		if err == nil || !strings.Contains(err.Error(), "an address of this vclu") {
			t.Errorf("%s: want refusal, got %v", u, err)
		}
	}
	hm.SetLANHosts("@t/a", []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})
	if _, _, _, err := hm.Request("@t/a", "GET", srv.URL, nil, "", 5000, true, true); err == nil {
		t.Error("a host list naming this vclu does not allow it")
	}
	if status, _, _, err := hm.Request("module", "GET", srv.URL, nil, "", 5000, true, true); err != nil || status != 200 {
		t.Errorf("a module's request: %d %v", status, err)
	}

	// with vclu's ports known, only those are refused: a service next to vclu stays reachable
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("influx"))
	}))
	defer other.Close()
	_, vcluPort, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	n, _ := strconv.Atoi(vcluPort)
	hm.SetOwnPorts(func() []int { return []int{n} })
	hm.SetLANHosts("@t/a", nil)
	if _, _, _, err := hm.Request("@t/a", "GET", srv.URL, nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), "an address of this vclu") {
		t.Errorf("vclu's own port: %v", err)
	}
	if status, body, _, err := hm.Request("@t/a", "GET", other.URL, nil, "", 5000, true, true); err != nil || status != 200 || body != "influx" {
		t.Errorf("another service on this machine: %d %q %v", status, body, err)
	}

	// a name resolving to an address of this vclu is refused after DNS
	hm = NewHTTPManager()
	hm.ownAddr = func(ip netip.Addr) bool { return ip == netip.MustParseAddr("127.0.0.2") }
	hm.testResolve = map[string]string{"dev.lan": "127.0.0.2"}
	hm.SetLANAllowed("@t/a", true)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if _, _, _, err := hm.Request("@t/a", "GET", "http://dev.lan:"+port+"/", nil, "", 5000, true, true); err == nil || !strings.Contains(err.Error(), "an address of this vclu") {
		t.Errorf("a name resolving to this vclu: %v", err)
	}
	if status, _, _, err := hm.Request("@t/a", "GET", srv.URL, nil, "", 5000, true, true); err != nil || status != 200 {
		t.Errorf("another local address: %d %v", status, err)
	}
}
