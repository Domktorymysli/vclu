package lua

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	lua "github.com/yuin/gopher-lua"
)

const (
	wsConnectTimeout    = 10 * time.Second
	wsConnectTimeoutMax = 30 * time.Second
	wsWriteTimeout      = 10 * time.Second
	wsMessageMax        = 1 << 20 // one message from the server
	wsSendQueueBytes    = 1 << 20 // unsent bytes of one connection
)

// wsConn: a plugin's WebSocket; sends queue here and a writer sends them, as for tcp.
type wsConn struct {
	url    *url.URL
	policy func() dialPolicy // where the plugin may connect now
	mu     sync.Mutex
	conn   *websocket.Conn // nil until open
	remote netip.Addr
	out    []wsOut
	size   int
	wake   chan struct{}
	ended  bool
	cancel context.CancelFunc
}

type wsOut struct {
	data   []byte
	binary bool
}

// close ends the connection with a close frame, sent without holding up the caller.
func (c *wsConn) close(code int, reason string) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	c.cancel()
	if conn != nil {
		go func() {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
			_ = conn.Close()
		}()
	}
}

// end reports, once, how the connection ended.
func (c *wsConn) end(h *ioHandle, code int, why string) {
	c.mu.Lock()
	first := !c.ended
	c.ended = true
	c.mu.Unlock()
	if first && !h.closed() {
		h.q.push(ioEvent{err: why, port: code}, false)
	}
}

// still: the connection's host and address are allowed as when it opened.
func (c *wsConn) still() error {
	p := c.policy()
	if err := p.checkURL(c.url); err != nil {
		return err
	}
	return p.allows(c.url.Hostname(), c.remote)
}

// wsHeaderSetByLibrary: headers the handshake sets itself.
func wsHeaderSetByLibrary(name string) bool {
	n := strings.ToLower(name)
	return n == "host" || n == "upgrade" || n == "connection" || strings.HasPrefix(n, "sec-websocket-")
}

// OpenWS connects the plugin to a WebSocket server where its HTTP may go (the internet
// host list, lan and its hosts), not to this vclu; it opens, or fails, through the queue.
func (p *PluginIO) OpenWS(plugin, rawURL string, headers map[string]string, protocols []string, timeout time.Duration, policy func() dialPolicy) (int, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" {
		return 0, fmt.Errorf("%q is not a ws:// or wss:// URL", rawURL)
	}
	if timeout <= 0 {
		timeout = wsConnectTimeout
	}
	if timeout > wsConnectTimeoutMax {
		return 0, fmt.Errorf("timeout %s: at most %s", timeout, wsConnectTimeoutMax)
	}
	header := http.Header{}
	for k, v := range headers {
		if wsHeaderSetByLibrary(k) {
			return 0, fmt.Errorf("header %s is set by the WebSocket handshake", k)
		}
		header.Set(k, v)
	}
	pol := policy()
	if err := pol.checkURL(u); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	wc := &wsConn{url: u, policy: policy, wake: make(chan struct{}, 1), cancel: cancel}
	h, err := p.add(plugin, "ws", func(pluginIOGrant) error { return nil })
	if err != nil {
		cancel()
		return 0, err
	}
	p.mu.Lock()
	h.ws = wc
	p.mu.Unlock()
	go p.pump(h)
	go func() {
		var remote netip.Addr
		d := websocket.Dialer{
			NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				c, err := pol.dialContext(ctx, network, addr)
				if err == nil {
					if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
						remote, _ = netip.AddrFromSlice(a.IP)
					}
				}
				return c, err
			},
			HandshakeTimeout: timeout,
			Subprotocols:     protocols,
		}
		dctx, stop := context.WithTimeout(ctx, timeout)
		conn, resp, err := d.DialContext(dctx, u.String(), header)
		stop()
		if err != nil {
			why := "connect: " + err.Error()
			if resp != nil {
				why = fmt.Sprintf("connect: the server answered HTTP %d", resp.StatusCode)
			}
			wc.end(h, websocket.CloseAbnormalClosure, why)
			return
		}
		conn.SetReadLimit(wsMessageMax)
		wc.mu.Lock()
		if h.closed() {
			wc.mu.Unlock()
			_ = conn.Close()
			return
		}
		wc.conn, wc.remote = conn, remote.Unmap()
		wc.mu.Unlock()
		h.q.push(ioEvent{what: "open", data: []byte(conn.Subprotocol())}, false)
		go p.writeWS(h, wc, conn)
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				code, why := websocket.CloseAbnormalClosure, err.Error()
				var ce *websocket.CloseError
				switch {
				case errors.As(err, &ce):
					code, why = ce.Code, ce.Text
				case errors.Is(err, websocket.ErrReadLimit):
					code, why = websocket.CloseMessageTooBig, fmt.Sprintf("a message over %d bytes", wsMessageMax)
					_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, ""), time.Now().Add(time.Second))
					_ = conn.Close()
				}
				if !h.closed() {
					log.Printf("[PLUGINS] %s: WebSocket %s: %d %s", plugin, u.Redacted(), code, why)
				}
				wc.end(h, code, why)
				return
			}
			h.q.push(ioEvent{what: "message", data: data, binary: mt == websocket.BinaryMessage}, false)
		}
	}()
	return h.id, nil
}

