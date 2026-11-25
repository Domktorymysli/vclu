package omparser

import (
	"os"
	"strings"
	"testing"
)

func readModbusFixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/om-modbus-gate.lua")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The om.lua of a real GATE MODBUS (anonymised): every object of both classes with its
// settings, the removed ones left out.
func TestReadModbusGate(t *testing.T) {
	m := ReadModbus(readModbusFixture(t))
	if m == nil {
		t.Fatal("not read as a GATE MODBUS")
	}
	if !m.FromCLU || m.CLU.Name != "MODBAS" {
		t.Errorf("FromCLU %v, name %q", m.FromCLU, m.CLU.Name)
	}
	if len(m.Objects) != 16 {
		t.Fatalf("%d objects, want 16", len(m.Objects))
	}
	by := map[string]ModbusObject{}
	for _, o := range m.Objects {
		by[o.ID] = o
	}
	for _, removed := range []string{"MOD9016", "MOD9017"} {
		if _, ok := by[removed]; ok {
			t.Errorf("%s is commented out, read anyway", removed)
		}
	}

	v2 := by["MOD9001"]
	if v2.Class != ClassModbus || v2.Name != "rekuperacja_stan" {
		t.Errorf("MOD9001: %+v", v2)
	}
	if v2.get("slave") != "100" || v2.get("register") != "0" || v2.get("regType") != "2" || v2.line() != "19200 8E1" {
		t.Errorf("MOD9001: slave %q register %q type %q line %q", v2.get("slave"), v2.get("register"), v2.get("regType"), v2.line())
	}
	if v2.readOnly() || v2.data() != "number, 16 bits from bit 0" {
		t.Errorf("MOD9001: readOnly %v data %q", v2.readOnly(), v2.data())
	}

	rtu := by["MOD9012"]
	if rtu.Class != ClassModbusRTU || rtu.Name != "MODBAS_WILGOTNOSC_SCHODY" {
		t.Errorf("MOD9012: %+v", rtu)
	}
	if rtu.get("slave") != "1" || rtu.get("register") != "2" || rtu.get("regType") != "3" || rtu.get("divisor") != "10" {
		t.Errorf("MOD9012: slave %q register %q type %q divisor %q", rtu.get("slave"), rtu.get("register"), rtu.get("regType"), rtu.get("divisor"))
	}
	if rtu.line() != "9600 8N1" || !rtu.readOnly() || rtu.data() != "int16" || rtu.endianness() != "LittleBigEndian (swap bytes)" {
		t.Errorf("MOD9012: line %q readOnly %v data %q endian %q", rtu.line(), rtu.readOnly(), rtu.data(), rtu.endianness())
	}

	doc := m.Doc()
	for _, want := range []string{
		"# Modbus of GATE MODBUS MODBAS (CLU500000001)",
		"| rekuperacja_stan | MOD9001 | Modbus | 19200 8E1 | 100 | 0 | readHolding | writeSingle / writeMultiple | number, 16 bits from bit 0 | NoSwap (little endian) | 1 | 2000 | 1000 |",
		"| MODBAS_WILGOTNOSC_SCHODY | MOD9012 | ModbusRTU | 9600 8N1 | 1 | 2 | readInput | — | int16 | LittleBigEndian (swap bytes) | 10 | 3000 | 300 |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "no settings") {
		t.Error("a CLU's own om.lua said to have no settings")
	}
}

// Only a GATE MODBUS: the same classes on another CLU are not read as Modbus.
func TestReadModbusOnlyOnGateModbus(t *testing.T) {
	src := readModbusFixture(t)
	if m := ReadModbus(strings.Replace(src, "-- FwType 00000001", "-- FwType 00000003", 1)); m != nil {
		t.Errorf("HTTP GATE read as GATE MODBUS: %d objects", len(m.Objects))
	}
	cluOM, err := os.ReadFile("testdata/CLU212345678_om.lua")
	if err != nil {
		t.Fatal(err)
	}
	if m := ReadModbus(string(cluOM)); m != nil {
		t.Error("a CLU read as GATE MODBUS")
	}
}

// What StripGateSettings leaves (no OnInit settings, as a project's om.lua) is no map of its own.
func TestReadModbusWithoutSettings(t *testing.T) {
	src := readModbusFixture(t)
	src = src[:strings.Index(src, "function OnInit")]
	m := ReadModbus(src)
	if m == nil || m.FromCLU || len(m.Objects) != 16 {
		t.Fatalf("%+v", m)
	}
	if !strings.Contains(m.Doc(), "no settings") {
		t.Error("the doc does not say the settings are missing")
	}
}

// The other Modbus objects of the GATE (as a slave or a server) are named, not mapped; a
// ModbusClient gives its socket address.
func TestReadModbusOtherClasses(t *testing.T) {
	src := `-- FwType 00000001
-- HwType 00000012
CLU500000001 = GATE:new(5000, 0xC0A80A0A, 0xFFFFFF00, 0xC0A80A01, "", "")
SRV0001 = GATE:new(206, "SRV0001")
CLI0001 = GATE:new(205, "CLI0001")
-- NAME_PERIPHERY licznik=CLI0001
function OnInit()
CLI0001:set(1, "192.0.2.5:502")
CLI0001:set(2, 3)
CLI0001:set(5, 12)
CLI0001:set(7, 3)
CLI0001:set(11, 2)
CLI0001:set(12, 32)
CLI0001:set(13, 0)
end
`
	doc := ReadModbus(src).Doc()
	for _, want := range []string{
		"| licznik | CLI0001 | ModbusClient | TCP 192.0.2.5:502 | 3 | 12 | readInput | — | float32 | BigEndian |",
		"not mapped: SRV0001 (ModbusServer)",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc lacks %q:\n%s", want, doc)
		}
	}
}

// Modbus v1 has no parity and stop bits; a | of om.lua does not break the table; class 200's
// float and bit values.
func TestReadModbusV1AndCells(t *testing.T) {
	src := `-- FwType 00000001
-- HwType 00000012
CLU500000001 = GATE:new(5000, 0xC0A80A0A, 0xFFFFFF00, 0xC0A80A01, "", "")
MOD0001 = GATE:new(200, "MOD0001")
-- NAME_PERIPHERY a|b=MOD0001
MOD0002 = GATE:new(200, "MOD0002")
function OnInit()
MOD0001:set(0, 7)
MOD0001:set(3, 9600)
MOD0001:set(4, 2)
MOD0001:set(6, 32)
MOD0002:set(4, 3)
MOD0002:set(5, 4)
end
`
	doc := ReadModbus(src).Doc()
	for _, want := range []string{
		"| a\\|b | MOD0001 | Modbus | 9600 | 7 |",
		"| float32 |",
		"| — | MOD0002 | Modbus | — | — | — | register type — | — | bit 4 |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc lacks %q:\n%s", want, doc)
		}
	}
}
