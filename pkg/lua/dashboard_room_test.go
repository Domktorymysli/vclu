package lua

import (
	"strconv"
	"strings"
	"testing"
)

// section:room is one tile per room: its name, what is on, its temperature and humidity; a tap
// opens the room's widgets, chosen as section:area does (#94). Tests go through Dashboard.API.

const dashRoomSetup = `
	_dashboards = {}
	R = {
		l1 = VirtualSwitch:new("l1", { initial = 1 }),
		l2 = VirtualSwitch:new("l2", { initial = 0 }),
		d0 = VirtualDimmer:new("d0", { initial = 0 }),
		d4 = VirtualDimmer:new("d4", { initial = 0.4 }),
		cOpen = VirtualCover:new("co", { initial = 60 }),
		cMove = VirtualCover:new("cm", { initial = 0 }),
		cShut = VirtualCover:new("cc", { initial = 0 }),
		t1 = VirtualNumber:new("t1", { initial = 21 }),
		t2 = VirtualNumber:new("t2", { initial = 22.4 }),
		h = VirtualNumber:new("h", { initial = 60 }),
		gn = VirtualSwitch:new("gn", { initial = 1 }),
		zamek = VirtualSwitch:new("zamek", { initial = 1 }),
		tk = VirtualNumber:new("tk", { initial = 19.25 }),
		lk = VirtualSwitch:new("lk", { initial = 0 }),
		lg = VirtualSwitch:new("lg", { initial = 0 }),
	}
	R.cMove.getState = function() return 1 end -- moving up
	local function ex(o, typ, path, name, area, floor)
		return expose(o, typ, { path = path, name = name, area = area or "Sypialnia", floor = floor or "Piętro" })
	end
	ex(R.l1, "light", "syp.l1", "Sufit")
	ex(R.l2, "light", "syp.l2", "Kinkiet")
	ex(R.d0, "dimmer", "syp.d0", "Lampka")
	ex(R.d4, "dimmer", "syp.d4", "Nocna")
	ex(R.cOpen, "cover", "syp.co", "Okno")
	ex(R.cMove, "cover", "syp.cm", "Balkon")
	ex(R.cShut, "cover", "syp.cc", "Dach")
	ex(R.t1, "temperature", "syp.t1", "Temperatura 1")
	ex(R.t2, "temperature", "syp.t2", "Temperatura 2")
	ex(R.h, "humidity", "syp.h", "Wilgotność")
	ex(R.gn, "switch", "syp.gn", "Gniazdko")
	ex(R.zamek, "lock", "syp.zamek", "Zamek")
	ex(R.tk, "temperature", "kuch.t", "Temperatura", "Kuchnia")
	ex(R.lk, "light", "kuch.l", "Sufit", "Kuchnia")
	ex(R.lg, "light", "gab.l", "Lampa", "Gabinet")
`

func newRoomEngine(t *testing.T, dash string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashRoomSetup)
	mustExec(t, engine, dash)
	return engine
}

type roomDef struct {
	Sections []map[string]interface{} `json:"sections"`
	Widgets  []map[string]interface{} `json:"widgets"`
}

func roomDefOf(t *testing.T, engine *LuaEngine, id string) roomDef {
	t.Helper()
	var def roomDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("`+id+`")`, &def)
	return def
}

func (d roomDef) widget(id string) map[string]interface{} {
	return dashDef{Widgets: d.Widgets}.widget(id)
}

// The widgets of the section a room tile opens (nil without one)
func (d roomDef) overlay(tile string) []map[string]interface{} {
	for i, s := range d.Sections {
		if s["room"] == tile {
			var out []map[string]interface{}
			for _, w := range d.Widgets {
				if w["section"] == float64(i) {
					out = append(out, w)
				}
			}
			return out
		}
	}
	return nil
}

func roomValues(t *testing.T, engine *LuaEngine, id string) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("`+id+`")`, &vals)
	return vals
}

