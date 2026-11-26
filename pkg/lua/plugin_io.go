package lua

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	tftp "github.com/pin/tftp/v3"
	lua "github.com/yuin/gopher-lua"
)

// grentonPort: where CLUs listen; the only port a clu socket may broadcast to (discovery).
const grentonPort = 1234

// tftpPort: the CLU's TFTP; with grentonPort reached only with clu or tftp, not udp.
const tftpPort = 69

const (
	pluginIOMaxHandles = 16       // sockets, ports and transfers of one plugin
	pluginIOQueueBytes = 1 << 20  // undelivered bytes of one handle before it drops
	pluginTFTPMaxBytes = 16 << 20 // one file
)

// PluginIO holds what plugins with udp, tcp, clu, tftp or serial opened: their UDP sockets,
// TCP connections, serial ports and TFTP transfers, and every plugin's WebSockets. Each belongs to one plugin, data reaches Lua through
// deliver (the run loop) and every handle closes with the plugin.
type PluginIO struct {
	mu       sync.Mutex
	grants   map[string]pluginIOGrant
	handles  map[int]*ioHandle
	nextID   int
	serial   map[string]int    // resolved path -> handle holding it
	reserved map[int]bool      // UDP ports vCLU listens on
	sent     map[int]time.Time // local ports of plugin sockets, kept a while after they close
	deliver  func(name string, fn func(L *lua.LState) (string, error)) (string, error)
	own      func(netip.Addr) bool // an address of this machine; tests replace it
	// tcpTaken: addresses vclu itself talks to with another permission (its MQTT broker)
	tcpTaken func() map[netip.AddrPort]string
	dialTCP  func(ctx context.Context, addr string) (net.Conn, error)
}

// pluginIOGrant: what the user allowed a plugin; nil host lists allow any local host.
type pluginIOGrant struct {
	grentonRaw, tftp bool
	serial           bool
	rawHosts         []netip.Prefix
	tftpHosts        []netip.Prefix
	serialPorts      []string
	udp              bool
	udpHosts         []netip.Prefix
	udpPorts         []int // udp: the ports it sends to, from plugin.json
	broadcastPorts   []int // udp: the ports it broadcasts to, among udpPorts
	tcp              bool
	tcpHosts         []netip.Prefix
	tcpPorts         []int // tcp: from plugin.json
	wsLANPorts       []int // WebSockets to local addresses: plugin.json's lan.ports, nil any
	cluDiscovery     bool  // clu with rawHosts set: broadcasts to the CLU port all the same
}

type ioHandle struct {
	id     int
	plugin string
	kind   string // "udp", "tcp", "ws", "serial", "tftp"
	perm   string // udp: the permission it was opened under, "udp" or "grenton_raw"
	path   string // serial: resolved path
	conn   *net.UDPConn
	port   io.ReadWriteCloser
	tcp    *tcpConn
	ws     *wsConn
	// ws: the close frame the plugin asked for; 0 sends 1001 (going away: reload, unload)
	wsCode   int
	wsReason string
	// udp: who may answer, under PluginIO.mu: addresses it sent to, ports it broadcast to
	peers map[netip.Addr]bool
	bcast map[int]time.Time
	q     ioQueue
	done  chan struct{}
	once  sync.Once
}

type ioEvent struct {
	what string // tcp: "connect" once it is up; ws: "open" (data: protocol), "message"
	data []byte
	// ws: a binary message; port is the close code with err
	binary bool
	ip     string
	port   int
	err    string
}

// ioQueue keeps what arrived until Lua takes it, so a slow callback does not stop the
// reader; serial bytes are merged into one chunk, datagrams stay apart.
type ioQueue struct {
	mu      sync.Mutex
	events  []ioEvent
	size    int
	dropped int
	wake    chan struct{}
}

// push queues e; past pluginIOQueueBytes data is dropped, unless whole (a transfer's answer).
func (q *ioQueue) push(e ioEvent, merge bool) {
	q.push2(e, merge, false)
}

