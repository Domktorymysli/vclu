package lua

import (
	"strings"
	"testing"
)

// A cover tile shows a slat slider when its cover has slats, as the group tile does (#227).

const dashCoverTiltSetup = `
	_dashboards = {}
	T = { tilt = 30, sent = {} }
	local function moves()
		return { getPosition = function() return 40 end, getState = function() return 0 end,
			open = function() end, close = function() end, stop = function() end }
	end
	local slats = moves()
	slats.getTilt = function() return T.tilt end
	slats.setTilt = function(a) T.sent[#T.sent + 1] = a end
	local unread = moves()
	unread.getTilt = function() return nil end
	unread.setTilt = function() end
	local readOnly = moves()
	readOnly.getTilt = function() return 30 end
	local locked = moves()
	locked.getTilt = function() return 10 end
	locked.setTilt = function(a) T.sent[#T.sent + 1] = a end

	SENT = {}
	local clu = { executeNoReply = function(self, n, s) SENT[#SENT + 1] = s end }
	local function roller(id, tilt)
		local c = RemoteCover:new(clu, id, id)
		c._hasPosition, c._hasTilt, c._lastState, c._movement = true, tilt, 50, 0
		if tilt then c._lastTilt = 45 end
		return c
	end
	expose(roller('ROL1', true), 'cover', { path = 'salon.lamele', name = 'Lamele', area = 'Salon' })
	expose(roller('ROL2', false), 'cover', { path = 'salon.roleta', name = 'Roleta', area = 'Salon' })
	expose(roller('ROL3', true), 'cover', { path = 'salon.ro', name = 'Tylko odczyt', area = 'Salon', readonly = true })

	Dashboard.new('ct')
		:section('Rolety')
			:cover('slats', 'Żaluzja', 'window', slats)
			:cover('plain', 'Roleta', 'window', moves())
			:cover('unread', 'Bez odczytu', 'window', unread)
			:cover('readonly', 'Bez setTilt', 'window', readOnly)
			:cover('locked', 'Zablokowana', 'window', locked, { disabled = function() return true end })
		:section('Salon')
			:area('Salon', { kinds = { 'cover' } })
		:build()
`

func newCoverTiltEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashCoverTiltSetup)
	return engine
}

func coverTiltAction(t *testing.T, engine *LuaEngine, widget, value string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("ct", "`+widget+`", "tilt", `+value+`)`, &res)
	return res
}

// The angle is in the definition and the values while getTilt gives a number and setTilt is
// given; a cover without slats carries no tilt key at all.
func TestDashboardCoverTiltData(t *testing.T) {
	engine := newCoverTiltEngine(t)

	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("ct")`, &def)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("ct")`, &vals)

	want := map[string]interface{}{
		"slats": 30.0, "plain": nil, "unread": nil, "readonly": nil, "locked": 10.0,
		"area:salon.lamele": 45.0, "area:salon.roleta": nil, "area:salon.ro": 45.0,
	}
	for id, tilt := range want {
		w := def.widget(id)
		if w == nil {
			t.Fatalf("no widget %s in %v", id, def.Widgets)
		}
		for where, got := range map[string]map[string]interface{}{"definition": w, "values": vals[id]} {
			v, ok := got["tilt"]
			if tilt == nil && ok || tilt != nil && v != tilt {
				t.Errorf("%s %s tilt = %v (present %v), want %v", id, where, v, ok, tilt)
			}
		}
	}
	// disabled and readonly covers keep the angle; the page disables the tile, both sliders
	if vals["locked"]["disabled"] != true || vals["area:salon.ro"]["disabled"] != true {
		t.Errorf("locked = %v, readonly = %v", vals["locked"], vals["area:salon.ro"])
	}

	// an angle reported later shows up
	mustExec(t, engine, `T.tilt = 89.6`)
	dashJSON(t, engine, `Dashboard.API.getValues("ct")`, &vals)
	if vals["slats"]["tilt"] != 90.0 {
		t.Errorf("slats tilt = %v, want 90 (rounded)", vals["slats"]["tilt"])
	}
}

// The tilt action calls setTilt with the angle clamped to 0..90; without setTilt, on a disabled
// tile or with no number it is refused.
func TestDashboardCoverTiltAction(t *testing.T) {
	engine := newCoverTiltEngine(t)
	for _, v := range []string{"45", "120", "-5", `"30"`} {
		if res := coverTiltAction(t, engine, "slats", v); res["success"] != true {
			t.Errorf("tilt %s: %v", v, res)
		}
	}
	if got := mustExec(t, engine, `return table.concat(T.sent, ",")`); got != "45,90,0,30" {
		t.Errorf("setTilt got %s, want 45,90,0,30", got)
	}

	for _, c := range []struct{ widget, value, err string }{
		{"plain", "10", "No setTilt function"},
		{"readonly", "10", "No setTilt function"},
		{"locked", "10", "Widget is disabled"},
		{"slats", "nil", "Value must be a number"},
		{"slats", `"abc"`, "Value must be a number"},
		{"slats", "0/0", "Value must be a number"},
	} {
		res := coverTiltAction(t, engine, c.widget, c.value)
		if res["error"] != c.err {
			t.Errorf("%s tilt %s = %v, want error %q", c.widget, c.value, res, c.err)
		}
	}
	if got := mustExec(t, engine, `return #T.sent`); got != "4" {
		t.Errorf("refused actions reached setTilt: %s calls", got)
	}
}

// A tile from expose() sends the angle to the Grenton roller (SetLamelPosition, execute 9);
// a readonly one refuses it.
func TestDashboardCoverTiltArea(t *testing.T) {
	engine := newCoverTiltEngine(t)
	if res := coverTiltAction(t, engine, "area:salon.lamele", "60"); res["success"] != true {
		t.Fatalf("tilt: %v", res)
	}
	if got := mustExec(t, engine, `return table.concat(SENT, ";")`); !strings.Contains(got, "ROL1:execute(9, 60)") {
		t.Errorf("sent %q, want ROL1:execute(9, 60)", got)
	}
	if res := coverTiltAction(t, engine, "area:salon.ro", "60"); res["error"] == nil {
		t.Errorf("readonly tilt: %v", res)
	}
	if got := mustExec(t, engine, `return #SENT`); got != "1" {
		t.Errorf("readonly tilt sent a command: %s", got)
	}
}
