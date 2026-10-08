package lua

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeModbusServer answers MBAP frames with the supplied handler.
// The handler receives (unit, pdu) and returns the response PDU.
func fakeModbusServer(t *testing.T, handler func(unit byte, pdu []byte) []byte) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					header := make([]byte, 6)
					if _, err := io.ReadFull(c, header); err != nil {
						return
					}
					length := int(binary.BigEndian.Uint16(header[4:]))
					body := make([]byte, length)
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					resp := handler(body[0], body[1:])
					if resp == nil {
						continue // simulate a silent slave
					}
					out := make([]byte, 7+len(resp))
					copy(out[0:], header[0:4])
					binary.BigEndian.PutUint16(out[4:], uint16(len(resp)+1))
					out[6] = body[0]
					copy(out[7:], resp)
					if _, err := c.Write(out); err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), func() { ln.Close(); close(done) }
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split %q: %v", addr, err)
	}
	var port int
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}
	return host, port
}

func TestModbusReadInputRegisters(t *testing.T) {
	// SDM120 voltage: 242.34 V encoded as 0x4372 0x55DB (captured from a real meter).
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		if pdu[0] != fcReadInput {
			t.Errorf("expected FC04, got %d", pdu[0])
		}
		return []byte{fcReadInput, 4, 0x43, 0x72, 0x55, 0xDB}
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	regs, err := mm.Request(host, port, 2, fcReadInput, 0x0000, 2, nil, 1000)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if len(regs) != 2 {
		t.Fatalf("expected 2 registers, got %d", len(regs))
	}
	if regs[0] != 0x4372 || regs[1] != 0x55DB {
		t.Errorf("got registers %#v, want [0x4372 0x55DB]", regs)
	}
}

func TestModbusException(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadHolding | 0x80, 0x02} // illegal data address
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	_, err := mm.Request(host, port, 1, fcReadHolding, 0x9999, 1, nil, 1000)
	if err == nil {
		t.Fatal("expected an exception error")
	}
	if !strings.Contains(err.Error(), "illegal data address") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestModbusUnitMismatchIsRejected(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 2, 0x00, 0x01}
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	// The fake server echoes the requested unit, so a matching read must succeed.
	if _, err := mm.Request(host, port, 3, fcReadInput, 0, 1, nil, 1000); err != nil {
		t.Fatalf("matching unit should succeed: %v", err)
	}
}

func TestModbusWriteSingleEcho(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return pdu // FC06 echoes the request
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	out, err := mm.Request(host, port, 1, fcWriteSingle, 0x0014, 1, []uint16{0x1234}, 1000)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if len(out) != 2 || out[0] != 0x0014 || out[1] != 0x1234 {
		t.Errorf("unexpected echo %#v", out)
	}
}

func TestModbusTimeoutOnSilentSlave(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return nil // never answers, like an unpowered meter
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	start := time.Now()
	_, err := mm.Request(host, port, 1, fcReadInput, 0, 2, nil, 200)
	if err == nil {
		t.Fatal("expected a timeout")
	}
	// Two attempts of 200ms plus dial overhead; must not hang.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
}

func TestModbusValidatesArguments(t *testing.T) {
	mm := NewModbusManager()
	defer mm.Close()

	cases := []struct {
		name string
		fc   int
		qty  int
		unit int
		want string
	}{
		{"unsupported function", 99, 1, 1, "unsupported function"},
		{"quantity too large", fcReadHolding, 200, 1, "out of range"},
		{"unit out of range", fcReadHolding, 1, 300, "unit id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := mm.Request("127.0.0.1", 1, tc.unit, tc.fc, 0, tc.qty, nil, 100)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want error containing %q", err, tc.want)
			}
		})
	}
}

func TestModbusReadCoilsUnpacksBits(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadCoils, 1, 0x05} // bits 0 and 2 set
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	bits, err := mm.Request(host, port, 1, fcReadCoils, 0, 4, nil, 1000)
	if err != nil {
		t.Fatalf("read coils failed: %v", err)
	}
	want := []uint16{1, 0, 1, 0}
	if len(bits) != len(want) {
		t.Fatalf("got %d bits, want %d", len(bits), len(want))
	}
	for i := range want {
		if bits[i] != want[i] {
			t.Errorf("bit %d = %d, want %d", i, bits[i], want[i])
		}
	}
}

// A gateway that answers with fewer data bytes than the bit count implies must
// be rejected, not indexed past the end of the frame.
func TestModbusShortBitResponseIsRejected(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadCoils, 1, 0xFF} // 1 byte of data for a 32-bit request
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	_, err := mm.Request(host, port, 1, fcReadCoils, 0, 32, nil, 1000)
	if err == nil {
		t.Fatal("expected a short-response error, got none")
	}
	if !strings.Contains(err.Error(), "short bit response") {
		t.Errorf("unexpected error: %v", err)
	}
}

// A truncated register response must not be decoded past its data either.
func TestModbusTruncatedRegisterResponseIsRejected(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 8, 0x43, 0x72} // claims 8 bytes, sends 2
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	if _, err := mm.Request(host, port, 1, fcReadInput, 0, 4, nil, 1000); err == nil {
		t.Fatal("expected a truncation error, got none")
	}
}