func (q *ioQueue) push2(e ioEvent, merge, whole bool) {
	q.mu.Lock()
	if !whole && q.size+len(e.data) > pluginIOQueueBytes {
		q.dropped += len(e.data)
	} else {
		q.size += len(e.data)
		if n := len(q.events); merge && n > 0 && q.events[n-1].err == "" && e.err == "" && q.events[n-1].what == "" && e.what == "" {
			q.events[n-1].data = append(q.events[n-1].data, e.data...)
		} else {
			q.events = append(q.events, e)
		}
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *ioQueue) take() ([]ioEvent, int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	events, dropped := q.events, q.dropped
	q.events, q.size, q.dropped = nil, 0, 0
	return events, dropped
}

// NewPluginIO: no plugin may use anything until PluginSandbox grants it.
func NewPluginIO() *PluginIO {
	return &PluginIO{
		grants:   map[string]pluginIOGrant{},
		handles:  map[int]*ioHandle{},
		serial:   map[string]int{},
		reserved: map[int]bool{},
		sent:     map[int]time.Time{},
		own:      ownAddr,
		dialTCP:  dialTCPDefault,
	}
}

// ReserveUDPPorts: ports vCLU listens on, which no plugin socket may bind.
func (p *PluginIO) ReserveUDPPorts(ports ...int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, port := range ports {
		p.reserved[port] = true
	}
}

// sentTTL: how long a closed plugin socket's port still marks datagrams as the plugin's;
// they may wait in vCLU's socket after the plugin closed its own.
const sentTTL = 5 * time.Minute

// FromPlugin: a datagram sent by a plugin's socket on this machine, open or closed in the
// last minutes; vCLU's own listeners drop those, so a plugin's broadcast does not reach
// the vCLU it runs in.
func (p *PluginIO) FromPlugin(addr *net.UDPAddr) bool {
	if p == nil || addr == nil {
		return false
	}
	p.mu.Lock()
	at, ok := p.sent[addr.Port]
	open := false
	for _, h := range p.handles { // an open socket is the plugin's however long it waited
		if h.conn != nil && h.conn.LocalAddr().(*net.UDPAddr).Port == addr.Port {
			open = true
			break
		}
	}
	p.mu.Unlock()
	if !open && (!ok || time.Since(at) > sentTTL) {
		return false
	}
	ip, ok := netip.AddrFromSlice(addr.IP)
	return ok && p.own(ip.Unmap())
}

// markSent records a plugin socket's port as in use now; old marks go. Caller holds p.mu.
func (p *PluginIO) markSent(port int) {
	now := time.Now()
	for k, at := range p.sent {
		if now.Sub(at) > sentTTL {
			delete(p.sent, k)
		}
	}
	p.sent[port] = now
}

// ownAddr: an address of this machine, where vCLU itself listens.
func ownAddr(ip netip.Addr) bool {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if own, ok := netip.AddrFromSlice(n.IP); ok && own.Unmap() == ip {
				return true
			}
		}
	}
	return false
}

// isBroadcast: 255.255.255.255 or the broadcast address of a network this machine is on.
func isBroadcast(ip netip.Addr) bool {
	if ip == broadcastIP4 {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil || !ip.Is4() {
		return false
	}
	b := ip.As4()
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil || len(n.Mask) != 4 {
			continue
		}
		net4, last := n.IP.To4(), [4]byte{}
		for i := range last {
			last[i] = net4[i] | ^n.Mask[i]
		}
		if last == b {
			return true
		}
	}
	return false
}

// SetGrant: what the plugin may open. Called by PluginSandbox:create for every plugin; a
// handle the new grant does not allow closes.
func (p *PluginIO) SetGrant(plugin string, g pluginIOGrant) {
	p.mu.Lock()
	p.grants[plugin] = g
	var gone []*ioHandle
	for _, h := range p.handles {
		if h.plugin == plugin && !handleAllowed(p, g, h) {
			gone = append(gone, h)
		}
	}
	p.mu.Unlock()
	for _, h := range gone {
		p.close(h)
	}
}

