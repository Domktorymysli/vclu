package lua

import (
	"bytes"
	"encoding/base64"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	vtftp "github.com/Domktorymysli/vclu/pkg/tftp"
	"github.com/creack/pty"
)

// waitPlugin evaluates expr until it gives want.
func waitPlugin(t *testing.T, engine *LuaEngine, expr, want string) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		engine.ProcessTimers()
		got, _ = engine.Execute(expr)
		if got == want {
			return
		}
	}
	t.Fatalf("%s = %q, want %q", expr, got, want)
}

// loopbackAllowed lets plugins reach 127.0.0.1 in tests, where the other end lives.
func loopbackAllowed(engine *LuaEngine) {
	engine.PluginIO().own = func(netip.Addr) bool { return false }
}

func openHandles(p *PluginIO) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.handles)
}

func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	_ = c.Close()
	return port
}

// Without the permission there is no socket, cipher, TFTP or serial port, in Lua and in Go.
func TestPluginIORefusedWithoutPermission(t *testing.T) {
	engine := sandboxEngine(t, "@t/none", `{api = 2, lan = true}`)
	for _, call := range []string{
		`p:grentonSocket()`,
		`p:grentonCipher("0123456789abcdef", "0123456789abcdef")`,
		`p:tftp("192.168.1.10")`,
		`p:serialOpen("/dev/ttyUSB0")`,
	} {
		_, e := runPlugin(t, engine, "@t/none", `local p = Plugin:new("n") `+call)
		if !strings.Contains(e, "needs") {
			t.Errorf("%s: %s", call, e)
		}
	}
	io := engine.PluginIO()
	if _, err := io.OpenUDP("@t/none", 0); err == nil {
		t.Error("Go: socket without grenton_raw")
	}
	if _, err := io.TFTP("@t/none", "192.168.1.10", 69, "a", nil, false); err == nil {
		t.Error("Go: TFTP without tftp")
	}
	if _, err := io.OpenSerial("@t/none", "/dev/ttyUSB0", serialSettings{baud: 9600, data: 8, parity: "none", stop: 1}); err == nil {
		t.Error("Go: serial without serial")
	}
}

// A datagram goes out and the answer comes back to the plugin's callback.
func TestPluginUDPEcho(t *testing.T) {
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
	engine := sandboxEngine(t, "@t/raw", `{api = 2, grenton_raw = true}`)
	loopbackAllowed(engine)
	port := echo.LocalAddr().(*net.UDPAddr).Port
	if _, e := runPlugin(t, engine, "@t/raw", `
		local p = Plugin:new("raw")
		local s = assert(p:grentonSocket())
		s:onReceive(function(bytes, ip, port) p.got = bytes .. "|" .. ip end)
		assert(s:send("127.0.0.1", `+strconv.Itoa(port)+`, "\0\1ping"))
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/raw").got)`, "re:\x00\x01ping|127.0.0.1")
}

// Where a plugin's datagrams may go: not this vCLU, not outside the house, only the
// allowed hosts; a broadcast only to the CLU port and only without a host list.
func TestPluginUDPTargets(t *testing.T) {
	io := NewPluginIO()
	io.SetGrant("@t/a", pluginIOGrant{grentonRaw: true})
	io.SetGrant("@t/b", pluginIOGrant{grentonRaw: true, rawHosts: []netip.Prefix{netip.MustParsePrefix("192.168.7.0/24")}})
	a, err := io.OpenUDP("@t/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.OpenUDP("@t/b", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer io.CloseAll()
	for _, c := range []struct {
		id      int
		plugin  string
		ip      string
		port    int
		refusal string
	}{
		{a, "@t/a", "127.0.0.1", 1234, "this vCLU"},
		{a, "@t/a", "8.8.8.8", 1234, "not on the local network"},
		{a, "@t/a", "224.0.0.1", 1234, "multicast"},
		{a, "@t/a", "255.255.255.255", 69, "only to the CLU port"},
		{b, "@t/b", "192.168.8.1", 1234, "not among the hosts"},
		{b, "@t/b", "255.255.255.255", 1234, "only some hosts"},
		{a, "@t/b", "192.168.7.1", 1234, "closed"}, // another plugin's socket
	} {
		err := io.SendUDP(c.id, c.plugin, c.ip, c.port, []byte("x"))
		if err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Errorf("%s -> %s:%d: %v, want %q", c.plugin, c.ip, c.port, err, c.refusal)
		}
	}
	if err := io.SendUDP(b, "@t/b", "192.168.7.1", 1234, []byte("x")); !passedChecks(err) {
		t.Errorf("allowed host: %v", err)
	}
}

