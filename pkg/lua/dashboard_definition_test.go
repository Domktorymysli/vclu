package lua

import (
	"bytes"
	"encoding/json"
	"testing"
)

// A dashboard with every widget type. The tile style (#91) is drawn by the browser only:
// getDefinition must keep returning exactly this JSON.
const allWidgetsDashboard = `
	_dashboards = {}
	Dashboard.new('all')
		:name('All widgets')
		:icon('grid')
		:columns(3)
		:auth('public')
		:poll(4)
		:theme({accent = '#ff9500', cardBg = '#ffffff'})
		:header()
			:status('h_temp', 'Outside', 'thermometer-half', function() return 12.5 end, '°C')
			:toggle('h_away', 'Away', 'house', function() return false end, function() end)
		:section('Lights', 'lightbulb', { columns = 4 })
			:toggle('t_on', 'Ceiling', 'lightbulb', function() return true end, function() end, { color = '#ffcc00' })
			:toggle('t_err', 'Broken', 'lightbulb', function() error('no reading') end, function() end)
			:dimmer('d1', 'Lamp', 'brightness-high', {
				get = function() return 40 end, set = function() end,
				switchOn = function() end, switchOff = function() end })
			:dimmer('d_nil', 'Lamp 2', 'brightness-high', { get = function() return nil end })
			:color('c1', 'Strip', 'palette', {
				getColor = function() return { r = 255, g = 128, b = 0 } end, setColor = function() end,
				get = function() return 70 end, set = function() end,
				switchOn = function() end, switchOff = function() end, isOn = function() return true end })
			:group('Scenes', 'stars')
			:button('b1', 'All off', 'power', function() end, { confirm = 'Sure?', size = 'small' })
		:section('Climate', 'thermometer-half')
			:status('s1', 'Living room', 'thermometer-half', function() return 21.5 end, '°C',
				{ spark = 'temp:living', sparkRange = '6h', color = function() return 'danger' end })
			:status('s_txt', 'Mode', 'gear', function() return 'Away' end)
			:slider('sl1', 'Target', 18, 24, function() return 21 end, function() end, { span = 2 })
			:select('sel1', 'Mode', 'gear', { 'Home', { value = 'away', label = 'Away' } },
				function() return 'Home' end, function() end, { disabled = function() return true end })
			:chart('ch1', 'Temperature', 'temp:living', { range = '24h', unit = '°C', min = 10, max = 30, fill = true })
		:section('Other', 'grid')
			:cover('cv1', 'Blinds', 'arrows-expand', {
				getPosition = function() return 70 end, getState = function() return 2 end,
				open = function() end, close = function() end, stop = function() end, set = function() end })
			:cover('cv2', 'Garage', 'door-closed', { getPosition = function() return nil end })
			:link('l1', 'Docs', 'globe', 'https://example.com')
			:image('img1', 'Camera', 'https://example.com/cam.jpg', { refresh = 5, height = 200, align = 'center' })
			:calendar('cal1', 'House', 'house_cal', { view = 'day', namedays = true })
		:build()
`