// handleAllowed: g still allows the open handle. Caller holds p.mu.
func handleAllowed(p *PluginIO, g pluginIOGrant, h *ioHandle) bool {
	switch h.kind {
	case "tcp":
		return h.tcp == nil || p.tcpAllowed(g, h.tcp.remote) == nil
	case "udp":
		return h.perm == "" || (h.perm == "udp" && g.udp) || (h.perm == "grenton_raw" && g.grentonRaw)
	case "tftp":
		return g.tftp
	case "serial":
		return g.serial && (h.path == "" || slices.ContainsFunc(g.serialPorts, func(a string) bool { return realPath(a) == h.path }))
	}
	return true
}

// Release closes everything the plugin opened and forgets its grant.
func (p *PluginIO) Release(plugin string) {
	p.mu.Lock()
	delete(p.grants, plugin)
	var hs []*ioHandle
	for _, h := range p.handles {
		if h.plugin == plugin {
			hs = append(hs, h)
		}
	}
	p.mu.Unlock()
	for _, h := range hs {
		p.close(h)
	}
}

// CloseAll: on reload and shutdown, every handle of every plugin.
func (p *PluginIO) CloseAll() {
	p.mu.Lock()
	hs := make([]*ioHandle, 0, len(p.handles))
	for _, h := range p.handles {
		hs = append(hs, h)
	}
	p.grants = map[string]pluginIOGrant{}
	p.mu.Unlock()
	for _, h := range hs {
		p.close(h)
	}
}

func (p *PluginIO) close(h *ioHandle) {
	h.once.Do(func() {
		p.mu.Lock()
		close(h.done)
		conn, port, tc, wc := h.conn, h.port, h.tcp, h.ws
		code, reason := h.wsCode, h.wsReason
		if conn != nil {
			p.markSent(conn.LocalAddr().(*net.UDPAddr).Port)
		}
		delete(p.handles, h.id)
		if h.path != "" && p.serial[h.path] == h.id {
			delete(p.serial, h.path)
		}
		p.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		if port != nil {
			_ = port.Close()
		}
		if tc != nil {
			tc.close()
		}
		if wc != nil {
			if code == 0 {
				code, reason = 1001, "going away"
			}
			wc.close(code, reason)
		}
	})
}

func (h *ioHandle) closed() bool {
	select {
	case <-h.done:
		return true
	default:
		return false
	}
}

// add registers a handle if the plugin is allowed what check says and has room for one.
func (p *PluginIO) add(plugin, kind string, check func(pluginIOGrant) error) (*ioHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	g, ok := p.grants[plugin]
	if !ok {
		g = pluginIOGrant{}
	}
	if err := check(g); err != nil {
		return nil, err
	}
	n := 0
	for _, h := range p.handles {
		if h.plugin == plugin {
			n++
		}
	}
	if n >= pluginIOMaxHandles {
		return nil, fmt.Errorf("%s has %d sockets, ports and transfers open, the most a plugin may", plugin, n)
	}
	p.nextID++
	h := &ioHandle{id: p.nextID, plugin: plugin, kind: kind, done: make(chan struct{})}
	h.q.wake = make(chan struct{}, 1)
	p.handles[h.id] = h
	return h, nil
}

func (p *PluginIO) handle(id int, plugin string) (*ioHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.handles[id]
	if !ok || h.plugin != plugin {
		return nil, fmt.Errorf("closed")
	}
	return h, nil
}

func needs(allowed bool, plugin, name string) error {
	if !allowed {
		return fmt.Errorf("%s needs %q in its plugin.json permissions, allowed in the plugin's settings", plugin, name)
	}
	return nil
}

