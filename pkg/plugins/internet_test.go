package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func TestInternetHostsParse(t *testing.T) {
	for _, c := range []struct {
		in   string
		want InternetHosts
	}{
		{`"*"`, InternetHosts{"*"}},
		{`["*"]`, InternetHosts{"*"}},
		{`["api.example.com", "*"]`, InternetHosts{"*"}},
		{`"api.example.com"`, InternetHosts{"api.example.com"}},
		{`["API.Example.com.", "api.example.com", "*.Tuyaeu.com"]`, InternetHosts{"api.example.com", "*.tuyaeu.com"}},
		{`["1.2.3.4", "2606:4700::1111", "::ffff:5.6.7.8"]`, InternetHosts{"1.2.3.4", "2606:4700::1111", "5.6.7.8"}},
		{`["xn--bcher-kva.de", "_dmarc.example.com"]`, InternetHosts{"xn--bcher-kva.de", "_dmarc.example.com"}},
		{`[]`, nil},
	} {
		var got InternetHosts
		if err := json.Unmarshal([]byte(c.in), &got); err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q %v, want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{
		`""`, `[""]`, `["https://api.example.com"]`, `["api.example.com/v1"]`, `["api.example.com:443"]`,
		`["*.com"]`, `["api.*.com"]`, `["*api.example.com"]`, `["a*.example.com"]`, `["**.example.com"]`,
		`["user@api.example.com"]`, `["bücher.de"]`, `["fe80::1%en0"]`, `["[::1]"]`, `["a..b.com"]`,
		`["-a.com"]`, `["a b.com"]`, `true`, `[1]`, `{"hosts":["a.com"]}`,
	} {
		var got InternetHosts
		if err := json.Unmarshal([]byte(bad), &got); err == nil {
			t.Errorf("%s accepted as %q", bad, got)
		}
	}
}

// A bad host refuses the manifest at download and at load.
func TestManifestWithBadInternetHostRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, []byte(`{"id":"x","api":2,"permissions":{"internet":["https://api.example.com/"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := checkDownloadedManifest(path)
	if err == nil || !strings.Contains(err.Error(), "internet") || !strings.Contains(err.Error(), "no scheme") {
		t.Errorf("download: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"id":"x","api":2,"permissions":{"internet":["api.example.com"]},"internetWhy":"ignored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkDownloadedManifest(path); err != nil {
		t.Errorf("good manifest: %v", err)
	}
}

func TestInternetHostsAllows(t *testing.T) {
	list := InternetHosts{"api.example.com", "*.tuyaeu.com", "1.2.3.4", "2606:4700::1111", "xn--bcher-kva.de"}
	for host, want := range map[string]bool{
		"api.example.com":             true,
		"API.EXAMPLE.COM":             true,
		"api.example.com.":            true,
		"api.example.com..":           false,
		"example.com":                 false,
		"x.api.example.com":           false,
		"openapi.tuyaeu.com":          true,
		"a.b.tuyaeu.com":              true,
		"tuyaeu.com":                  false,
		"eviltuyaeu.com":              false,
		"tuyaeu.com.evil.net":         false,
		"openapi.tuyaeu.com.evil.net": false,
		".tuyaeu.com":                 false,
		"1.2.3.4":                     true,
		"1.2.3.5":                     false,
		"::ffff:1.2.3.4":              true,
		"2606:4700::1111":             true,
		"[2606:4700::1111]":           true,
		"2606:4700:0::1111":           true,
		"bücher.de":                   true,
		"BÜCHER.de":                   true,
		"":                            false,
		"*":                           false,
	} {
		if got := list.Allows(host); got != want {
			t.Errorf("%q: %v, want %v", host, got, want)
		}
	}
	if !(InternetHosts{"*"}).Allows("anything.example.org") || (InternetHosts(nil)).Allows("api.example.com") {
		t.Error("* allows any, none allows none")
	}
}

// The hosts allowed before keep working; a new host or "*" waits for the user.
func TestInternetGrants(t *testing.T) {
	grant := func(hosts ...string) *config.PluginGrants {
		return &config.PluginGrants{Internet: &config.InternetGrant{Hosts: hosts}}
	}
	for _, c := range []struct {
		asks             InternetHosts
		grants           *config.PluginGrants
		allowed, pending InternetHosts
	}{
		{InternetHosts{"a.com"}, nil, InternetHosts{"a.com"}, nil},
		{InternetHosts{"a.com", "b.com"}, grant("a.com"), InternetHosts{"a.com"}, InternetHosts{"b.com"}},
		{InternetHosts{"a.com"}, &config.PluginGrants{}, nil, InternetHosts{"a.com"}},
		{InternetHosts{"*"}, grant("a.com"), InternetHosts{"a.com"}, InternetHosts{"*"}},
		{InternetHosts{"*"}, &config.PluginGrants{}, nil, InternetHosts{"*"}},
		{InternetHosts{"a.com"}, grant("*"), InternetHosts{"a.com"}, nil},
		{InternetHosts{"*"}, grant("*"), InternetHosts{"*"}, nil},
		{InternetHosts{"a.com"}, grant("A.com."), InternetHosts{"a.com"}, nil},
		{InternetHosts{"a.com"}, grant("https://a.com"), nil, InternetHosts{"a.com"}},
		{nil, grant("a.com"), nil, nil},
	} {
		allowed, pending := Permissions{Internet: c.asks}.allowedBy(c.grants)
		if !reflect.DeepEqual(allowed.Internet, c.allowed) || !reflect.DeepEqual(pending.Internet, c.pending) {
			t.Errorf("asks %q, grants %+v: allowed %q pending %q, want %q %q", c.asks, c.grants, allowed.Internet, pending.Internet, c.allowed, c.pending)
		}
	}
	p := Permissions{Internet: InternetHosts{"a.com"}, LAN: true}
	if got := p.Names(); strings.Join(got, ",") != "internet,lan" {
		t.Errorf("names %v", got)
	}
	if got := p.Only([]string{"lan"}); got.Internet != nil || !got.LAN {
		t.Errorf("only lan: %+v", got)
	}
	if got := p.Only([]string{"internet"}); !reflect.DeepEqual(got.Internet, p.Internet) || got.LAN {
		t.Errorf("only internet: %+v", got)
	}
	if g := p.Grants(); g.Internet == nil || !reflect.DeepEqual(g.Internet.Hosts, []string{"a.com"}) {
		t.Errorf("grants %+v", g.Internet)
	}
	if err := ValidateGrants(*grant("a.com", "*.b.com", "*")); err != nil {
		t.Error(err)
	}
	if err := ValidateGrants(*grant("a.com/x")); err == nil {
		t.Error("bad host in a grant accepted")
	}
}

// API 1 reaches any host; API 2 the allowed part of its list, none without it.
func TestEffectivePermissionsInternet(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{
		{ID: "@jan/old"},
		{ID: "@jan/upd", Grants: &config.PluginGrants{Internet: &config.InternetGrant{Hosts: []string{"a.com"}}}},
	})
	hosts := func(id string, m PluginManifest) string {
		_, limits, _, _ := l.effectivePermissions(id, m)
		data, _ := json.Marshal(limits.InternetHosts)
		return string(data)
	}
	if got := hosts("@jan/old", PluginManifest{}); got != "null" {
		t.Errorf("API 1: %s, want any (null)", got)
	}
	if got := hosts("@jan/old", PluginManifest{API: 2, Permissions: &Permissions{LAN: true}}); got != "[]" {
		t.Errorf("API 2 without internet: %s", got)
	}
	if got := hosts("@jan/old", PluginManifest{API: 2, Permissions: &Permissions{Internet: InternetHosts{"*"}}}); got != `["*"]` {
		t.Errorf("API 2 with *: %s", got)
	}
	if got := hosts("@jan/upd", PluginManifest{API: 2, Permissions: &Permissions{Internet: InternetHosts{"a.com", "b.com"}}}); got != `["a.com"]` {
		t.Errorf("update adding b.com: %s", got)
	}
}
