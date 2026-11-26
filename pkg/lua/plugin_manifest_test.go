package lua

import (
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// lan.ports cover a plugin's HTTP in the house: without the list any local port, with it only
// those; a port of another permission never.
func TestPluginHTTPLANPorts(t *testing.T) {
	url, port := localServer(t)
	hm := NewHTTPManager()
	hm.ownAddr = func(netip.Addr) bool { return false }
	hm.SetInternet("@x/any", plugins.InternetHosts{})
	hm.SetLAN("@x/any", true, nil, nil)
	if _, _, _, err := hm.Request("@x/any", "GET", url, nil, "", 3000, true, true); err != nil {
		t.Errorf("any port: %v", err)
	}
	hm.SetInternet("@x/listed", plugins.InternetHosts{})
	hm.SetLAN("@x/listed", true, nil, []int{port})
	if _, _, _, err := hm.Request("@x/listed", "GET", url, nil, "", 3000, true, true); err != nil {
		t.Errorf("a listed port: %v", err)
	}
	hm.SetInternet("@x/other", plugins.InternetHosts{})
	hm.SetLAN("@x/other", true, nil, []int{8123})
	if _, _, _, err := hm.Request("@x/other", "GET", url, nil, "", 3000, true, true); err == nil || !strings.Contains(err.Error(), "permissions.lan.ports") {
		t.Errorf("a port not listed: %v", err)
	}
	if _, _, _, err := hm.Request("@x/any", "GET", "http://127.0.0.1:502/", nil, "", 3000, true, true); err == nil || !strings.Contains(err.Error(), "modbus permission") {
		t.Errorf("the Modbus port with any port: %v", err)
	}
	// from the sandbox, as the loader passes plugin.json's lan.ports
	engine := sandboxEngine(t, "@t/n", `{api = 2, lan = true, lanPorts = {8123}}`)
	if _, _, _, err := engine.httpManager.Request("@t/n", "GET", url, nil, "", 3000, true, true); err == nil || !strings.Contains(err.Error(), "permissions.lan.ports") {
		t.Errorf("sandbox lan.ports: %v", err)
	}
	engine = sandboxEngine(t, "@t/p", `{api = 2, lan = true, lanPorts = {`+strconv.Itoa(port)+`}}`)
	if _, _, _, err := engine.httpManager.Request("@t/p", "GET", url, nil, "", 3000, true, true); err != nil {
		t.Errorf("sandbox, the listed port: %v", err)
	}
}

// The user's modbus hosts narrow the gateways a plugin reaches.
func TestPluginModbusHosts(t *testing.T) {
	engine := sandboxEngine(t, "@t/m", `{api = 2, modbus = true, limits = {modbusHosts = {"192.168.0.9"}}}`)
	if _, e := runPlugin(t, engine, "@t/m", `Modbus.request({host = "192.168.0.10", fc = 4}, function() end)`); !strings.Contains(e, "not among the hosts") {
		t.Errorf("another gateway: %s", e)
	}
	if _, e := runPlugin(t, engine, "@t/m", `Modbus.request({host = "192.168.0.9", fc = 4}, function() end)`); strings.Contains(e, "not among the hosts") {
		t.Errorf("the allowed gateway: %s", e)
	}
	if _, e := runPlugin(t, engine, "@t/m", `return Modbus.toInt16(65535)`); e != "nil" {
		t.Errorf("conversions: %s", e)
	}
}

// plugin:has answers what this vclu offers, never errors.
func TestPluginHas(t *testing.T) {
	engine := sandboxEngine(t, "@t/h", `{api = 2}`)
	r, e := runPlugin(t, engine, "@t/h", `
		local p = Plugin:new("h", {})
		local out = {}
		for _, n in ipairs({"websocket", "tcpConnect", "permission:udp.broadcast", "permission:clu", "form:host",
			"permission:gpio", "form:colour", "nosuch", "_sandbox", "permission:grenton_raw"}) do
			out[#out + 1] = tostring(p:has(n))
		end
		out[#out + 1] = tostring(p:has(nil)) .. tostring(p:has(5))
		return table.concat(out, ",")`)
	if e != "nil" || r != "true,true,true,true,true,false,false,false,false,false,falsefalse" {
		t.Errorf("has: %s / %s", r, e)
	}
}

// A plugin's KV store keeps its limits; secure only keeps values out of the logs.
func TestPluginKVLimits(t *testing.T) {
	store := plugins.NewKVStoreManager(t.TempDir()).GetStore("@t/kv")
	if err := store.Set(strings.Repeat("k", 129), "v", false, 0); err == nil {
		t.Error("a long key")
	}
	if err := store.Set("big", strings.Repeat("x", plugins.MaxPluginKVValue+1), false, 0); err == nil {
		t.Error("a large value")
	}
	for i := 0; i < 4; i++ {
		if err := store.Set("v"+strconv.Itoa(i), strings.Repeat("x", 250<<10), false, 0); err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
	}
	if err := store.Set("v4", strings.Repeat("x", 250<<10), false, 0); err == nil || !strings.Contains(err.Error(), "store is up to") {
		t.Errorf("over the file: %v", err)
	}
	if _, ok := store.Get("v4"); ok {
		t.Error("the refused value stayed")
	}
	if err := store.Set("v0", "small", false, 0); err != nil {
		t.Errorf("replacing within the limit: %v", err)
	}
	// a module's store has no limits
	if err := plugins.NewKVStoreManager(t.TempDir()).GetStore("module").Set("big", strings.Repeat("x", plugins.MaxPluginKVValue+1), false, 0); err != nil {
		t.Errorf("module store: %v", err)
	}
}