// vCLU's own ports cannot be bound, and its listener knows a plugin's datagrams.
func TestPluginUDPReservedPortsAndFromPlugin(t *testing.T) {
	io := NewPluginIO()
	io.SetGrant("@t/a", pluginIOGrant{grentonRaw: true})
	reserved := freeUDPPort(t)
	io.ReserveUDPPorts(reserved)
	if _, err := io.OpenUDP("@t/a", reserved); err == nil || !strings.Contains(err.Error(), "free port") {
		t.Errorf("reserved port: %v", err)
	}
	id, err := io.OpenUDP("@t/a", 0)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := io.handle(id, "@t/a")
	port := h.conn.LocalAddr().(*net.UDPAddr).Port
	if !io.FromPlugin(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}) {
		t.Error("a datagram from the plugin's socket not recognised")
	}
	if io.FromPlugin(&net.UDPAddr{IP: net.IPv4(192, 0, 2, 9), Port: port}) {
		t.Error("another machine's datagram taken for the plugin's")
	}
	// a broadcast sent just before closing may still wait in vCLU's socket
	io.Release("@t/a")
	if !io.FromPlugin(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}) {
		t.Error("a datagram from a socket closed a moment ago not recognised")
	}
	io.mu.Lock()
	io.sent[port] = time.Now().Add(-sentTTL - time.Second)
	io.mu.Unlock()
	if io.FromPlugin(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}) {
		t.Error("a socket closed long ago still recognised")
	}
}

// luaBytes: a Lua string literal holding b.
func luaBytes(b []byte) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, c := range b {
		sb.WriteString("\\" + strconv.Itoa(int(c)))
	}
	sb.WriteByte('"')
	return sb.String()
}

// The plugin's cipher is pkg/cipher: what the core encrypts the plugin decrypts, and back;
// the key and IV raw or in base64.
func TestPluginGrentonCipher(t *testing.T) {
	key := []byte("0123456789abcdef")
	iv := []byte("fedcba9876543210")
	ck := cipher.NewCipherKey(key, iv)
	frame := []byte("req:192.168.1.5:1234:SYSTEM:fetchValues({})")
	encrypted, err := ck.Encrypt(frame)
	if err != nil {
		t.Fatal(err)
	}
	engine := sandboxEngine(t, "@t/raw", `{api = 2, grenton_raw = true}`)
	k64, iv64 := base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(iv)
	if _, e := runPlugin(t, engine, "@t/raw", `
		local p = Plugin:new("raw")
		local c = p:grentonCipher("`+k64+`", "`+iv64+`")
		local raw = p:grentonCipher("`+string(key)+`", "`+string(iv)+`")
		p.plain = assert(c:decrypt(`+luaBytes(encrypted)+`))
		p.again = assert(raw:encrypt(p.plain))
		local _, bad = c:decrypt("not a frame")
		p.bad = bad ~= nil
		p.shortKey = pcall(function() p:grentonCipher("short", "`+iv64+`") end)
	`); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `Plugin._lookup("@t/raw").plain`, string(frame))
	again, _ := engine.Execute(`Plugin._lookup("@t/raw").again`)
	if back, err := ck.Decrypt([]byte(again)); err != nil || !bytes.Equal(back, frame) {
		t.Errorf("the plugin's encryption does not decrypt in the core: %q, %v", back, err)
	}
	mustLua(t, engine, `tostring(Plugin._lookup("@t/raw").bad)`, "true")
	mustLua(t, engine, `tostring(Plugin._lookup("@t/raw").shortKey)`, "false")
}