// checkTarget: a host on the house's network, not this machine, among the allowed ones.
func (p *PluginIO) checkTarget(ip netip.Addr, hosts []netip.Prefix) error {
	ip = ip.Unmap()
	if !ip.Is4() {
		return fmt.Errorf("refused %s: only IPv4 addresses", ip)
	}
	if p.own(ip) {
		return fmt.Errorf("refused %s: an address of this vCLU", ip)
	}
	if isBroadcast(ip) {
		return fmt.Errorf("refused %s: a broadcast address", ip)
	}
	if ip.IsMulticast() {
		return fmt.Errorf("refused %s: multicast", ip)
	}
	if !isLocalAddr(ip) && !onHostNetwork(ip) {
		return fmt.Errorf("refused %s: not on the local network", ip)
	}
	if hosts == nil {
		return nil
	}
	for _, h := range hosts {
		if h.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("refused %s: not among the hosts allowed in the plugin's settings", ip)
}

// pump hands what a handle received to Lua, in order, one callback at a time.
func (p *PluginIO) pump(h *ioHandle) {
	for {
		select {
		case <-h.done:
			return
		case <-h.q.wake:
		}
		events, dropped := h.q.take()
		if dropped > 0 {
			log.Printf("[PLUGINS] %s: %d bytes dropped, its callback is too slow", h.plugin, dropped)
		}
		for _, e := range events {
			if h.closed() || p.deliver == nil {
				return
			}
			if _, err := p.deliver("PluginIO", func(L *lua.LState) (string, error) {
				if h.closed() {
					return "", nil
				}
				return "", callIO(L, h, e)
			}); err != nil {
				log.Printf("[PLUGINS] %s: %v", h.plugin, err)
			}
		}
		// a transfer answers once; a port that failed stays closed
		if n := len(events); n > 0 && (h.kind == "tftp" || events[n-1].err != "") {
			p.close(h)
			return
		}
	}
}

// callIO calls __vclu_plugin_io(id, kind, data, ip|err, port), a global looked up each
// time, so a reload in between does no harm.
func callIO(L *lua.LState, h *ioHandle, e ioEvent) error {
	fn := L.GetGlobal("__vclu_plugin_io")
	if fn == lua.LNil {
		return nil
	}
	data := lua.LValue(lua.LNil)
	if e.data != nil {
		data = lua.LString(e.data)
	}
	if h.kind == "ws" { // (id, "ws", protocol | message | reason, "open" | "message" | "close", binary | code)
		if e.err != "" {
			return L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true},
				lua.LNumber(h.id), lua.LString(h.kind), lua.LString(e.err), lua.LString("close"), lua.LNumber(e.port))
		}
		return L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true},
			lua.LNumber(h.id), lua.LString(h.kind), data, lua.LString(e.what), lua.LBool(e.binary))
	}
	if h.kind == "tcp" { // (id, "tcp", bytes | reason, "connect" | "data" | "close")
		what := "data"
		switch {
		case e.what == "connect":
			what = "connect"
		case e.err != "":
			what, data = "close", lua.LString(e.err)
		}
		return L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true},
			lua.LNumber(h.id), lua.LString(h.kind), data, lua.LString(what), lua.LNumber(0))
	}
	second := lua.LValue(lua.LNil)
	switch {
	case e.err != "":
		second = lua.LString(e.err)
	case e.ip != "":
		second = lua.LString(e.ip)
	}
	return L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true},
		lua.LNumber(h.id), lua.LString(h.kind), data, second, lua.LNumber(e.port))
}

// --- UDP ---

// OpenUDP binds a clu socket for the plugin, port 0 for any free one.
func (p *PluginIO) OpenUDP(plugin string, port int) (int, error) {
	return p.openUDP(plugin, port, "grenton_raw")
}

// OpenDeviceUDP binds a udp socket: devices on the local network, not the CLU's ports.
func (p *PluginIO) OpenDeviceUDP(plugin string, port int) (int, error) {
	return p.openUDP(plugin, port, "udp")
}

