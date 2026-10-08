package lua

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitForLua pumps timers until the given Lua expression turns true.
func waitForLua(t *testing.T, engine *LuaEngine, expr string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		engine.ProcessTimers()
		if result, _ := engine.Execute(expr); result == "true" {
			return true
		}
	}
	return false
}

func TestE2E_ModbusModuleIsLoaded(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute(`type(Modbus) == "table" and Modbus.isAvailable()`)
	if err != nil {
		t.Fatalf("Modbus module not reachable: %v", err)
	}
	if result != "true" {
		t.Fatalf("expected Modbus to be available, got %q", result)
	}

	result, err = engine.Execute(`Modbus.READ_INPUT`)
	if err != nil || result != "4" {
		t.Errorf("READ_INPUT = %q (err %v), want 4", result, err)
	}
}

func TestE2E_ModbusDecoders(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Register pairs captured from a real SDM120M behind a Waveshare gateway.
	cases := []struct {
		expr string
		want string
	}{
		{`string.format("%.2f", Modbus.toFloat32(0x4372, 0x55DB))`, "242.34"},
		{`string.format("%.2f", Modbus.toFloat32(0xC309, 0x96A4))`, "-137.59"},
		{`string.format("%.3f", Modbus.toFloat32(0x4247, 0xDB23))`, "49.964"},
		{`string.format("%.1f", Modbus.toFloat32(0x0000, 0x0000))`, "0.0"},
		{`tostring(Modbus.toInt32(0xFFFF, 0xFFFF))`, "-1"},
		{`tostring(Modbus.toUint32(0x0001, 0x0000))`, "65536"},
	}
	for _, tc := range cases {
		got, err := engine.Execute(tc.expr)
		if err != nil {
			t.Errorf("%s failed: %v", tc.expr, err)
			continue
		}
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestE2E_ModbusReadThroughLua(t *testing.T) {
	// SDM120 voltage register pair, served by a fake gateway.
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 4, 0x43, 0x72, 0x55, 0xDB}
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	script := fmt.Sprintf(`
		mbDone, mbErr, mbVoltage = false, nil, nil
		Modbus.readInput({
			host = "%s", port = %d, unit = 2, addr = 0x0000, qty = 2, timeout = 1000
		}, function(registers, err)
			mbErr = err
			if registers then
				mbVoltage = Modbus.toFloat32(registers[1], registers[2])
			end
			mbDone = true
		end)
	`, host, port)

	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("readInput call failed: %v", err)
	}
	if !waitForLua(t, engine, `mbDone`, 5*time.Second) {
		t.Fatal("callback never fired")
	}

	if result, _ := engine.Execute(`tostring(mbErr)`); result != "nil" {
		t.Fatalf("read returned an error: %s", result)
	}
	result, err := engine.Execute(`string.format("%.2f", mbVoltage)`)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if result != "242.33" && result != "242.34" {
		t.Errorf("voltage = %s, want ~242.34", result)
	}
}

func TestE2E_ModbusErrorReachesLua(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput | 0x80, 0x02}
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	script := fmt.Sprintf(`
		mbDone, mbErr = false, nil
		Modbus.readInput({ host = "%s", port = %d, unit = 1, addr = 0x9999, qty = 2, timeout = 1000 },
			function(registers, err) mbErr = err; mbDone = true end)
	`, host, port)

	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("readInput call failed: %v", err)
	}
	if !waitForLua(t, engine, `mbDone`, 5*time.Second) {
		t.Fatal("callback never fired")
	}
	result, _ := engine.Execute(`tostring(mbErr)`)
	if !strings.Contains(result, "illegal data address") {
		t.Errorf("error = %q, want an illegal-data-address exception", result)
	}
}

// FC01 must travel as FC01 and come back as one 0/1 entry per requested bit.
func TestE2E_ModbusReadCoilsThroughLua(t *testing.T) {
	var mu sync.Mutex
	var seenFC []byte
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		mu.Lock()
		seenFC = append(seenFC, pdu[0])
		mu.Unlock()
		return []byte{fcReadCoils, 1, 0x05} // bits 0 and 2 set
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	script := fmt.Sprintf(`
		mbDone, mbErr, mbBits = false, nil, nil
		Modbus.readCoils({ host = "%s", port = %d, unit = 1, addr = 0, qty = 4, timeout = 1000 },
			function(values, err)
				mbErr = err
				mbBits = values and table.concat(values, ",") or nil
				mbDone = true
			end)
	`, host, port)
	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("readCoils failed: %v", err)
	}
	if !waitForLua(t, engine, `mbDone`, 5*time.Second) {
		t.Fatal("callback never fired")
	}
	if r, _ := engine.Execute(`tostring(mbErr)`); r != "nil" {
		t.Fatalf("readCoils returned an error: %s", r)
	}
	if r, _ := engine.Execute(`mbBits`); r != "1,0,1,0" {
		t.Errorf("bits = %q, want \"1,0,1,0\"", r)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenFC) != 1 || seenFC[0] != fcReadCoils {
		t.Errorf("gateway saw function codes %v, want [%d]", seenFC, fcReadCoils)
	}
}

