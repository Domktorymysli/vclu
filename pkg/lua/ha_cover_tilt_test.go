package lua

import (
	"strings"
	"testing"
)

// A Grenton blind (ROLLER_SHUTTER, class 24, with slats) shows a tilt slider in Home Assistant
// (#175). Grenton counts the slats in degrees, 0 = open .. 90 = closed (feature 8
// LamelPosition, execute 9 SetLamelPosition); HA tilt is 0 = closed .. 100 = open.

const haCoverTiltSetup = `
	_pub = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function() end, unsubscribe = function() end,
	})
	_sent = {}
	local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
	function _rol(id, position, tilt)
		local r = RemoteCover:new(clu, id, id)
		r._hasPosition = position -- the OM import marks class 24
		r._hasTilt = tilt         -- and a class 24 roller with LamelMoveTimeout above 0
		return r
	end
	_blind = _rol("ROL1", true, true)
	_shutter = _rol("ROL2", true, false)
	_sh = _rol("ROL3", false, true) -- ROLLER_SH: no position, so no tilt either
	_ro = _rol("ROL4", true, true)
	_hid = _rol("ROL5", true, true)
	expose(_blind, "cover", { name = "Salon", path = "t:blind" })
	expose(_shutter, "cover", { name = "Garaz", path = "t:shutter" })
	expose(_sh, "cover", { name = "Strych", path = "t:sh" })
	expose(_ro, "cover", { name = "Kuchnia", path = "t:ro", readonly = true })
	expose(_hid, "cover", { name = "Biuro", path = "t:hid" })
	expose(VirtualCover:new("taras"), "cover", { name = "Taras", path = "t:virtual" })

	function _last(topic)
		local v
		for _, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
		return tostring(v)
	end
`

func newTiltEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(haCoverTiltSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	return engine
}

func TestHACoverTiltDiscovery(t *testing.T) {
	engine := newTiltEngine(t)

	blind := findDiscovery(t, engine, "t_blind")
	for _, want := range []string{
		`"tilt_status_topic":"vclu/exposed/t/cover/blind/tilt"`,
		`"tilt_command_topic":"vclu/exposed/t/cover/blind/tilt/set"`,
		`"tilt_min":0`, `"tilt_max":100`, `"tilt_closed_value":0`, `"tilt_opened_value":100`,
		`"unique_id":"t_blind"`,
	} {
		if !strings.Contains(blind, want) {
			t.Errorf("blind discovery lacks %s: %s", want, blind)
		}
	}

	// A shutter without slats, a ROLLER_SH and a virtual cover get no tilt
	for _, id := range []string{"t_shutter", "t_sh", "t_virtual"} {
		p := findDiscovery(t, engine, id)
		if p == "" || strings.Contains(p, "tilt") {
			t.Errorf("%s discovery = %s, want one without tilt", id, p)
		}
	}

	// A readonly blind shows its slats but takes no command
	ro := findDiscovery(t, engine, "t_ro")
	if !strings.Contains(ro, `"tilt_status_topic"`) || strings.Contains(ro, "tilt_command_topic") {
		t.Errorf("readonly blind discovery = %s", ro)
	}
}

func TestHACoverTiltScale(t *testing.T) {
	engine := newTiltEngine(t)

	for _, c := range []struct{ fn, in, want string }{
		{"coverTiltToHA", "0", "100"}, {"coverTiltToHA", "90", "0"}, {"coverTiltToHA", "45", "50"},
		{"coverTiltToHA", "30", "67"}, {"coverTiltToHA", "120", "0"}, {"coverTiltToHA", "-5", "100"},
		{"coverTiltToHA", "abc", "nil"},
		{"coverTiltFromHA", "100", "0"}, {"coverTiltFromHA", "0", "90"}, {"coverTiltFromHA", "50", "45"},
		{"coverTiltFromHA", "67", "30"}, {"coverTiltFromHA", "150", "0"}, {"coverTiltFromHA", "-5", "90"},
		{"coverTiltFromHA", "33.3", "60"}, {"coverTiltFromHA", "abc", "nil"}, {"coverTiltFromHA", "", "nil"},
		{"coverTiltFromHA", "nan", "nil"}, {"coverTiltFromHA", "inf", "nil"},
	} {
		got := mustExec(t, engine, `tostring(HAStatePublisher.`+c.fn+`("`+c.in+`"))`)
		if got != c.want {
			t.Errorf("%s(%q) = %s, want %s", c.fn, c.in, got, c.want)
		}
	}
}

