package omparser

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// reSetCall finds the object of every `<ID>:set(` on a line.
var reSetCall = regexp.MustCompile(`([A-Za-z][A-Za-z0-9_]*)\s*:\s*set\s*\(`)

// reGateDefStart is the start of a GATE's own definition, whose arguments after the
// address, mask and gateway are its Wi-Fi network and password:
// CLU511000022 = GATE:new(5000, 0xC0A80004, 0xFFFFFF00, 0xC0A80001, "ssid", "password")
var reGateDefStart = regexp.MustCompile(`^\s*[A-Za-z_][A-Za-z0-9_]*\s*=\s*GATE:new\s*\(\s*5000\s*,`)

// blankGateWiFi gives a GATE definition with every string argument after the first four
// (5000, address, mask, gateway) as "" and nothing after its closing parenthesis but
// spaces; ok is false when the line is no GATE definition.
func blankGateWiFi(line string) (string, bool) {
	start := reGateDefStart.FindStringIndex(line)
	if start == nil {
		return line, false
	}
	open := strings.Index(line, "(")
	args := []string{}
	var cur strings.Builder
	end := -1
	for i := open + 1; i < len(line) && end < 0; i++ {
		c := line[i]
		switch {
		case c == '"' || c == '\'':
			// a quoted string, its escapes included
			cur.WriteByte(c)
			for i++; i < len(line); i++ {
				cur.WriteByte(line[i])
				if line[i] == '\\' && i+1 < len(line) {
					i++
					cur.WriteByte(line[i])
				} else if line[i] == c {
					break
				}
			}
		case c == '[' && strings.HasPrefix(line[i:], "[["):
			j := strings.Index(line[i+2:], "]]")
			if j < 0 {
				cur.WriteString(line[i:])
				i = len(line)
			} else {
				cur.WriteString(line[i : i+2+j+2])
				i += 2 + j + 1
			}
		case c == ',':
			args = append(args, strings.TrimSpace(cur.String()))
			cur.Reset()
		case c == ')':
			end = i
		default:
			cur.WriteByte(c)
		}
	}
	args = append(args, strings.TrimSpace(cur.String()))
	for i := 4; i < len(args); i++ {
		if strings.ContainsAny(args[i], "\"'[") {
			args[i] = `""`
		}
	}
	// only the line end is kept after the call: a comment there could name the network
	tail := ""
	if end >= 0 {
		rest := line[end+1:]
		tail = rest[len(strings.TrimRight(rest, " \t\r")):]
	}
	return line[:open+1] + strings.Join(args, ", ") + ")" + tail, true
}

// StripStoredGateSettings runs StripGateSettings over the om_*.lua copies kept in
// importsDir and rewrites those it changes, for copies kept before vCLU removed the
// settings. It returns how many files and lines it changed; nothing of a line is logged.
func StripStoredGateSettings(importsDir string) (files, lines int, err error) {
	paths, err := filepath.Glob(filepath.Join(importsDir, "om_*.lua"))
	if err != nil {
		return 0, 0, err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return files, lines, err
		}
		out, n := StripGateSettings(string(data))
		if n == 0 {
			continue
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(out), 0644); err != nil {
			return files, lines, err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return files, lines, err
		}
		files++
		lines += n
	}
	return files, lines, nil
}

// StripGateSettings removes from an om.lua every line that sets a feature of a GATE's own
// objects (`SAT0001 = GATE:new(100, "SAT0001")` ... `SAT0001:set(2, "...")`), and returns
// how many lines it removed or changed. A GATE Alarm's om.lua sets the panel's address, the
// admin and user passwords and the encryption key there; vCLU needs none of them, so the copy
// it keeps of the file has none. A GATE's own definition loses its Wi-Fi network and password
// the same way. Nothing of a removed line is returned or logged. A GATE MODBUS's Modbus
// objects keep their settings (register, line, type): they are no secrets, and the import
// tells the objects' kinds from them.
func StripGateSettings(content string) (string, int) {
	lines := strings.Split(content, "\n")
	gate := make(map[string]bool)
	removed := 0
	var info CLUInfo
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if m := reFwType.FindStringSubmatch(line); m != nil {
			info.FwType = m[1]
		} else if m := reHwType.FindStringSubmatch(line); m != nil {
			info.HwType = m[1]
		}
	}
	modbus := info.IsGateModbus()
	for i, line := range lines {
		if m := reGateObject.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			class, _ := strconv.Atoi(m[2])
			if !modbus || !ObjectClass(class).IsModbus() {
				gate[m[1]] = true
			}
		}
		if blank, ok := blankGateWiFi(line); ok && blank != line {
			lines[i] = blank
			removed++
		}
	}
	if len(gate) == 0 {
		if removed == 0 {
			return content, 0
		}
		return strings.Join(lines, "\n"), removed
	}
	kept := lines[:0:0]
	for _, line := range lines {
		drop := false
		for _, m := range reSetCall.FindAllStringSubmatch(line, -1) {
			if gate[m[1]] {
				drop = true
				break
			}
		}
		if drop {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return content, 0
	}
	return strings.Join(kept, "\n"), removed
}
