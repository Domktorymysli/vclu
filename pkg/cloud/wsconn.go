package cloud

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsConn is a WebSocket as a net.Conn for yamux: binary messages carry the
// byte stream. It keeps the first read error, which carries the close code
// the cloud sent (*websocket.CloseError).
type wsConn struct {
	ws *websocket.Conn
	r  io.Reader

	wmu sync.Mutex

	emu sync.Mutex
	err error
}

func newWSConn(ws *websocket.Conn) *wsConn { return &wsConn{ws: ws} }

func (c *wsConn) Read(b []byte) (int, error) {
	for {
		if c.r == nil {
			typ, r, err := c.ws.NextReader()
			if err != nil {
				c.setErr(err)
				return 0, err
			}
			if typ != websocket.BinaryMessage {
				continue
			}
			c.r = r
		}
		n, err := c.r.Read(b)
		if err == io.EOF {
			c.r = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		return n, err
	}
}

func (c *wsConn) Write(b []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// closeWith sends a close frame with code, then closes the connection.
func (c *wsConn) closeWith(code int, reason string) error {
	c.wmu.Lock()
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	c.wmu.Unlock()
	return c.ws.Close()
}

func (c *wsConn) Close() error { return c.closeWith(websocket.CloseNormalClosure, "") }

func (c *wsConn) setErr(err error) {
	c.emu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.emu.Unlock()
}

// closeCode returns the code of the close frame the cloud sent, or 0.
func (c *wsConn) closeCode() int {
	c.emu.Lock()
	defer c.emu.Unlock()
	if ce, ok := c.err.(*websocket.CloseError); ok {
		return ce.Code
	}
	return 0
}

func (c *wsConn) LocalAddr() net.Addr                { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr               { return c.ws.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error      { return c.ws.NetConn().SetDeadline(t) }
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }
