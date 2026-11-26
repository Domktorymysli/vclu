package lua

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
)

// udp is its own permission: a device's datagram goes out and its answer comes back
// without grenton_raw, and udp does not open grentonSocket.
func TestPluginDeviceUDPEcho(t *testing.T) {
	echo, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = echo.Close() }()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := echo.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = echo.WriteToUDP(append([]byte("re:"), buf[:n]...), from)
		}
	}()
	engine := sandboxEngine(t, "@t/ac", `{api = 2, udp = true}`)
	loopbackAllowed(engine)
	port := echo.LocalAddr().(*net.UDPAddr).Port
	if _, e := runPlugin(t, engine, "@t/ac", `
		local p = Plugin:new("ac")
		local s = assert(p:udpSocket())
		s:onReceive(function(bytes, ip, port) p.got = bytes .. "|" .. ip end)
		assert(s:send("127.0.0.1", `+strconv.Itoa(port)+`, '{"t":"scan"}'))
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `tostring(Plugin.getPlugin("@t/ac").got)`, `re:{"t":"scan"}|127.0.0.1`)

	if _, e := runPlugin(t, engine, "@t/ac", `Plugin:new("ac2"):grentonSocket()`); !strings.Contains(e, `needs "grenton_raw"`) {
		t.Errorf("grentonSocket with udp only: %s", e)
	}
	if _, err := engine.PluginIO().OpenUDP("@t/ac", 0); err == nil {
		t.Error("Go: grenton_raw socket with udp only")
	}
}

// Without udp there is no udp socket, in Lua and in Go, also with grenton_raw.
func TestPluginDeviceUDPRefusedWithoutPermission(t *testing.T) {
	engine := sandboxEngine(t, "@t/raw", `{api = 2, lan = true, grenton_raw = true}`)
	if _, e := runPlugin(t, engine, "@t/raw", `Plugin:new("n"):udpSocket()`); !strings.Contains(e, `needs "udp"`) {
		t.Errorf("udpSocket without udp: %s", e)
	}
	if _, err := engine.PluginIO().OpenDeviceUDP("@t/raw", 0); err == nil || !strings.Contains(err.Error(), `"udp"`) {
		t.Errorf("Go: udp socket without udp: %v", err)
	}
}

// Where a udp socket's datagrams may go: devices on the local network, never the CLU
// ports or vCLU's own, a broadcast only to the ports in plugin.json and without a host list.
func TestPluginDeviceUDPTargets(t *testing.T) {
	io := NewPluginIO()
	own := freeUDPPort(t)
	io.ReserveUDPPorts(own)
	io.SetGrant("@t/a", pluginIOGrant{udp: true, broadcastPorts: []int{7000}})
	io.SetGrant("@t/b", pluginIOGrant{udp: true, broadcastPorts: []int{7000},
		udpHosts: []netip.Prefix{netip.MustParsePrefix("192.168.7.0/24")}})
	// grenton_raw too: its own socket keeps the CLU rules, the udp one does not get them
	io.SetGrant("@t/c", pluginIOGrant{udp: true, grentonRaw: true})
	open := func(plugin string, raw bool) int {
		t.Helper()
		f := io.OpenDeviceUDP
		if raw {
			f = io.OpenUDP
		}
		id, err := f(plugin, 0)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b, c, cRaw := open("@t/a", false), open("@t/b", false), open("@t/c", false), open("@t/c", true)
	defer io.CloseAll()
	for _, x := range []struct {
		id      int
		plugin  string
		ip      string
		port    int
		refusal string
	}{
		{a, "@t/a", "127.0.0.1", 7000, "this vCLU"},
		{a, "@t/a", "8.8.8.8", 7000, "not on the local network"},
		{a, "@t/a", "224.0.0.1", 7000, "multicast"},
		{a, "@t/a", "192.168.1.10", 1234, "CLU port"},
		{a, "@t/a", "192.168.1.10", 69, "CLU port"},
		{a, "@t/a", "255.255.255.255", 1234, "CLU port"},
		{a, "@t/a", "192.168.1.10", own, "vclu listens on"},
		{a, "@t/a", "255.255.255.255", 7001, "broadcastPorts"},
		{b, "@t/b", "192.168.8.1", 7000, "not among the hosts"},
		{b, "@t/b", "255.255.255.255", 7000, "only some hosts"},
		{c, "@t/c", "192.168.1.10", 1234, "CLU port"},
		{c, "@t/c", "255.255.255.255", 7000, "broadcastPorts"}, // none declared
		{cRaw, "@t/c", "255.255.255.255", 7000, "only to the CLU port"},
	} {
		err := io.SendUDP(x.id, x.plugin, x.ip, x.port, []byte("x"))
		if err == nil || !strings.Contains(err.Error(), x.refusal) {
			t.Errorf("%s -> %s:%d: %v, want %q", x.plugin, x.ip, x.port, err, x.refusal)
		}
	}
	if err := io.SendUDP(b, "@t/b", "192.168.7.1", 7000, []byte("x")); err != nil {
		t.Errorf("allowed host: %v", err)
	}
	if err := io.SendUDP(cRaw, "@t/c", "192.168.1.10", 1234, []byte("x")); err != nil {
		t.Errorf("grenton_raw to the CLU port: %v", err)
	}
	// a declared port passes the checks; the network may still not route a broadcast
	if err := io.SendUDP(a, "@t/a", "255.255.255.255", 7000, []byte("x")); err != nil && strings.Contains(err.Error(), "refused") {
		t.Errorf("broadcast to a declared port: %v", err)
	}
	// the grant taken away (settings changed before a reload): the open socket stops
	io.SetGrant("@t/a", pluginIOGrant{})
	if err := io.SendUDP(a, "@t/a", "192.168.1.10", 7000, []byte("x")); err == nil || !strings.Contains(err.Error(), `"udp"`) {
		t.Errorf("after the grant went: %v", err)
	}
}

// The broadcast ports reach Go from the sandbox: only the declared one passes.
func TestPluginDeviceUDPBroadcastPortsFromSandbox(t *testing.T) {
	engine := sandboxEngine(t, "@t/ac", `{api = 2, udp = true, udpBroadcastPorts = {7000}}`)
	if _, e := runPlugin(t, engine, "@t/ac", `
		local p = Plugin:new("ac")
		local s = assert(p:udpSocket())
		local _, other = s:broadcast(7001, "x")
		local _, declared = s:broadcast(7000, "x")
		p.other, p.declared = tostring(other), tostring(declared)
	`); e != "nil" {
		t.Fatal(e)
	}
	if got, _ := engine.Execute(`Plugin.getPlugin("@t/ac").other`); !strings.Contains(got, "broadcastPorts") {
		t.Errorf("broadcast to an undeclared port: %s", got)
	}
	if got, _ := engine.Execute(`Plugin.getPlugin("@t/ac").declared`); strings.Contains(got, "refused") {
		t.Errorf("broadcast to the declared port: %s", got)
	}
}
