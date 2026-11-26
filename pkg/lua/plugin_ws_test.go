package lua

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsServer: a WebSocket server on 127.0.0.1 handing each connection to serve; its port.
func wsServer(t *testing.T, serve func(c *websocket.Conn, r *http.Request)) string {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, Subprotocols: []string{"json"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if to := r.URL.Query().Get("to"); to != "" {
			http.Redirect(w, r, to, http.StatusFound)
			return
		}
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		serve(c, r)
	}))
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	return port
}

// echo sends back every message as it came, until the plugin closes.
func echo(c *websocket.Conn, _ *http.Request) {
	for {
		mt, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		if string(data) == "close-me" {
			_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(4001, "bye"))
			_, _, _ = c.ReadMessage()
			return
		}
		_ = c.WriteMessage(mt, data)
	}
}

// closeCodes records the close code each connection to the server ended with.
type closeCodes struct {
	mu    sync.Mutex
	codes []int
}

func (cc *closeCodes) serve(c *websocket.Conn, _ *http.Request) {
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			code := 0
			if ce, ok := err.(*websocket.CloseError); ok {
				code = ce.Code
			}
			cc.mu.Lock()
			cc.codes = append(cc.codes, code)
			cc.mu.Unlock()
			return
		}
	}
}

func (cc *closeCodes) wait(t *testing.T, want int) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		cc.mu.Lock()
		got := append([]int(nil), cc.codes...)
		cc.mu.Unlock()
		if len(got) > 0 {
			if got[len(got)-1] != want {
				t.Errorf("server saw close %v, want %d", got, want)
			}
			return
		}
	}
	t.Errorf("server saw no close, want %d", want)
}

func wsEngine(t *testing.T, id, perms string) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, id, perms)
	loopbackAllowed(engine)
	engine.StartTimers()
	return engine
}