// A reported feature 8 is the blind's tilt: getTilt returns the angle, HA gets it on its scale.
func TestHACoverTiltPublishes(t *testing.T) {
	engine := newTiltEngine(t)

	got := mustExec(t, engine, `(function()
		local out = {}
		for _, deg in ipairs({ 90, 0, 45 }) do
			_pub = {}
			local v = _blind:fromFeature(RemoteCover.FEATURE_TILT, deg)
			table.insert(out, tostring(v) .. "/" .. tostring(_blind:getTilt()) .. "/" ..
				_last("vclu/exposed/t/cover/blind/tilt"))
		end
		-- the position does not change the tilt; a report of the same angle publishes nothing
		_pub = {}
		_blind:updateState(_blind:fromFeature(RemoteCover.FEATURE_POSITION, 30))
		table.insert(out, _last("vclu/exposed/t/cover/blind/position") .. "/" .. _last("vclu/exposed/t/cover/blind/tilt"))
		_pub = {}
		_blind:fromFeature(RemoteCover.FEATURE_TILT, 45)
		table.insert(out, #_pub)
		return table.concat(out, ",")
	end)()`)
	if got != "nil/90/0,nil/0/100,nil/45/50,30/50,0" {
		t.Errorf("tilt reports = %s, want nil/90/0,nil/0/100,nil/45/50,30/50,0", got)
	}

	// A shutter without slats keeps no tilt and publishes none
	none := mustExec(t, engine, `(function()
		_pub = {}
		_shutter:fromFeature(RemoteCover.FEATURE_TILT, 45)
		_shutter:updateState(_shutter:fromFeature(RemoteCover.FEATURE_POSITION, 20))
		for _, p in ipairs(_pub) do if p.topic:find("/tilt") then return "published " .. p.topic end end
		return tostring(_shutter:getTilt()) .. "/" .. tostring(_shutter:getState())
	end)()`)
	if none != "nil/nil" {
		t.Errorf("shutter without slats = %s, want nil/nil", none)
	}
}

func TestHACoverTiltCommands(t *testing.T) {
	engine := newTiltEngine(t)

	got := mustExec(t, engine, `(function()
		for _, p in ipairs({ "100", "0", "50", "150", "-5", "33.3" }) do
			HACommands.handleExposedMessage("vclu/exposed/t/cover/blind/tilt/set", p)
		end
		return table.concat(_sent, ";")
	end)()`)
	want := "ROL1:execute(9, 0);ROL1:execute(9, 90);ROL1:execute(9, 45);" +
		"ROL1:execute(9, 0);ROL1:execute(9, 90);ROL1:execute(9, 60)"
	if got != want {
		t.Errorf("tilt commands\n got: %s\nwant: %s", got, want)
	}

	for _, c := range []struct{ topic, payload, want string }{
		{"blind/tilt/set", "abc", "false|invalid tilt value|0"},
		{"blind/tilt/set", "", "false|invalid tilt value|0"},
		{"shutter/tilt/set", "50", "false|cover has no tilt|0"},
		{"sh/tilt/set", "50", "false|cover has no tilt|0"},
		{"virtual/tilt/set", "50", "false|cover has no tilt|0"},
	} {
		refused := mustExec(t, engine, `(function()
			_sent = {}
			local ok, err = HACommands.handleExposedMessage("vclu/exposed/t/cover/`+c.topic+`", "`+c.payload+`")
			return tostring(ok) .. "|" .. tostring(err) .. "|" .. #_sent
		end)()`)
		if refused != c.want {
			t.Errorf("%s %q = %s, want %s", c.topic, c.payload, refused, c.want)
		}
	}

	// tilt on another type changes nothing
	sw := mustExec(t, engine, `(function()
		_sw = VirtualSwitch:new("lampa")
		expose(_sw, "switch", { path = "t:lampa" })
		local ok, err = HACommands.handleExposedMessage("vclu/exposed/t/switch/lampa/tilt/set", "ON")
		return tostring(ok) .. "|" .. tostring(err) .. "|" .. tostring(_sw:getValue())
	end)()`)
	if !strings.HasPrefix(sw, "false|only a cover takes tilt|") || strings.HasSuffix(sw, "|1") {
		t.Errorf("tilt on a switch = %s", sw)
	}
}

// A readonly or hidden blind refuses a tilt before anything is sent to the CLU.
func TestHACoverTiltAccess(t *testing.T) {
	engine := newTiltEngine(t)

	got := mustExec(t, engine, `(function()
		AccessControl.set("mqtt", "t:blind", "readonly")
		AccessControl.set("mqtt", "t:hid", "hidden")
		_sent = {}
		local out = {}
		for _, id in ipairs({ "ro", "blind", "hid" }) do
			local ok, err = HACommands.handleExposedMessage("vclu/exposed/t/cover/" .. id .. "/tilt/set", "50")
			table.insert(out, tostring(ok) .. ":" .. tostring(err))
		end
		return table.concat(out, ",") .. "|" .. #_sent
	end)()`)
	want := "false:object is readonly,false:access denied: t:blind,false:access denied: t:hid|0"
	if got != want {
		t.Errorf("tilt on readonly/hidden\n got: %s\nwant: %s", got, want)
	}
}