func (p *PluginIO) openUDP(plugin string, port int, perm string) (int, error) {
	if port < 0 || port > 65535 {
		return 0, fmt.Errorf("port %d out of range", port)
	}
	// a plugin does not listen: its socket takes a free port and hears only answers
	if port != 0 {
		return 0, fmt.Errorf("port %d: a plugin socket takes a free port, it does not listen on a chosen one", port)
	}
	h, err := p.add(plugin, "udp", func(g pluginIOGrant) error {
		allowed, name := g.grentonRaw, "clu"
		if perm == "udp" {
			allowed, name = g.udp, "udp"
		}
		if err := needs(allowed, plugin, name); err != nil {
			return err
		}
		if p.reserved[port] {
			return fmt.Errorf("port %d is vCLU's own", port)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		p.close(h)
		return 0, err
	}
	p.mu.Lock()
	if h.closed() { // closed while opening (shutdown)
		p.mu.Unlock()
		_ = conn.Close()
		return 0, fmt.Errorf("closed")
	}
	h.conn = conn
	h.perm = perm
	h.peers, h.bcast = map[netip.Addr]bool{}, map[int]time.Time{}
	p.markSent(conn.LocalAddr().(*net.UDPAddr).Port)
	p.mu.Unlock()
	go p.pump(h)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				if !h.closed() {
					log.Printf("[PLUGINS] %s: UDP socket: %v", plugin, err)
					h.q.push(ioEvent{err: err.Error()}, false)
				}
				return
			}
			if !p.answer(h, from) {
				continue
			}
			h.q.push(ioEvent{data: bytes.Clone(buf[:n]), ip: from.IP.String(), port: from.Port}, false)
		}
	}()
	return h.id, nil
}

// SendUDP sends one datagram from the plugin's socket. A clu socket broadcasts only
// to the CLU port (discovery), where vCLU's own listener drops it (FromPlugin); a udp one
// only to the ports in plugin.json, and to no CLU port at all.
func (p *PluginIO) SendUDP(id int, plugin, host string, port int, data []byte) error {
	h, err := p.handle(id, plugin)
	if err != nil || h.conn == nil {
		return fmt.Errorf("socket closed")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Unmap().Is4() {
		return fmt.Errorf("%q is not an IPv4 address", host)
	}
	ip = ip.Unmap()
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d out of range", port)
	}
	p.mu.Lock()
	g, ownPort := p.grants[plugin], p.reserved[port]
	p.mu.Unlock()
	hosts := g.rawHosts
	if h.perm != "udp" && !g.grentonRaw {
		return needs(false, plugin, "clu")
	}
	if h.perm == "udp" {
		hosts = g.udpHosts
		if !g.udp {
			return needs(false, plugin, "udp")
		}
		if port == grentonPort || port == tftpPort {
			return fmt.Errorf("refused port %d: a CLU port, reached only with clu or tftp", port)
		}
		if ownPort {
			return fmt.Errorf("refused port %d: a port vclu listens on", port)
		}
		if isBroadcast(ip) && !slices.Contains(g.broadcastPorts, port) {
			return fmt.Errorf("refused broadcast to port %d: not in plugin.json's permissions.udp.broadcast", port)
		}
		if !isBroadcast(ip) {
			if err := p.checkTarget(ip, hosts); err != nil {
				return err
			}
			if !slices.Contains(g.udpPorts, port) {
				return fmt.Errorf("refused port %d: not in plugin.json's permissions.udp.ports", port)
			}
		}
	} else if isBroadcast(ip) && port != grentonPort {
		return fmt.Errorf("refused broadcast to port %d: only to the CLU port %d", port, grentonPort)
	}
	if isBroadcast(ip) {
		if hosts != nil && !(h.perm != "udp" && g.cluDiscovery) {
			return fmt.Errorf("refused broadcast: the plugin's settings allow only some hosts")
		}
	} else if err := p.checkTarget(ip, hosts); err != nil {
		return err
	}
	_, err = h.conn.WriteToUDP(data, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, uint16(port))))
	if err == nil {
		p.mu.Lock()
		p.markSent(h.conn.LocalAddr().(*net.UDPAddr).Port)
		if isBroadcast(ip) {
			h.bcast[port] = time.Now()
		} else if len(h.peers) < maxUDPPeers {
			h.peers[ip] = true
		}
		p.mu.Unlock()
	}
	return err
}

// maxUDPPeers bounds the addresses one socket remembers; past it only those it has answer.
const maxUDPPeers = 4096