// A connection opens with the server's protocol, text and binary messages go both ways in
// order, and the server closing it reaches onClose with its code and reason.
func TestPluginWSEchoAndClose(t *testing.T) {
	port := wsServer(t, echo)
	engine := wsEngine(t, "@t/ws", `{api = 2, lan = true, lanPorts = {`+port+`}}`)
	if _, e := runPlugin(t, engine, "@t/ws", `
		local p = Plugin:new("ws")
		p.got, p.events = "", ""
		local ws = assert(p:websocket("ws://127.0.0.1:`+port+`/rpc", {protocols = {"json"}}))
		local _, early = ws:send("x")
		p.early = tostring(early)
		ws:onOpen(function(protocol)
			p.events = p.events .. "open:" .. protocol .. "," .. tostring(ws:isOpen()) .. ","
			assert(ws:send("one")) assert(ws:send("\0\1", {binary = true})) assert(ws:send("three"))
		end)
		ws:onMessage(function(data, binary)
			p.got = p.got .. (binary and "b" or "t") .. #data .. ","
			if data == "three" then ws:send("close-me") end
		end)
		ws:onClose(function(code, reason) p.events = p.events .. "close:" .. code .. ":" .. reason .. "," .. tostring(ws:isOpen()) end)
	`); e != "nil" {
		t.Fatal(e)
	}
	if got, _ := engine.Execute(`Plugin._lookup("@t/ws").early`); !strings.Contains(got, "not open yet") {
		t.Errorf("send before open: %s", got)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/ws").got`, "t3,b2,t5,")
	waitPlugin(t, engine, `Plugin._lookup("@t/ws").events`, "open:json,true,close:4001:bye,false")
	waitHandles(t, engine.PluginIO(), 0)
}

// The internet host list holds as for HTTP, before DNS and after it: a listed name that
// resolves to a local address still needs lan.
func TestPluginWSInternetHosts(t *testing.T) {
	port := wsServer(t, echo)
	engine := wsEngine(t, "@t/net", `{api = 2, limits = {internetHosts = {"api.example.com"}}}`)
	hm := engine.httpManager
	hm.isLocal = func(netip.Addr) bool { return false }
	hm.testResolve = map[string]string{"api.example.com": "127.0.0.1", "other.example.com": "127.0.0.1"}
	if _, e := runPlugin(t, engine, "@t/net", `
		local p = Plugin:new("net")
		p.state = ""
		local ws = assert(p:websocket("ws://api.example.com:`+port+`/"))
		ws:onOpen(function() p.state = "open" end)
		local _, err = p:websocket("ws://other.example.com:`+port+`/")
		p.refused = tostring(err)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/net").state`, "open")
	mustLua(t, engine, `Plugin._lookup("@t/net").refused`, "other.example.com is not on the host list of @t/net (permissions.internet)")

	// the listed name on a local address
	hm.isLocal = defaultIsLocal
	if _, e := runPlugin(t, engine, "@t/net", `
		local p = Plugin.getPlugin("@t/net")
		local ws = assert(p:websocket("ws://api.example.com:`+port+`/"))
		ws:onClose(function(code, reason) p.local_ = code .. " " .. reason end)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/net").local_ ~= nil and Plugin._lookup("@t/net").local_:find('needs "lan"', 1, true) ~= nil`, "true")
}

// A local server needs lan and its hosts; this vclu's own addresses are refused.
func TestPluginWSLAN(t *testing.T) {
	port := wsServer(t, echo)
	open := func(engine *LuaEngine, id string) {
		t.Helper()
		if _, e := runPlugin(t, engine, id, `
			local p = Plugin:new("l")
			p.r = ""
			local ws, err = p:websocket("ws://127.0.0.1:`+port+`/")
			if not ws then p.r = "refused:" .. err return end
			ws:onOpen(function() p.r = "open" end)
			ws:onClose(function(code, reason) p.r = "close:" .. reason end)
		`); e != "nil" {
			t.Fatal(e)
		}
	}
	result := `Plugin._lookup("%s").r`
	cases := []struct {
		id, perms string
		own       bool
		want      string
	}{
		{"@t/nolan", `{api = 2}`, false, `needs "lan"`},
		{"@t/lan", `{api = 2, lan = true, lanPorts = {` + port + `}}`, false, "open"},
		{"@t/anyport", `{api = 2, lan = true}`, false, "open"}, // lan without ports: any port
		{"@t/otherport", `{api = 2, lan = true, lanPorts = {8123}}`, false, `permissions.lan.ports`},
		{"@t/narrow", `{api = 2, lan = true, lanPorts = {` + port + `}, limits = {lanHosts = {"192.168.9.0/24"}}}`, false, "not among the hosts"},
		{"@t/own", `{api = 2, lan = true, lanPorts = {` + port + `}}`, true, "an address of this vclu"},
	}
	for _, c := range cases {
		engine := sandboxEngine(t, c.id, c.perms)
		if !c.own {
			loopbackAllowed(engine)
		}
		engine.StartTimers()
		open(engine, c.id)
		expr := strings.Replace(result, "%s", c.id, 1)
		waitPlugin(t, engine, expr+` ~= "" and (`+expr+`):find(`+strconv.Quote(c.want)+`, 1, true) ~= nil`, "true")
	}
}

// Headers the handshake sets are refused; others reach the server.
func TestPluginWSHeaders(t *testing.T) {
	port := wsServer(t, func(c *websocket.Conn, r *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, []byte(r.Header.Get("Authorization")+"|"+r.Header.Get("Origin")))
		_, _, _ = c.ReadMessage()
	})
	engine := wsEngine(t, "@t/h", `{api = 2, lan = true, lanPorts = {`+port+`}}`)
	if _, e := runPlugin(t, engine, "@t/h", `
		local p = Plugin:new("h")
		local ws = assert(p:websocket("ws://127.0.0.1:`+port+`/", {headers = {Authorization = "Bearer t", Origin = "https://app"}}))
		ws:onMessage(function(m) p.got = m end)
		local bad = {}
		for _, h in ipairs({"Host", "upgrade", "Connection", "Sec-WebSocket-Key", "sec-websocket-protocol"}) do
			local _, err = p:websocket("ws://127.0.0.1:`+port+`/", {headers = {[h] = "x"}})
			bad[#bad + 1] = tostring(err)
		end
		p.bad = table.concat(bad, ";")
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/h").got`, "Bearer t|https://app")
	bad, _ := engine.Execute(`Plugin._lookup("@t/h").bad`)
	if strings.Count(bad, "set by the WebSocket handshake") != 5 {
		t.Errorf("handshake headers: %s", bad)
	}
}