// The counts and the climate of a tile as "on open moving temperature humidity", "-" for none
func roomSummary(w map[string]interface{}) string {
	var parts []string
	for _, k := range []string{"on", "open", "moving", "temperature", "humidity"} {
		switch v := w[k].(type) {
		case float64:
			parts = append(parts, strconv.FormatFloat(v, 'f', -1, 64))
		case nil:
			parts = append(parts, "-")
		default:
			parts = append(parts, "?")
		}
	}
	return strings.Join(parts, " ")
}

func TestDashboardRoomSummary(t *testing.T) {
	engine := newRoomEngine(t, `
		_registry['CLU1.t1'] = R.t1
		Dashboard.new('r')
			:section('Pokoje', 'house')
				:room('syp', 'Sypialnia')
				:room('kuch', 'Kuchnia', { title = 'Kuchnia i jadalnia' })
				:room('gab', 'Gabinet')
				:room('all', 'Sypialnia', { kinds = { 'light', 'switch', 'dimmer', 'cover', 'lock' } })
				:room('p', 'Gabinet', { temperature = 'syp.t2', humidity = function() return 55.55 end })
				:room('reg', 'Gabinet', { temperature = 'CLU1.t1' })
			:build()
	`)
	def := roomDefOf(t, engine, "r")
	vals := roomValues(t, engine, "r")
	for _, c := range []struct{ id, label, want string }{
		// l1 and the dimmer at 0.4 on, l2 and the dimmer at 0 off; the open and the moving cover
		// open, the closed one not; two thermometers averaged (21 and 22.4)
		{"syp", "Sypialnia", "2 2 1 21.7 60"},
		// one thermometer, to one decimal; no humidity sensor: none
		{"kuch", "Kuchnia i jadalnia", "0 0 0 19.3 -"},
		// no sensor at all
		{"gab", "Gabinet", "0 0 0 - -"},
		// a switch counts when the room shows switches; a lock never does
		{"all", "Sypialnia", "3 2 1 21.7 60"},
		// a path to an exposed device and a function
		{"p", "Gabinet", "0 0 0 22.4 55.6"},
		// a path in the registry
		{"reg", "Gabinet", "0 0 0 21 -"},
	} {
		w := def.widget(c.id)
		if w == nil || w["type"] != "room" || w["label"] != c.label {
			t.Fatalf("tile %s = %v", c.id, w)
		}
		if got := roomSummary(w); got != c.want {
			t.Errorf("definition of %s = %s, want %s", c.id, got, c.want)
		}
		if got := roomSummary(vals[c.id]); got != c.want {
			t.Errorf("values of %s = %s, want %s", c.id, got, c.want)
		}
	}

	// values are read live
	mustExec(t, engine, `R.l2:switchOn(); R.d4:setValue(0); R.cMove.getState = function() return 0 end; R.t1:setValue(23)`)
	if got := roomSummary(roomValues(t, engine, "r")["syp"]); got != "2 1 0 22.7 60" {
		t.Errorf("after changes = %s", got)
	}

	// a failing function leaves the value out, the rest stays
	mustExec(t, engine, `Dashboard.new('f'):section('S'):room('x', 'Sypialnia', { temperature = function() error('czujnik') end }):build()`)
	if got := roomSummary(roomValues(t, engine, "f")["x"]); got != "2 1 0 - 60" {
		t.Errorf("failing temperature = %s", got)
	}
}

