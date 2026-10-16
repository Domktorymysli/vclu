package lua

import (
	"encoding/json"
	"strings"
	"testing"
)

// ==================== Dashboard Module Tests ====================

func TestDashboardModuleExists(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute("type(Dashboard)")
	if err != nil {
		t.Fatalf("Dashboard check failed: %v", err)
	}
	if result != "table" {
		t.Errorf("Dashboard type is %q, expected 'table'", result)
	}

	result, _ = engine.Execute("type(Dashboard.new)")
	if result != "function" {
		t.Errorf("Dashboard.new type is %q, expected 'function'", result)
	}

	result, _ = engine.Execute("type(Dashboard.dynamic)")
	if result != "function" {
		t.Errorf("Dashboard.dynamic type is %q, expected 'function'", result)
	}

	result, _ = engine.Execute("type(Dashboard.API)")
	if result != "table" {
		t.Errorf("Dashboard.API type is %q, expected 'table'", result)
	}
}

func TestDashboardBuildMinimal(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		Dashboard.new('test')
			:name('Test Dashboard')
			:icon('house')
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	result, err := engine.Execute("_dashboards['test'] ~= nil")
	if err != nil || result != "true" {
		t.Errorf("dashboard not registered in _dashboards")
	}
}

func TestDashboardRequiresID(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute("Dashboard.new('')")
	if err == nil {
		t.Errorf("expected error for empty ID")
	}
}

