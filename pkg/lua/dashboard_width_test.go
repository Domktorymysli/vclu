package lua

import (
	"strings"
	"testing"
)

// :width('full') (#99): the definition says "width" only when a module set it, so dashboards
// without it keep their definition (TestDashboardDefinitionAllWidgetTypes).
func TestDashboardWidth(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('plain'):section('S', 'grid'):build()
		Dashboard.new('full'):width('full'):section('S', 'grid'):build()
		Dashboard.new('sec'):section('S', 'grid'):width('full'):build()
		Dashboard.new('normal'):width('full'):width('normal'):section('S', 'grid'):build()
	`); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]any{"plain": nil, "full": "full", "sec": "full", "normal": "normal"} {
		var def map[string]any
		dashJSON(t, engine, `Dashboard.API.getDefinition("`+id+`")`, &def)
		got, set := def["width"]
		if want == nil && set {
			t.Errorf("%s: width = %v, want no width in the definition", id, got)
		}
		if want != nil && got != want {
			t.Errorf("%s: width = %v, want %v", id, got, want)
		}
	}
}

func TestDashboardWidthBadValue(t *testing.T) {
	for _, call := range []string{
		`:width('wide')`, `:width()`, `:width(100)`, `:width('FULL')`,
		`:section('S', 'grid'):width('wide')`, `:header():width(true)`,
	} {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil {
			t.Errorf("%s: no error", call)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, ":width") {
			t.Errorf("%s: %s", call, msg)
		}
	}
}

// What the home-screen app reads without the dashboard's auth: name, width and background only.
func TestDashboardAppInfo(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('kitchen'):name('Kitchen'):auth('pin'):pin('4321'):width('full')
			:theme({ background = '#f4f4f7', accent = '#ff9500', wallpaper = '/static/x.jpg' })
			:section('Lights', 'lightbulb')
				:status('s1', 'Secret', 'eye', function() return 'value-42' end)
			:build()
	`); err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	dashJSON(t, engine, `Dashboard.API.getAppInfo("kitchen")`, &info)
	want := map[string]any{"name": "Kitchen", "width": "full", "background": "#f4f4f7"}
	if len(info) != len(want) {
		t.Errorf("app info = %v, want only %v", info, want)
	}
	for k, v := range want {
		if info[k] != v {
			t.Errorf("%s = %v, want %v", k, info[k], v)
		}
	}
	dashJSON(t, engine, `Dashboard.API.getAppInfo("nope")`, &info)
	if info["error"] == nil {
		t.Errorf("unknown dashboard: %v", info)
	}
}
