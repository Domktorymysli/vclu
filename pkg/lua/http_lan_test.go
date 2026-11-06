package lua

import (
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
	}
	internet := []string{"1.1.1.1", "8.8.8.8", "172.32.0.1", "100.128.0.1", "2606:4700::1111"}
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
		w.Write([]byte("ok"))
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