// What the server cannot do: a message over the limit, a redirect, no answer in time.
func TestPluginWSLimits(t *testing.T) {
	big := wsServer(t, func(c *websocket.Conn, _ *http.Request) {
		_ = c.WriteMessage(websocket.TextMessage, make([]byte, wsMessageMax+1))
		_, _, _ = c.ReadMessage()
	})
	silent := tcpServer(t, func(c net.Conn) {
		buf := make([]byte, 1024)
		_, _ = c.Read(buf)
		time.Sleep(2 * time.Second)
		c.Close()
	})
	engine := wsEngine(t, "@t/lim", `{api = 2, lan = true, lanPorts = {`+big+`, `+strconv.Itoa(silent)+`}}`)
	if _, e := runPlugin(t, engine, "@t/lim", `
		local p = Plugin:new("lim")
		local function watch(key, url, opts)
			local ws, err = p:websocket(url, opts)
			if not ws then p[key] = "refused:" .. err return end
			ws:onClose(function(code, reason) p[key] = code .. " " .. reason end)
		end
		watch("big", "ws://127.0.0.1:`+big+`/")
		watch("redirect", "ws://127.0.0.1:`+big+`/?to=ws://127.0.0.1:1/")
		watch("slow", "ws://127.0.0.1:`+strconv.Itoa(silent)+`/", {timeout = 0.3})
		watch("long", "ws://127.0.0.1:`+big+`/", {timeout = 31})
		watch("scheme", "http://127.0.0.1:`+big+`/")
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/lim").big`, "1009 a message over 1048576 bytes")
	waitPlugin(t, engine, `Plugin._lookup("@t/lim").redirect`, "1006 connect: the server answered HTTP 302")
	waitPlugin(t, engine, `Plugin._lookup("@t/lim").slow ~= nil and Plugin._lookup("@t/lim").slow:find("1006 connect:", 1, true) == 1`, "true")
	mustLua(t, engine, `Plugin._lookup("@t/lim").long`, "refused:timeout 31s: at most 30s")
	mustLua(t, engine, `Plugin._lookup("@t/lim").scheme`, `refused:"http://127.0.0.1:`+big+`/" is not a ws:// or wss:// URL`)
	waitHandles(t, engine.PluginIO(), 0)
}

// The plugin's close sends its code; a reload closes with 1001; a plugin has at most
// pluginIOMaxHandles connections and other handles together.
func TestPluginWSCloseReloadAndLimit(t *testing.T) {
	var cc closeCodes
	port := wsServer(t, cc.serve)
	engine := wsEngine(t, "@t/c", `{api = 2, lan = true, lanPorts = {`+port+`}}`)
	if _, e := runPlugin(t, engine, "@t/c", `
		local p = Plugin:new("c")
		local ws = assert(p:websocket("ws://127.0.0.1:`+port+`/"))
		local _, bad = ws:close(1001)
		p.bad = tostring(bad)
		ws:onOpen(function() p.closed = tostring(ws:close(4000, "done")) .. "," .. tostring(ws:isOpen()) end)
	`); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `Plugin._lookup("@t/c").bad`, "close code 1001: 1000 or 3000-4999")
	waitPlugin(t, engine, `Plugin._lookup("@t/c").closed`, "true,false")
	cc.wait(t, 4000)
	waitHandles(t, engine.PluginIO(), 0)

	cc = closeCodes{}
	if _, e := runPlugin(t, engine, "@t/c", `
		local p = Plugin.getPlugin("@t/c")
		p.opened, p.refused = 0, ""
		for i = 1, 17 do
			local ws, err = p:websocket("ws://127.0.0.1:`+port+`/")
			if ws then ws:onOpen(function() p.opened = p.opened + 1 end) else p.refused = err end
		end
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/c").opened`, "16")
	mustLua(t, engine, `Plugin._lookup("@t/c").refused`, "@t/c has 16 sockets, ports and transfers open, the most a plugin may")
	engine.PluginIO().Release("@t/c")
	cc.wait(t, 1001)
	waitHandles(t, engine.PluginIO(), 0)
}

