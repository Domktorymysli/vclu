package omparser

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ModbusDocsDir is where the Modbus maps of imported GATE MODBUS CLUs go, under the data
// directory, one <CLU>.md each.
const ModbusDocsDir = "ai/modbus"

// Modbus objects of a GATE MODBUS (FwType 00000001, HwType 00000012), declared in its om.lua
// as GATE objects: MOD1234 = GATE:new(203, "MOD1234"). The numbers are Object Manager's
// (object_modbus_*.xml); another GATE may use them for other objects.
const (
	ClassModbus            ObjectClass = 200 // Modbus (v1, v2): a register read over the GATE's RS485
	ClassModbusValue       ObjectClass = 201 // ModbusValue: a register read over RS485
	ClassModbusSlaveConfig ObjectClass = 202 // ModbusSlaveConfigRTU: the GATE as a slave
	ClassModbusRTU         ObjectClass = 203 // ModbusRTU: registers read over RS485
	ClassModbusSlaveRTU    ObjectClass = 204 // ModbusSlaveRTU: a register the GATE serves as a slave
	ClassModbusClient      ObjectClass = 205 // ModbusClient: registers read over Modbus TCP
	ClassModbusServer      ObjectClass = 206 // ModbusServer: a register the GATE serves over TCP
)

// IsGateModbus tells whether the CLU is a GATE MODBUS: FwType 00000001 and HwType 00000012.
func (c CLUInfo) IsGateModbus() bool {
	fw, errF := strconv.ParseUint(c.FwType, 16, 32)
	hw, errH := strconv.ParseUint(c.HwType, 16, 32)
	return errF == nil && errH == nil && fw == 0x01 && hw == 0x12
}

// modbusFeatures are, per class, the feature indices of the settings in the map. A field a
// class has not is missing.
var modbusFeatures = map[ObjectClass]map[string]int{
	ClassModbus: {"slave": 0, "access": 1, "register": 2, "speed": 3, "valueType": 4, "bitPos": 5,
		"width": 6, "refresh": 8, "timeout": 9, "divisor": 10, "endian": 11, "regType": 12, "stop": 16, "parity": 17},
	ClassModbusValue: {"speed": 0, "parity": 1, "stop": 2, "slave": 5, "timeout": 6, "refresh": 7, "register": 8,
		"regType": 9, "count": 11, "dataType": 13, "width": 14, "endian": 15, "bitWidth": 16, "bitPos": 17, "divisor": 18},
	ClassModbusRTU: {"speed": 0, "parity": 1, "stop": 2, "slave": 5, "timeout": 6, "refresh": 7, "register": 8,
		"regType": 10, "count": 12, "dataType": 14, "width": 15, "endian": 16, "bitWidth": 17, "bitPos": 18, "divisor": 19},
	ClassModbusClient: {"socket": 1, "slave": 2, "timeout": 3, "refresh": 4, "register": 5, "regType": 7,
		"count": 9, "dataType": 11, "width": 12, "endian": 13, "bitWidth": 14, "bitPos": 15, "divisor": 16},
}

// modbusOther are the GATE's Modbus objects that read nothing from a device: named in the map only.
var modbusOther = map[ObjectClass]string{
	ClassModbusSlaveConfig: "ModbusSlaveConfigRTU", ClassModbusSlaveRTU: "ModbusSlaveRTU", ClassModbusServer: "ModbusServer",
}

var modbusClassNames = map[ObjectClass]string{
	ClassModbus: "Modbus", ClassModbusValue: "ModbusValue", ClassModbusRTU: "ModbusRTU", ClassModbusClient: "ModbusClient",
}

// reModbusSet is a feature set to a literal: MOD1234:set(8, 902) or MOD1234:set(19, "10").
var reModbusSet = regexp.MustCompile(`^(\w+):set\s*\(\s*(\d+)\s*,\s*("[^"]*"|-?[0-9.]+)\s*\)`)

// ModbusObject is a Modbus object of a GATE MODBUS with the features om.lua sets.
type ModbusObject struct {
	ID       string
	Name     string
	Class    ObjectClass
	Settings map[int]string // feature index -> value as om.lua has it, quotes removed
}