// FC02 must travel as FC02, not silently fall back to another read.
func TestE2E_ModbusReadDiscreteThroughLua(t *testing.T) {
	var mu sync.Mutex
	var seenFC []byte
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		mu.Lock()
		seenFC = append(seenFC, pdu[0])
		mu.Unlock()
		return []byte{fcReadDiscreteInputs, 1, 0x02} // only bit 1 set
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	script := fmt.Sprintf(`
		mbDone, mbErr, mbBits = false, nil, nil
		Modbus.readDiscrete({ host = "%s", port = %d, unit = 1, addr = 0, qty = 3, timeout = 1000 },
			function(values, err)
				mbErr = err
				mbBits = values and table.concat(values, ",") or nil
				mbDone = true
			end)
	`, host, port)
	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("readDiscrete failed: %v", err)
	}
	if !waitForLua(t, engine, `mbDone`, 5*time.Second) {
		t.Fatal("callback never fired")
	}
	if r, _ := engine.Execute(`tostring(mbErr)`); r != "nil" {
		t.Fatalf("readDiscrete returned an error: %s", r)
	}
	if r, _ := engine.Execute(`mbBits`); r != "0,1,0" {
		t.Errorf("bits = %q, want \"0,1,0\"", r)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenFC) != 1 || seenFC[0] != fcReadDiscreteInputs {
		t.Errorf("gateway saw function codes %v, want [%d]", seenFC, fcReadDiscreteInputs)
	}
}

// Helpers must not write into the caller's option table: it is often a config
// fragment reused across ticks.
func TestE2E_ModbusHelpersDoNotMutateOpts(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 2, 0x00, 0x2A}
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	script := fmt.Sprintf(`
		mbOpts = { host = "%s", port = %d, unit = 1, addr = 0, qty = 1, value = 7,
		           values = { 7 }, timeout = 1000 }
		mbKeysBefore = 0
		for _ in pairs(mbOpts) do mbKeysBefore = mbKeysBefore + 1 end
		mbValuesTable = mbOpts.values

		local noop = function() end
		Modbus.readCoils(mbOpts, noop)
		Modbus.readDiscrete(mbOpts, noop)
		Modbus.readHolding(mbOpts, noop)
		Modbus.readInput(mbOpts, noop)
		Modbus.writeSingle(mbOpts, noop)
		Modbus.writeMultiple(mbOpts, noop)

		mbKeysAfter = 0
		for _ in pairs(mbOpts) do mbKeysAfter = mbKeysAfter + 1 end
	`, host, port)
	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("helper calls failed: %v", err)
	}

	checks := map[string]string{
		`tostring(mbOpts.fc)`:                      "nil",
		`tostring(mbKeysAfter == mbKeysBefore)`:    "true",
		`tostring(mbOpts.values == mbValuesTable)`: "true",
		`tostring(#mbOpts.values)`:                 "1",
		`tostring(mbOpts.value)`:                   "7",
		`tostring(mbOpts.qty)`:                     "1",
	}
	for expr, want := range checks {
		got, err := engine.Execute(expr)
		if err != nil {
			t.Errorf("%s: %v", expr, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q — opts was mutated", expr, got, want)
		}
	}
}