// answer: a datagram from an address the socket sent to, or from the port it broadcast to on
// the local network; anything else is dropped, so a socket is not a listener.
func (p *PluginIO) answer(h *ioHandle, from *net.UDPAddr) bool {
	ip, ok := netip.AddrFromSlice(from.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	p.mu.Lock()
	defer p.mu.Unlock()
	if h.peers[ip] {
		return true
	}
	at, ok := h.bcast[from.Port]
	return ok && time.Since(at) < broadcastAnswers && (isLocalAddr(ip) || onHostNetwork(ip))
}

// broadcastAnswers: how long after a broadcast answers to it are heard.
const broadcastAnswers = time.Minute

// --- cipher ---

// cipherBytes reads a key or IV given raw or in base64 (as in a CLU's configuration).
func cipherBytes(s string, sizes ...int) ([]byte, bool) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		for _, n := range sizes {
			if len(b) == n {
				return b, true
			}
		}
	}
	for _, n := range sizes {
		if len(s) == n {
			return []byte(s), true
		}
	}
	return nil, false
}

// GrentonCipher encrypts or decrypts with the CLU cipher (AES-CBC, PKCS7).
func GrentonCipher(encrypt bool, key, iv string, data []byte) ([]byte, error) {
	k, ok := cipherBytes(key, 16, 24, 32)
	if !ok {
		return nil, fmt.Errorf("the key must be 16, 24 or 32 bytes, raw or in base64")
	}
	v, ok := cipherBytes(iv, 16)
	if !ok {
		return nil, fmt.Errorf("the IV must be 16 bytes, raw or in base64")
	}
	ck := cipher.NewCipherKey(k, v)
	if encrypt {
		return ck.Encrypt(data)
	}
	return ck.Decrypt(data)
}

// --- TFTP ---

var errTransferClosed = fmt.Errorf("closed")

// limitedBuffer takes a received file up to pluginTFTPMaxBytes; closing the handle stops it.
type limitedBuffer struct {
	bytes.Buffer
	done <-chan struct{}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	select {
	case <-b.done:
		return 0, errTransferClosed
	default:
	}
	if b.Len()+len(p) > pluginTFTPMaxBytes {
		return 0, fmt.Errorf("file larger than %d bytes", pluginTFTPMaxBytes)
	}
	return b.Buffer.Write(p)
}

// stoppableReader gives a file to send until the handle closes.
type stoppableReader struct {
	r    io.Reader
	done <-chan struct{}
}

func (s stoppableReader) Read(p []byte) (int, error) {
	select {
	case <-s.done:
		return 0, errTransferClosed
	default:
	}
	return s.r.Read(p)
}

// TFTP gets (data nil) or puts a file on a device off the run loop; the answer comes
// as one event of the returned handle.
func (p *PluginIO) TFTP(plugin, host string, port int, name string, data []byte, put bool) (int, error) {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return 0, fmt.Errorf("%q is not an IP address", host)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d out of range", port)
	}
	if put && len(data) > pluginTFTPMaxBytes {
		return 0, fmt.Errorf("file larger than %d bytes", pluginTFTPMaxBytes)
	}
	h, err := p.add(plugin, "tftp", func(g pluginIOGrant) error {
		if err := needs(g.tftp, plugin, "tftp"); err != nil {
			return err
		}
		return p.checkTarget(ip, g.tftpHosts)
	})
	if err != nil {
		return 0, err
	}
	go p.pump(h)
	go func() {
		got, err := tftpTransfer(netip.AddrPortFrom(ip.Unmap(), uint16(port)).String(), name, data, put, h.done)
		e := ioEvent{data: got}
		if err != nil {
			e = ioEvent{err: err.Error()}
		} else if got == nil {
			e.data = []byte{} // put, or an empty file
		}
		h.q.push2(e, false, true)
	}()
	return h.id, nil
}

