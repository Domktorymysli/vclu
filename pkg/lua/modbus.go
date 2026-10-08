package lua

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	fcReadCoils          = 1
	fcReadDiscreteInputs = 2
	fcReadHolding        = 3
	fcReadInput          = 4
	fcWriteSingle        = 6
	fcWriteMultiple      = 16
)

const (
	modbusMaxReadRegs  = 125
	modbusMaxWriteRegs = 123
	modbusMaxReadBits  = 2000
)

// ModbusManager talks Modbus TCP to gateways such as the Waveshare RS485-to-ETH.
// Requests to one target are serialised: the RS485 bus behind it carries one at a time.
type ModbusManager struct {
	mu    sync.Mutex
	conns map[string]*modbusConn

	asyncMu        sync.Mutex
	asyncResponses map[string]*modbusResponse
	pendingAsync   chan string

	statsMu   sync.Mutex
	requests  uint64
	errors    uint64
	onRequest func(hasError bool)
}

type modbusConn struct {
	mu    sync.Mutex // serialises transactions against this target
	conn  net.Conn
	addr  string
	txID  uint16
	fails int
}

type modbusResponse struct {
	Values []uint16
	Error  string
}

// NewModbusManager creates a Modbus TCP manager.
func NewModbusManager() *ModbusManager {
	return &ModbusManager{
		conns:          make(map[string]*modbusConn),
		asyncResponses: make(map[string]*modbusResponse),
		pendingAsync:   make(chan string, 100),
	}
}

// SetOnRequest sets the callback used for metrics.
func (mm *ModbusManager) SetOnRequest(fn func(hasError bool)) {
	mm.statsMu.Lock()
	mm.onRequest = fn
	mm.statsMu.Unlock()
}

func (mm *ModbusManager) getConn(addr string) *modbusConn {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	c, ok := mm.conns[addr]
	if !ok {
		c = &modbusConn{addr: addr}
		mm.conns[addr] = c
	}
	return c
}

// dial opens the socket if needed. Caller must hold c.mu.
func (c *modbusConn) dial(timeout time.Duration) error {
	if c.conn != nil {
		return nil
	}
	conn, err := net.DialTimeout("tcp", c.addr, timeout)
	if err != nil {
		return err
	}
	// Modbus frames are small and latency-sensitive; Nagle only adds delay.
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	c.conn = conn
	c.fails = 0
	return nil
}

// drop closes the socket so the next call redials. Caller must hold c.mu.
func (c *modbusConn) drop() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func buildPDU(fc, addr, qty int, values []uint16) ([]byte, error) {
	switch fc {
	case fcReadCoils, fcReadDiscreteInputs:
		if qty < 1 || qty > modbusMaxReadBits {
			return nil, fmt.Errorf("quantity %d out of range (1-%d)", qty, modbusMaxReadBits)
		}
	case fcReadHolding, fcReadInput:
		if qty < 1 || qty > modbusMaxReadRegs {
			return nil, fmt.Errorf("quantity %d out of range (1-%d)", qty, modbusMaxReadRegs)
		}
	case fcWriteSingle:
		if len(values) < 1 {
			return nil, fmt.Errorf("function 6 needs one value")
		}
	case fcWriteMultiple:
		if len(values) < 1 || len(values) > modbusMaxWriteRegs {
			return nil, fmt.Errorf("value count %d out of range (1-%d)", len(values), modbusMaxWriteRegs)
		}
	default:
		return nil, fmt.Errorf("unsupported function code %d", fc)
	}
	if addr < 0 || addr > 0xFFFF {
		return nil, fmt.Errorf("address %d out of range", addr)
	}

	switch fc {
	case fcWriteSingle:
		pdu := make([]byte, 5)
		pdu[0] = byte(fc)
		binary.BigEndian.PutUint16(pdu[1:], uint16(addr))
		binary.BigEndian.PutUint16(pdu[3:], values[0])
		return pdu, nil
	case fcWriteMultiple:
		pdu := make([]byte, 6+2*len(values))
		pdu[0] = byte(fc)
		binary.BigEndian.PutUint16(pdu[1:], uint16(addr))
		binary.BigEndian.PutUint16(pdu[3:], uint16(len(values)))
		pdu[5] = byte(2 * len(values))
		for i, v := range values {
			binary.BigEndian.PutUint16(pdu[6+2*i:], v)
		}
		return pdu, nil
	default:
		pdu := make([]byte, 5)
		pdu[0] = byte(fc)
		binary.BigEndian.PutUint16(pdu[1:], uint16(addr))
		binary.BigEndian.PutUint16(pdu[3:], uint16(qty))
		return pdu, nil
	}
}

