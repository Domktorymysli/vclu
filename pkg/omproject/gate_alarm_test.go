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