func tftpTransfer(addr, name string, data []byte, put bool, done <-chan struct{}) ([]byte, error) {
	c, err := tftp.NewClient(addr)
	if err != nil {
		return nil, err
	}
	c.SetTimeout(3 * time.Second)
	c.SetRetries(3)
	if put {
		rf, err := c.Send(name, "octet")
		if err != nil {
			return nil, err
		}
		_, err = rf.ReadFrom(stoppableReader{bytes.NewReader(data), done})
		return nil, err
	}
	wt, err := c.Receive(name, "octet")
	if err != nil {
		return nil, err
	}
	buf := limitedBuffer{done: done}
	if _, err := wt.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- Lua ---

func hostsArg(L *lua.LState, n int, who string) []netip.Prefix {
	t, ok := L.Get(n).(*lua.LTable)
	if !ok {
		return nil
	}
	var names []string
	t.ForEach(func(_, v lua.LValue) { names = append(names, v.String()) })
	parsed, err := plugins.ParseHosts(names)
	if err != nil {
		log.Printf("[PLUGINS] %s: %v; no host allowed", who, err)
		return []netip.Prefix{}
	}
	return parsed
}

// portsArg: a Lua list of port numbers.
func portsArg(L *lua.LState, n int) []int {
	var out []int
	if t, ok := L.Get(n).(*lua.LTable); ok {
		t.ForEach(func(_, v lua.LValue) {
			if n, ok := v.(lua.LNumber); ok {
				out = append(out, int(n))
			}
		})
	}
	return out
}

func pushResult(L *lua.LState, v lua.LValue, err error) int {
	if err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}
	L.Push(v)
	L.Push(lua.LNil)
	return 2
}

