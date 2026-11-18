package omproject

import (
	"os"
	"strings"
	"testing"
)

func interfaceProject(t *testing.T) *Project {
	t.Helper()
	data, err := os.ReadFile("testdata/mygrenton.json")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Read(projectZip(t, map[string]string{
		"system.xml":             twoCLUs,
		"properties.xml":         properties,
		"scripts/Home/setup.lua": "",
		"nodes/173/data.json":    string(data),
		"nodes/173/other.json":   "{}",
	}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadInterface(t *testing.T) {
	p := interfaceProject(t)
	if len(p.Interfaces) != 1 {
		t.Fatalf("%d interfaces, warnings %q", len(p.Interfaces), p.Warnings)
	}
	in := p.Interfaces[0]
	if in.ID != "0a1b2c3d-0000-4000-8000-000000000001" || in.Label != `Home "panel"` || len(in.Pages) != 2 {
		t.Fatalf("%+v", in)
	}
	hall, climate := in.Pages[0], in.Pages[1]
	if hall.ID != "1700000000001" || hall.Label != "Hall" || len(hall.Widgets) != 9 || len(hall.LeftOut) != 0 {
		t.Errorf("hall: %d widgets, left out %q", len(hall.Widgets), hall.LeftOut)
	}
	// the thermostat and the text whose label is Lua code; the rest lost their calls
	if len(climate.Widgets) != 2 || climate.Widgets[1]["label"] != "\"); os.exit(1) --]] \\ \x01" {
		t.Errorf("climate: %d widgets", len(climate.Widgets))
	}
	left := strings.Join(climate.LeftOut, "\n")
	for _, want := range []string{
		`SCHEDULER "Week": schedulers have no vclu widget yet`,
		`CLU221000002.DOU0009 is not in the project`,
		`script setup: arguments "1); os.exit(1" are not numbers and plain strings`,
		`user feature "x;os.exit(1)" is no Lua name`,
		`is no object ID`,
		`GAUGE_3D "Future": widget type not supported`,
		`ON_OFF "Gone": no call left, left out`,
	} {
		if !strings.Contains(left, want) {
			t.Errorf("left out has no %q:\n%s", want, left)
		}
	}

	// calls, checked and typed
	on := hall.Widgets[1]["buttonBistable"].(map[string]any)
	if c := on["actionOn"].(*Call); c.CallType != "PSEUDO_METHOD" || c.Index != 0 || c.Value != 1.0 || c.Type != "ON" {
		t.Errorf("actionOn %+v", c)
	}
	if _, ok := on["image"]; ok {
		t.Error("image kept")
	}
	if c := hall.Widgets[3]["value"].(*Call); c.CallType != "VARIABLE" || c.Index != "setpoint" {
		t.Errorf("variable %+v", c)
	}
	scene := hall.Widgets[6]["buttonMonostable"].(map[string]any)["actionClick"].(*Call)
	if len(scene.Args) != 2 || scene.Args[0] != "evening" || scene.Args[1] != 2.0 || scene.Value != nil {
		t.Errorf("script %+v", scene)
	}
	double := hall.Widgets[7]
	if a := double["buttonMonostableFirst"].(map[string]any)["actionClick"].(*Call).Args; len(a) != 2 || a[1] != -1.0 {
		t.Errorf("script args %v", a)
	}
	if a := double["buttonMonostableSecond"].(map[string]any)["actionClick"].(*Call).Args; len(a) != 0 {
		t.Errorf("no args %v", a)
	}
	slider := hall.Widgets[8]["value"].(map[string]any)["actionClick"].(*Call)
	if slider.Value != "$value$" {
		t.Errorf("slider %+v", slider)
	}
	if m := hall.Widgets[5]["object"].(map[string]any)["clickAction"].(*Call); m.CallType != "METHOD" || m.Index != 2 || m.Value != 3000.0 {
		t.Errorf("method %+v", m)
	}
}

func TestScriptArgs(t *testing.T) {
	for in, want := range map[string]int{`1,-1`: 2, `"on","2"`: 2, ` 1 , "a b" `: 2, ``: 0, `0.5`: 1} {
		if a, err := scriptArgs(in); err != nil || len(a) != want {
			t.Errorf("%q: %v %v", in, a, err)
		}
	}
	for _, in := range []string{`1,`, `x`, `"a\"b"`, `"a",os.exit(1)`, `1 2`, `"a"..b`, "\"a\nb\"", `{}`} {
		if a, err := scriptArgs(in); err == nil {
			t.Errorf("%q taken: %v", in, a)
		}
	}
}

func TestReadInterfaceNoInterface(t *testing.T) {
	p, err := Read(projectZip(t, map[string]string{"system.xml": twoCLUs, "nodes/1/data.json": `{"id":"../x","pages":[]}`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Interfaces) != 0 || !strings.Contains(strings.Join(p.Warnings, "\n"), `interface id "../x" is no id`) {
		t.Errorf("%+v %q", p.Interfaces, p.Warnings)
	}
}