func TestDashboardDuplicateWidgetID(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		Dashboard.new('dup')
			:section('S1', 'icon')
			:status('same_id', 'A', 'icon', function() return 1 end, '')
			:status('same_id', 'B', 'icon', function() return 2 end, '')
			:build()
	`)
	if err == nil {
		t.Errorf("expected error for duplicate widget ID")
	}
	if err != nil && !strings.Contains(err.Error(), "Duplicate widget ID") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDashboardGroupSkipsDuplicateCheck(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Group widgets get auto-generated IDs and should not trigger duplicate check
	_, err := engine.Execute(`
		Dashboard.new('grp')
			:section('S1', 'icon')
			:group('Label A', 'icon')
			:group('Label B', 'icon')
			:status('stat1', 'Stat', 'icon', function() return 1 end, '')
			:build()
	`)
	if err != nil {
		t.Fatalf("build with groups failed: %v", err)
	}
}

func TestDashboardAPIList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('alpha')
			:name('Alpha')
			:icon('a')
			:section('S', 'icon')
			:status('s1', 'Stat', 'icon', function() return 1 end, '')
			:build()
		Dashboard.new('beta')
			:name('Beta')
			:icon('b')
			:section('S', 'icon')
			:button('b1', 'Btn', 'icon', function() end)
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute("Dashboard.API.list()")
	if err != nil {
		t.Fatalf("list() failed: %v", err)
	}

	var list []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &list); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("expected 2 dashboards, got %d", len(list))
	}

	// Should be sorted by ID
	if list[0]["id"] != "alpha" || list[1]["id"] != "beta" {
		t.Errorf("list not sorted: %v", list)
	}
}

func TestDashboardAPIGetDefinition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('def_test')
			:name('Def Test')
			:icon('house')
			:columns(4)
			:auth('public')
			:poll(5)
			:theme({accent = '#FF0000'})
			:section('Temps', 'thermometer-half')
			:status('t1', 'Salon', 'thermometer-half', function() return 21.5 end, '°C',
				{spark = 'temp:salon', sparkRange = '2h', color = '#FF6B6B'})
			:link('lnk1', 'Google', 'globe', 'https://google.com')
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`Dashboard.API.getDefinition("def_test")`)
	if err != nil {
		t.Fatalf("getDefinition failed: %v", err)
	}

	var def map[string]interface{}
	if err := json.Unmarshal([]byte(result), &def); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if def["name"] != "Def Test" {
		t.Errorf("name = %v", def["name"])
	}
	if def["columns"].(float64) != 4 {
		t.Errorf("columns = %v", def["columns"])
	}
	if def["auth"] != "public" {
		t.Errorf("auth = %v", def["auth"])
	}
	if def["poll"].(float64) != 5 {
		t.Errorf("poll = %v", def["poll"])
	}

	widgets := def["widgets"].([]interface{})
	if len(widgets) != 2 {
		t.Fatalf("expected 2 widgets, got %d", len(widgets))
	}

	w0 := widgets[0].(map[string]interface{})
	if w0["type"] != "status" || w0["value"].(float64) != 21.5 {
		t.Errorf("widget 0: type=%v, value=%v", w0["type"], w0["value"])
	}
	if w0["unit"] != "°C" {
		t.Errorf("widget 0 unit = %v", w0["unit"])
	}
	if w0["spark"] != "temp:salon" {
		t.Errorf("widget 0 spark = %v", w0["spark"])
	}

	w1 := widgets[1].(map[string]interface{})
	if w1["type"] != "link" || w1["url"] != "https://google.com" {
		t.Errorf("widget 1: type=%v, url=%v", w1["type"], w1["url"])
	}
}

func TestDashboardAPIGetValues(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		test_val = 42
		Dashboard.new('vals')
			:section('S', 'icon')
			:status('s1', 'Stat', 'icon', function() return test_val end, '')
			:button('b1', 'Btn', 'icon', function() end)
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`Dashboard.API.getValues("vals")`)
	if err != nil {
		t.Fatalf("getValues failed: %v", err)
	}

	var vals map[string]interface{}
	if err := json.Unmarshal([]byte(result), &vals); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	s1 := vals["s1"].(map[string]interface{})
	if s1["value"].(float64) != 42 {
		t.Errorf("s1 value = %v, expected 42", s1["value"])
	}
}

func TestDashboardAPIGetValuesErrorHandler(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('errs')
			:section('S', 'icon')
			:status('bad', 'Bad', 'icon', function() error("boom") end, '')
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`Dashboard.API.getValues("errs")`)
	if err != nil {
		t.Fatalf("getValues failed: %v", err)
	}

	var vals map[string]interface{}
	json.Unmarshal([]byte(result), &vals)

	bad := vals["bad"].(map[string]interface{})
	if bad["error"] == nil || bad["error"] == "" {
		t.Errorf("expected error for 'bad' widget, got: %v", bad)
	}
}

func TestDashboardAPIExecuteAction(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		tapped = false
		toggled_val = nil
		slider_val = nil
		selected_val = nil
		Dashboard.new('act')
			:section('S', 'icon')
			:button('btn1', 'Btn', 'icon', function() tapped = true; return "Done!" end)
			:toggle('tog1', 'Tog', 'icon', function() return false end,
				function(v) toggled_val = v end)
			:slider('sld1', 'Sld', 0, 100, function() return 50 end,
				function(v) slider_val = v end)
			:select('sel1', 'Sel', 'icon', {"a","b","c"}, function() return "a" end,
				function(v) selected_val = v; return "Selected: " .. v end)
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Test button tap
	result, _ := engine.Execute(`Dashboard.API.executeAction("act", "btn1", "tap")`)
	var resp map[string]interface{}
	json.Unmarshal([]byte(result), &resp)
	if resp["success"] != true {
		t.Errorf("button tap: %v", resp)
	}
	if resp["message"] != "Done!" {
		t.Errorf("button toast message = %v", resp["message"])
	}
	r, _ := engine.Execute("tapped")
	if r != "true" {
		t.Errorf("tapped = %v", r)
	}

	// Test toggle
	result, _ = engine.Execute(`Dashboard.API.executeAction("act", "tog1", "toggle", true)`)
	json.Unmarshal([]byte(result), &resp)
	if resp["success"] != true {
		t.Errorf("toggle: %v", resp)
	}
	r, _ = engine.Execute("toggled_val")
	if r != "true" {
		t.Errorf("toggled_val = %v", r)
	}

	// Test slider
	result, _ = engine.Execute(`Dashboard.API.executeAction("act", "sld1", "set", 75)`)
	json.Unmarshal([]byte(result), &resp)
	if resp["success"] != true {
		t.Errorf("slider: %v", resp)
	}
	r, _ = engine.Execute("slider_val")
	if r != "75" {
		t.Errorf("slider_val = %v", r)
	}

	// Test slider out of range
	result, _ = engine.Execute(`Dashboard.API.executeAction("act", "sld1", "set", 200)`)
	json.Unmarshal([]byte(result), &resp)
	if resp["error"] == nil {
		t.Errorf("slider should reject out-of-range value")
	}

	// Test select
	result, _ = engine.Execute(`Dashboard.API.executeAction("act", "sel1", "select", "b")`)
	json.Unmarshal([]byte(result), &resp)
	if resp["success"] != true {
		t.Errorf("select: %v", resp)
	}
	if resp["message"] != "Selected: b" {
		t.Errorf("select toast = %v", resp["message"])
	}
	r, _ = engine.Execute("selected_val")
	if r != "b" {
		t.Errorf("selected_val = %v", r)
	}
}

func TestDashboardAPIExecuteActionWrongType(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('wrong')
			:section('S', 'icon')
			:button('btn1', 'Btn', 'icon', function() end)
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Try toggle action on button
	result, _ := engine.Execute(`Dashboard.API.executeAction("wrong", "btn1", "toggle", true)`)
	var resp map[string]interface{}
	json.Unmarshal([]byte(result), &resp)
	if resp["error"] == nil {
		t.Errorf("expected error for wrong action type")
	}
}

func TestDashboardNotFound(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, _ = engine.Execute("_dashboards = {}")

	result, _ := engine.Execute(`Dashboard.API.getDefinition("nonexistent")`)
	if !strings.Contains(result, "not found") {
		t.Errorf("expected 'not found' error, got: %s", result)
	}

	result, _ = engine.Execute(`Dashboard.API.getValues("nonexistent")`)
	if !strings.Contains(result, "not found") {
		t.Errorf("expected 'not found' error, got: %s", result)
	}
}

func TestDashboardAuth(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('pub'):auth('public'):section('S','i'):build()
		Dashboard.new('sess'):auth('session'):section('S','i'):build()
		Dashboard.new('pinned'):auth('pin'):pin('1234'):section('S','i'):build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// getAuth
	result, _ := engine.Execute(`Dashboard.API.getAuth("pub")`)
	if !strings.Contains(result, `"public"`) {
		t.Errorf("pub auth: %s", result)
	}

	result, _ = engine.Execute(`Dashboard.API.getAuth("sess")`)
	if !strings.Contains(result, `"session"`) {
		t.Errorf("sess auth: %s", result)
	}

	// PIN verify
	result, _ = engine.Execute(`Dashboard.API.verifyPin("pinned", "1234")`)
	if !strings.Contains(result, "true") {
		t.Errorf("correct PIN should be valid: %s", result)
	}

	result, _ = engine.Execute(`Dashboard.API.verifyPin("pinned", "0000")`)
	if strings.Contains(result, "true") {
		t.Errorf("wrong PIN should be invalid: %s", result)
	}
}

func TestDashboardDynamic(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		call_count = 0
		Dashboard.dynamic('dyn', function(d)
			call_count = call_count + 1
			d:name('Dynamic #' .. call_count)
			 :icon('refresh')
			 :section('S', 'icon')
			 :status('s1', 'Count', 'icon', function() return call_count end, '')
		end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// First call
	result, err := engine.Execute(`Dashboard.API.getDefinition("dyn")`)
	if err != nil {
		t.Fatalf("getDefinition failed: %v", err)
	}

	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)
	if def["name"] != "Dynamic #1" {
		t.Errorf("first call name = %v", def["name"])
	}

	// Second call — should rebuild
	result, _ = engine.Execute(`Dashboard.API.getDefinition("dyn")`)
	json.Unmarshal([]byte(result), &def)
	if def["name"] != "Dynamic #2" {
		t.Errorf("second call name = %v, should be Dynamic #2", def["name"])
	}

	// Dynamic sentinel should be preserved
	result, _ = engine.Execute("_dashboards['dyn']._isDynamic == true")
	if result != "true" {
		t.Errorf("dynamic sentinel lost after resolve")
	}
}

func TestDashboardDynamicRequiresFunction(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`Dashboard.dynamic('bad', 'not a function')`)
	if err == nil {
		t.Errorf("expected error for non-function builder")
	}
}

func TestDashboardTimeOfDay(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute("Dashboard.timeOfDay()")
	if err != nil {
		t.Fatalf("timeOfDay failed: %v", err)
	}

	valid := map[string]bool{"morning": true, "afternoon": true, "evening": true, "night": true}
	if !valid[result] {
		t.Errorf("timeOfDay returned %q, expected one of morning/afternoon/evening/night", result)
	}
}

func TestDashboardPeriodLabel(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	tests := map[string]string{
		"morning":   "Rano",
		"afternoon": "Popołudnie",
		"evening":   "Wieczór",
		"night":     "Noc",
	}

	for period, expected := range tests {
		result, _ := engine.Execute(`Dashboard.periodLabel("` + period + `")`)
		if result != expected {
			t.Errorf("periodLabel(%q) = %q, expected %q", period, result, expected)
		}
	}
}

func TestDashboardChartWidget(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('charts')
			:section('S', 'icon')
			:chart('c1', 'Temperature', 'temp:salon',
				{unit='°C', range='24h', refresh=300, span=2, fill=true, min=10, max=40})
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	result, _ := engine.Execute(`Dashboard.API.getDefinition("charts")`)
	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)

	widgets := def["widgets"].([]interface{})
	w := widgets[0].(map[string]interface{})

	if w["type"] != "chart" {
		t.Errorf("type = %v", w["type"])
	}
	if w["metricPath"] != "temp:salon" {
		t.Errorf("metricPath = %v", w["metricPath"])
	}
	if w["range"] != "24h" {
		t.Errorf("range = %v", w["range"])
	}
	if w["unit"] != "°C" {
		t.Errorf("unit = %v", w["unit"])
	}
	if w["fill"] != true {
		t.Errorf("fill = %v", w["fill"])
	}
	if w["min"].(float64) != 10 {
		t.Errorf("min = %v", w["min"])
	}
}

func TestDashboardImageWidget(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('img')
			:section('S', 'icon')
			:image('i1', 'Camera', '/api/camera/front/snapshot', {refresh=10, span=3})
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	result, _ := engine.Execute(`Dashboard.API.getDefinition("img")`)
	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)

	widgets := def["widgets"].([]interface{})
	w := widgets[0].(map[string]interface{})

	if w["type"] != "image" {
		t.Errorf("type = %v", w["type"])
	}
	if w["url"] != "/api/camera/front/snapshot" {
		t.Errorf("url = %v", w["url"])
	}
	if w["refresh"].(float64) != 10 {
		t.Errorf("refresh = %v", w["refresh"])
	}
}

func TestDashboardSelectWidget(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('sel')
			:section('S', 'icon')
			:select('s1', 'Mode', 'icon',
				{"auto", {value="manual", label="Manual Mode"}, "off"},
				function() return "auto" end,
				function(v) end)
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	result, _ := engine.Execute(`Dashboard.API.getDefinition("sel")`)
	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)

	widgets := def["widgets"].([]interface{})
	w := widgets[0].(map[string]interface{})

	if w["type"] != "select" {
		t.Errorf("type = %v", w["type"])
	}
	if w["value"] != "auto" {
		t.Errorf("value = %v", w["value"])
	}

	opts := w["options"].([]interface{})
	if len(opts) != 3 {
		t.Fatalf("expected 3 options, got %d", len(opts))
	}

	// First option: string normalized to {value, label}
	o0 := opts[0].(map[string]interface{})
	if o0["value"] != "auto" || o0["label"] != "auto" {
		t.Errorf("option 0: %v", o0)
	}

	// Second option: value/label object
	o1 := opts[1].(map[string]interface{})
	if o1["value"] != "manual" || o1["label"] != "Manual Mode" {
		t.Errorf("option 1: %v", o1)
	}
}

func TestDashboardDisabledWidget(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('dis')
			:section('S', 'icon')
			:button('btn1', 'Disabled Btn', 'icon', function() end,
				{disabled = function() return true end})
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Widget should be disabled
	result, _ := engine.Execute(`Dashboard.API.getDefinition("dis")`)
	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)

	widgets := def["widgets"].([]interface{})
	w := widgets[0].(map[string]interface{})
	if w["disabled"] != true {
		t.Errorf("widget should be disabled: %v", w["disabled"])
	}

	// Action should be rejected
	result, _ = engine.Execute(`Dashboard.API.executeAction("dis", "btn1", "tap")`)
	var resp map[string]interface{}
	json.Unmarshal([]byte(result), &resp)
	if resp["error"] == nil {
		t.Errorf("should reject action on disabled widget")
	}
}

func TestDashboardHeaderBeforeSections(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// header() after section() should error
	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('bad')
			:section('S', 'icon')
			:header()
			:build()
	`)
	if err == nil {
		t.Errorf("expected error when header() called after section()")
	}
}