func registerPluginIOFunctions(L *lua.LState, p *PluginIO) {
	// __go_pluginio_grant(pluginId, clu, tftp, serial, cluHosts, tftpHosts, serialPorts,
	//   udp, udpHosts, broadcastPorts, tcp, tcpHosts, tcpPorts, lanPorts (nil any), udpPorts, cluDiscovery)
	L.SetGlobal("__go_pluginio_grant", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		g := pluginIOGrant{grentonRaw: L.ToBool(2), tftp: L.ToBool(3), serial: L.ToBool(4), udp: L.ToBool(8)}
		g.rawHosts = hostsArg(L, 5, id)
		g.tftpHosts = hostsArg(L, 6, id)
		if t, ok := L.Get(7).(*lua.LTable); ok {
			t.ForEach(func(_, v lua.LValue) { g.serialPorts = append(g.serialPorts, v.String()) })
		}
		g.udpHosts = hostsArg(L, 9, id)
		g.broadcastPorts = portsArg(L, 10)
		g.tcp = L.ToBool(11)
		g.tcpHosts = hostsArg(L, 12, id)
		g.tcpPorts = portsArg(L, 13)
		if _, ok := L.Get(14).(*lua.LTable); ok {
			g.wsLANPorts = append([]int{}, portsArg(L, 14)...)
		}
		g.udpPorts = portsArg(L, 15)
		g.cluDiscovery = L.ToBool(16)
		p.SetGrant(id, g)
		return 0
	}))

	// __go_plugin_hosts_allow(hosts, host) -> bool: host is an IP address within the list of
	// addresses and prefixes (a grant's hosts), for checks Lua makes before a request
	L.SetGlobal("__go_plugin_hosts_allow", L.NewFunction(func(L *lua.LState) int {
		var names []string
		if t, ok := L.Get(1).(*lua.LTable); ok {
			t.ForEach(func(_, v lua.LValue) { names = append(names, v.String()) })
		}
		allowed, err := plugins.ParseHosts(names)
		ip, perr := netip.ParseAddr(L.CheckString(2))
		ok := err == nil && perr == nil && slices.ContainsFunc(allowed, func(n netip.Prefix) bool { return n.Contains(ip.Unmap()) })
		L.Push(lua.LBool(ok))
		return 1
	}))

	// __go_pluginio_release(pluginId): on the plugin's cleanup
	L.SetGlobal("__go_pluginio_release", L.NewFunction(func(L *lua.LState) int {
		p.Release(L.CheckString(1))
		return 0
	}))

	// __go_pluginio_close(id, pluginId)
	L.SetGlobal("__go_pluginio_close", L.NewFunction(func(L *lua.LState) int {
		if h, err := p.handle(L.CheckInt(1), L.CheckString(2)); err == nil {
			p.close(h)
		}
		return 0
	}))

	// __go_pluginio_udp_open(pluginId, port, "udp"|"grenton_raw") -> id | nil, err
	L.SetGlobal("__go_pluginio_udp_open", L.NewFunction(func(L *lua.LState) int {
		open := p.OpenUDP
		if L.OptString(3, "grenton_raw") == "udp" {
			open = p.OpenDeviceUDP
		}
		id, err := open(L.CheckString(1), L.OptInt(2, 0))
		return pushResult(L, lua.LNumber(id), err)
	}))

	// __go_pluginio_udp_send(id, pluginId, ip, port, bytes) -> true | nil, err
	L.SetGlobal("__go_pluginio_udp_send", L.NewFunction(func(L *lua.LState) int {
		err := p.SendUDP(L.CheckInt(1), L.CheckString(2), L.CheckString(3), L.CheckInt(4), []byte(L.CheckString(5)))
		return pushResult(L, lua.LTrue, err)
	}))

	// __go_pluginio_tcp_open(pluginId, ip, port, timeoutSeconds) -> id | nil, err
	L.SetGlobal("__go_pluginio_tcp_open", L.NewFunction(func(L *lua.LState) int {
		timeout := time.Duration(float64(L.OptNumber(4, 0)) * float64(time.Second))
		id, err := p.OpenTCP(L.CheckString(1), L.CheckString(2), L.CheckInt(3), timeout)
		return pushResult(L, lua.LNumber(id), err)
	}))

	// __go_pluginio_tcp_send(id, pluginId, bytes) -> true | nil, err
	L.SetGlobal("__go_pluginio_tcp_send", L.NewFunction(func(L *lua.LState) int {
		err := p.SendTCP(L.CheckInt(1), L.CheckString(2), []byte(L.CheckString(3)))
		return pushResult(L, lua.LTrue, err)
	}))

	// __go_pluginio_cipher(encrypt, key, iv, bytes) -> bytes | nil, err
	L.SetGlobal("__go_pluginio_cipher", L.NewFunction(func(L *lua.LState) int {
		out, err := GrentonCipher(L.ToBool(1), L.CheckString(2), L.CheckString(3), []byte(L.CheckString(4)))
		return pushResult(L, lua.LString(out), err)
	}))

	// __go_pluginio_tftp(pluginId, ip, port, name, bytes|nil) -> id | nil, err; nil bytes: get
	L.SetGlobal("__go_pluginio_tftp", L.NewFunction(func(L *lua.LState) int {
		var data []byte
		put := L.Get(5) != lua.LNil
		if put {
			data = []byte(L.CheckString(5))
		}
		id, err := p.TFTP(L.CheckString(1), L.CheckString(2), L.CheckInt(3), L.CheckString(4), data, put)
		return pushResult(L, lua.LNumber(id), err)
	}))

	// __go_pluginio_serial_open(pluginId, path, baud, dataBits, parity, stopBits) -> id | nil, err
	L.SetGlobal("__go_pluginio_serial_open", L.NewFunction(func(L *lua.LState) int {
		id, err := p.OpenSerial(L.CheckString(1), L.CheckString(2), serialSettings{
			baud: L.OptInt(3, 9600), data: L.OptInt(4, 8), parity: L.OptString(5, "none"), stop: L.OptInt(6, 1),
		})
		return pushResult(L, lua.LNumber(id), err)
	}))

	// __go_pluginio_serial_write(id, pluginId, bytes) -> true | nil, err
	L.SetGlobal("__go_pluginio_serial_write", L.NewFunction(func(L *lua.LState) int {
		err := p.WriteSerial(L.CheckInt(1), L.CheckString(2), []byte(L.CheckString(3)))
		return pushResult(L, lua.LTrue, err)
	}))
}
