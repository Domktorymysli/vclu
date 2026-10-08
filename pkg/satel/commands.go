package satel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// State read commands (response = same cmd byte + bitmask payload).
const (
	cmdZonesViolated  byte = 0x00
	cmdZonesTamper    byte = 0x01
	cmdZonesAlarm     byte = 0x02
	cmdZonesBypass    byte = 0x06
	cmdPartsArmed     byte = 0x0A // "really armed" (mode 0)
	cmdPartsEntryTime byte = 0x0E
	cmdPartsExitTime  byte = 0x0F // exit time > 10s
	cmdPartsAlarm     byte = 0x13
	cmdPartsFireAlarm byte = 0x14
	cmdRTCStatus      byte = 0x1A // also used as keepalive probe
	cmdOutputsState   byte = 0x17
	cmdModuleVersion  byte = 0x7C
	cmdZoneTemp       byte = 0x7D
	cmdPanelVersion   byte = 0x7E
	cmdReadDeviceName byte = 0xEE
)

// Control commands (payload = 8-byte user code + bitmask; response = 0xEF + result).
const (
	cmdArmMode0      byte = 0x80 // 0x80-0x83: arm in mode 0-3
	cmdDisarm        byte = 0x84
	cmdClearAlarm    byte = 0x85
	cmdOutputsOn     byte = 0x88
	cmdOutputsOff    byte = 0x89
	cmdForceArmMode0 byte = 0xA0 // 0xA0-0xA3: force arm in mode 0-3
	cmdResult        byte = 0xEF
)

// Device type codes for cmdReadDeviceName.
const (
	deviceTypePartition byte = 0
	deviceTypeZone      byte = 1
	deviceTypeOutput    byte = 4
)

// resultMessages maps 0xEF result codes to human-readable messages.
var resultMessages = map[byte]string{
	0x00: "success",
	0x01: "requesting user code not found",
	0x02: "no access",
	0x03: "selected user does not exist",
	0x04: "selected user already exists",
	0x05: "wrong code or code already exists",
	0x06: "telephone code already exists",
	0x07: "changed code is the same",
	0x08: "other error",
	0x11: "cannot arm, but can use force arm",
	0x12: "cannot arm",
	0xFF: "command accepted",
}

// resultMessage returns a description for a 0xEF result code.
func resultMessage(code byte) string {
	if msg, ok := resultMessages[code]; ok {
		return msg
	}
	return fmt.Sprintf("error 0x%02X", code)
}

// resultOK reports whether a 0xEF result code means the command was accepted.
// 0xFF means "accepted, will be processed" and is treated as success.
func resultOK(code byte) bool {
	return code == 0x00 || code == 0xFF
}

// EncodeUserCode packs up to 16 PIN digits into 8 bytes: BCD nibbles
// big-endian (first digit = high nibble of byte 0), 0xF padding nibble,
// fully unused bytes set to 0xFF.
func EncodeUserCode(code string) ([8]byte, error) {
	var out [8]byte
	if len(code) > 16 {
		return out, fmt.Errorf("satel: user code longer than 16 digits")
	}
	for i := 0; i < 16; i++ {
		switch {
		case i < len(code):
			d := code[i]
			if d < '0' || d > '9' {
				return out, fmt.Errorf("satel: user code must contain only digits")
			}
			if i%2 == 0 {
				out[i/2] = (d - '0') << 4
			} else {
				out[i/2] |= d - '0'
			}
		case i%2 == 0:
			out[i/2] = 0xFF
		case i == len(code):
			out[i/2] |= 0x0F
		}
	}
	return out, nil
}

// numbersToMask builds a little-endian bitmask of size bytes with the given
// 1-based object numbers set (byte i, bit j (LSB=0) = number i*8+j+1).
func numbersToMask(size int, numbers ...int) ([]byte, error) {
	mask := make([]byte, size)
	for _, n := range numbers {
		if n < 1 || n > size*8 {
			return nil, fmt.Errorf("satel: number %d out of range 1-%d", n, size*8)
		}
		mask[(n-1)/8] |= 1 << uint((n-1)%8)
	}
	return mask, nil
}

// maskBit reports whether 1-based object number n is set in the bitmask.
func maskBit(mask []byte, n int) bool {
	idx := (n - 1) / 8
	if n < 1 || idx >= len(mask) {
		return false
	}
	return mask[idx]&(1<<uint((n-1)%8)) != 0
}

// diffMask returns the 1-based numbers whose bits differ between old and new.
func diffMask(oldMask, newMask []byte) []int {
	var changed []int
	maxLen := len(oldMask)
	if len(newMask) > maxLen {
		maxLen = len(newMask)
	}
	for i := 0; i < maxLen; i++ {
		var o, n byte
		if i < len(oldMask) {
			o = oldMask[i]
		}
		if i < len(newMask) {
			n = newMask[i]
		}
		d := o ^ n
		for j := 0; d != 0 && j < 8; j++ {
			if d&(1<<uint(j)) != 0 {
				changed = append(changed, i*8+j+1)
			}
		}
	}
	return changed
}

// ParseRange parses a range spec like "1-16,20,25" into a sorted, deduplicated
// list of 1-based numbers. Empty string returns an empty list.
func ParseRange(spec string) ([]int, error) {
	seen := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			a, err1 := strconv.Atoi(strings.TrimSpace(lo))
			b, err2 := strconv.Atoi(strings.TrimSpace(hi))
			if err1 != nil || err2 != nil || a < 1 || b < a {
				return nil, fmt.Errorf("satel: invalid range %q", part)
			}
			for n := a; n <= b; n++ {
				seen[n] = true
			}
		} else {
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("satel: invalid number %q", part)
			}
			seen[n] = true
		}
	}
	out := make([]int, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}
