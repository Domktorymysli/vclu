package omproject

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/omparser"
)

// Every alarm object of a GATE Alarm in the project reaches its om.lua with the class
// Object Manager gives it, and omparser reads them all back.
func TestOMLuaGateAlarmObjects(t *testing.T) {
	types := []struct {
		id, typ string
		class   omparser.ObjectClass
	}{
		{"SAT0001", "Satel", omparser.ClassGateSatel},
		{"SAT0002", "SatelZone", omparser.ClassGateSatelZone},
		{"SAT0003", "SatelInput", omparser.ClassGateSatelInput},
		{"SAT0004", "SatelOutput", omparser.ClassGateSatelOutput},
		{"JAB0001", "Jablotron", omparser.ClassGateJablotron},
		{"JAB0002", "JablotronSection", omparser.ClassGateJablotronSection},
		{"JAB0003", "JablotronOutput", omparser.ClassGateJablotronOutput},
		{"JAB0004", "JablotronDevice", omparser.ClassGateJablotronDevice},
	}
	var list strings.Builder
	for i, ty := range types {
		fmt.Fprintf(&list, `<HardwarePeripheryCLU id="%d"><name>obj_%s</name><nameOnCLU>%s</nameOnCLU><typeName>%s</typeName><isRemoved>false</isRemoved></HardwarePeripheryCLU>`,
			900+i, ty.id, ty.id, ty.typ)
	}
	sys := strings.Replace(twoCLUs,
		`<HardwarePeripheryCLU id="17"><name>alarm_zone</name><nameOnCLU>SAT0001</nameOnCLU><typeName>SatelZone</typeName><isRemoved>false</isRemoved></HardwarePeripheryCLU>`,
		list.String(), 1)
	p, err := Read(projectZip(t, map[string]string{"system.xml": sys, "properties.xml": properties,
		"scripts/Home/setup.lua": "\n"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 0 {
		t.Errorf("warnings %q", p.Warnings)
	}
	parsed, err := omparser.Parse(p.OMLua(p.CLUs[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.CLU.IsGateAlarm() || len(parsed.Objects) != len(types) || len(parsed.Warnings) != 0 {
		t.Fatalf("CLU %+v objects %+v warnings %q", parsed.CLU, parsed.Objects, parsed.Warnings)
	}
	for _, ty := range types {
		if o := parsed.ObjectMap[ty.id]; o.Class != ty.class || o.Name != "obj_"+ty.id {
			t.Errorf("%s (%s): %+v", ty.id, ty.typ, o)
		}
	}
}

// What a script or binding writes into a GATE Alarm's passwords and codes stays out of the
// agent's files (ai/om); the rest of the script and binding stays.
func TestDocsRedactGateSecrets(t *testing.T) {
	sys := strings.Replace(twoCLUs, `<commands class="linked-list" id="35"/>`,
		`<commands class="linked-list" id="35"><string>Gate-&gt;alarm_zone-&gt;SetUserPassword("MARKER-1")</string><string>Home-&gt;hall_light-&gt;SwitchOff(0)</string></commands>`, 1)
	p, err := Read(projectZip(t, map[string]string{
		"system.xml": sys, "properties.xml": properties,
		"scripts/Home/setup.lua": "Gate->alarm_zone->SetUserPassword(\"MARKER-2\")\n" +
			"SAT0001:set(2, 'MARKER-3')\n" +
			"local pin = \"MARKER-4\" -- UserPassword of the zone\n" +
			"Gate->alarm_zone->ArmZone()\nHome->hall_light->SwitchOn(0)\nlocal s = \"keep me\"\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	var all strings.Builder
	for _, d := range p.Docs() {
		all.WriteString(d)
	}
	out := all.String()
	if strings.Contains(out, "MARKER") {
		t.Errorf("a secret in the docs:\n%s", out)
	}
	for _, want := range []string{
		`Gate->alarm_zone->SetUserPassword("(removed by vclu)")`, `SAT0001:set(2, "(removed by vclu)")`,
		"Gate->alarm_zone->ArmZone()", "Home->hall_light->SwitchOn(0)", `local s = "keep me"`,
		"Home->hall_light->SwitchOff(0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in the docs:\n%s", want, out)
		}
	}
}

func TestRedactGate(t *testing.T) {
	gate := map[string]bool{"SAT0001": true, "zone": true}
	for in, want := range map[string]string{
		`zone->SetUserPassword(code("x", 1))`:     `zone->SetUserPassword("(removed by vclu)")`,
		`SAT0001 : set (4, "a)b")`:                `SAT0001 : set (4, "(removed by vclu)")`,
		`DOU0001:set(0, 1)`:                       `DOU0001:set(0, 1)`,
		`zone->SetNr(3)`:                          `zone->SetNr(3)`,
		`zone->set(0)`:                            `zone->set(0)`,
		`other->SetUserPassword("x")`:             `other->SetUserPassword("(removed by vclu)")`,
		"a(); zone->SetAdminCode(1234); b(\"k\")": "a(); zone->SetAdminCode(\"(removed by vclu)\"); b(\"(removed by vclu)\")", // the line names a secret
	} {
		if got := redactGate(in, gate); got != want {
			t.Errorf("%s: %s, want %s", in, got, want)
		}
	}
	// a project without a GATE Alarm keeps its scripts as they are
	if in := `local pin = "1234" -- UserPassword`; redactGate(in, nil) != in {
		t.Errorf("no GATE: %s", redactGate(in, nil))
	}
}