func parsePDU(fc, qty int, pdu []byte) ([]uint16, error) {
	if len(pdu) < 2 {
		return nil, fmt.Errorf("short response (%d bytes)", len(pdu))
	}
	respFC := pdu[0]
	if respFC&0x80 != 0 {
		return nil, fmt.Errorf("modbus exception 0x%02x (%s)", pdu[1], exceptionText(pdu[1]))
	}
	if int(respFC) != fc {
		return nil, fmt.Errorf("function mismatch: sent %d, got %d", fc, respFC)
	}

	switch fc {
	case fcReadHolding, fcReadInput:
		byteCount := int(pdu[1])
		// A short-but-consistent frame would otherwise pass as success.
		if byteCount != 2*qty {
			return nil, fmt.Errorf("register count mismatch: asked for %d registers, got %d bytes", qty, byteCount)
		}
		if len(pdu) < 2+byteCount {
			return nil, fmt.Errorf("truncated response: want %d data bytes, got %d", byteCount, len(pdu)-2)
		}
		out := make([]uint16, byteCount/2)
		for i := range out {
			out[i] = binary.BigEndian.Uint16(pdu[2+2*i:])
		}
		return out, nil

	case fcReadCoils, fcReadDiscreteInputs:
		byteCount := int(pdu[1])
		if len(pdu) < 2+byteCount {
			return nil, fmt.Errorf("truncated response: want %d data bytes, got %d", byteCount, len(pdu)-2)
		}
		// Unpacking below would read past the frame if the count is short.
		if need := (qty + 7) / 8; byteCount < need {
			return nil, fmt.Errorf("short bit response: %d bytes for %d bits, want %d", byteCount, qty, need)
		}
		// One Lua value per bit keeps the Lua side free of bit twiddling.
		out := make([]uint16, 0, qty)
		for i := 0; i < qty; i++ {
			b := pdu[2+i/8]
			out = append(out, uint16((b>>(uint(i)%8))&1))
		}
		return out, nil

	case fcWriteSingle, fcWriteMultiple:
		if len(pdu) < 5 {
			return nil, fmt.Errorf("short write echo (%d bytes)", len(pdu))
		}
		return []uint16{binary.BigEndian.Uint16(pdu[1:]), binary.BigEndian.Uint16(pdu[3:])}, nil
	}
	return nil, fmt.Errorf("unsupported function code %d", fc)
}

func exceptionText(code byte) string {
	switch code {
	case 0x01:
		return "illegal function"
	case 0x02:
		return "illegal data address"
	case 0x03:
		return "illegal data value"
	case 0x04:
		return "slave device failure"
	case 0x05:
		return "acknowledge"
	case 0x06:
		return "slave device busy"
	case 0x0B:
		return "gateway target device failed to respond"
	default:
		return "unknown"
	}
}

// Request runs one Modbus TCP transaction and returns the register values.
func (mm *ModbusManager) Request(host string, port, unit, fc, addr, qty int, values []uint16, timeoutMs int) ([]uint16, error) {
	out, err := mm.request(host, port, unit, fc, addr, qty, values, timeoutMs)
	mm.recordRequest(err != nil)
	return out, err
}