// ModbusMap is what a GATE MODBUS's om.lua says about its Modbus objects. FromCLU is false
// for an om.lua without OnInit, as vCLU writes from a project: it has no settings.
type ModbusMap struct {
	CLU     CLUInfo
	Objects []ModbusObject
	FromCLU bool
}

// ReadModbus reads the Modbus objects of a GATE MODBUS from its om.lua as the CLU has it,
// before StripGateSettings. Nil when the CLU is no GATE MODBUS. Objects commented out
// (-- REMOVED) are not read.
func ReadModbus(content string) *ModbusMap {
	parsed, err := Parse(content)
	if err != nil || !parsed.CLU.IsGateModbus() {
		return nil
	}
	m := &ModbusMap{CLU: parsed.CLU}
	byID := map[string]int{}
	names := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "function OnInit") {
			m.FromCLU = true
		}
		if g := reGateObject.FindStringSubmatch(line); g != nil {
			class, _ := strconv.Atoi(g[2])
			c := ObjectClass(class)
			if _, ok := modbusFeatures[c]; ok || modbusOther[c] != "" {
				byID[g[1]] = len(m.Objects)
				m.Objects = append(m.Objects, ModbusObject{ID: g[1], Class: c, Settings: map[int]string{}})
			}
			continue
		}
		if n := rePeripheryName.FindStringSubmatch(line); n != nil {
			names[n[2]] = n[1]
			continue
		}
		if s := reModbusSet.FindStringSubmatch(line); s != nil {
			if i, ok := byID[s[1]]; ok {
				idx, _ := strconv.Atoi(s[2])
				m.Objects[i].Settings[idx] = strings.Trim(s[3], `"`)
			}
		}
	}
	for i := range m.Objects {
		m.Objects[i].Name = names[m.Objects[i].ID]
	}
	return m
}

// get is a setting of the object by its field name, "" when the class has not the field or
// om.lua does not set it.
func (o ModbusObject) get(field string) string {
	idx, ok := modbusFeatures[o.Class][field]
	if !ok {
		return ""
	}
	return o.Settings[idx]
}

// readOnly tells whether vCLU should only read the register: Modbus v1/v2 with AccessRights 0,
// and discrete inputs and input registers, which Modbus cannot write.
func (o ModbusObject) readOnly() bool {
	if o.Class == ClassModbus && o.get("access") == "0" {
		return true
	}
	t := o.get("regType")
	return t == "1" || t == "3"
}

// modbusCalls are vCLU's Modbus functions per register type: read, write.
var modbusCalls = map[string][2]string{
	"0": {"readCoils", "— (FC05 not in vclu's Modbus)"},
	"1": {"readDiscrete", "—"},
	"2": {"readHolding", "writeSingle / writeMultiple"},
	"3": {"readInput", "—"},
}

// data is the value's type and width: "uint16", "float32", "bit 3".
func (o ModbusObject) data() string {
	width := o.get("width")
	if o.Class == ClassModbus {
		switch o.get("valueType") {
		case "2":
			return "float" + width
		case "3":
			return "bit " + o.get("bitPos")
		}
		return "number, " + width + " bits from bit " + orDash(o.get("bitPos"))
	}
	kind := map[string]string{"0": "uint", "1": "int", "2": "float"}[o.get("dataType")]
	if kind == "" {
		kind = "type " + orDash(o.get("dataType")) + ", "
	}
	s := kind + width
	if bw := o.get("bitWidth"); bw != "" && bw != "0" {
		s += fmt.Sprintf(", bits %s-%s", orDash(o.get("bitPos")), bitEnd(o.get("bitPos"), bw))
	}
	return s
}

func bitEnd(pos, width string) string {
	p, errP := strconv.Atoi(pos)
	w, errW := strconv.Atoi(width)
	if errP != nil || errW != nil {
		return "?"
	}
	return strconv.Itoa(p + w - 1)
}