// The overlay holds what section:area shows for the area, with its ids, and acts on the objects
func TestDashboardRoomOverlayIsTheArea(t *testing.T) {
	engine := newRoomEngine(t, `
		Dashboard.new('room'):section('Pokoje'):room('syp', 'Sypialnia', { skip = { 'syp.cc' } }):build()
		Dashboard.new('area'):section('Sypialnia'):area('Sypialnia', { skip = { 'syp.cc' } }):build()
	`)
	room := roomDefOf(t, engine, "room")
	area := roomDefOf(t, engine, "area")
	got := room.overlay("syp")
	if len(got) == 0 || len(got) != len(area.Widgets) {
		t.Fatalf("overlay = %d widgets, area = %d", len(got), len(area.Widgets))
	}
	for i, w := range area.Widgets {
		for _, k := range []string{"id", "type", "label", "icon", "value", "position", "unit", "disabled"} {
			if got[i][k] != w[k] {
				t.Errorf("overlay widget %d %s = %v, area gives %v", i, k, got[i][k], w[k])
			}
		}
	}
	// the overlay is a section of its own, named after the room; the visible one keeps index 0
	if len(room.Sections) != 2 || room.Sections[1]["name"] != "Sypialnia" || room.Sections[0]["room"] != nil {
		t.Errorf("sections = %v", room.Sections)
	}
	if room.widget("syp")["section"] != 0.0 {
		t.Errorf("tile section = %v", room.widget("syp")["section"])
	}

	// actions and values through the normal endpoints
	for _, a := range []string{
		`Dashboard.API.executeAction("room", "area:syp.l2", "toggle", true)`,
		`Dashboard.API.executeAction("room", "area:syp.d0", "set", 30)`,
		`Dashboard.API.executeAction("room", "area:syp.co", "set", 10)`,
	} {
		if res := mustExec(t, engine, a); !strings.Contains(res, `"success":true`) {
			t.Errorf("%s: %s", a, res)
		}
	}
	if got := mustExec(t, engine, `return table.concat({ R.l2:getValue(), R.d0:getValue(), R.cOpen:getPosition() }, ",")`); got != "1,0.3,10" {
		t.Errorf("objects after actions = %s", got)
	}
	vals := roomValues(t, engine, "room")
	if vals["area:syp.l2"]["value"] != true || vals["area:syp.d0"]["value"] != 30.0 || vals["syp"]["on"] != 4.0 {
		t.Errorf("values = %v / %v / %v", vals["area:syp.l2"], vals["area:syp.d0"], vals["syp"])
	}
	if res := mustExec(t, engine, `Dashboard.API.executeAction("room", "syp", "tap")`); !strings.Contains(res, "no server action") {
		t.Errorf("tile action: %s", res)
	}

	// a read-only device is disabled in the overlay as in the area
	mustExec(t, engine, `ExposedObjects:get("syp.l1"):readonly()`)
	if res := mustExec(t, engine, `Dashboard.API.executeAction("room", "area:syp.l1", "toggle", false)`); !strings.Contains(res, "disabled") {
		t.Errorf("readonly in the overlay: %s", res)
	}
}

// A disabled tile disables its room's widgets too
func TestDashboardRoomDisabled(t *testing.T) {
	engine := newRoomEngine(t, `
		OFF = { v = true }
		Dashboard.new('d'):section('S'):room('syp', 'Sypialnia', { disabled = function() return OFF.v end }):build()
	`)
	if res := mustExec(t, engine, `Dashboard.API.executeAction("d", "area:syp.l2", "toggle", true)`); !strings.Contains(res, "disabled") {
		t.Errorf("action in a disabled room: %s", res)
	}
	vals := roomValues(t, engine, "d")
	if vals["syp"]["disabled"] != true || vals["area:syp.l2"]["disabled"] != true {
		t.Errorf("disabled = %v / %v", vals["syp"], vals["area:syp.l2"])
	}
	mustExec(t, engine, `OFF.v = false`)
	if res := mustExec(t, engine, `Dashboard.API.executeAction("d", "area:syp.l2", "toggle", true)`); !strings.Contains(res, `"success":true`) {
		t.Errorf("enabled again: %s", res)
	}
}