func (mm *ModbusManager) request(host string, port, unit, fc, addr, qty int, values []uint16, timeoutMs int) ([]uint16, error) {
	if host == "" {
		return nil, fmt.Errorf("host is required")
	}
	if unit < 0 || unit > 247 {
		return nil, fmt.Errorf("unit id %d out of range (0-247)", unit)
	}
	if timeoutMs <= 0 {
		timeoutMs = 2000
	}
	timeout := time.Duration(timeoutMs) * time.Millisecond

	pdu, err := buildPDU(fc, addr, qty, values)
	if err != nil {
		return nil, err
	}

	c := mm.getConn(net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	c.mu.Lock()
	defer c.mu.Unlock()

	// One retry: a pooled socket may have been closed by the gateway meanwhile.
	for attempt := 0; attempt < 2; attempt++ {
		if err = c.dial(timeout); err != nil {
			return nil, fmt.Errorf("connect %s: %w", c.addr, err)
		}

		var out []uint16
		out, err = c.transact(unit, fc, qty, pdu, timeout)
		if err == nil {
			return out, nil
		}
		var fe *frameError
		if errors.As(err, &fe) {
			c.drop()
			return nil, err
		}
		// Protocol-level answers (exceptions) are real replies, not transport faults.
		if _, isNetErr := err.(net.Error); !isNetErr && !isConnReset(err) {
			return nil, err
		}
		c.drop()
	}
	return nil, err
}

// frameError means the byte stream can no longer be trusted (bad MBAP header,
// reply never found): leftover bytes would shift every later reply, so the
// socket must be dropped. Not retried — the device may already have acted.
type frameError struct{ msg string }

func (e *frameError) Error() string { return e.msg }

func isConnReset(err error) bool {
	return err == io.EOF || err == io.ErrUnexpectedEOF
}

// transact writes one MBAP frame and reads the matching reply. Caller holds c.mu.
func (c *modbusConn) transact(unit, fc, qty int, pdu []byte, timeout time.Duration) ([]uint16, error) {
	c.txID++
	if c.txID == 0 {
		c.txID = 1
	}
	tid := c.txID

	frame := make([]byte, 7+len(pdu))
	binary.BigEndian.PutUint16(frame[0:], tid)
	binary.BigEndian.PutUint16(frame[2:], 0) // protocol id
	binary.BigEndian.PutUint16(frame[4:], uint16(len(pdu)+1))
	frame[6] = byte(unit)
	copy(frame[7:], pdu)

	deadline := time.Now().Add(timeout)
	if err := c.conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(frame); err != nil {
		return nil, err
	}

	// A gateway may still be draining an earlier, timed-out reply.
	for {
		header := make([]byte, 6)
		if _, err := io.ReadFull(c.conn, header); err != nil {
			return nil, err
		}
		respTID := binary.BigEndian.Uint16(header[0:])
		length := int(binary.BigEndian.Uint16(header[4:]))
		if length < 2 || length > 260 {
			return nil, &frameError{fmt.Sprintf("invalid frame length %d", length)}
		}
		if proto := binary.BigEndian.Uint16(header[2:]); proto != 0 {
			return nil, &frameError{fmt.Sprintf("invalid protocol id %d", proto)}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(c.conn, body); err != nil {
			return nil, err
		}
		if respTID != tid {
			if time.Now().After(deadline) {
				return nil, &frameError{fmt.Sprintf("stale frames only (last tid %d, want %d)", respTID, tid)}
			}
			continue
		}
		if int(body[0]) != unit {
			return nil, fmt.Errorf("unit mismatch: sent %d, got %d", unit, body[0])
		}
		return parsePDU(fc, qty, body[1:])
	}
}

func (mm *ModbusManager) recordRequest(hasError bool) {
	mm.statsMu.Lock()
	mm.requests++
	if hasError {
		mm.errors++
	}
	fn := mm.onRequest
	mm.statsMu.Unlock()
	if fn != nil {
		fn(hasError)
	}
}

// AsyncRequest performs the transaction off the Lua thread.
func (mm *ModbusManager) AsyncRequest(requestID, host string, port, unit, fc, addr, qty int, values []uint16, timeoutMs int) {
	go func() {
		out, err := mm.Request(host, port, unit, fc, addr, qty, values, timeoutMs)

		mm.asyncMu.Lock()
		if err != nil {
			mm.asyncResponses[requestID] = &modbusResponse{Error: err.Error()}
		} else {
			mm.asyncResponses[requestID] = &modbusResponse{Values: out}
		}
		mm.asyncMu.Unlock()

		select {
		case mm.pendingAsync <- requestID:
		default:
			// Nobody will claim it now, so drop the stored response too.
			mm.asyncMu.Lock()
			delete(mm.asyncResponses, requestID)
			mm.asyncMu.Unlock()
			log.Printf("[MODBUS] Warning: async response channel full, dropping request %s", requestID)
		}
	}()
}

// GetAsyncResponse retrieves and removes an async response.
func (mm *ModbusManager) GetAsyncResponse(requestID string) *modbusResponse {
	mm.asyncMu.Lock()
	defer mm.asyncMu.Unlock()
	resp, ok := mm.asyncResponses[requestID]
	if ok {
		delete(mm.asyncResponses, requestID)
	}
	return resp
}

// GetPendingAsyncID returns a completed request ID, or "" when none is ready.
func (mm *ModbusManager) GetPendingAsyncID() string {
	select {
	case id := <-mm.pendingAsync:
		return id
	default:
		return ""
	}
}

// Close drops every pooled connection. Lifecycle only: the pool is shared by
// every plugin, so it is never reachable from Lua.
func (mm *ModbusManager) Close() {
	mm.mu.Lock()
	conns := make([]*modbusConn, 0, len(mm.conns))
	for _, c := range mm.conns {
		conns = append(conns, c)
	}
	mm.conns = make(map[string]*modbusConn)
	mm.mu.Unlock()

	for _, c := range conns {
		c.mu.Lock()
		c.drop()
		c.mu.Unlock()
	}
}

// Stats reports counters for the dashboard.
func (mm *ModbusManager) Stats() map[string]interface{} {
	mm.statsMu.Lock()
	requests, errors := mm.requests, mm.errors
	mm.statsMu.Unlock()

	mm.asyncMu.Lock()
	uncollected := len(mm.asyncResponses)
	mm.asyncMu.Unlock()

	mm.mu.Lock()
	targets := make([]string, 0, len(mm.conns))
	open := 0
	for addr, c := range mm.conns {
		targets = append(targets, addr)
		c.mu.Lock()
		if c.conn != nil {
			open++
		}
		c.mu.Unlock()
	}
	mm.mu.Unlock()

	return map[string]interface{}{
		"requests":    requests,
		"errors":      errors,
		"targets":     targets,
		"open":        open,
		"uncollected": uncollected,
	}
}

func valuesFromJSON(raw string) ([]uint16, error) {
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var nums []float64
	if err := json.Unmarshal([]byte(raw), &nums); err != nil {
		return nil, fmt.Errorf("invalid values JSON: %w", err)
	}
	out := make([]uint16, len(nums))
	for i, n := range nums {
		if n < 0 || n > 0xFFFF {
			return nil, fmt.Errorf("value %v out of register range (0-65535)", n)
		}
		out[i] = uint16(n)
	}
	return out, nil
}

// registerModbusFunctions registers Go Modbus functions in Lua.
func registerModbusFunctions(L *lua.LState, mm *ModbusManager) {
	if mm == nil {
		return
	}

	// __go_modbus_request(requestId, host, port, unit, fc, addr, qty, valuesJson, timeoutMs)
	L.SetGlobal("__go_modbus_request", L.NewFunction(func(L *lua.LState) int {
		requestID := L.CheckString(1)
		host := L.CheckString(2)
		port := L.OptInt(3, 502)
		unit := L.OptInt(4, 1)
		fc := L.OptInt(5, fcReadHolding)
		addr := L.OptInt(6, 0)
		qty := L.OptInt(7, 1)
		valuesJSON := L.OptString(8, "")
		timeout := L.OptInt(9, 2000)

		values, err := valuesFromJSON(valuesJSON)
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		mm.AsyncRequest(requestID, host, port, unit, fc, addr, qty, values, timeout)
		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_modbus_poll_async() -> requestId or ""
	L.SetGlobal("__go_modbus_poll_async", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(mm.GetPendingAsyncID()))
		return 1
	}))

	// __go_modbus_get_response(requestId) -> valuesJson, error
	L.SetGlobal("__go_modbus_get_response", L.NewFunction(func(L *lua.LState) int {
		requestID := L.CheckString(1)
		resp := mm.GetAsyncResponse(requestID)
		if resp == nil {
			L.Push(lua.LNil)
			L.Push(lua.LString("no response for " + requestID))
			return 2
		}
		if resp.Error != "" {
			L.Push(lua.LNil)
			L.Push(lua.LString(resp.Error))
			return 2
		}
		encoded, err := json.Marshal(resp.Values)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(string(encoded)))
		L.Push(lua.LNil)
		return 2
	}))

	// __go_modbus_stats() -> json
	L.SetGlobal("__go_modbus_stats", L.NewFunction(func(L *lua.LState) int {
		encoded, _ := json.Marshal(mm.Stats())
		L.Push(lua.LString(string(encoded)))
		return 1
	}))
}