// A frame that is internally consistent but carries fewer registers than were
// asked for must not pass as success.
func TestModbusShortRegisterCountIsRejected(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 2, 0x43, 0x72} // 1 register for a 32-register read
	})
	defer stop()

	host, port := splitHostPort(t, addr)
	mm := NewModbusManager()
	defer mm.Close()

	_, err := mm.Request(host, port, 1, fcReadInput, 0, 32, nil, 1000)
	if err == nil {
		t.Fatal("expected a register-count error, got none")
	}
	if !strings.Contains(err.Error(), "register count mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
}

// Async traffic must leave nothing behind: no stored responses, no goroutines.
func TestModbusAsyncLeavesNothingBehind(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 4, 0x43, 0x72, 0x55, 0xDB}
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	mm := NewModbusManager()
	defer mm.Close()

	const n = 50
	baseline := runtime.NumGoroutine()

	for i := 0; i < n; i++ {
		mm.AsyncRequest(fmt.Sprintf("leak_%d", i), host, port, 2, fcReadInput, 0, 2, nil, 1000)
	}

	collected := 0
	deadline := time.Now().Add(10 * time.Second)
	for collected < n && time.Now().Before(deadline) {
		if id := mm.GetPendingAsyncID(); id != "" {
			if resp := mm.GetAsyncResponse(id); resp != nil {
				collected++
			}
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	if collected != n {
		t.Fatalf("collected %d of %d responses", collected, n)
	}

	mm.asyncMu.Lock()
	left := len(mm.asyncResponses)
	mm.asyncMu.Unlock()
	if left != 0 {
		t.Errorf("%d responses left in the map", left)
	}

	// Request goroutines are short-lived; allow a moment for them to exit.
	for i := 0; i < 100 && runtime.NumGoroutine() > baseline+2; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if grown := runtime.NumGoroutine() - baseline; grown > 2 {
		t.Errorf("goroutine count grew by %d after %d requests", grown, n)
	}

	if stats := mm.Stats(); stats["uncollected"].(int) != 0 {
		t.Errorf("stats report %v uncollected responses", stats["uncollected"])
	}
}

// A full channel must not strand the stored response.
func TestModbusFullQueueDoesNotStrandResponses(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 4, 0x43, 0x72, 0x55, 0xDB}
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	mm := NewModbusManager()
	defer mm.Close()

	// Fill the signalling channel so every completion has to take the drop path.
	for i := 0; i < cap(mm.pendingAsync); i++ {
		mm.pendingAsync <- "filler"
	}

	for i := 0; i < 10; i++ {
		mm.AsyncRequest(fmt.Sprintf("dropped_%d", i), host, port, 2, fcReadInput, 0, 2, nil, 1000)
	}
	time.Sleep(500 * time.Millisecond)

	mm.asyncMu.Lock()
	left := len(mm.asyncResponses)
	mm.asyncMu.Unlock()
	if left != 0 {
		t.Errorf("%d responses stranded after the queue filled up", left)
	}
}

func TestModbusConnectionIsReused(t *testing.T) {
	var conns int
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			conns++
			go func(c net.Conn) {
				defer c.Close()
				for {
					header := make([]byte, 6)
					if _, err := io.ReadFull(c, header); err != nil {
						return
					}
					length := int(binary.BigEndian.Uint16(header[4:]))
					body := make([]byte, length)
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					resp := []byte{fcReadInput, 2, 0x00, 0x2A}
					out := make([]byte, 7+len(resp))
					copy(out[0:], header[0:4])
					binary.BigEndian.PutUint16(out[4:], uint16(len(resp)+1))
					out[6] = body[0]
					copy(out[7:], resp)
					c.Write(out)
				}
			}(conn)
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	mm := NewModbusManager()
	defer mm.Close()

	for i := 0; i < 5; i++ {
		if _, err := mm.Request(host, port, 1, fcReadInput, 0, 1, nil, 1000); err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if conns != 1 {
		t.Errorf("expected 1 pooled connection, server saw %d", conns)
	}
}

// A reply with a bogus MBAP length used to leave its body in the pooled
// socket, so every later reply from that gateway was read shifted.
func TestModbusBadFrameDropsSocket(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	var served atomic.Int32 // across connections: only the very first reply is bad
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				for {
					header := make([]byte, 6)
					if _, err := io.ReadFull(c, header); err != nil {
						return
					}
					body := make([]byte, int(binary.BigEndian.Uint16(header[4:])))
					if _, err := io.ReadFull(c, body); err != nil {
						return
					}
					out := make([]byte, 7, 32)
					copy(out[0:], header[0:4])
					out[6] = body[0]
					if served.Add(1) == 1 {
						// Noise on the bus: absurd length, then stray bytes
						binary.BigEndian.PutUint16(out[4:], 999)
						out = append(out, 0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03)
					} else {
						resp := []byte{fcReadInput, 2, 0x00, 0x2A}
						binary.BigEndian.PutUint16(out[4:], uint16(len(resp)+1))
						out = append(out, resp...)
					}
					c.Write(out)
				}
			}(conn)
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	mm := NewModbusManager()
	defer mm.Close()

	if _, err := mm.Request(host, port, 1, fcReadInput, 0, 1, nil, 500); err == nil || !strings.Contains(err.Error(), "invalid frame length") {
		t.Fatalf("first request: err = %v, want invalid frame length", err)
	}
	for i := 0; i < 3; i++ {
		out, err := mm.Request(host, port, 1, fcReadInput, 0, 1, nil, 500)
		if err != nil || len(out) != 1 || out[0] != 42 {
			t.Fatalf("request %d after a bad frame: %v, %v (socket still desynchronised?)", i, out, err)
		}
	}
}
