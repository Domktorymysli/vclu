package lua

import (
	"strings"
	"testing"
)

// section:area builds a room from what expose() put in an area, and dash:floor a section per
// area on a floor (#92). Tests go through Dashboard.API, as the web does.

const dashAreaSetup = `
	_dashboards = {}
	_sent = {}
	local clu = {
		executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end,
		execute = function(s, i, cmd) table.insert(_sent, "QUERY " .. cmd) end,
	}
	D = {
		sufit = VirtualSwitch:new("sufit"),
		kinkiet = VirtualDimmer:new("kinkiet"),
		led = RemoteRGBLight:new(clu, "LED1", "led"),
		roleta = VirtualCover:new("roleta", { initial = 0 }),
		temp = VirtualNumber:new("temp", { min = -40, max = 80, initial = 21.5 }),
		wilg = VirtualNumber:new("wilg", { initial = 45 }),
		gniazdko = VirtualSwitch:new("gniazdko"),
		zamek = VirtualSwitch:new("zamek"),
		kuchnia = VirtualSwitch:new("kuchnia"),
		reczny = VirtualSwitch:new("reczny"),
	}
	local function ex(o, typ, path, name, extra)
		local opts = { path = path, name = name, area = "Salon", floor = "Parter" }
		for k, v in pairs(extra or {}) do opts[k] = v end
		return expose(o, typ, opts)
	end
	ex(D.sufit, "light", "salon.sufit", "Sufit")
	ex(D.kinkiet, "dimmer", "salon.kinkiet", "Kinkiet")
	ex(D.led, "light", "salon.led", "Taśma LED")
	ex(D.roleta, "cover", "salon.roleta", "Roleta")
	ex(D.temp, "temperature", "salon.temp", "Temperatura")
	ex(D.wilg, "humidity", "salon.wilg", "Wilgotność")
	ex(D.gniazdko, "switch", "plugin:Gniazdko 1", "Gniazdko")
	ex(D.zamek, "lock", "salon.zamek", "Zamek")
	ex(D.kuchnia, "light", "kuchnia.sufit", "Kuchnia", { area = "Kuchnia" })
	ALL = { "light", "switch", "dimmer", "cover", "temperature", "humidity", "lock" }
`

type dashSection struct {
	Name string `json:"name"`
}

type dashFull struct {
	Sections []dashSection            `json:"sections"`
	Widgets  []map[string]interface{} `json:"widgets"`
}

func (d dashFull) ids() []string {
	var out []string
	for _, w := range d.Widgets {
		out = append(out, w["id"].(string))
	}
	return out
}

func (d dashFull) widget(id string) map[string]interface{} {
	return dashDef{Widgets: d.Widgets}.widget(id)
}

func newAreaEngine(t *testing.T, dash string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashAreaSetup)
	mustExec(t, engine, dash)
	return engine
}

func areaDef(t *testing.T, engine *LuaEngine, id string) dashFull {
	t.Helper()
	var def dashFull
	dashJSON(t, engine, `Dashboard.API.getDefinition("`+id+`")`, &def)
	return def
}