// TFTP against vCLU's own server: put a file, get it back; a host outside the allowed
// ones is refused in the callback.
func TestPluginTFTPGetPut(t *testing.T) {
	dir := t.TempDir()
	port := freeUDPPort(t)
	srv := vtftp.NewServer(dir, "127.0.0.1", port, vtftp.GetAndReplace)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := sandboxEngine(t, "@t/files", `{api = 2, tftp = true, limits = {tftpHosts = {"127.0.0.1"}}}`)
	loopbackAllowed(engine)
	engine.StartTimers()
	defer engine.StopTimers()
	if _, e := runPlugin(t, engine, "@t/files", `
		local p = Plugin:new("files")
		local c = p:tftp("127.0.0.1", {port = `+strconv.Itoa(port)+`})
		c:put("a", "hello\0world", function(ok, err)
			p.put = tostring(ok) .. "|" .. tostring(err)
			c:get("a", function(bytes, err) p.got = tostring(bytes) .. "|" .. tostring(err) end)
		end)
		p:tftp("192.168.1.10"):get("a", function(bytes, err) p.other = tostring(err) end)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/files").put)`, "true|nil")
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/files").got)`, "hello\x00world|nil")
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/files").other):match("not among the hosts") ~= nil`, "true")
	if b, _ := os.ReadFile(filepath.Join(dir, "a")); string(b) != "hello\x00world" {
		t.Errorf("file on the server: %q", b)
	}
	for deadline := time.Now().Add(2 * time.Second); openHandles(engine.PluginIO()) != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d handles left after the transfers", openHandles(engine.PluginIO()))
		}
	}
}

// openPty: the master end for the test and the path of the port the plugin opens.
func openPty(t *testing.T) (*os.File, string) {
	t.Helper()
	master, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = tty.Close() })
	return master, tty.Name()
}

// A serial port the user assigned: bytes both ways, nothing lost while the callback is
// slow; a port not assigned or already open is refused.
func TestPluginSerialOverPty(t *testing.T) {
	master, path := openPty(t)
	engine := sandboxEngine(t, "@t/rs", `{api = 2, serial = true, limits = {serialPorts = {"`+path+`"}}}`)
	if _, err := engine.Execute(`PluginSandbox:create("@t/other", {api = 2, serial = true, limits = {serialPorts = {"` + path + `"}}})`); err != nil {
		t.Fatal(err)
	}
	if _, e := runPlugin(t, engine, "@t/rs", `
		local p = Plugin:new("rs")
		local port = assert(p:serialOpen("`+path+`", {baud = 19200, parity = "even"}))
		p.n = 0
		port:onData(function(bytes)
			p.n = p.n + #bytes
			local x = 0
			for i = 1, 20000 do x = x + i end -- a slow callback
		end)
		assert(port:write("\1\2\3"))
		local _, err = p:serialOpen("/dev/ttyNOTASSIGNED")
		p.unassigned = tostring(err)
	`); e != "nil" {
		t.Fatal(e)
	}
	got := make([]byte, 3)
	_ = master.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := master.Read(got); err != nil || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("written by the plugin: %v, %v", got, err)
	}
	const total = 64 << 10
	go func() {
		chunk := bytes.Repeat([]byte{0x55}, 512)
		for i := 0; i < total/len(chunk); i++ {
			_, _ = master.Write(chunk)
		}
	}()
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/rs").n)`, strconv.Itoa(total))
	mustLua(t, engine, `tostring(Plugin._lookup("@t/rs").unassigned):match("not assigned") ~= nil`, "true")

	_, e := runPlugin(t, engine, "@t/other", `
		local p = Plugin:new("other")
		local port, err = p:serialOpen("`+path+`")
		p.err = tostring(err)
	`)
	if e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `tostring(Plugin._lookup("@t/other").err):match("already open") ~= nil`, "true")
}