// writeWS sends what the plugin queued, each message within wsWriteTimeout.
func (p *PluginIO) writeWS(h *ioHandle, wc *wsConn, conn *websocket.Conn) {
	for {
		select {
		case <-h.done:
			return
		case <-wc.wake:
		}
		wc.mu.Lock()
		out := wc.out
		wc.out, wc.size = nil, 0
		wc.mu.Unlock()
		for _, m := range out {
			mt := websocket.TextMessage
			if m.binary {
				mt = websocket.BinaryMessage
			}
			_ = conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			if err := conn.WriteMessage(mt, m.data); err != nil {
				wc.end(h, websocket.CloseAbnormalClosure, "write: "+err.Error())
				_ = conn.Close()
				return
			}
		}
	}
}

// SendWS queues a message, refused before it is open, after it ended, past wsSendQueueBytes
// unsent, or once the plugin may no longer reach the server (the connection then closes).
func (p *PluginIO) SendWS(id int, plugin string, data []byte, binary bool) error {
	h, err := p.handle(id, plugin)
	if err != nil || h.ws == nil {
		return fmt.Errorf("connection closed")
	}
	wc := h.ws
	wc.mu.Lock()
	open := wc.conn != nil
	wc.mu.Unlock()
	if open {
		if err := wc.still(); err != nil {
			p.close(h)
			return err
		}
	}
	wc.mu.Lock()
	defer wc.mu.Unlock()
	switch {
	case wc.ended:
		return fmt.Errorf("connection closed")
	case wc.conn == nil:
		return fmt.Errorf("not open yet: send after onOpen")
	case wc.size+len(data) > wsSendQueueBytes:
		return fmt.Errorf("refused: %d bytes not sent yet, the server does not take them", wc.size)
	}
	wc.out = append(wc.out, wsOut{data: bytes.Clone(data), binary: binary})
	wc.size += len(data)
	select {
	case wc.wake <- struct{}{}:
	default:
	}
	return nil
}

// CloseWS closes the plugin's WebSocket with a code (1000, or 3000-4999) and a reason.
func (p *PluginIO) CloseWS(id int, plugin string, code int, reason string) error {
	if code != websocket.CloseNormalClosure && (code < 3000 || code > 4999) {
		return fmt.Errorf("close code %d: 1000 or 3000-4999", code)
	}
	if len(reason) > 123 {
		return fmt.Errorf("close reason: at most 123 bytes")
	}
	h, err := p.handle(id, plugin)
	if err != nil || h.ws == nil {
		return nil
	}
	p.mu.Lock()
	h.wsCode, h.wsReason = code, reason
	p.mu.Unlock()
	p.close(h)
	return nil
}

// recheckWS closes the plugin's open WebSockets its lan or internet no longer allows.
func (p *PluginIO) recheckWS(plugin string) {
	p.mu.Lock()
	var hs []*ioHandle
	for _, h := range p.handles {
		if h.plugin == plugin && h.ws != nil {
			hs = append(hs, h)
		}
	}
	p.mu.Unlock()
	for _, h := range hs {
		h.ws.mu.Lock()
		open := h.ws.conn != nil
		h.ws.mu.Unlock()
		if !open {
			continue
		}
		if err := h.ws.still(); err != nil {
			log.Printf("[PLUGINS] %s: WebSocket %s closed: %v", plugin, h.ws.url.Redacted(), err)
			p.mu.Lock()
			h.wsCode, h.wsReason = websocket.ClosePolicyViolation, "not allowed"
			p.mu.Unlock()
			p.close(h)
		}
	}
}

func registerPluginWSFunctions(L *lua.LState, p *PluginIO, hm *HTTPManager) {
	hm.SetOnPolicy(p.recheckWS)
	// __go_pluginio_ws_open(pluginId, url, headers, protocols, timeoutSeconds) -> id | nil, err
	L.SetGlobal("__go_pluginio_ws_open", L.NewFunction(func(L *lua.LState) int {
		plugin := L.CheckString(1)
		headers := map[string]string{}
		if t, ok := L.Get(3).(*lua.LTable); ok {
			t.ForEach(func(k, v lua.LValue) { headers[k.String()] = v.String() })
		}
		var protocols []string
		if t, ok := L.Get(4).(*lua.LTable); ok {
			t.ForEach(func(_, v lua.LValue) { protocols = append(protocols, v.String()) })
		}
		timeout := time.Duration(float64(L.OptNumber(5, 0)) * float64(time.Second))
		id, err := p.OpenWS(plugin, L.CheckString(2), headers, protocols, timeout, func() dialPolicy {
			pol := hm.policy(plugin)
			p.mu.Lock()
			pol.own = p.own
			p.mu.Unlock()
			return pol
		})
		return pushResult(L, lua.LNumber(id), err)
	}))

	// __go_pluginio_ws_send(id, pluginId, data, binary) -> true | nil, err
	L.SetGlobal("__go_pluginio_ws_send", L.NewFunction(func(L *lua.LState) int {
		err := p.SendWS(L.CheckInt(1), L.CheckString(2), []byte(L.CheckString(3)), L.ToBool(4))
		return pushResult(L, lua.LTrue, err)
	}))

	// __go_pluginio_ws_close(id, pluginId, code, reason) -> true | nil, err
	L.SetGlobal("__go_pluginio_ws_close", L.NewFunction(func(L *lua.LState) int {
		err := p.CloseWS(L.CheckInt(1), L.CheckString(2), L.OptInt(3, websocket.CloseNormalClosure), L.OptString(4, ""))
		return pushResult(L, lua.LTrue, err)
	}))
}
