package plugins

import (
	"slices"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// An update that asks for more internet hosts waits for the user; the allowed ones stay.
func TestReviewUpdateNewInternetHostWaits(t *testing.T) {
	v1 := Permissions{Internet: InternetHosts{"api.example.com"}}
	g := v1.Grants()
	v2 := Permissions{Internet: InternetHosts{"api.example.com", "other.example.com"}}
	allowed, pending := v2.allowedBy(g)
	if !slices.Equal(allowed.Internet, InternetHosts{"api.example.com"}) || !slices.Equal(pending.Internet, InternetHosts{"other.example.com"}) {
		t.Errorf("allowed %v pending %v", allowed.Internet, pending.Internet)
	}
	// "*" asked by the update waits too
	allowed, pending = Permissions{Internet: InternetHosts{"*"}}.allowedBy(g)
	if !slices.Equal(allowed.Internet, InternetHosts{"api.example.com"}) || !pending.Internet.Any() {
		t.Errorf("*: allowed %v pending %v", allowed.Internet, pending.Internet)
	}
	// a bad stored list allows none
	allowed, _ = v1.allowedBy(&config.PluginGrants{Internet: &config.InternetGrant{Hosts: []string{"http://x"}}})
	if len(allowed.Internet) != 0 {
		t.Errorf("bad list: %v", allowed.Internet)
	}
}

// Installing never grants grenton_raw, tftp or serial, whatever the accept list says.
func TestReviewInstallAcceptNeverGrantsSensitive(t *testing.T) {
	g := AllPermissions.Only([]string{"lan", "grenton_raw", "tftp", "serial", "bogus"}).Grants()
	if g.LAN == nil || g.GrentonRaw != nil || g.TFTP != nil || g.Serial != nil || g.Internet != nil || g.TCP != nil {
		t.Errorf("grants %+v", g)
	}
}

// Host matching: subdomains only under "*.", IPv4-mapped literals as their IPv4 entry.
func TestReviewInternetHostsAllows(t *testing.T) {
	h, err := NormalizeInternetHosts([]string{"*.example.com", "1.2.3.4", "API.Other.com."})
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]bool{
		"a.example.com": true, "A.B.Example.COM.": true, "example.com": false, "badexample.com": false,
		"example.com.evil.net": false, "1.2.3.4": true, "[::ffff:1.2.3.4]": true, "::ffff:1.2.3.4": true,
		"1.2.3.5": false, "api.other.com": true, "other.com": false, "": false, "*.example.com": false,
	} {
		if got := h.Allows(host); got != want {
			t.Errorf("%q: %v, want %v", host, got, want)
		}
	}
	for _, bad := range []string{"*.com", "a.*.com", "example.com:443", "fe80::1%eth0", "user@x.com", "x.com/p"} {
		if _, err := NormalizeInternetHosts([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A tcp port or a broadcast port added by an update should wait for the user, as a new
// internet host does.
func TestReviewUpdateNewTCPPortWaits(t *testing.T) {
	v1 := PluginManifest{API: 2, Permissions: &Permissions{TCP: true}, TCP: &ManifestTCP{Ports: []int{23}}}
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/tv", Grants: requestedPermissions(v1).Grants()}})
	v2 := PluginManifest{API: 2, Permissions: &Permissions{TCP: true}, TCP: &ManifestTCP{Ports: []int{23, 3306}}}
	perms, _, _, _ := l.effectivePermissions("@jan/tv", v2)
	if !perms.TCP || slices.Contains(perms.TCPPorts, 3306) || !slices.Contains(perms.TCPPorts, 23) {
		t.Errorf("the update's new port should wait, the old one work: %+v", perms)
	}
}
