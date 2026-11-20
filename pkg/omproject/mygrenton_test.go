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
	if in.ID != "0a1b2c3d-0000-4000-8000-000000000001" || in.Label != `Home "panel"` || in.Source != "myGrenton" || len(in.Pages) != 3 {
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
	if w := h["w2"]; w.Kind != dashspec.Toggle || *w.Read.Feature != 0 || w.Read.Object != "DOU0001" ||
		w.On.Op != dashspec.SetFeature || w.On.Value != 1.0 || w.Off.Value != 0.0 {
		t.Errorf("toggle %+v", w)
	}
	if w := h["w4"]; w.Kind != dashspec.Status || w.Read.Variable != "setpoint" || *w.Decimals != 1 || w.Unit != "°C" {
		t.Errorf("variable %+v", w)
	}
	if w := h["w6"]; w.OnText != "Open" || w.OffText != "Closed" || h["w6_click"].Do.Op != dashspec.Execute || h["w6_click"].Do.Value != 3000.0 {
		t.Errorf("contact %+v %+v", w, h["w6_click"])
	}
	if w := h["w7"]; w.Kind != dashspec.Button || w.Do.Script != "setup" || len(w.Do.Args) != 2 || w.Do.Args[0] != "evening" || w.Do.Args[1] != 2.0 {
		t.Errorf("scene %+v", w.Do)
	}
	if a := h["w8_1"].Do.Args; len(a) != 2 || a[1] != -1.0 || h["w8_2"].Label != "Down" || len(h["w8_2"].Do.Args) != 0 {
		t.Errorf("double %+v %+v", h["w8_1"], h["w8_2"])
	}
	if w := h["w9"]; w.Kind != dashspec.Slider || !w.Set.Input || *w.Min != 0 || *w.Max != 100 {
		t.Errorf("slider %+v", w)
	}

	c := byID(climate.Widgets)
	if w := c["w20"]; w.Kind != dashspec.Thermostat || *w.Current.Feature != 14 || !w.Set.Input || w.State == nil || *w.Max != 30 {
		t.Errorf("thermostat %+v", w)
	}
	// numbers where OM writes strings; with only actionOn no toggle: its state and a button
	if w := c["w28"]; w.Kind != dashspec.Status || *w.Read.Feature != 0 {
		t.Errorf("numbers %+v", w)
	}
	if w := c["w28_click"]; w.Kind != dashspec.Button || w.Do.Value != 1.0 {
		t.Errorf("numbers' action %+v", w)
	}
	// a second widget with the same id gets one of its own; a read of the CLU itself is kept
	if w := c["w28_2"]; w.Read == nil || w.Read.Object != "" || *w.Read.Feature != 9 {
		t.Errorf("same id %+v", w)
	}
	if w := c["w27"]; w.Label != "\"); os.exit(1) --]] \\ \x01" {
		t.Errorf("label %q", w.Label)
	}
	if len(climate.Widgets) != 5 {
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
	for _, in := range []string{`1,`, `x`, `"a\"b"`, `"a",os.exit(1)`, `1 2`, `"a"..b`, "\"a\nb\"", `{}`, "1" + strings.Repeat("0", 400)} {
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

// Dimmers, LEDs and roller shutters become toggles, sliders and buttons; units read as OM writes them.
func TestReadInterfaceDimmersAndShutters(t *testing.T) {
	p := interfaceProject(t, nil)
	pg := p.Interfaces[0].Pages[2]
	h := byID(pg.Widgets)
	kinds := map[string]string{}
	for _, w := range pg.Widgets {
		kinds[w.ID] = w.Kind
	}
	want := map[string]string{
		"w40": dashspec.Toggle, "w40_level": dashspec.Slider,
		"w41": dashspec.Toggle, "w41_brightness": dashspec.Slider, "w41_hue": dashspec.Slider, "w41_saturation": dashspec.Slider,
		"w42": dashspec.Button, "w42_position": dashspec.Status,
		"w43": dashspec.Button, "w43_up": dashspec.Button, "w43_down": dashspec.Button, "w43_position": dashspec.Slider, "w43_slats": dashspec.Slider,
		"w44": dashspec.Toggle, "w44_level": dashspec.Slider,
		// one action only (a toggle() of its own): a button, the level shows the value
		"w46": dashspec.Button, "w46_level": dashspec.Status,
	}
	if len(kinds) != len(want) {
		t.Errorf("widgets %v", kinds)
	}
	for id, k := range want {
		if kinds[id] != k {
			t.Errorf("%s is %q, want %q", id, kinds[id], k)
		}
	}
	// 0-1 in %, as OM writes a dimmer's level: a slider of 0-100 that sends 0-1
	if w := h["w40_level"]; w.Label != "Lamp" || *w.Min != 0 || *w.Max != 100 || !w.Percent || w.Unit != "%" || !w.Set.Input || w.Set.Op != dashspec.Execute {
		t.Errorf("dimmer level %+v", w)
	}
	if w := h["w41_brightness"]; *w.Max != 100 || !w.Percent {
		t.Errorf("brightness %+v", w)
	}
	if w := h["w41_hue"]; w.Percent || *w.Max != 360 || w.Unit != "°" {
		t.Errorf("hue %+v", w)
	}
	if w := h["w43_position"]; w.Percent || *w.Max != 100 {
		t.Errorf("position %+v", w)
	}
	// DIMMER_V2 as Object Manager 1.8 writes it: one object, set by feature
	if w := h["w44"]; w.Kind != dashspec.Toggle || w.On.Op != dashspec.SetFeature || w.On.Value != 1.0 || w.Off.Value != 0.0 {
		t.Errorf("dimmer v2 %+v", w)
	}
	if w := h["w44_level"]; w.Label != "Hall" || !w.Percent || *w.Max != 100 || w.Set.Op != dashspec.SetFeature || !w.Set.Input || *w.Read.Feature != 0 {
		t.Errorf("dimmer v2 level %+v", w)
	}
	if w := h["w41_hue"]; w.Label != "Strip (hue)" || *w.Max != 360 || w.Set.Index != 10 {
		t.Errorf("hue %+v", w)
	}
	if w := h["w43_up"]; w.Label != "Terrace ▲" || w.Do.Index != 3 {
		t.Errorf("up %+v", w)
	}
	if len(pg.LeftOut) != 1 || !strings.Contains(pg.LeftOut[0], "LED_CCT") {
		t.Errorf("left out %q", pg.LeftOut)
	}
	if w := byID(p.Interfaces[0].Pages[0].Widgets)["w9"]; w.Unit != "%" {
		t.Errorf("PERCENT reads as %q", w.Unit)
	}
}

// A GATE Alarm's objects only on a GATE Alarm: on another CLU their features are not
// guarded (the panel's passwords), so a widget reading one is left out.
func TestReadInterfaceAlarmObjectOnlyOnGateAlarm(t *testing.T) {
	page := `{"id":"if-1","label":"Home","pages":[{"id":1,"label":"Alarm","widgets":[
		{"id":1,"type":"VALUE_V2","label":"Zone","value":{"callType":"ATTRIBUTE","cluId":"CLU511000001","objectName":"SAT0001","index":"4"}}]}]}`
	for _, tc := range []struct {
		name, hw string
		kept     bool
	}{{"GATE Alarm", "00000012", true}, {"another GATE", "00000013", false}} {
		sys := strings.Replace(twoCLUs, "<hardwareType>00000012</hardwareType>", "<hardwareType>"+tc.hw+"</hardwareType>", 1)
		p, err := Read(projectZip(t, map[string]string{"system.xml": sys, "properties.xml": properties, "nodes/1/data.json": page}))
		if err != nil {
			t.Fatal(err)
		}
		pg := p.Interfaces[0].Pages[0]
		if kept := len(pg.Widgets) == 1; kept != tc.kept {
			t.Errorf("%s: widgets %+v left out %q", tc.name, pg.Widgets, pg.LeftOut)
		}
	}
}
