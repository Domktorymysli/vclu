package lua

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tcpServer listens on 127.0.0.1 and hands each connection to serve.
func tcpServer(t *testing.T, serve func(net.Conn)) int {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func tcpEngine(t *testing.T, id string, port int) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, id, `{api = 2, tcp = true, tcpPorts = {`+strconv.Itoa(port)+`}}`)
	loopbackAllowed(engine)
	return engine
}

// tcp is its own permission: a connection comes up, bytes go out and come back in order,
// and the device closing it ends it with a reason.
func TestPluginTCPEchoAndClose(t *testing.T) {
	port := tcpServer(t, func(c net.Conn) {
		defer c.Close()
		buf := make([]byte, 64)
		got := ""
		for !strings.HasSuffix(got, "three\n") {
			n, err := c.Read(buf)
			if err != nil {
				return
			}
			got += string(buf[:n])
			_, _ = c.Write(buf[:n])
		}
	})
	engine := tcpEngine(t, "@t/tv", port)
	if _, e := runPlugin(t, engine, "@t/tv", `
		local p = Plugin:new("tv")
		p.got, p.events = "", ""
		local c = assert(p:tcpConnect({host = "127.0.0.1", port = `+strconv.Itoa(port)+`}))
		local _, early = c:send("x")
		p.early = tostring(early)
		c:onConnect(function()
			p.events = p.events .. "connect," .. tostring(c:isOpen()) .. ","
			assert(c:send("one\n")) assert(c:send("two\n")) assert(c:send("three\n"))
		end)
		c:onReceive(function(bytes) p.got = p.got .. bytes end)
		c:onClose(function(reason) p.events = p.events .. "close:" .. reason .. "," .. tostring(c:isOpen()) end)
	`); e != "nil" {
		t.Fatal(e)
	}
	if got, _ := engine.Execute(`Plugin.getPlugin("@t/tv").early`); !strings.Contains(got, "not connected yet") {
		t.Errorf("send before the connection is up: %s", got)
	}
	waitPlugin(t, engine, `Plugin.getPlugin("@t/tv").got`, "one\ntwo\nthree\n")
	waitPlugin(t, engine, `Plugin.getPlugin("@t/tv").events`, "connect,true,close:closed by the device,false")
	waitHandles(t, engine.PluginIO(), 0)
}

// Without tcp there is no connection, in Lua and in Go, also with udp and lan.
func TestPluginTCPRefusedWithoutPermission(t *testing.T) {
	engine := sandboxEngine(t, "@t/n", `{api = 2, lan = true, udp = true, tcpPorts = {23}}`)
	if _, e := runPlugin(t, engine, "@t/n", `Plugin:new("n"):tcpConnect({host = "192.168.1.20", port = 23})`); !strings.Contains(e, `needs "tcp"`) {
		t.Errorf("tcpConnect without tcp: %s", e)
	}
	if _, err := engine.PluginIO().OpenTCP("@t/n", "192.168.1.20", 23, 0); err == nil || !strings.Contains(err.Error(), `"tcp"`) {
		t.Errorf("Go: tcp without tcp: %v", err)
	}
}

