package omparser

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Regex patterns for parsing om.lua
var (
	// Header patterns
	reFwType    = regexp.MustCompile(`^-- FwType\s+(\w+)`)
	reFwVersion = regexp.MustCompile(`^-- FwVersion\s+(\w+)`)
	reHwType    = regexp.MustCompile(`^-- HwType\s+(\w+)`)
	reHwVersion = regexp.MustCompile(`^-- HwVersion\s+(\w+)`)

	// CLU definition: CLU212345678 = OBJECT:new(0, 0xC0A80003) or OBJECT:new(0, 0xC0A80003, "CLU212345678")
	reCLUDef = regexp.MustCompile(`^(CLU\d+)\s*=\s*OBJECT:new\s*\(\s*0\s*,\s*(0x[0-9A-Fa-f]+)(?:\s*,\s*"[^"]*")?\s*\)`)

	// GATE CLU definition: CLU511000022 = GATE:new(5000, 0xC0A80004, 0xFFFFFF00, 0xC0A80001, "", "")
	reGateDef = regexp.MustCompile(`^(CLU\d+)\s*=\s*GATE:new\s*\(\s*5000\s*,\s*(0x[0-9A-Fa-f]+)\s*,`)

	// CLU name comment: -- NAME_CLU DOM20=CLU212345678
	reCLUName = regexp.MustCompile(`^-- NAME_CLU\s+(\w+)=(CLU\d+)`)

	// CLU reference: CLU_0d1cf150 = OBJECT:new(1, 0xC0A80002) or OBJECT:new(1, 0xC0A80002, "CLU_0d1cf150")
	reCLURef = regexp.MustCompile(`^(CLU_?\w+)\s*=\s*OBJECT:new\s*\(\s*1\s*,\s*(0x[0-9A-Fa-f]+)(?:\s*,\s*"[^"]*")?\s*\)`)

	// Module definition: mm_330000027 = OBJECT:new(2, 0x13ab669b, 0x1e, "13ab669b")
	reModule = regexp.MustCompile(`^(mm_\d+)\s*=\s*OBJECT:new\s*\(\s*2\s*,\s*(0x[0-9A-Fa-f]+)\s*,\s*(0x[0-9A-Fa-f]+)\s*,\s*"(\w+)"\s*\)`)

	// IO object: DOU5048 = OBJECT:new(4, mm_330000027, 0) or OBJECT:new(4, mm_330000027, 0, "DOU5048")
	// Any module-attached object matches: the `mm_<serial>` 2nd argument uniquely identifies
	// IO objects (DOUT/DIN/DIMM/ANA/ROLLER/ONE_WIRE/PANELSENS*/HUMIDITY/...). The class number
	// drives behaviour; unsupported classes are parsed but simply not exposed.
	reIOObject = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9_]*)\s*=\s*OBJECT:new\s*\(\s*(\d+)\s*,\s*(mm_\d+)\s*,\s*(\d+)(?:\s*,\s*"[^"]*")?\s*\)`)

	// Peripheral object: THE0855 = OBJECT:new(18, "THE0855")
	// Thermostats attached directly to the CLU (no module, no index). Captured here,
	// but not yet exposed — a thermostat is a climate device (see roadmap).
	rePeripheryObject = regexp.MustCompile(`^\s*(THE\w*)\s*=\s*OBJECT:new\s*\(\s*(\d+)\s*,\s*"[^"]*"\s*\)`)

	// IO name comment: -- NAME_IO P1_DACHOWE_BOK=DOU5048
	reIOName = regexp.MustCompile(`^-- NAME_IO\s+(\S+)=(\w+)`)

	// Peripheral name comment: -- NAME_PERIPHERY aSalon=THE0855
	rePeripheryName = regexp.MustCompile(`^-- NAME_PERIPHERY\s+(\S+)=(\w+)`)

	// Event handler: DIN1473:add_event(2, EventsFor_DIN1473_2)
	reEventAdd = regexp.MustCompile(`^(\w+):add_event\s*\(\s*(\d+)\s*,\s*(\w+)\s*\)`)
)