// Frozen on origin/main before #91 (period removed: it depends on the time of day).
const allWidgetsDefinition = `{"auth":"public","columns":3,"icon":"grid","id":"all","name":"All widgets","poll":4,"sections":[{"icon":"","isHeader":true,"name":""},{"columns":4,"icon":"lightbulb","name":"Lights"},{"icon":"thermometer-half","name":"Climate"},{"icon":"grid","name":"Other"}],"theme":{"accent":"#ff9500","cardBg":"#ffffff"},"widgets":[
	{"disabled":false,"icon":"thermometer-half","id":"h_temp","label":"Outside","section":0,"type":"status","unit":"°C","value":12.5},
	{"disabled":false,"icon":"house","id":"h_away","label":"Away","section":0,"type":"toggle","value":false},
	{"color":"#ffcc00","disabled":false,"icon":"lightbulb","id":"t_on","label":"Ceiling","section":1,"type":"toggle","value":true},
	{"disabled":false,"error":"<string>:15: no reading","icon":"lightbulb","id":"t_err","label":"Broken","section":1,"type":"toggle"},
	{"capabilities":{"toggle":true},"disabled":false,"icon":"brightness-high","id":"d1","label":"Lamp","on":true,"section":1,"type":"dimmer","value":40},
	{"capabilities":{"toggle":false},"disabled":false,"icon":"brightness-high","id":"d_nil","label":"Lamp 2","section":1,"type":"dimmer"},
	{"brightness":70,"capabilities":{"brightness":true,"toggle":true,"white":false},"disabled":false,"icon":"palette","id":"c1","label":"Strip","on":true,"presets":[{"b":100,"g":180,"label":"Warm White","r":255},{"b":255,"g":232,"label":"Cool White","r":224},{"b":68,"g":68,"label":"Red","r":255},{"b":68,"g":255,"label":"Green","r":68},{"b":255,"g":68,"label":"Blue","r":68},{"b":255,"g":68,"label":"Purple","r":170},{"b":0,"g":140,"label":"Orange","r":255},{"b":209,"g":206,"label":"Cyan","r":0}],"rgb":{"b":0,"g":128,"r":255},"section":1,"type":"color"},
	{"disabled":false,"icon":"stars","id":"_group_5","label":"Scenes","section":1,"type":"group"},
	{"confirm":"Sure?","disabled":false,"icon":"power","id":"b1","label":"All off","section":1,"size":"small","type":"button"},
	{"color":"danger","disabled":false,"icon":"thermometer-half","id":"s1","label":"Living room","section":2,"spark":"temp:living","sparkRange":"6h","type":"status","unit":"°C","value":21.5},
	{"disabled":false,"icon":"gear","id":"s_txt","label":"Mode","section":2,"type":"status","unit":"","value":"Away"},
	{"disabled":false,"id":"sl1","label":"Target","max":24,"min":18,"section":2,"span":2,"type":"slider","value":21},
	{"disabled":true,"icon":"gear","id":"sel1","label":"Mode","options":[{"label":"Home","value":"Home"},{"label":"Away","value":"away"}],"section":2,"type":"select","value":"Home"},
	{"disabled":false,"fill":true,"id":"ch1","label":"Temperature","max":30,"metricPath":"temp:living","min":10,"range":"24h","section":2,"type":"chart","unit":"°C"},
	{"capabilities":{"set":true},"disabled":false,"icon":"arrows-expand","id":"cv1","label":"Blinds","position":70,"section":3,"state":2,"type":"cover"},
	{"capabilities":{"set":false},"disabled":false,"icon":"door-closed","id":"cv2","label":"Garage","section":3,"state":0,"type":"cover"},
	{"disabled":false,"icon":"globe","id":"l1","label":"Docs","section":3,"type":"link","url":"https://example.com"},
	{"align":"center","disabled":false,"height":200,"id":"img1","label":"Camera","refresh":5,"section":3,"type":"image","url":"https://example.com/cam.jpg"},
	{"calendar":"house_cal","disabled":false,"holidays":true,"id":"cal1","label":"House","namedays":true,"section":3,"sun":true,"type":"calendar","view":"day"}
]}`

func TestDashboardDefinitionAllWidgetTypes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(allWidgetsDashboard); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	var def map[string]any
	dashJSON(t, engine, `Dashboard.API.getDefinition("all")`, &def)
	switch def["period"] {
	case "morning", "afternoon", "evening", "night":
	default:
		t.Errorf("period = %v", def["period"])
	}
	delete(def, "period")

	types := map[string]bool{}
	for _, w := range def["widgets"].([]any) {
		types[w.(map[string]any)["type"].(string)] = true
	}
	for _, typ := range []string{"button", "toggle", "slider", "status", "link", "chart", "select",
		"image", "group", "cover", "dimmer", "color", "calendar"} {
		if !types[typ] {
			t.Errorf("no %s widget in the definition", typ)
		}
	}

	// encoding/json sorts map keys, so the comparison does not depend on Lua's table order
	var got, want bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(def); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&want, []byte(allWidgetsDefinition)); err != nil {
		t.Fatal(err)
	}
	if g := bytes.TrimSpace(got.Bytes()); string(g) != want.String() {
		t.Errorf("definition changed\n got: %s\nwant: %s", g, want.String())
	}
}