func TestDashboardAreaWidgets(t *testing.T) {
	engine := newAreaEngine(t, `
		Dashboard.new('salon')
			:section('Salon', 'house'):area('Salon', { kinds = ALL })
				:toggle('reczny', 'Ręczny', 'power', function() return D.reczny:getValue() == 1 end,
					function(v) D.reczny:setValue(v and 1 or 0) end)
			:build()
	`)
	def := areaDef(t, engine, "salon")

	// lights, switches, dimmers and locks by name, then covers, then the rest; the hand-made last
	want := []struct{ id, typ, label string }{
		{"area:plugin:Gniazdko_1", "toggle", "Gniazdko"},
		{"area:salon.kinkiet", "dimmer", "Kinkiet"},
		{"area:salon.sufit", "toggle", "Sufit"},
		{"area:salon.led", "color", "Taśma LED"},
		{"area:salon.zamek", "toggle", "Zamek"},
		{"area:salon.roleta", "cover", "Roleta"},
		{"area:salon.temp", "status", "Temperatura"},
		{"area:salon.wilg", "status", "Wilgotność"},
		{"reczny", "toggle", "Ręczny"},
	}
	if len(def.Widgets) != len(want) {
		t.Fatalf("widgets = %v, want %d", def.ids(), len(want))
	}
	for i, w := range want {
		got := def.Widgets[i]
		if got["id"] != w.id || got["type"] != w.typ || got["label"] != w.label {
			t.Errorf("widget %d = %v %v %v, want %v", i, got["id"], got["type"], got["label"], w)
		}
	}
	if u := def.widget("area:salon.temp")["unit"]; u != "°C" {
		t.Errorf("temperature unit = %v", u)
	}
	if u := def.widget("area:salon.wilg")["unit"]; u != "%" {
		t.Errorf("humidity unit = %v", u)
	}

	// actions reach the objects
	for _, a := range []string{
		`Dashboard.API.executeAction("salon", "area:salon.sufit", "toggle", true)`,
		`Dashboard.API.executeAction("salon", "area:plugin:Gniazdko_1", "toggle", true)`,
		`Dashboard.API.executeAction("salon", "area:salon.zamek", "toggle", true)`,
		`Dashboard.API.executeAction("salon", "area:salon.kinkiet", "set", 40)`,
		`Dashboard.API.executeAction("salon", "area:salon.roleta", "set", 30)`,
		`Dashboard.API.executeAction("salon", "area:salon.led", "setColor", { r = 255, g = 0, b = 0 })`,
		`Dashboard.API.executeAction("salon", "reczny", "toggle", true)`,
	} {
		if res := mustExec(t, engine, a); !strings.Contains(res, `"success":true`) {
			t.Errorf("%s: %s", a, res)
		}
	}
	got := mustExec(t, engine, `return table.concat({ D.sufit:getValue(), D.gniazdko:getValue(), D.zamek:getValue(),
		D.kinkiet:getValue(), D.roleta:getPosition(), D.reczny:getValue() }, ",")`)
	if got != "1,1,1,0.4,30,1" {
		t.Errorf("objects after actions = %s", got)
	}
	if sent := mustExec(t, engine, `return table.concat(_sent, ";")`); !strings.Contains(sent, "LED1") {
		t.Errorf("colour did not reach the RGB light: %s", sent)
	}
	if res := mustExec(t, engine, `Dashboard.API.executeAction("salon", "area:salon.roleta", "open")`); !strings.Contains(res, `"success":true`) {
		t.Errorf("cover open: %s", res)
	}

	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("salon")`, &vals)
	if vals["area:salon.sufit"]["value"] != true || vals["area:salon.temp"]["value"] != 21.5 ||
		vals["area:salon.wilg"]["value"] != 45.0 || vals["area:salon.kinkiet"]["value"] != 40.0 ||
		vals["area:salon.roleta"]["position"] != 100.0 {
		t.Errorf("values = %v", vals)
	}

	// a read-only device shows, but nothing switches it
	mustExec(t, engine, `ExposedObjects:get("salon.sufit"):readonly()`)
	if res := mustExec(t, engine, `Dashboard.API.executeAction("salon", "area:salon.sufit", "toggle", false)`); !strings.Contains(res, "disabled") {
		t.Errorf("readonly toggle: %s", res)
	}
	if v := mustExec(t, engine, `return D.sufit:getValue()`); v != "1" {
		t.Errorf("readonly light switched: %s", v)
	}
}

func TestDashboardAreaChoosesDevices(t *testing.T) {
	engine := newAreaEngine(t, `
		THERMO = VirtualNumber:new("thermo", { initial = 20 })
		expose(THERMO, "climate", { path = "salon.thermo", name = "Termostat", area = "Salon", dashboard = true })
		expose(VirtualSwitch:new("ukryta"), "light", { path = "salon.ukryta", name = "Ukryta", area = "Salon", dashboard = false })
		expose(VirtualSwitch:new("domofon"), "switch", { path = "salon.domofon", name = "Domofon", area = "Salon", dashboard = true })
		expose(VirtualSwitch:new("tylko_panel"), "light", { path = "salon.panel", name = "Panel", area = "Salon", panel = false })
		Dashboard.new('d')
			:section('Domyślne'):area('Salon')
			:section('Wybrane'):area({ 'Salon', 'Kuchnia' }, { kinds = { 'light' }, skip = { 'salon.sufit' } })
			:build()
	`)
	def := areaDef(t, engine, "d")
	inSection := func(sec float64) []string {
		var out []string
		for _, w := range def.Widgets {
			if w["section"] == sec {
				out = append(out, w["id"].(string))
			}
		}
		return out
	}
	// default kinds: no switch, no lock; dashboard = true adds the intercom, false hides one;
	// a thermostat has no widget yet; panel = false does not concern dashboards
	got := strings.Join(inSection(0), " ")
	if got != "area:salon.domofon area:salon.kinkiet area:salon.panel area:salon.sufit area:salon.led area:salon.roleta area:salon.temp area:salon.wilg" {
		t.Errorf("default section = %s", got)
	}
	// the same object in a second section gets its own id
	got = strings.Join(inSection(1), " ")
	if got != "area:salon.domofon#2 area:kuchnia.sufit area:salon.panel#2 area:salon.led#2" {
		t.Errorf("kinds and skip = %s", got)
	}

	// exposed after the dashboard was built: on the next read
	mustExec(t, engine, `expose(VirtualSwitch:new("nowa"), "light", { path = "salon.nowa", name = "Nowa", area = "Salon" })`)
	if w := areaDef(t, engine, "d").widget("area:salon.nowa"); w == nil || w["label"] != "Nowa" {
		t.Errorf("late expose not shown: %v", w)
	}
	// and unexposed: gone
	mustExec(t, engine, `ExposedObjects:get("salon.nowa"):unexpose()`)
	if w := areaDef(t, engine, "d").widget("area:salon.nowa"); w != nil {
		t.Errorf("unexposed still shown: %v", w)
	}
}

func TestDashboardFloor(t *testing.T) {
	engine := newAreaEngine(t, `
		expose(VirtualSwitch:new("a"), "light", { path = "lazienka.lustro", name = "Lustro", area = "Łazienka", floor = "Parter" })
		expose(VirtualSwitch:new("b"), "switch", { path = "kotlownia.pompa", name = "Pompa", area = "Kotłownia", floor = "Parter" })
		expose(VirtualSwitch:new("c"), "light", { path = "sypialnia.sufit", name = "Sufit", area = "Sypialnia", floor = "Piętro" })
		Dashboard.new('parter')
			:floor('Parter', { order = { 'Kuchnia' }, titles = { ['Salon'] = 'Pokój dzienny' } })
			:section('Inne'):toggle('x', 'X', 'power', function() return false end, function() end)
			:build()
	`)
	def := areaDef(t, engine, "parter")
	var names []string
	for _, s := range def.Sections {
		names = append(names, s.Name)
	}
	// Kuchnia first (order), then by name; Kotłownia has only a switch: left out
	if got := strings.Join(names, "|"); got != "Kuchnia|Pokój dzienny|Łazienka|Inne" {
		t.Errorf("sections = %s", got)
	}
	if w := def.widget("area:lazienka.lustro"); w == nil || w["section"] != 2.0 {
		t.Errorf("Łazienka widget = %v", w)
	}
	if w := def.widget("x"); w == nil || w["section"] != 3.0 {
		t.Errorf("manual section after the floor = %v", w)
	}
	if res := mustExec(t, engine, `Dashboard.API.executeAction("parter", "area:lazienka.lustro", "toggle", true)`); !strings.Contains(res, `"success":true`) {
		t.Errorf("floor action: %s", res)
	}
}

// Ids come from the path: the same after a reload and when other devices come and go
func TestDashboardAreaIDsStable(t *testing.T) {
	dash := `Dashboard.new('s'):section('Salon'):area('Salon', { kinds = ALL }):build()`
	first := areaDef(t, newAreaEngine(t, dash), "s").ids()
	engine := newAreaEngine(t, dash)
	mustExec(t, engine, `expose(VirtualSwitch:new("aaa"), "light", { path = "salon.aaa", name = "Aaa", area = "Salon" })`)
	mustExec(t, engine, dash) // the module reloads
	second := areaDef(t, engine, "s").ids()
	if strings.Join(second, " ") != "area:salon.aaa "+strings.Join(first, " ") {
		t.Errorf("ids changed:\n%v\n%v", first, second)
	}
}

func TestDashboardAreaDynamic(t *testing.T) {
	engine := newAreaEngine(t, `Dashboard.dynamic('dyn', function(d) d:section('Kuchnia'):area('Kuchnia') end)`)
	if w := areaDef(t, engine, "dyn").widget("area:kuchnia.sufit"); w == nil {
		t.Error("dynamic dashboard has no area widget")
	}
}

func TestDashboardAreaErrors(t *testing.T) {
	engine := newAreaEngine(t, "")
	for _, c := range []struct{ code, want string }{
		{`Dashboard.new('e'):section('S'):area(42)`, `section:area: expected an area name or a list of them, got number`},
		{`Dashboard.new('e'):section('S'):area('Salon', { kind = { 'light' } })`, `section:area: unknown option "kind" (kinds, skip)`},
		{`Dashboard.new('e'):section('S'):area('Salon', { kinds = { 'climate' } })`, `section:area: kind "climate" has no widget (light, switch, dimmer, cover, temperature, humidity, lock)`},
		{`Dashboard.new('e'):section('S'):area('Salon'):area('Kuchnia')`, `section:area: this section already shows an area`},
		{`Dashboard.new('e'):floor()`, `dash:floor: expected a floor name, got nil`},
		{`Dashboard.new('e'):section('S'):floor('Parter', { title = 'x' })`, `dash:floor: unknown option "title" (kinds, order, skip, titles)`},
		{`Dashboard.new('e'):floor('Parter', { order = 'Salon' })`, `dash:floor: order expects a list of areas`},
		{`Dashboard.new('e'):section('S'):toggle('area:x', 'X', 'power', function() end, function() end)`, `ids starting with "area:" are kept for section:area`},
		{`Dashboard.new('e'):section('S'); local d = Dashboard.new('e'); d:section('S'); d:status('area:y', 'Y', 'x', function() end)`, `widget id "area:y"`},
		{`expose(VirtualSwitch:new("q"), "light", { dashboard = "yes" })`, `dashboard must be true or false`},
	} {
		res := mustExec(t, engine, `local ok, err = pcall(function()
`+c.code+`
end) return tostring(err)`)
		if !strings.Contains(res, c.want) {
			t.Errorf("%s: %s, want %q", c.code, res, c.want)
		}
		// the error names the module's line, not Dashboard.lua
		if !strings.Contains(c.code, "expose(") && !strings.Contains(res, ":2:") {
			t.Errorf("%s: error does not point at the caller: %s", c.code, res)
		}
	}
}

// Home Assistant and HomeKit do not see the dashboard option
func TestDashboardOptionNotForIntegrations(t *testing.T) {
	engine := newAreaEngine(t, `expose(VirtualSwitch:new("q"), "light", { path = "salon.q", area = "Salon", dashboard = false })`)
	if js := mustExec(t, engine, `return ExposedObjects:toJSON()`); strings.Contains(js, "dashboard") || !strings.Contains(js, "salon.q") {
		t.Errorf("toJSON = %s", js)
	}
}
