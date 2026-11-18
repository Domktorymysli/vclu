package omproject

import (
	"os"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

func interfaceProject(t *testing.T, extra map[string]string) *Project {
	t.Helper()
	data, err := os.ReadFile("testdata/mygrenton.json")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"system.xml":             twoCLUs,
		"properties.xml":         properties,
		"scripts/Home/setup.lua": "",
		"nodes/173/data.json":    string(data),
		"nodes/173/other.json":   "{}",
	}
	for k, v := range extra {
		files[k] = v
	}
	p, err := Read(projectZip(t, files))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func byID(ws []dashspec.Widget) map[string]dashspec.Widget {
	m := map[string]dashspec.Widget{}
	for _, w := range ws {
		m[w.ID] = w
	}
	return m
}

func TestReadInterface(t *testing.T) {
	p := interfaceProject(t, nil)
	if len(p.Interfaces) != 1 {
		t.Fatalf("%d interfaces, warnings %q", len(p.Interfaces), p.Warnings)
	}
	in := p.Interfaces[0]
	if in.ID != "0a1b2c3d-0000-4000-8000-000000000001" || in.Label != `Home "panel"` || in.Source != "myGrenton" || len(in.Pages) != 2 {
		t.Fatalf("%+v", in)
	}
	hall, climate := in.Pages[0], in.Pages[1]
	if hall.ID != "1700000000001" || len(hall.Widgets) != 11 || len(hall.LeftOut) != 0 {
		t.Errorf("hall: %d widgets, left out %q", len(hall.Widgets), hall.LeftOut)
	}
	h := byID(hall.Widgets)
	if hall.Widgets[0].Kind != dashspec.Group || hall.Widgets[0].Label != "Lights" {
		t.Errorf("header %+v", hall.Widgets[0])
	}
	if w := h["mg2"]; w.Kind != dashspec.Toggle || *w.Read.Feature != 0 || w.Read.Object != "DOU0001" ||
		w.On.Op != dashspec.SetFeature || w.On.Value != 1.0 || w.Off.Value != 0.0 {
		t.Errorf("toggle %+v", w)
	}
	if w := h["mg4"]; w.Kind != dashspec.Status || w.Read.Variable != "setpoint" || *w.Decimals != 1 || w.Unit != "°C" {
		t.Errorf("variable %+v", w)
	}
	if w := h["mg6"]; w.OnText != "Open" || w.OffText != "Closed" || h["mg6_click"].Do.Op != dashspec.Execute || h["mg6_click"].Do.Value != 3000.0 {
		t.Errorf("contact %+v %+v", w, h["mg6_click"])
	}
	if w := h["mg7"]; w.Kind != dashspec.Button || w.Do.Script != "setup" || len(w.Do.Args) != 2 || w.Do.Args[0] != "evening" || w.Do.Args[1] != 2.0 {
		t.Errorf("scene %+v", w.Do)
	}
	if a := h["mg8_1"].Do.Args; len(a) != 2 || a[1] != -1.0 || h["mg8_2"].Label != "Down" || len(h["mg8_2"].Do.Args) != 0 {
		t.Errorf("double %+v %+v", h["mg8_1"], h["mg8_2"])
	}
	if w := h["mg9"]; w.Kind != dashspec.Slider || !w.Set.Input || *w.Min != 0 || *w.Max != 100 {
		t.Errorf("slider %+v", w)
	}

	c := byID(climate.Widgets)
	if w := c["mg20"]; w.Kind != dashspec.Thermostat || *w.Current.Feature != 14 || !w.Set.Input || w.State == nil || *w.Max != 30 {
		t.Errorf("thermostat %+v", w)
	}
	// numbers where OM writes strings
	if w := c["mg28"]; w.Kind != dashspec.Toggle || w.On.Value != 1.0 || *w.Read.Feature != 0 {
		t.Errorf("numbers %+v", w)
	}
	// a second widget with the same id gets one of its own; a read of the CLU itself is kept
	if w := c["mg28_2"]; w.Read == nil || w.Read.Object != "" || *w.Read.Feature != 9 {
		t.Errorf("same id %+v", w)
	}
	if w := c["mg27"]; w.Label != "\"); os.exit(1) --]] \\ \x01" {
		t.Errorf("label %q", w.Label)
	}
	if len(climate.Widgets) != 4 {
		t.Errorf("climate: %d widgets %+v", len(climate.Widgets), climate.Widgets)
	}
	left := strings.Join(climate.LeftOut, "\n")
	for _, want := range []string{
		`THERMOSTAT "Heating": mode and schedule are not shown`,
		`SCHEDULER "Week": schedulers have no vclu widget yet`,
		`CLU221000002.DOU0009 is not in the project`,
		`script setup: arguments "1); os.exit(1" are not numbers and plain strings`,
		`user feature "x;os.exit(1)" is no Lua name`,
		`is no object ID`,
		`commands to the CLU itself are left out`,
		`script nosuch is not on CLU221000002`,
		`GAUGE_3D "Future": widget type not supported`,
	} {
		if !strings.Contains(left, want) {
			t.Errorf("left out has no %q:\n%s", want, left)
		}
	}
	// every widget kept is one the generator writes
	for _, pg := range in.Pages {
		for _, w := range pg.Widgets {
			if err := w.Check(); err != nil {
				t.Errorf("%s: %v", w.ID, err)
			}
		}
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

// An interface or a page id seen before is left out: they name files and dashboards.
func TestReadInterfaceIDsOnce(t *testing.T) {
	data, _ := os.ReadFile("testdata/mygrenton.json")
	p := interfaceProject(t, map[string]string{"nodes/174/data.json": strings.Replace(string(data), "0a1b2c3d", "0A1B2C3D", 1)})
	w := strings.Join(p.Warnings, "\n")
	if len(p.Interfaces) != 1 || !strings.Contains(w, "is used twice, left out the second time") {
		t.Errorf("%d interfaces, warnings %q", len(p.Interfaces), w)
	}
	p = interfaceProject(t, map[string]string{"nodes/174/data.json": strings.Replace(string(data), "0a1b2c3d", "ffffffff", 1)})
	if len(p.Interfaces) != 2 || len(p.Interfaces[1].Pages) != 0 || !strings.Contains(strings.Join(p.Warnings, "\n"), "one already used") {
		t.Errorf("%+v %q", p.Interfaces, p.Warnings)
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