// dash:floor with rooms = true: one section of room tiles, one per area, ordered as dash:floor
func TestDashboardFloorRooms(t *testing.T) {
	engine := newRoomEngine(t, `
		expose(VirtualSwitch:new("pompa"), "switch", { path = "kot.pompa", name = "Pompa", area = "Kotłownia", floor = "Piętro" })
		expose(VirtualSwitch:new("dol"), "light", { path = "sal.l", name = "Sufit", area = "Salon", floor = "Parter" })
		Dashboard.new('f')
			:floor('Piętro', { rooms = true, order = { 'Sypialnia' }, titles = { Gabinet = 'Biuro' } })
			:section('Inne'):toggle('x', 'X', 'power', function() return false end, function() end)
			:build()
		Dashboard.new('a'):floor('Piętro', { order = { 'Sypialnia' } }):build()
	`)
	def := roomDefOf(t, engine, "f")
	var visible []string
	for _, s := range def.Sections {
		if s["room"] == nil {
			visible = append(visible, s["name"].(string))
		}
	}
	// one section for the floor, then the hand-made one; the overlays come after them
	if got := strings.Join(visible, "|"); got != "Piętro|Inne" {
		t.Errorf("visible sections = %s", got)
	}
	var tiles, labels []string
	for _, w := range def.Widgets {
		if w["type"] == "room" {
			if w["section"] != 0.0 {
				t.Errorf("tile %v in section %v", w["id"], w["section"])
			}
			tiles = append(tiles, w["id"].(string))
			labels = append(labels, w["label"].(string))
		}
	}
	// Sypialnia first (order), then by name; Kotłownia shows nothing (a switch): left out
	if got := strings.Join(tiles, " "); got != "area:@Sypialnia area:@Gabinet area:@Kuchnia" {
		t.Errorf("tiles = %s", got)
	}
	if got := strings.Join(labels, "|"); got != "Sypialnia|Biuro|Kuchnia" {
		t.Errorf("labels = %s", got)
	}
	if w := def.widget("x"); w == nil || w["section"] != 1.0 {
		t.Errorf("manual widget = %v", w)
	}
	// one overlay per tile, none for the area left out
	overlays := 0
	for _, s := range def.Sections {
		if s["room"] != nil {
			overlays++
		}
	}
	if overlays != len(tiles) {
		t.Errorf("%d overlays for %d tiles", overlays, len(tiles))
	}
	// each tile opens what dash:floor shows for its area
	area := roomDefOf(t, engine, "a")
	var got, want []string
	for _, tile := range tiles {
		for _, w := range def.overlay(tile) {
			got = append(got, w["id"].(string))
		}
	}
	for _, w := range area.Widgets {
		want = append(want, w["id"].(string))
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("overlays:\n got %v\nwant %v", got, want)
	}
	if got := roomSummary(roomValues(t, engine, "f")["area:@Sypialnia"]); got != "2 2 1 21.7 60" {
		t.Errorf("floor tile = %s", got)
	}
	if res := mustExec(t, engine, `Dashboard.API.executeAction("f", "area:gab.l", "toggle", true)`); !strings.Contains(res, `"success":true`) {
		t.Errorf("overlay action: %s", res)
	}
}

// A device exposed later is counted and shown on the next read; polls reuse the rooms
func TestDashboardRoomFollowsExpose(t *testing.T) {
	engine := newRoomEngine(t, `
		C = { n = 0 }
		local inArea = ExposedObjects._inArea
		ExposedObjects._inArea = function(...) C.n = C.n + 1; return inArea(...) end
		Dashboard.new('r'):auth('pin'):pin(1234):section('S'):room('gab', 'Gabinet'):build()
	`)
	count := func() string { return mustExec(t, engine, `C.n`) }
	mustExec(t, engine, `Dashboard.API.getAuth("r")`)
	mustExec(t, engine, `Dashboard.API.getPin("r")`)
	mustExec(t, engine, `Dashboard.API.verifyPin("r", "1234")`)
	mustExec(t, engine, `Dashboard.API.getAppInfo("r")`)
	if n := count(); n != "0" {
		t.Errorf("auth, PIN and the app manifest filled the room %s times", n)
	}
	roomDefOf(t, engine, "r")
	for i := 0; i < 3; i++ {
		roomValues(t, engine, "r")
		mustExec(t, engine, `Dashboard.API.executeAction("r", "area:gab.l", "toggle", true)`)
	}
	after := count()
	if after == "0" {
		t.Fatal("room never filled")
	}
	roomValues(t, engine, "r")
	if n := count(); n != after {
		t.Errorf("filled again without an expose change: %s -> %s", after, n)
	}

	mustExec(t, engine, `
		NEW = VirtualSwitch:new("nowa", { initial = 1 })
		expose(NEW, "light", { path = "gab.nowa", name = "Nowa", area = "Gabinet" })
		expose(VirtualNumber:new("tg", { initial = 20 }), "temperature", { path = "gab.t", name = "T", area = "Gabinet" })
	`)
	def := roomDefOf(t, engine, "r")
	var ids []string
	for _, w := range def.overlay("gab") {
		ids = append(ids, w["id"].(string))
	}
	if got := strings.Join(ids, " "); got != "area:gab.l area:gab.nowa area:gab.t" {
		t.Errorf("overlay after expose = %s", got)
	}
	if got := roomSummary(def.widget("gab")); got != "2 0 0 20 -" {
		t.Errorf("tile after expose = %s", got)
	}
	mustExec(t, engine, `ExposedObjects:get("gab.nowa"):update({ dashboard = false })`)
	if got := roomSummary(roomValues(t, engine, "r")["gab"]); got != "1 0 0 20 -" {
		t.Errorf("tile after dashboard = false = %s", got)
	}
}

