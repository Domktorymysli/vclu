package lua

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"
)

const (
	tcpConnectTimeout    = 5 * time.Second
	tcpConnectTimeoutMax = 30 * time.Second
	tcpWriteTimeout      = 10 * time.Second
	tcpSendQueueBytes    = 1 << 20 // unsent bytes of one connection
)

// tcpPortOwners: ports of protocols with a permission of their own, on any host.
var tcpPortOwners = map[int]string{grentonPort: "clu", tftpPort: "tftp", 502: "modbus", 1883: "mqtt", 8883: "mqtt"}

// tcpConn: a plugin's connection; sends queue here and a writer sends them, so a slow
// device does not hold the Lua loop.
type tcpConn struct {
	remote netip.AddrPort
	mu     sync.Mutex
	conn   net.Conn // nil until connected
	out    [][]byte
	size   int
	wake   chan struct{}
	ended  bool // the first end (read, write, device) is the one reported
	cancel context.CancelFunc
}

func (c *tcpConn) close() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	c.cancel()
	if conn != nil {
		_ = conn.Close()
	}
}

// connected: up and not ended.
func (c *tcpConn) connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.ended
}

// end reports why the connection ended, once.
func (c *tcpConn) end(h *ioHandle, why string) {
	c.mu.Lock()
	first := !c.ended
	c.ended = true
	c.mu.Unlock()
	if first && !h.closed() {
		h.q.push(ioEvent{err: why}, false)
	}
}

// dialTCP connects; tests replace it.
var dialTCPDefault = func(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp4", addr)
}

// SetTCPTaken: addresses a plugin's tcp may not reach because vclu uses them under another
// permission; read at each connect.
func (p *PluginIO) SetTCPTaken(f func() map[netip.AddrPort]string) {
	p.mu.Lock()
	p.tcpTaken = f
	p.mu.Unlock()
}

// tcpAllowed: the grant lets the plugin reach to. Caller may hold p.mu.
func (p *PluginIO) tcpAllowed(g pluginIOGrant, to netip.AddrPort) error {
	if !g.tcp {
		return fmt.Errorf("needs \"tcp\" in its plugin.json permissions, allowed in the plugin's settings")
	}
	if perm, ok := tcpPortOwners[int(to.Port())]; ok {
		return fmt.Errorf("refused port %d: it belongs to the %s permission", to.Port(), perm)
	}
	if !slices.Contains(g.tcpPorts, int(to.Port())) {
		return fmt.Errorf("refused port %d: not in plugin.json's \"tcp\": {\"ports\"}", to.Port())
	}
	return p.checkTarget(to.Addr(), g.tcpHosts)
}

// OpenTCP connects the plugin to a device on the local network; the connection comes up,
// or fails, through the handle's queue.
func (p *PluginIO) OpenTCP(plugin, host string, port int, timeout time.Duration) (int, error) {
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.Unmap().Is4() {
		return 0, fmt.Errorf("%q is not an IPv4 address", host)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d out of range", port)
	}
	if timeout <= 0 {
		timeout = tcpConnectTimeout
	}
	if timeout > tcpConnectTimeoutMax {
		return 0, fmt.Errorf("timeout %s: at most %s", timeout, tcpConnectTimeoutMax)
	}
	to := netip.AddrPortFrom(ip.Unmap(), uint16(port))
	p.mu.Lock()
	taken := p.tcpTaken
	p.mu.Unlock()
	if taken != nil {
		if perm, ok := taken()[to]; ok {
			return 0, fmt.Errorf("refused %s: vclu uses it with the %s permission", to, perm)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	tc := &tcpConn{remote: to, wake: make(chan struct{}, 1), cancel: cancel}
	h, err := p.add(plugin, "tcp", func(g pluginIOGrant) error {
		if err := p.tcpAllowed(g, to); err != nil {
			if !g.tcp {
				return needs(false, plugin, "tcp")
			}
			return err
		}
		return nil
	})
	if err != nil {
		cancel()
		return 0, err
	}
	p.mu.Lock()
	h.tcp = tc
	p.mu.Unlock()
	go p.pump(h)
	go func() {
		dctx, stop := context.WithTimeout(ctx, timeout)
		conn, err := p.dialTCP(dctx, to.String())
		stop()
		if err != nil {
			tc.end(h, "connect: "+err.Error())
			return
		}
		tc.mu.Lock()
		if h.closed() {
			tc.mu.Unlock()
			_ = conn.Close()
			return
		}
		tc.conn = conn
		tc.mu.Unlock()
		h.q.push(ioEvent{what: "connect"}, false)
		go p.writeTCP(h, tc, conn)
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				h.q.push(ioEvent{data: bytes.Clone(buf[:n])}, true)
			}
			if err != nil {
				why := err.Error()
				if errors.Is(err, io.EOF) {
					why = "closed by the device"
				}
				if !h.closed() {
					log.Printf("[PLUGINS] %s: TCP %s: %s", plugin, to, why)
				}
				tc.end(h, why)
				return
			}
		}
	}()
	return h.id, nil
}

// writeTCP sends what the plugin queued, each write within tcpWriteTimeout.
func (p *PluginIO) writeTCP(h *ioHandle, tc *tcpConn, conn net.Conn) {
	for {
		select {
		case <-h.done:
			return
		case <-tc.wake:
		}
		tc.mu.Lock()
		out := tc.out
		tc.out, tc.size = nil, 0
		tc.mu.Unlock()
		for _, b := range out {
			_ = conn.SetWriteDeadline(time.Now().Add(tcpWriteTimeout))
			if _, err := conn.Write(b); err != nil {
				tc.end(h, "write: "+err.Error())
				_ = conn.Close()
				return
			}
		}
	}
}

// SendTCP queues bytes on the plugin's connection, refused before it is up, after it ended,
// past tcpSendQueueBytes unsent, or once the grant no longer allows it.
func (p *PluginIO) SendTCP(id int, plugin string, data []byte) error {
	h, err := p.handle(id, plugin)
	if err != nil || h.tcp == nil {
		return fmt.Errorf("connection closed")
	}
	p.mu.Lock()
	g := p.grants[plugin]
	err = p.tcpAllowed(g, h.tcp.remote)
	p.mu.Unlock()
	if err != nil {
		p.close(h)
		if !g.tcp {
			return needs(false, plugin, "tcp")
		}
		return err
	}
	tc := h.tcp
	tc.mu.Lock()
	defer tc.mu.Unlock()
	switch {
	case tc.ended:
		return fmt.Errorf("connection closed")
	case tc.conn == nil:
		return fmt.Errorf("not connected yet: send after onConnect")
	case tc.size+len(data) > tcpSendQueueBytes:
		return fmt.Errorf("refused: %d bytes not sent yet, the device does not take them", tc.size)
	}
	tc.out = append(tc.out, bytes.Clone(data))
	tc.size += len(data)
	select {
	case tc.wake <- struct{}{}:
	default:
	}
	return nil
}