// endianness is the byte order by Object Manager's names, which differ between the classes.
func (o ModbusObject) endianness() string {
	v := o.get("endian")
	if o.Class == ClassModbus {
		return enumName(v, "NoSwap (little endian)", "SwapBytesAndWords (big endian)",
			"SwapBytes (bytes big, words little endian)", "SwapWords (bytes little, words big endian)")
	}
	return enumName(v, "BigEndian", "LittleBigEndian (swap bytes)", "BigLittleEndian (swap words)", "LittleEndian")
}

func enumName(v string, names ...string) string {
	i, err := strconv.Atoi(v)
	if err != nil || i < 0 || i >= len(names) {
		return orDash(v)
	}
	return names[i]
}

// line is the object's serial line: "19200 8N1"; for a ModbusClient its socket address.
func (o ModbusObject) line() string {
	if o.Class == ClassModbusClient {
		return "TCP " + orDash(o.get("socket"))
	}
	parity := map[string]string{"0": "N", "1": "O", "2": "E"}[o.get("parity")]
	stop := map[string]string{"0": "1", "1": "1.5", "2": "2"}[o.get("stop")]
	if parity == "" || stop == "" {
		// Modbus v1 has no parity and stop bits, they are the GATE's
		return orDash(o.get("speed"))
	}
	return orDash(o.get("speed")) + " 8" + parity + stop
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// cell keeps a value of om.lua from breaking the table.
func cell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// Doc is the map the agent reads: one row per register the GATE reads, with vCLU's Modbus
// call for it. Empty when the GATE has no Modbus objects.
func (m *ModbusMap) Doc() string {
	if m == nil || len(m.Objects) == 0 {
		return ""
	}
	var b strings.Builder
	name := m.CLU.ID
	if m.CLU.Name != "" {
		name = m.CLU.Name + " (" + m.CLU.ID + ")"
	}
	fmt.Fprintf(&b, "# Modbus of GATE MODBUS %s\n\n", name)
	b.WriteString("Written by vclu from the CLU's om.lua when it was imported; the next import of this CLU replaces it.\n\n")
	b.WriteString("vclu's `Modbus` (see `vclu_runtime_schema`) is a Modbus TCP client: a device on the GATE's RS485 reaches it " +
		"through an RS485-to-TCP gateway set to the row's line (its host and port are not in the project); a ModbusClient's line is already TCP. " +
		"`unit` is the slave, `addr` the register as Object Manager has it (check the device's manual for an offset of 1). " +
		"The value is the raw value divided by the divisor.\n\n")
	b.WriteString("| Name | ID | Class | Line | Slave | Register | Read | Write | Data | Endianness | Divisor | Refresh ms | Timeout ms |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	var other []string
	for _, o := range m.Objects {
		if n := modbusOther[o.Class]; n != "" {
			other = append(other, fmt.Sprintf("%s (%s)", cell(label(o)), n))
			continue
		}
		calls, ok := modbusCalls[o.get("regType")]
		if !ok {
			calls = [2]string{"register type " + orDash(o.get("regType")), "—"}
		}
		write := calls[1]
		if o.readOnly() {
			write = "—"
		}
		reg := orDash(o.get("register"))
		if c := o.get("count"); c != "" && c != "1" {
			reg += fmt.Sprintf(" (%s)", c)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			cell(orDash(o.Name)), o.ID, modbusClassNames[o.Class], cell(o.line()), cell(orDash(o.get("slave"))), cell(reg),
			calls[0], write, cell(o.data()), cell(o.endianness()), cell(orDash(o.get("divisor"))), cell(orDash(o.get("refresh"))), cell(orDash(o.get("timeout"))))
	}
	if !m.FromCLU {
		b.WriteString("\nThis om.lua has no settings (written from a project): import the GATE's own om.lua or a diagnostic pack.\n")
	}
	if len(other) > 0 {
		sort.Strings(other)
		fmt.Fprintf(&b, "\nThe GATE as a Modbus slave or server, not mapped: %s.\n", strings.Join(other, ", "))
	}
	return b.String()
}

func label(o ModbusObject) string {
	if o.Name != "" {
		return o.Name + " " + o.ID
	}
	return o.ID
}
