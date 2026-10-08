package satel

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// ErrBusy is returned when the ETHM-1 module already has an integration
// client connected (it allows only one at a time).
var ErrBusy = errors.New("satel: ETHM-1 busy (another integration client connected)")

// connection is a single TCP session with the ETHM-1 module, optionally
// encrypted. The protocol is strict lockstep: one request, one response.
type connection struct {
	tcp     net.Conn
	reader  *bufio.Reader
	enc     *encrypter
	dec     frameDecoder
	pending []Frame
	timeout time.Duration
	skipped int // frames discarded by execute while waiting for a reply (protocol desync signal)
}

// dialPanel connects to the ETHM-1 module, performs the busy sniff
// (any data arriving right after connect means another client holds the
// integration session) and returns a ready connection.
func dialPanel(host string, port int, encryptionKey string, timeout time.Duration) (*connection, error) {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	tcp, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}

	c := &connection{
		tcp:     tcp,
		reader:  bufio.NewReader(tcp),
		timeout: timeout,
	}
	if encryptionKey != "" {
		enc, err := newEncrypter(encryptionKey)
		if err != nil {
			tcp.Close()
			return nil, err
		}
		c.enc = enc
	}

	// Busy sniff: a healthy connection stays silent until we send something.
	tcp.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	sniff := make([]byte, 64)
	n, _ := c.reader.Read(sniff)
	if n > 0 {
		if c.enc != nil || bytes.Contains(sniff[:n], []byte("Busy")) {
			tcp.Close()
			return nil, ErrBusy
		}
	}
	tcp.SetReadDeadline(time.Time{})

	return c, nil
}

// close terminates the TCP session.
func (c *connection) close() {
	if c.tcp != nil {
		c.tcp.Close()
	}
}

// sendFrame encodes and writes one frame (wrapping it in an encrypted PDU
// when encryption is active).
func (c *connection) sendFrame(cmd byte, data []byte) error {
	frame := EncodeFrame(cmd, data)
	if c.enc != nil {
		pdu, err := c.enc.wrap(frame)
		if err != nil {
			return err
		}
		frame = pdu
	}
	c.tcp.SetWriteDeadline(time.Now().Add(c.timeout))
	_, err := c.tcp.Write(frame)
	return err
}

// readFrame returns the next decoded frame, reading from the socket as needed.
func (c *connection) readFrame() (Frame, error) {
	for {
		if len(c.pending) > 0 {
			f := c.pending[0]
			c.pending = c.pending[1:]
			return f, nil
		}

		c.tcp.SetReadDeadline(time.Now().Add(c.timeout))
		var raw []byte
		if c.enc != nil {
			// Encrypted stream: one length byte, then exactly n PDU bytes.
			n, err := c.reader.ReadByte()
			if err != nil {
				return Frame{}, err
			}
			pdu := make([]byte, int(n))
			if _, err := io.ReadFull(c.reader, pdu); err != nil {
				return Frame{}, err
			}
			raw, err = c.enc.unwrap(pdu)
			if err != nil {
				return Frame{}, err
			}
		} else {
			buf := make([]byte, 1024)
			n, err := c.reader.Read(buf)
			if err != nil {
				return Frame{}, err
			}
			raw = buf[:n]
		}

		frames, err := c.dec.Feed(raw)
		if err != nil {
			return Frame{}, err
		}
		c.pending = append(c.pending, frames...)
	}
}

// execute sends a command and waits for its response: a frame with the same
// command byte, or a 0xEF result frame. Non-matching frames (pushed state in
// monitoring mode) are skipped.
func (c *connection) execute(cmd byte, data []byte) (Frame, error) {
	if err := c.sendFrame(cmd, data); err != nil {
		return Frame{}, fmt.Errorf("satel: sending %s: %w", describeCommand(cmd, data), err)
	}
	deadline := time.Now().Add(c.timeout)
	for time.Now().Before(deadline) {
		f, err := c.readFrame()
		if err != nil {
			// Name the command, so a stalled panel shows what it choked on
			return Frame{}, fmt.Errorf("satel: no response to %s: %w", describeCommand(cmd, data), err)
		}
		if f.Cmd == cmd || f.Cmd == cmdResult {
			return f, nil
		}
		c.skipped++
	}
	return Frame{}, fmt.Errorf("satel: timeout waiting for response to 0x%02X", cmd)
}

// describeCommand names a command for error messages. Data is shown only for short
// queries (device number etc.): control commands start with the user code, which
// must never reach logs or events.
func describeCommand(cmd byte, data []byte) string {
	if len(data) == 0 || len(data) > 2 {
		return fmt.Sprintf("0x%02X", cmd)
	}
	return fmt.Sprintf("0x%02X %X", cmd, data)
}
