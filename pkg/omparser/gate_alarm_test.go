package omparser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The om.lua of a GATE Alarm, lines as Object Manager writes them (anonymised: serials,
// addresses, names and every setting made up; the passwords, codes and key are markers).
const gateAlarmMarker = "MARKER-PASSWORD-123"

var gateAlarmOM = `-- FwType 00000002
-- FwVersion 00000456
-- HwType 00000012
-- HwVersion 00000002
CLU510000001 = GATE:new(5000, 0xC0A80104, 0xFFFFFF00, 0xC0A80101, "", "")
-- NAME_CLU Alarm=CLU510000001
CLU220000002 = OBJECT:new(1, 0xC0A80105, "CLU220000002")
SAT0001 = GATE:new(103, "SAT0001")
-- NAME_PERIPHERY satel_panel=SAT0001
SAT0002 = GATE:new(100, "SAT0002")
-- NAME_PERIPHERY alarm_ground=SAT0002
SAT0003 = GATE:new(101, "SAT0003")
-- NAME_PERIPHERY pir_hall=SAT0003
SAT0004 = GATE:new(102, "SAT0004")
-- NAME_PERIPHERY siren=SAT0004
JAB0001 = GATE:new(150, "JAB0001")
-- NAME_PERIPHERY jablotron=JAB0001
JAB0002 = GATE:new(151, "JAB0002")
-- NAME_PERIPHERY section_house=JAB0002
JAB0003 = GATE:new(152, "JAB0003")
-- NAME_PERIPHERY pg_gate=JAB0003
JAB0004 = GATE:new(153, "JAB0004")
-- NAME_PERIPHERY door_contact=JAB0004
TIM0001 = GATE:new(6, "TIM0001")
-- NAME_PERIPHERY timer=TIM0001
SAT0001:set(2, "192.0.2.77")
SAT0001:set(3, 7094)
SAT0001:set(4, "MARKER-PASSWORD-123")
SAT0001:set(6, 1)
SAT0001:set(7, "MARKER-PASSWORD-123-KEY")
SAT0002:set(1, 1)
SAT0002:set(2, "MARKER-PASSWORD-123-USER")
SAT0003:set(1, 12)
SAT0004:set(1, 3)
SAT0004:set(2, "MARKER-PASSWORD-123-OUT")
JAB0001:set(0, "MARKER-PASSWORD-123-ADMIN")
JAB0001:set(1, 5)
JAB0002:set(1, 2)
JAB0002:set(2, "MARKER-PASSWORD-123-SECTION")
JAB0003:set(1, 4)
JAB0003:set(2, "MARKER-PASSWORD-123-PG")
JAB0004:set(1, 9)
TIM0001:set(0, 1000)
CLU510000001:add_event(0, OnInit)
SAT0002:add_event(1, EventsFor_SAT0002_1)
`

var gateAlarmObjects = map[string]struct {
	class ObjectClass
	name  string
}{
	"SAT0001": {ClassGateSatel, "satel_panel"},
	"SAT0002": {ClassGateSatelZone, "alarm_ground"},
	"SAT0003": {ClassGateSatelInput, "pir_hall"},
	"SAT0004": {ClassGateSatelOutput, "siren"},
	"JAB0001": {ClassGateJablotron, "jablotron"},
	"JAB0002": {ClassGateJablotronSection, "section_house"},
	"JAB0003": {ClassGateJablotronOutput, "pg_gate"},
	"JAB0004": {ClassGateJablotronDevice, "door_contact"},
}

// A GATE Alarm's objects are read with their names; nothing a :set line sets is in the result.
func TestParseGateAlarm(t *testing.T) {
	parsed, err := Parse(gateAlarmOM)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.CLU.IsGate || !parsed.CLU.IsGateAlarm() || len(parsed.Warnings) != 0 {
		t.Errorf("CLU %+v warnings %q", parsed.CLU, parsed.Warnings)
	}
	if len(parsed.Objects) != len(gateAlarmObjects) {
		t.Errorf("objects %+v", parsed.Objects)
	}
	for id, want := range gateAlarmObjects {
		got, ok := parsed.ObjectMap[id]
		if !ok || got.Class != want.class || got.Name != want.name {
			t.Errorf("%s: %+v, want class %d name %s", id, got, want.class, want.name)
		}
	}
	if _, ok := parsed.ObjectMap["TIM0001"]; ok {
		t.Error("the GATE's timer is read; vCLU has no proxy for it")
	}
	js, _ := json.Marshal(parsed)
	if strings.Contains(string(js), gateAlarmMarker) || strings.Contains(string(js), "192.0.2.77") {
		t.Errorf("a setting reached the parsed result: %s", js)
	}
}

// The same classes on another GATE (an HTTP GATE has the same hardware), or on a CLU, are
// something else: left out, with a warning that names the object.
func TestParseGateAlarmClassesElsewhere(t *testing.T) {
	for name, header := range map[string]string{
		"HTTP GATE":        "-- FwType 00000003\n-- HwType 00000012\nCLU510000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, \"\", \"\")\n",
		"Smart Panel WiFi": "-- FwType 00000002\n-- HwType 00000021\nCLU470000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, \"\", \"\")\n",
		"no header at all": "CLU510000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, \"\", \"\")\n",
		"CLU, not a GATE":  "-- FwType 00000003\n-- HwType 00000013\nCLU220000009 = OBJECT:new(0, 0xC0A80109)\n",
		"GATE Alarm, numbers without leading zeros": "",
	} {
		t.Run(name, func(t *testing.T) {
			om := header + "SAT0002 = GATE:new(100, \"SAT0002\")\nREQ0001 = GATE:new(121, \"REQ0001\")\nJAB0004 = GATE:new(153, \"JAB0004\")\n"
			if header == "" {
				om = "-- FwType 2\n-- HwType 12\nCLU510000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, \"\", \"\")\n" + om
			}
			parsed, err := Parse(om)
			if err != nil {
				t.Fatal(err)
			}
			if header == "" {
				// the numbers written without leading zeros are still a GATE Alarm's
				if len(parsed.Objects) != 2 || len(parsed.Warnings) != 0 {
					t.Errorf("objects %+v warnings %q", parsed.Objects, parsed.Warnings)
				}
				return
			}
			if len(parsed.Objects) != 0 {
				t.Errorf("objects %+v", parsed.Objects)
			}
			if len(parsed.Warnings) != 2 || !strings.HasPrefix(parsed.Warnings[0], "SAT0002: class 100") ||
				!strings.HasPrefix(parsed.Warnings[1], "JAB0004: class 153") {
				t.Errorf("warnings %q", parsed.Warnings)
			}
		})
	}
}

// Every :set line of a GATE's objects goes; the rest of the file stays as it was.
func TestStripGateSettings(t *testing.T) {
	om := gateAlarmOM + "ROL0001:set(10, 1500)\n  SAT0002 : set ( 2, \"MARKER-PASSWORD-123-SPACED\")\r\n"
	out, n := StripGateSettings(om)
	if n != 19 {
		t.Errorf("removed %d lines, want 19", n)
	}
	if strings.Contains(out, gateAlarmMarker) || strings.Contains(out, "192.0.2.77") || strings.Contains(out, "TIM0001:set") {
		t.Errorf("settings left:\n%s", out)
	}
	for _, keep := range []string{"ROL0001:set(10, 1500)", `SAT0002 = GATE:new(100, "SAT0002")`,
		"-- NAME_PERIPHERY siren=SAT0004", "SAT0002:add_event(1, EventsFor_SAT0002_1)", "-- HwType 00000012"} {
		if !strings.Contains(out, keep) {
			t.Errorf("%q removed", keep)
		}
	}
	a, _ := Parse(om)
	b, _ := Parse(out)
	if fmt.Sprint(a.Objects) != fmt.Sprint(b.Objects) {
		t.Errorf("objects changed:\n%v\n%v", a.Objects, b.Objects)
	}
	// a GATE's Wi-Fi network and password go too, its address stays
	wifi := "CLU511000009 = GATE:new(5000, 0xC0A80109, 0xFFFFFF00, 0xC0A80101, \"home-net\", \"MARKER-PASSWORD-123-WIFI\")\r\nX = 1\n"
	if out, n := StripGateSettings(wifi); n != 1 || out != "CLU511000009 = GATE:new(5000, 0xC0A80109, 0xFFFFFF00, 0xC0A80101, \"\", \"\")\r\nX = 1\n" {
		t.Errorf("Wi-Fi kept: %d %q", n, out)
	}
	if p, err := Parse(strings.Replace(wifi, "MARKER", "x", 1)); err != nil || p.CLU.ID != "CLU511000009" {
		t.Errorf("GATE not parsed: %v", err)
	}
	if out, _ := StripGateSettings(wifi); func() bool { p, err := Parse(out); return err != nil || p.CLU.ID != "CLU511000009" }() {
		t.Errorf("GATE not parsed after stripping:\n%s", out)
	}

	// whatever the network arguments look like, every string after the first four goes,
	// and nothing after the call but the line end stays
	for in, want := range map[string]string{
		`CLU511000009 = GATE:new(5000, 0xC0A80109, 0xFFFFFF00, 0xC0A80101, "home-net", "MARKER-PASSWORD-123-WIFI") -- MARKER-PASSWORD-123-NOTE`: `CLU511000009 = GATE:new(5000, 0xC0A80109, 0xFFFFFF00, 0xC0A80101, "", "")`,
		`CLU511000009 = GATE:new(5000, 3232235785, 4294967040, 3232235777, "home-net", "MARKER-PASSWORD-123-WIFI")`:                             `CLU511000009 = GATE:new(5000, 3232235785, 4294967040, 3232235777, "", "")`,
		`CLU511000009=GATE:new(5000,0xC0A80109,0x0,0x0,'home, net',"MARKER-PASSWORD-123 \"x\"), y",[[MARKER-PASSWORD-123]])`:                    `CLU511000009=GATE:new(5000, 0xC0A80109, 0x0, 0x0, "", "", "")`,
		`CLU511000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, "home-net", "MARKER-PASSWORD-123-UNCLOSED`:                                         `CLU511000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, "", "")`,
	} {
		if out, n := StripGateSettings(in + "\r\n"); n != 1 || strings.Contains(out, "MARKER") || !strings.HasPrefix(out, want) {
			t.Errorf("%s\n-> %d %q\nwant %q", in, n, out, want)
		}
	}
	// the definition as vCLU writes it stays as it is
	if blank := "CLU511000009 = GATE:new(5000, 0xC0A80109, 0x0, 0x0, \"\", \"\")\n"; func() bool { _, n := StripGateSettings(blank); return n != 0 }() {
		t.Error("a blank GATE definition counted as changed")
	}

	plain := "CLU220000002 = OBJECT:new(0, 0xC0A80105)\nROL0001:set(10, 1500)\n"
	if out, n := StripGateSettings(plain); n != 0 || out != plain {
		t.Errorf("a CLU without GATE objects changed: %d %q", n, out)
	}
}

// The proxies: one per object with its name, registered under <CLU ID>.<object ID>, nothing
// of the settings in them.
func TestGenerateProxyGateAlarm(t *testing.T) {
	parsed, err := Parse(gateAlarmOM)
	if err != nil {
		t.Fatal(err)
	}
	out := GenerateProxy(parsed, DefaultConfig())
	for _, want := range []string{
		`alarm.satel_panel = RemoteGateSatel:new(_clu, "SAT0001", "satel_panel")`,
		`alarm.alarm_ground = RemoteGateSatelZone:new(_clu, "SAT0002", "alarm_ground")`,
		`alarm.pir_hall = RemoteGateSatelInput:new(_clu, "SAT0003", "pir_hall")`,
		`alarm.siren = RemoteGateSatelOutput:new(_clu, "SAT0004", "siren")`,
		`alarm.jablotron = RemoteGateJablotron:new(_clu, "JAB0001", "jablotron")`,
		`alarm.section_house = RemoteGateJablotronSection:new(_clu, "JAB0002", "section_house")`,
		`alarm.pg_gate = RemoteGateJablotronOutput:new(_clu, "JAB0003", "pg_gate")`,
		`alarm.door_contact = RemoteGateJablotronDevice:new(_clu, "JAB0004", "door_contact")`,
		`CLU510000001._omFirmware = { type = "00000002", version = "00000456" }`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s", want)
		}
	}
	for id, o := range gateAlarmObjects {
		if want := fmt.Sprintf("registerObject(\"CLU510000001.%s\", alarm.%s)", id, o.name); !strings.Contains(out, want) {
			t.Errorf("missing %s", want)
		}
	}
	if regexp.MustCompile(`MARKER|192\.0\.2\.77|7094`).MatchString(out) {
		t.Errorf("a setting reached the proxy:\n%s", out)
	}
}

// Copies kept before the settings were removed lose them; clean ones are left untouched.
func TestStripStoredGateSettings(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("om_510000001.lua", gateAlarmOM)
	write("om_220000002.lua", "CLU220000002 = OBJECT:new(0, 0xC0A80105)\n")
	write("proxy_510000001.lua", "SAT0001:set(4, \"MARKER-PASSWORD-123\")\n") // not a copy of om.lua
	files, lines, err := StripStoredGateSettings(dir)
	if err != nil || files != 1 || lines != 18 {
		t.Errorf("files %d lines %d err %v", files, lines, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "om_510000001.lua"))
	if strings.Contains(string(data), gateAlarmMarker) {
		t.Errorf("stored copy:\n%s", data)
	}
	if files, lines, err := StripStoredGateSettings(dir); files != 0 || lines != 0 || err != nil {
		t.Errorf("second run: %d %d %v", files, lines, err)
	}
}