// setTilt keeps taking degrees, clamped and rounded; a non-number is an error, nothing is sent.
func TestRemoteCoverSetTilt(t *testing.T) {
	engine := newTiltEngine(t)

	got := mustExec(t, engine, `(function()
		_sent = {}
		for _, a in ipairs({ 0, 90, 45.4, 120, -3 }) do _blind:setTilt(a) end
		local ok = pcall(_blind.setTilt, _blind, "abc")
		return table.concat(_sent, ";") .. "|" .. tostring(ok)
	end)()`)
	want := "ROL1:execute(9, 0);ROL1:execute(9, 90);ROL1:execute(9, 45);ROL1:execute(9, 90);ROL1:execute(9, 0)|false"
	if got != want {
		t.Errorf("setTilt\n got: %s\nwant: %s", got, want)
	}
}

// From the OM import (registry path): a blind is subscribed to feature 8 next to 0 and 7, a
// reported angle reaches getTilt, the registry tilt topic and the dashboard slat slider, and a
// tilt from HA on vclu/<clu>/cover/<obj>/tilt/set moves the slats.
func TestRegistryCoverTilt(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_scripts = {}
		_sent = {}
		__vclu_ip = "192.168.0.100"
		__go_send_command = function(ip, script, delay)
			table.insert(_scripts, script)
			local sid = script:match("clientRegister%('.-', %d+, (%d+),")
			return "clientReport:" .. sid .. ":{0,30,45,0,20}", nil
		end
		local clu = { address = "192.168.0.3", executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
		_blind = RemoteCover:new(clu, "ROL1", "salon")
		_blind._hasPosition = true
		_blind._hasTilt = true
		registerObject("CLU1.ROL1", _blind)
		_shutter = RemoteCover:new(clu, "ROL2", "garaz")
		_shutter._hasPosition = true
		registerObject("CLU1.ROL2", _shutter)

		_published = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload)
				if topic:match("/cover/") then _published[topic] = payload end
				return true
			end,
		})

		RemoteSubscription.config.enabled = true
		RemoteSubscription.config.whitelist = { ["CLU1.ROL1"] = true, ["CLU1.ROL2"] = true }
		RemoteSubscription.config.batchSize = 8
		RemoteSubscription.config.batchDelay = 0
		RemoteSubscription.activeSubscriptions = {}
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	subs := mustExec(t, engine, `(function()
		RemoteSubscription.autoSubscribe()
		return table.concat(_scripts, "|"):match("{{.*}}")
	end)()`)
	if !contains(subs, "{ROL1, 0}, {ROL1, 7}, {ROL1, 8}") || !contains(subs, "{ROL2, 0}, {ROL2, 7}") {
		t.Errorf("subscription = %s", subs)
	}
	if contains(subs, "{ROL2, 8}") {
		t.Errorf("shutter without slats subscribed to feature 8: %s", subs)
	}

	got := mustExec(t, engine, `(function()
		RemoteSubscription.activeSubscriptions = {}
		RemoteSubscription.activeSubscriptions["192.168.0.3:5"] = {
			cluSerial = "CLU1",
			objects = {
				{ objId = "ROL1", featureIndex = 0 }, { objId = "ROL1", featureIndex = 7 },
				{ objId = "ROL1", featureIndex = 8 },
			},
		}
		local out = {}
		local function report(values)
			_published = {}
			RemoteSubscription.updateRegistry("192.168.0.3", 5, values)
			table.insert(out, string.format("%s/%s/%s %s",
				tostring(_blind:getPosition()), tostring(_blind:getState()), tostring(_blind:getTilt()),
				tostring(_published["vclu/clu1/cover/rol1/tilt"])))
		end
		report({ 0, 40, 90 })
		report({ 0, 40, 30 }) -- only the slats moved
		return table.concat(out, ";")
	end)()`)
	if got != "40/0/90 0;40/0/30 67" {
		t.Errorf("registry tilt reports = %s, want 40/0/90 0;40/0/30 67", got)
	}

	cmds := mustExec(t, engine, `(function()
		_sent = {}
		local out = {}
		for _, c in ipairs({ { "rol1", "100" }, { "rol1", "abc" }, { "rol2", "50" } }) do
			local ok, err = HACommands.handleMessage("vclu/clu1/cover/" .. c[1] .. "/tilt/set", c[2])
			table.insert(out, tostring(ok) .. ":" .. tostring(err))
		end
		return table.concat(out, ",") .. "|" .. table.concat(_sent, ";")
	end)()`)
	if cmds != "true:nil,false:invalid tilt value,false:cover has no tilt|ROL1:execute(9, 0)" {
		t.Errorf("registry tilt commands = %s", cmds)
	}

	// The dashboard slat slider shows for the blind only
	var e map[string]interface{}
	mustExec(t, engine, `Dashboard.new('tilt'):section('R', 'window', { covers = true })
		:covers('b', 'Salon', { 'CLU1.ROL1' }):covers('s', 'Garaz', { 'CLU1.ROL2' }):build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("tilt")`, &def)
	if e = def.widget("b"); e["tilt"] != 30.0 {
		t.Errorf("blind tile = %v, want tilt 30", e)
	}
	if e = def.widget("s"); e["tilt"] != nil {
		t.Errorf("shutter tile = %v, want no tilt", e)
	}
}