func TestDashboardSectionHandleChaining(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// SectionHandle should delegate build/name/theme/poll back to builder
	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('chain')
			:section('S1', 'icon')
			:status('s1', 'A', 'icon', function() return 1 end, '')
			:section('S2', 'icon')
			:button('b1', 'B', 'icon', function() end)
			:name('Chained')
			:theme({accent = '#00FF00'})
			:poll(10)
			:build()
	`)
	if err != nil {
		t.Fatalf("chaining failed: %v", err)
	}

	result, _ := engine.Execute(`Dashboard.API.getDefinition("chain")`)
	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)

	if def["name"] != "Chained" {
		t.Errorf("name = %v, expected 'Chained'", def["name"])
	}
	if def["poll"].(float64) != 10 {
		t.Errorf("poll = %v, expected 10", def["poll"])
	}

	sections := def["sections"].([]interface{})
	if len(sections) != 2 {
		t.Errorf("expected 2 sections, got %d", len(sections))
	}
}

func TestDashboardConfirmOption(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		Dashboard.new('conf')
			:section('S', 'icon')
			:button('btn1', 'Danger', 'icon', function() end,
				{confirm = 'Are you sure?'})
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	result, _ := engine.Execute(`Dashboard.API.getDefinition("conf")`)
	var def map[string]interface{}
	json.Unmarshal([]byte(result), &def)

	widgets := def["widgets"].([]interface{})
	w := widgets[0].(map[string]interface{})
	if w["confirm"] != "Are you sure?" {
		t.Errorf("confirm = %v", w["confirm"])
	}
}

// dashJSON runs a Dashboard.API call and decodes its JSON.
func dashJSON(t *testing.T, engine *LuaEngine, call string, out interface{}) {
	t.Helper()
	result, err := engine.Execute(call)
	if err != nil {
		t.Fatalf("%s failed: %v", call, err)
	}
	if err := json.Unmarshal([]byte(result), out); err != nil {
		t.Fatalf("%s: invalid JSON: %v (%s)", call, err, result)
	}
}

type dashDef struct {
	Widgets []map[string]interface{} `json:"widgets"`
}

func (d dashDef) widget(id string) map[string]interface{} {
	for _, w := range d.Widgets {
		if w["id"] == id {
			return w
		}
	}
	return nil
}

func TestDashboardFunctionColor(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		sms = 10
		local function smsColor() if sms < 20 then return 'danger' end end
		Dashboard.new('clr')
			:header()
			:status('h1', 'SMS', 'chat', function() return sms end, '', {color = smsColor})
			:section('S', 'icon')
			:status('s1', 'SMS', 'chat', function() return sms end, '', {color = smsColor})
			:status('s2', 'Fixed', 'icon', function() return 1 end, '', {color = 'info'})
			:toggle('t1', 'Lamp', 'icon', function() return 1 end, function() end, {color = smsColor})
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	// Definition carries the current colour, never a function
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("clr")`, &def)
	for id, want := range map[string]string{"h1": "danger", "s1": "danger", "s2": "info"} {
		if c := def.widget(id)["color"]; c != want {
			t.Errorf("definition %s color = %v, expected %q", id, c, want)
		}
	}

	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("clr")`, &vals)
	for _, id := range []string{"h1", "s1"} {
		if vals[id]["color"] != "danger" {
			t.Errorf("values %s color = %v, expected 'danger'", id, vals[id]["color"])
		}
	}
	if c, ok := vals["s2"]["color"]; ok {
		t.Errorf("values s2 carries a string colour %v, expected none", c)
	}
	// Only status takes a function colour
	if c, ok := vals["t1"]["color"]; ok || def.widget("t1")["color"] != nil {
		t.Errorf("toggle with a function colour: values %v, definition %v, expected none", c, def.widget("t1")["color"])
	}

	// nil from the function: colour is false in values, so the page drops it
	if _, err := engine.Execute(`sms = 50`); err != nil {
		t.Fatal(err)
	}
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("clr")`, &vals)
	for _, id := range []string{"h1", "s1"} {
		if c := vals[id]["color"]; c != false {
			t.Errorf("values %s color = %v, expected false", id, c)
		}
		if vals[id]["value"] != float64(50) {
			t.Errorf("values %s value = %v, expected 50", id, vals[id]["value"])
		}
	}
	def = dashDef{}
	dashJSON(t, engine, `Dashboard.API.getDefinition("clr")`, &def)
	for _, id := range []string{"h1", "s1"} {
		if c := def.widget(id)["color"]; c != nil {
			t.Errorf("definition %s color = %v, expected none", id, c)
		}
	}
}

