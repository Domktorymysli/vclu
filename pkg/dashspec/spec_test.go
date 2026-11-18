package dashspec

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func ip(n int) *int { return &n }

func TestCheck(t *testing.T) {
	read := &Read{CLU: "CLU1", Object: "DOU1", Feature: ip(0)}
	set := &Action{Op: SetFeature, CLU: "CLU1", Object: "DOU1", Value: 1.0}
	for name, w := range map[string]Widget{
		"id is code":         {Kind: Status, ID: `a");os.exit(1)--`, Read: read},
		"object is code":     {Kind: Status, ID: "a", Read: &Read{CLU: "CLU1", Object: "DOU1;x", Feature: ip(0)}},
		"object is keyword":  {Kind: Status, ID: "a", Read: &Read{CLU: "CLU1", Object: "end", Feature: ip(0)}},
		"no feature":         {Kind: Status, ID: "a", Read: &Read{CLU: "CLU1", Object: "DOU1"}},
		"variable is code":   {Kind: Status, ID: "a", Read: &Read{CLU: "CLU1", Variable: "x)os.exit("}},
		"CLU is code":        {Kind: Status, ID: "a", Read: &Read{CLU: "CLU1;x", Feature: ip(0)}},
		"command to the CLU": {Kind: Button, ID: "a", Do: &Action{Op: Execute, CLU: "CLU1", Object: "CLU1"}},
		"command, no object": {Kind: Button, ID: "a", Do: &Action{Op: Execute, CLU: "CLU1"}},
		"script is code":     {Kind: Button, ID: "a", Do: &Action{Op: Script, CLU: "CLU1", Script: "a()b"}},
		"script arg table":   {Kind: Button, ID: "a", Do: &Action{Op: Script, CLU: "CLU1", Script: "a", Args: []any{map[string]any{}}}},
		"set without value":  {Kind: Toggle, ID: "a", Read: read, On: &Action{Op: SetFeature, CLU: "CLU1", Object: "DOU1"}},
		"slider no range":    {Kind: Slider, ID: "a", Read: read, Set: &Action{Op: SetFeature, CLU: "CLU1", Object: "DOU1", Input: true}},
		"slider fixed value": {Kind: Slider, ID: "a", Read: read, Set: set, Min: new(float64), Max: new(float64)},
		"button input":       {Kind: Button, ID: "a", Do: &Action{Op: SetFeature, CLU: "CLU1", Object: "DOU1", Input: true}},
		"unknown kind":       {Kind: "gauge", ID: "a", Read: read},
	} {
		if err := w.Check(); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	if err := (&Widget{Kind: Toggle, ID: "w2_1", Read: read, On: set}).Check(); err != nil {
		t.Error(err)
	}
}

// The generated page compiles whatever the labels hold, and writes only widgets that pass.
func TestPageLua(t *testing.T) {
	read := &Read{CLU: "CLU1", Object: "DOU1", Feature: ip(0)}
	labels := []string{`"); os.exit(1) --`, "]] --[[", "a\nb\r\x00c", `\`, "zażółć"}
	p := Page{ID: "7", Label: labels[0]}
	for i, l := range labels {
		p.Widgets = append(p.Widgets, Widget{Kind: Status, ID: "w" + string(rune('a'+i)), Label: l, Read: read, Unit: l})
	}
	p.Widgets = append(p.Widgets, Widget{Kind: Status, ID: "bad", Label: "x", Read: &Read{CLU: "CLU1;os.exit(1)", Feature: ip(0)}})
	code := PageLua(Interface{ID: "i", Label: labels[1], Source: "myGrenton"}, p)
	if _, err := lua.NewState().LoadString(code); err != nil {
		t.Fatalf("%v\n%s", err, code)
	}
	if strings.Contains(code, "os.exit(1)\")") || strings.Contains(code, `"bad"`) {
		t.Errorf("the bad widget was written:\n%s", code)
	}
	// the labels read back as they were
	L := lua.NewState()
	if err := L.DoString(`LABELS = {}
		Wire = setmetatable({}, { __index = function() return function() return function() end end end })
		local b = {}
		function b:name() return self end
		function b:tabGroup() return self end
		function b:section() return self end
		function b:status(id, label) LABELS[#LABELS + 1] = label return self end
		function b:build() end
		Dashboard = { new = function() return b end }` + "\n" + code); err != nil {
		t.Fatal(err)
	}
	got := L.GetGlobal("LABELS").(*lua.LTable)
	for i, l := range labels {
		if v := got.RawGetInt(i + 1).String(); v != l {
			t.Errorf("label %d reads back %q, want %q", i, v, l)
		}
	}
}