// The connection pool is shared by every plugin, so Lua must not be able to
// tear it down.
func TestE2E_ModbusPoolIsNotReachableFromLua(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	for _, expr := range []string{
		`type(__go_modbus_close)`,
		`type(Modbus.close)`,
	} {
		got, err := engine.Execute(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got != "nil" {
			t.Errorf("%s = %q, want \"nil\": Lua can still close the shared pool", expr, got)
		}
	}
}

// An id nobody waits for — what a Lua reload leaves behind — must still be
// collected, or its response sits in the Go-side map forever.
func TestE2E_ModbusOrphanResponseIsCollected(t *testing.T) {
	addr, stop := fakeModbusServer(t, func(unit byte, pdu []byte) []byte {
		return []byte{fcReadInput, 4, 0x43, 0x72, 0x55, 0xDB}
	})
	defer stop()
	host, port := splitHostPort(t, addr)

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	// Fire a request straight at the Go layer, so Lua has no pending entry for it.
	orphan := fmt.Sprintf(
		`__go_modbus_request("orphan_from_previous_reload", "%s", %d, 2, 4, 0, 2, "", 1000)`,
		host, port)
	if _, err := engine.Execute(orphan); err != nil {
		t.Fatalf("orphan request failed: %v", err)
	}
	if !waitForLua(t, engine, `Modbus.stats().uncollected > 0`, 3*time.Second) {
		t.Fatal("orphan response never landed in the Go map")
	}

	// Any normal request starts the poller, which must sweep the orphan too.
	script := fmt.Sprintf(`
		mbDone = false
		Modbus.readInput({ host = "%s", port = %d, unit = 2, addr = 0, qty = 2, timeout = 1000 },
			function() mbDone = true end)
	`, host, port)
	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("readInput failed: %v", err)
	}
	if !waitForLua(t, engine, `mbDone`, 5*time.Second) {
		t.Fatal("callback never fired")
	}
	if !waitForLua(t, engine, `Modbus.stats().uncollected == 0`, 3*time.Second) {
		left, _ := engine.Execute(`tostring(Modbus.stats().uncollected)`)
		t.Fatalf("orphan response was never collected, %s still in the map", left)
	}

	// The poller must also stop once nothing is pending.
	if !waitForLua(t, engine, `Modbus.stats().pending == 0`, 3*time.Second) {
		t.Error("pending requests were not cleared")
	}
}

// TestLiveModbusGateway talks to a real gateway. Enable with:
//
//	VCLU_MODBUS_HOST=192.168.0.9 VCLU_MODBUS_PORT=4196 VCLU_MODBUS_UNITS=2,3 \
//	  go test ./pkg/lua/ -run TestLiveModbusGateway -v
func TestLiveModbusGateway(t *testing.T) {
	host := os.Getenv("VCLU_MODBUS_HOST")
	if host == "" {
		t.Skip("set VCLU_MODBUS_HOST to run against a real gateway")
	}
	port := 502
	if p := os.Getenv("VCLU_MODBUS_PORT"); p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	units := os.Getenv("VCLU_MODBUS_UNITS")
	if units == "" {
		units = "1"
	}

	mm := NewModbusManager()
	defer mm.Close()

	answered := 0
	for _, field := range strings.Split(units, ",") {
		var unit int
		fmt.Sscanf(strings.TrimSpace(field), "%d", &unit)

		regs, err := mm.Request(host, port, unit, fcReadInput, 0x0000, 2, nil, 2000)
		if err != nil {
			// A silent unit is a legitimate state: an unpowered meter stays quiet.
			t.Logf("unit %d: no answer (%v)", unit, err)
			continue
		}
		answered++
		voltage := float32FromRegisters(regs)
		t.Logf("unit %d: %.2f V (registers 0x%04X 0x%04X)", unit, voltage, regs[0], regs[1])
		if voltage < 180 || voltage > 280 {
			t.Errorf("unit %d: voltage %.2f V outside a plausible mains range", unit, voltage)
		}
	}

	if answered == 0 {
		t.Fatalf("no unit on %s:%d answered", host, port)
	}
}

// float32FromRegisters decodes a big-endian IEEE-754 float from two registers.
func float32FromRegisters(regs []uint16) float64 {
	if len(regs) < 2 {
		return 0
	}
	bits := uint32(regs[0])<<16 | uint32(regs[1])
	sign := 1.0
	if bits&0x80000000 != 0 {
		sign = -1.0
	}
	exponent := int((bits >> 23) & 0xFF)
	mantissa := float64(bits & 0x7FFFFF)
	if exponent == 0 {
		return sign * mantissa * pow2(-149)
	}
	return sign * (1 + mantissa/8388608.0) * pow2(exponent-127)
}

func pow2(n int) float64 {
	result := 1.0
	if n >= 0 {
		for i := 0; i < n; i++ {
			result *= 2
		}
		return result
	}
	for i := 0; i < -n; i++ {
		result /= 2
	}
	return result
}