func TestDashboardFunctionColorError(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		colorLog = { n = 0, orig = Logger.warn }
		Logger.warn = function(_, msg, ...)
			if string.find(Logger:format(msg, ...), 'boom', 1, true) then colorLog.n = colorLog.n + 1 end
		end
		Dashboard.new('clrerr')
			:section('S', 'icon')
			:status('bad', 'Bad', 'icon', function() return 7 end, '', {color = function() error('boom') end})
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	defer func() { _, _ = engine.Execute(`Logger.warn = colorLog.orig`) }()

	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("clrerr")`, &def)
	if w := def.widget("bad"); w["value"] != float64(7) || w["color"] != nil {
		t.Errorf("definition bad = %v, expected value 7 and no colour", w)
	}
	for i := 0; i < 2; i++ {
		var vals map[string]map[string]interface{}
		dashJSON(t, engine, `Dashboard.API.getValues("clrerr")`, &vals)
		if bad := vals["bad"]; bad["value"] != float64(7) || bad["color"] != false || bad["error"] != nil {
			t.Errorf("values bad = %v, expected value 7, colour false, no error", bad)
		}
	}
	if n, _ := engine.Execute(`colorLog.n`); n != "1" {
		t.Errorf("colour error logged %s times, expected once", n)
	}
}

// A cover's position reaches the page as the object counts it (0 = closed); a roller that
// has not reported one sends none, so the page does not show it as closed.
func TestDashboardCoverPosition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_dashboards = {}
		_open = VirtualCover:new('taras')
		_open:open()
		_roller = RemoteCover:new({ executeNoReply = function() end }, 'ROL1', 'salon')
		_roller._hasPosition = true
		local function fns(c) return { getPosition = function() return c:getPosition() end,
			set = function(p) c:setPosition(p) end } end
		Dashboard.new('covers')
			:section('S', 'icon')
			:cover('taras', 'Taras', 'icon', fns(_open))
			:cover('salon', 'Salon', 'icon', fns(_roller))
			:build()
	`)
	if err != nil {
		t.Fatalf("build failed: %v", err)
	}

	var def map[string]interface{}
	result, _ := engine.Execute(`Dashboard.API.getDefinition("covers")`)
	if err := json.Unmarshal([]byte(result), &def); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	widgets := def["widgets"].([]interface{})
	if p := widgets[0].(map[string]interface{})["position"]; p != float64(100) {
		t.Errorf("open cover position = %v, want 100", p)
	}
	if p, ok := widgets[1].(map[string]interface{})["position"]; ok {
		t.Errorf("unreported position = %v, want none", p)
	}

	var vals map[string]map[string]interface{}
	result, _ = engine.Execute(`Dashboard.API.getValues("covers")`)
	if err := json.Unmarshal([]byte(result), &vals); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if p := vals["taras"]["position"]; p != float64(100) {
		t.Errorf("open cover value = %v, want 100", p)
	}
	if p, ok := vals["salon"]["position"]; ok {
		t.Errorf("unreported value = %v, want none", p)
	}
}