// The plugin losing lan while connected closes the connection (1008), as a new grant does
// for tcp; a send after that is refused.
func TestPluginWSGrantWithdrawn(t *testing.T) {
	var cc closeCodes
	port := wsServer(t, cc.serve)
	engine := wsEngine(t, "@t/g", `{api = 2, lan = true, lanPorts = {`+port+`}}`)
	if _, e := runPlugin(t, engine, "@t/g", `
		local p = Plugin:new("g")
		p.ws = assert(p:websocket("ws://127.0.0.1:`+port+`/"))
		p.ws:onOpen(function() p.open = true end)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/g").open`, "true")
	if _, err := engine.Execute(`__go_http_set_lan("@t/g", true, {"192.168.9.0/24"})`); err != nil {
		t.Fatal(err)
	}
	cc.wait(t, websocket.ClosePolicyViolation)
	waitHandles(t, engine.PluginIO(), 0)
	got, _ := engine.Execute(`select(2, Plugin._lookup("@t/g").ws:send("x"))`)
	if got != "connection closed" {
		t.Errorf("send after lan was narrowed: %s", got)
	}

	// SendWS checks again too, should a change not reach recheckWS
	io, hm := NewPluginIO(), NewHTTPManager()
	io.own = func(netip.Addr) bool { return false }
	defer io.CloseAll()
	open := true
	id, err := io.OpenWS("@t/x", "ws://127.0.0.1:"+port+"/", nil, nil, 0, func() dialPolicy {
		pol := hm.policy("@t/x")
		pol.noLAN = !open
		return pol
	})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if err := io.SendWS(id, "@t/x", []byte("x"), false); err == nil {
			break
		}
	}
	open = false
	if err := io.SendWS(id, "@t/x", []byte("x"), false); err == nil || !strings.Contains(err.Error(), `needs "lan"`) {
		t.Errorf("send after lan was withdrawn: %v", err)
	}
	waitHandles(t, io, 0)
}

// API 1 reaches what its HTTP does: any internet host, local ones.
func TestPluginWSAPI1(t *testing.T) {
	port := wsServer(t, echo)
	engine := wsEngine(t, "@t/old", `{api = 1, lan = true, lanPorts = {`+port+`}}`)
	engine.httpManager.testResolve = map[string]string{"any.example.org": "127.0.0.1"}
	if _, e := runPlugin(t, engine, "@t/old", `
		local p = Plugin:new("old")
		p.n = 0
		for _, host in ipairs({"127.0.0.1", "any.example.org"}) do
			local ws = assert(p:websocket("ws://" .. host .. ":`+port+`/"))
			ws:onOpen(function() p.n = p.n + 1 end)
		end
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/old").n`, "2")
}

// A WebSocket to a local address goes only to a port plugin.json lists, never to a port of
// another permission, and not to the host of the MQTT broker vclu uses on any port.
func TestPluginWSLANPorts(t *testing.T) {
	port := wsServer(t, echo)
	cases := []struct {
		id, perms string
		taken     bool
		want      string
	}{
		{"@t/listed", `{api = 2, lan = true, lanPorts = {` + port + `}}`, false, "open"},
		{"@t/mqtt", `{api = 2, lan = true, lanPorts = {1883, ` + port + `}}`, false, "belongs to the mqtt permission"},
		{"@t/broker", `{api = 2, lan = true, lanPorts = {` + port + `}}`, true, "vclu uses it with the mqtt permission"},
	}
	for _, c := range cases {
		engine := wsEngine(t, c.id, c.perms)
		if c.taken {
			engine.PluginIO().SetTCPTaken(func() map[netip.AddrPort]string {
				return map[netip.AddrPort]string{netip.MustParseAddrPort("127.0.0.1:1883"): "mqtt"}
			})
		}
		target := port
		if c.id == "@t/mqtt" {
			target = "1883"
		}
		if _, e := runPlugin(t, engine, c.id, `
			local p = Plugin:new("w")
			p.r = ""
			local ws, err = p:websocket("ws://127.0.0.1:`+target+`/")
			if not ws then p.r = "refused:" .. err return end
			ws:onOpen(function() p.r = "open" end)
			ws:onClose(function(code, reason) p.r = "close:" .. reason end)
		`); e != "nil" {
			t.Fatal(e)
		}
		expr := `Plugin._lookup("` + c.id + `").r`
		waitPlugin(t, engine, expr+` ~= "" and (`+expr+`):find(`+strconv.Quote(c.want)+`, 1, true) ~= nil`, "true")
	}
}