// The plugin's cleanup closes its sockets and ports: the port is free again.
func TestPluginIOClosedOnCleanup(t *testing.T) {
	_, path := openPty(t)
	engine := sandboxEngine(t, "@t/gone", `{api = 2, grenton_raw = true, serial = true, limits = {serialPorts = {"`+path+`"}}}`)
	if _, e := runPlugin(t, engine, "@t/gone", `
		local p = Plugin:new("gone")
		assert(p:grentonSocket())
		assert(p:serialOpen("`+path+`"))
	`); e != "nil" {
		t.Fatal(e)
	}
	if n := openHandles(engine.PluginIO()); n != 2 {
		t.Fatalf("%d handles open, want 2", n)
	}
	var port int
	engine.PluginIO().mu.Lock()
	for _, h := range engine.PluginIO().handles {
		if h.conn != nil {
			port = h.conn.LocalAddr().(*net.UDPAddr).Port
		}
	}
	engine.PluginIO().mu.Unlock()
	if _, err := engine.Execute(`PluginSandbox:cleanup("@t/gone")`); err != nil {
		t.Fatal(err)
	}
	if n := openHandles(engine.PluginIO()); n != 0 {
		t.Errorf("%d handles left after cleanup", n)
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		t.Errorf("the plugin's port is still bound: %v", err)
	} else {
		_ = c.Close()
	}
	// The grant went with the sandbox: nothing opens under that id any more
	if _, err := engine.PluginIO().OpenUDP("@t/gone", 0); err == nil {
		t.Error("socket opened after cleanup")
	}
}

// The TFTP client refuses broadcasts, IPv6 and this machine (also under another name).
func TestPluginTFTPTargets(t *testing.T) {
	io := NewPluginIO()
	io.SetGrant("@t/f", pluginIOGrant{tftp: true})
	for _, host := range []string{"255.255.255.255", "::1", "fe80::1%lo0", "::7f00:1", "64:ff9b::7f00:1", "127.0.0.1", "8.8.8.8"} {
		if _, err := io.TFTP("@t/f", host, 69, "a", nil, false); err == nil {
			t.Errorf("%s: not refused", host)
		}
	}
	if n := openHandles(io); n != 0 {
		t.Errorf("%d handles after refusals", n)
	}
}

// A file larger than the queue arrives whole, an empty one as "", not as an error.
func TestPluginTFTPLargeAndEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	port := freeUDPPort(t)
	srv := vtftp.NewServer(dir, "127.0.0.1", port, vtftp.GetAndReplace)
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	big := bytes.Repeat([]byte("0123456789abcdef"), (2<<20)/16)
	if err := os.WriteFile(filepath.Join(dir, "big"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	engine := sandboxEngine(t, "@t/files", `{api = 2, tftp = true}`)
	loopbackAllowed(engine)
	if _, e := runPlugin(t, engine, "@t/files", `
		local p = Plugin:new("files")
		local c = p:tftp("127.0.0.1", {port = `+strconv.Itoa(port)+`})
		c:get("big", function(bytes, err) p.big = tostring(bytes and #bytes) .. "|" .. tostring(err) end)
		c:get("empty", function(bytes, err) p.empty = "[" .. tostring(bytes) .. "]|" .. tostring(err) end)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/files").empty)`, "[]|nil")
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/files").big)`, strconv.Itoa(len(big))+"|nil")
	for deadline := time.Now().Add(2 * time.Second); openHandles(engine.PluginIO()) != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d handles left", openHandles(engine.PluginIO()))
		}
	}
}

// A port that goes away: onData gets (nil, err) once and the handle is gone.
func TestPluginSerialPortGone(t *testing.T) {
	master, path := openPty(t)
	engine := sandboxEngine(t, "@t/rs", `{api = 2, serial = true, limits = {serialPorts = {"`+path+`"}}}`)
	if _, e := runPlugin(t, engine, "@t/rs", `
		local p = Plugin:new("rs")
		local port = assert(p:serialOpen("`+path+`"))
		p.calls = 0
		port:onData(function(bytes, err)
			if bytes == nil then p.calls = p.calls + 1; p.err = err end
		end)
	`); e != "nil" {
		t.Fatal(e)
	}
	_ = master.Close()
	waitPlugin(t, engine, `tostring(Plugin._lookup("@t/rs").calls)`, "1")
	for deadline := time.Now().Add(2 * time.Second); openHandles(engine.PluginIO()) != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d handles left after the port went", openHandles(engine.PluginIO()))
		}
	}
}