// Where a connection may go: a device on the local network by IPv4 address, to a declared
// port that is not another permission's, among the user's hosts, not what vclu itself uses.
func TestPluginTCPTargets(t *testing.T) {
	io := NewPluginIO()
	io.dialTCP = func(ctx context.Context, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	io.SetTCPTaken(func() map[netip.AddrPort]string {
		return map[netip.AddrPort]string{netip.MustParseAddrPort("192.168.1.50:8000"): "mqtt"}
	})
	io.SetGrant("@t/a", pluginIOGrant{tcp: true, tcpPorts: []int{23, 8000, 502, 1883, 1234}})
	io.SetGrant("@t/b", pluginIOGrant{tcp: true, tcpPorts: []int{23},
		tcpHosts: []netip.Prefix{netip.MustParsePrefix("192.168.7.0/24")}})
	defer io.CloseAll()
	for _, x := range []struct {
		plugin, host string
		port         int
		timeout      time.Duration
		refusal      string
	}{
		{"@t/a", "127.0.0.1", 23, 0, "this vCLU"},
		{"@t/a", "8.8.8.8", 23, 0, "not on the local network"},
		{"@t/a", "224.0.0.1", 23, 0, "multicast"},
		{"@t/a", "255.255.255.255", 23, 0, "broadcast"},
		{"@t/a", "tv.local", 23, 0, "not an IPv4 address"},
		{"@t/a", "192.168.1.20", 24, 0, `"tcp": {"ports"}`},
		{"@t/a", "192.168.1.20", 502, 0, "modbus permission"},
		{"@t/a", "192.168.1.20", 1883, 0, "mqtt permission"},
		{"@t/a", "192.168.1.20", 1234, 0, "grenton_raw permission"},
		{"@t/a", "192.168.1.50", 8000, 0, "vclu uses it with the mqtt permission"},
		{"@t/a", "192.168.1.20", 23, 31 * time.Second, "at most 30s"},
		{"@t/b", "192.168.8.1", 23, 0, "not among the hosts"},
	} {
		_, err := io.OpenTCP(x.plugin, x.host, x.port, x.timeout)
		if err == nil || !strings.Contains(err.Error(), x.refusal) {
			t.Errorf("%s -> %s:%d: %v, want %q", x.plugin, x.host, x.port, err, x.refusal)
		}
	}
	if _, err := io.OpenTCP("@t/b", "192.168.7.1", 23, 0); err != nil {
		t.Errorf("an allowed host: %v", err)
	}
	if _, err := io.OpenTCP("@t/a", "192.168.1.50", 23, 0); err != nil {
		t.Errorf("the broker's host on another port: %v", err)
	}
}

// A connection that does not come up in its timeout ends with the reason; a plugin has at
// most pluginIOMaxHandles connections and other handles together.
func TestPluginTCPTimeoutAndHandleLimit(t *testing.T) {
	engine := sandboxEngine(t, "@t/slow", `{api = 2, tcp = true, tcpPorts = {23}}`)
	engine.PluginIO().dialTCP = func(ctx context.Context, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	if _, e := runPlugin(t, engine, "@t/slow", `
		local p = Plugin:new("slow")
		local c = assert(p:tcpConnect({host = "192.168.1.20", port = 23, timeout = 0.2}))
		c:onClose(function(reason) p.reason = reason end)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `tostring(Plugin.getPlugin("@t/slow").reason):match("deadline") and "timed out" or ""`, "timed out")

	io := engine.PluginIO()
	for i := 0; i < pluginIOMaxHandles; i++ {
		if _, err := io.OpenTCP("@t/slow", "192.168.1.20", 23, 0); err != nil {
			t.Fatalf("connection %d: %v", i+1, err)
		}
	}
	if _, err := io.OpenTCP("@t/slow", "192.168.1.20", 23, 0); err == nil || !strings.Contains(err.Error(), "the most a plugin may") {
		t.Errorf("one past the limit: %v", err)
	}
}

// The plugin's cleanup (reload, removal) and a grant taken away close an open connection;
// the device sees it end.
func TestPluginTCPClosedByReleaseAndGrant(t *testing.T) {
	ended := make(chan string, 2)
	port := tcpServer(t, func(c net.Conn) {
		_, err := io.Copy(io.Discard, c)
		if err == nil {
			ended <- "eof"
		} else {
			ended <- err.Error()
		}
	})
	engine := tcpEngine(t, "@t/r", port)
	pio := engine.PluginIO()
	open := func() int {
		t.Helper()
		id, err := pio.OpenTCP("@t/r", "127.0.0.1", port, 0)
		if err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if h, _ := pio.handle(id, "@t/r"); h != nil && h.tcp.connected() {
				return id
			}
			if time.Now().After(deadline) {
				t.Fatal("not connected")
			}
		}
	}
	waitEnd := func(what string) {
		t.Helper()
		select {
		case <-ended:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the device did not see the connection end", what)
		}
	}

	open()
	pio.Release("@t/r")
	waitEnd("release")

	pio.SetGrant("@t/r", pluginIOGrant{tcp: true, tcpPorts: []int{port}})
	id := open()
	pio.SetGrant("@t/r", pluginIOGrant{tcp: true, tcpPorts: []int{port}, tcpHosts: []netip.Prefix{netip.MustParsePrefix("192.168.7.0/24")}})
	waitEnd("hosts narrowed")
	if err := pio.SendTCP(id, "@t/r", []byte("x")); err == nil {
		t.Error("send after the grant narrowed")
	}
	waitHandles(t, pio, 0)
}

// lib/telnet over a real connection: options refused, a prompt answered, lines read.
func TestPluginTelnetLogin(t *testing.T) {
	heard := make(chan []byte, 1)
	port := tcpServer(t, func(c net.Conn) {
		defer c.Close()
		// WILL ECHO, DO TERMINAL-TYPE, a subnegotiation, then the banner and a prompt in pieces
		_, _ = c.Write([]byte{255, 251, 1, 255})
		time.Sleep(20 * time.Millisecond)
		_, _ = c.Write([]byte{253, 24, 255, 250, 24, 1, 255, 240})
		_, _ = c.Write([]byte("Welcome\r\nlog"))
		time.Sleep(20 * time.Millisecond)
		_, _ = c.Write([]byte("in: "))
		var got []byte
		buf := make([]byte, 64)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		for !bytes.HasSuffix(got, []byte("admin\r\n")) {
			n, err := c.Read(buf)
			if err != nil {
				break
			}
			got = append(got, buf[:n]...)
		}
		heard <- got
		_, _ = c.Write([]byte("OK \xff\xff\r\n> "))
		time.Sleep(200 * time.Millisecond)
	})
	engine := tcpEngine(t, "@t/tn", port)
	if _, e := runPlugin(t, engine, "@t/tn", `
		local p = Plugin:new("tn")
		p.log = ""
		local c = assert(p:tcpConnect({host = "127.0.0.1", port = `+strconv.Itoa(port)+`}))
		local t = Telnet.wrap(c)
		t:onLine(function(line) p.log = p.log .. "line:" .. line .. "|" end)
		t:onPrompt("login: $", function(text) p.log = p.log .. "prompt:" .. text .. "|" t:send("admin") end)
		t:onPrompt("> $", function() p.log = p.log .. "ready" end)
	`); e != "nil" {
		t.Fatal(e)
	}
	select {
	case got := <-heard:
		want := []byte{255, 254, 1, 255, 252, 24}
		if !bytes.HasPrefix(got, want) || !bytes.HasSuffix(got, []byte("admin\r\n")) {
			t.Errorf("the device heard % x, want DONT ECHO, WONT TERMINAL-TYPE, admin", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no login sent")
	}
	waitPlugin(t, engine, `Plugin.getPlugin("@t/tn").log`, "line:Welcome|prompt:login: |line:OK \xff|ready")
}

func waitHandles(t *testing.T, p *PluginIO, want int) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if openHandles(p) == want {
			return
		}
	}
	t.Errorf("%d handles open, want %d", openHandles(p), want)
}
