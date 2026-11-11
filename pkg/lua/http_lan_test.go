package lua

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
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