func TestDashboardRoomLink(t *testing.T) {
	engine := newRoomEngine(t, `
		Dashboard.new('l'):section('S')
			:room('a', 'Sypialnia', { link = 'sypialnia' })
			:room('b', 'Sypialnia', { link = '/history' })
			:room('c', 'Sypialnia', { link = 'https://example.com/x' })
			:room('d', 'Sypialnia')
			:build()
	`)
	def := roomDefOf(t, engine, "l")
	for id, want := range map[string]interface{}{"a": "/d/sypialnia", "b": "/history", "c": "https://example.com/x", "d": nil} {
		if got := def.widget(id)["link"]; got != want {
			t.Errorf("link of %s = %v, want %v", id, got, want)
		}
	}
	// a tile with a link opens no room: one overlay, for d
	n := 0
	for _, s := range def.Sections {
		if s["room"] != nil {
			n++
			if s["room"] != "d" {
				t.Errorf("overlay for %v", s["room"])
			}
		}
	}
	if n != 1 {
		t.Errorf("%d overlays, want 1", n)
	}
	// still counted
	if got := roomSummary(def.widget("a")); got != "2 2 1 21.7 60" {
		t.Errorf("linked tile = %s", got)
	}
}

func TestDashboardRoomErrors(t *testing.T) {
	engine := newRoomEngine(t, "")
	for _, c := range []struct{ code, want string }{
		{`Dashboard.new('e'):section('S'):room(nil, 'Salon')`, `section:room: id must be a non-empty string`},
		{`Dashboard.new('e'):section('S'):room('r', 42)`, `section:room("r"): expected an area name or a list of them, got number`},
		{`Dashboard.new('e'):section('S'):room('r', {})`, `section:room("r"): expected an area name`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { tytul = 'x' })`, `section:room("r"): unknown option "tytul" (color, disabled, humidity, icon, kinds, link, size, skip, span, temperature, title, visible)`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { kinds = { 'fan' } })`, `section:room("r"): kind "fan" has no widget`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { skip = 'x' })`, `section:room("r"): skip expects a list of paths`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { temperature = 21 })`, `section:room("r"): opts.temperature must be a path or a function`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { humidity = '' })`, `section:room("r"): opts.humidity must be a path or a function`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { title = 1 })`, `section:room("r"): opts.title must be a string`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { icon = 'a b' })`, `section:room("r"): opts.icon must be a Bootstrap icon name`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { disabled = true })`, `section:room("r"): opts.disabled must be a function`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { link = '//evil.example' })`, `leaves the site`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { link = '/\\evil.example' })`, `leaves the site`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { link = 'javascript:alert(1)' })`, `section:room("r"): opts.link must be http(s)://...`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { link = 'data:text/html,x' })`, `opts.link must be http(s)://...`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { link = 'a b' })`, `opts.link must be http(s)://...`},
		{`Dashboard.new('e'):section('S'):room('r', 'Salon', { link = '' })`, `opts.link must be a URL or a dashboard id`},
		{`Dashboard.new('e'):section('S'):room('area:r', 'Salon')`, `ids starting with "area:" are kept for section:area`},
		{`Dashboard.new('e'):header():room('r', 'Salon')`, `section:room("r"): a room tile goes in a section, not the header`},
		{`Dashboard.new('e'):room('r', 'Salon')`, `section:room: call :section() before adding widgets`},
		{`Dashboard.new('e'):floor('Parter'):room('r', 'Salon')`, `dash:floor() ends the current section`},
		{`Dashboard.new('e'):header(); local d = Dashboard.new('e'); d:header(); d:room('r', 'Salon')`, `a room tile goes in a section`},
		{`Dashboard.new('e'):floor('Parter', { rooms = 'yes' })`, `dash:floor: rooms must be true or false`},
		{`Dashboard.new('e'):section('S'):floor('Parter', { rooms = 1 })`, `dash:floor: rooms must be true or false`},
	} {
		res := mustExec(t, engine, `local ok, err = pcall(function()
`+c.code+`
end) return tostring(err)`)
		if !strings.Contains(res, c.want) {
			t.Errorf("%s: %s, want %q", c.code, res, c.want)
		}
		// the error names the module's line, not Dashboard.lua
		if !strings.Contains(res, "<string>:2:") {
			t.Errorf("%s: error does not point at the caller: %s", c.code, res)
		}
	}
}