// Parse parses an om.lua file content and returns structured data
func Parse(content string) (*ParsedOM, error) {
	result := NewParsedOM()
	lines := strings.Split(content, "\n")

	// Temporary map for IO names (collected from comments)
	ioNames := make(map[string]string)

	for i, line := range lines {
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Parse header
		if matches := reFwType.FindStringSubmatch(line); matches != nil {
			result.CLU.FwType = matches[1]
			continue
		}
		if matches := reFwVersion.FindStringSubmatch(line); matches != nil {
			result.CLU.FwVersion = matches[1]
			continue
		}
		if matches := reHwType.FindStringSubmatch(line); matches != nil {
			result.CLU.HwType = matches[1]
			continue
		}
		if matches := reHwVersion.FindStringSubmatch(line); matches != nil {
			result.CLU.HwVersion = matches[1]
			continue
		}

		// Parse CLU definition
		if matches := reCLUDef.FindStringSubmatch(line); matches != nil {
			result.CLU.ID = matches[1]
			ip, _ := parseHexUint32(matches[2])
			result.CLU.IP = ip
			continue
		}

		// Parse GATE CLU definition
		if matches := reGateDef.FindStringSubmatch(line); matches != nil {
			result.CLU.ID = matches[1]
			ip, _ := parseHexUint32(matches[2])
			result.CLU.IP = ip
			result.CLU.IsGate = true
			continue
		}

		// Parse CLU name
		if matches := reCLUName.FindStringSubmatch(line); matches != nil {
			if matches[2] == result.CLU.ID {
				result.CLU.Name = matches[1]
			}
			continue
		}

		// Parse CLU reference
		if matches := reCLURef.FindStringSubmatch(line); matches != nil {
			ip, _ := parseHexUint32(matches[2])
			result.References = append(result.References, CLUReference{
				ID: matches[1],
				IP: ip,
			})
			continue
		}

		// Parse module
		if matches := reModule.FindStringSubmatch(line); matches != nil {
			addr, _ := parseHexUint32(matches[2])
			modType, _ := parseHexInt(matches[3])
			result.Modules = append(result.Modules, Module{
				ID:         matches[1],
				Address:    addr,
				ModuleType: modType,
				Serial:     matches[4],
			})
			continue
		}

		// Parse IO name comment (collect for later)
		if matches := reIOName.FindStringSubmatch(line); matches != nil {
			ioNames[matches[2]] = matches[1]
			continue
		}

		// Parse peripheral name comment (collect for later)
		if matches := rePeripheryName.FindStringSubmatch(line); matches != nil {
			ioNames[matches[2]] = matches[1]
			continue
		}

		// Parse IO object
		if matches := reIOObject.FindStringSubmatch(line); matches != nil {
			classInt, err := strconv.Atoi(matches[2])
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid class: %s", i+1, matches[2])
			}
			index, err := strconv.Atoi(matches[4])
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid index: %s", i+1, matches[4])
			}

			obj := GrentonObject{
				ID:       matches[1],
				Class:    ObjectClass(classInt),
				ModuleID: matches[3],
				Index:    index,
			}
			result.Objects = append(result.Objects, obj)
			result.ObjectMap[obj.ID] = obj
			continue
		}

		// Parse peripheral object (thermometer attached directly to CLU, no module)
		if matches := rePeripheryObject.FindStringSubmatch(line); matches != nil {
			classInt, err := strconv.Atoi(matches[2])
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid class: %s", i+1, matches[2])
			}

			obj := GrentonObject{
				ID:    matches[1],
				Class: ObjectClass(classInt),
			}
			result.Objects = append(result.Objects, obj)
			result.ObjectMap[obj.ID] = obj
			continue
		}

		// Parse event handler
		if matches := reEventAdd.FindStringSubmatch(line); matches != nil {
			eventIndex, _ := strconv.Atoi(matches[2])
			result.Events = append(result.Events, EventHandler{
				ObjectID:   matches[1],
				EventIndex: eventIndex,
				Handler:    matches[3],
			})
			continue
		}
	}

	// Apply collected IO names to objects
	for i, obj := range result.Objects {
		if name, ok := ioNames[obj.ID]; ok {
			result.Objects[i].Name = name
			// Update map too
			obj.Name = name
			result.ObjectMap[obj.ID] = obj
		}
	}

	return result, nil
}

// parseHexUint32 parses a hex string like "0xC0A80003" to uint32
func parseHexUint32(s string) (uint32, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	val, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, err
	}
	return uint32(val), nil
}

// parseHexInt parses a hex string to int
func parseHexInt(s string) (int, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	val, err := strconv.ParseInt(s, 16, 32)
	if err != nil {
		return 0, err
	}
	return int(val), nil
}

// IPToString converts a uint32 IP to dotted string
func IPToString(ip uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		(ip>>24)&0xFF,
		(ip>>16)&0xFF,
		(ip>>8)&0xFF,
		ip&0xFF,
	)
}