// Two floors with an area of one name: tiles and their overlays get ids of their own
func TestDashboardFloorRoomsTwoFloors(t *testing.T) {
	engine := newRoomEngine(t, `
		expose(VirtualSwitch:new("x"), "light", { path = "dol.syp", name = "Dół", area = "Sypialnia", floor = "Parter" })
		Dashboard.new('two'):floor('Parter', { rooms = true }):floor('Piętro', { rooms = true }):build()
	`)
	def := roomDefOf(t, engine, "two")
	seen := map[string]bool{}
	for _, w := range def.Widgets {
		id := w["id"].(string)
		if seen[id] {
			t.Errorf("id %s twice", id)
		}
		seen[id] = true
	}
	if !seen["area:@Sypialnia"] || !seen["area:@Sypialnia#2"] {
		t.Errorf("ids = %v", seen)
	}
	if ov := def.overlay("area:@Sypialnia"); len(ov) != 1 || ov[0]["id"] != "area:dol.syp" {
		t.Errorf("Parter overlay = %v", ov)
	}
}

// The same area as a section and as tiles: every widget keeps an id of its own, the visible
// section the plain one, and each acts on the object
func TestDashboardRoomIDsUnique(t *testing.T) {
	engine := newRoomEngine(t, `
		Dashboard.new('u')
			:section('Pokoje'):room('a', 'Gabinet'):room('b', 'Gabinet')
			:section('Gabinet'):area('Gabinet')
			:build()
	`)
	def := roomDefOf(t, engine, "u")
	var got []string
	for _, w := range def.Widgets {
		got = append(got, w["id"].(string))
	}
	if s := strings.Join(got, " "); s != "a b area:gab.l area:gab.l#2 area:gab.l#3" {
		t.Errorf("ids = %s", s)
	}
	if ov := def.overlay("b"); len(ov) != 1 || ov[0]["id"] != "area:gab.l#3" {
		t.Errorf("overlay of b = %v", ov)
	}
	if res := mustExec(t, engine, `Dashboard.API.executeAction("u", "area:gab.l#3", "toggle", true)`); !strings.Contains(res, `"success":true`) {
		t.Errorf("action: %s", res)
	}
	if v := mustExec(t, engine, `return R.lg:getValue()`); v != "1" {
		t.Errorf("lamp = %s", v)
	}
}

// vclu_dashboards lists what the page shows: a room's widgets are not listed again
func TestDashboardRoomList(t *testing.T) {
	engine := newRoomEngine(t, `Dashboard.new('l'):section('S'):room('gab', 'Gabinet'):toggle('x', 'X', 'power', function() return false end, function() end):build()`)
	var list []struct {
		ID          string                   `json:"id"`
		WidgetCount int                      `json:"widgetCount"`
		Widgets     []map[string]interface{} `json:"widgets"`
	}
	dashJSON(t, engine, `Dashboard.API.list()`, &list)
	if len(list) != 1 || list[0].WidgetCount != 2 || len(list[0].Widgets) != 2 ||
		list[0].Widgets[0]["id"] != "gab" || list[0].Widgets[1]["id"] != "x" {
		t.Errorf("list = %+v", list)
	}
}
