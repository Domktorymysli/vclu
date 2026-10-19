package lua

import (
	"strings"
	"testing"
)

// A Grenton RGB light from the OM import is a colour light in Home Assistant: discovery with
// schema json and rgb (rgbw on an RGBW module), the colour reported by the CLU in the state,
// and one JSON command that reaches setColor and setWhite (#41).

const haExposedRGBSetup = `
	_pub = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function() end, unsubscribe = function() end,
	})
	_sent = {}
	local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
	_rgb = RemoteRGBLight:new(clu, "LED1", "salon")
	_rgbw = RemoteRGBLight:new(clu, "LED2", "kuchnia")
	_rgbw._hasWhite = true -- the OM import marks an RGBW module
	_plain = RemoteLight:new(clu, "DIM1", "hol")
	expose(_rgb, "light", { name = "Salon", path = "t:salon" })
	expose(_rgbw, "light", { name = "Kuchnia", path = "t:kuchnia" })
	expose(_plain, "light", { name = "Hol", path = "t:hol" })

	function _last(topic)
		local v
		for _, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
		return tostring(v)
	end
`

func TestHAExposedRGBDiscovery(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedRGBSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	for _, c := range []struct{ id, mode string }{{"salon", "rgb"}, {"kuchnia", "rgbw"}} {
		cfg := mustExec(t, engine, `_last("homeassistant/light/t_`+c.id+`/config")`)
		for _, want := range []string{`"schema":"json"`, `"supported_color_modes":["` + c.mode + `"]`, `"brightness":true`, `"brightness_scale":100`, `"command_topic":"vclu/exposed/t/light/` + c.id + `/set"`} {
			if !strings.Contains(cfg, want) {
				t.Errorf("%s discovery lacks %s: %s", c.id, want, cfg)
			}
		}
		if strings.Contains(cfg, "brightness_state_topic") {
			t.Errorf("%s: a colour light carries its brightness in the JSON state: %s", c.id, cfg)
		}
	}
	plain := mustExec(t, engine, `_last("homeassistant/light/t_hol/config")`)
	if strings.Contains(plain, "schema") || !strings.Contains(plain, "brightness_state_topic") {
		t.Errorf("a plain dimmer is announced as before: %s", plain)
	}
}

func TestHAExposedRGBState(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedRGBSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	// the subscription asks for the value before the colour (and the white): the colour is
	// reported scaled by the value
	if got := mustExec(t, engine, `table.concat(_rgb:subscribeFeatures(), ",") .. "|" .. table.concat(_rgbw:subscribeFeatures(), ",")`); got != "0,6|0,6,15" {
		t.Errorf("subscribed features = %s, want 0,6|0,6,15", got)
	}
	// reports as the subscription delivers them: the colour is kept aside, the value published
	got := mustExec(t, engine, `(function()
		_rgb:fromFeature(6, "#ff5500")
		_rgb:updateState(_rgb:fromFeature(0, 0.5))
		return _last("vclu/exposed/t/light/salon/state")
	end)()`)
	if want := `{"state":"ON","brightness":50,"color_mode":"rgb","color":{"r":255,"g":85,"b":0}}`; got != want {
		t.Errorf("rgb state = %s, want %s", got, want)
	}
	// a colour report alone republishes the state
	got = mustExec(t, engine, `(function()
		_rgb:fromFeature(6, "#0000ff")
		return _last("vclu/exposed/t/light/salon/state")
	end)()`)
	if want := `{"state":"ON","brightness":50,"color_mode":"rgb","color":{"r":0,"g":0,"b":255}}`; got != want {
		t.Errorf("after a colour report = %s, want %s", got, want)
	}
	got = mustExec(t, engine, `(function()
		_rgbw:fromFeature(6, "#102030")
		_rgbw:fromFeature(15, 128)
		_rgbw:updateState(_rgbw:fromFeature(0, 1))
		return _last("vclu/exposed/t/light/kuchnia/state") .. "|" .. _last("vclu/exposed/t/light/kuchnia/brightness")
	end)()`)
	// the brightness topic of an earlier version is cleared (empty retained), not fed
	if want := `{"state":"ON","brightness":100,"color_mode":"rgbw","color":{"r":16,"g":32,"b":48,"w":128}}|`; got != want {
		t.Errorf("rgbw state = %s, want %s", got, want)
	}
	// off: the colour stays known
	got = mustExec(t, engine, `(function()
		_rgbw:updateState(0)
		return _last("vclu/exposed/t/light/kuchnia/state")
	end)()`)
	if want := `{"state":"OFF","brightness":0,"color_mode":"rgbw","color":{"r":16,"g":32,"b":48,"w":128}}`; got != want {
		t.Errorf("rgbw off = %s, want %s", got, want)
	}
}

func TestHAExposedRGBCommands(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedRGBSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		HACommands.handleExposedMessage("vclu/exposed/t/light/kuchnia/set", '{"state":"ON","brightness":40,"color":{"r":1,"g":2,"b":3,"w":9}}')
		local r, g, b = _rgbw:getColor()
		return table.concat(_sent, ";") .. "|" .. r .. "," .. g .. "," .. b .. "," .. _rgbw:getWhite()
	end)()`)
	if want := `LED2:execute(6, "#010203");LED2:execute(12, 9);LED2:execute(0, 0.4)|1,2,3,9`; got != want {
		t.Errorf("rgbw command sent %s, want %s", got, want)
	}
	// the state HA gets right after the command carries the new colour
	state := mustExec(t, engine, `_last("vclu/exposed/t/light/kuchnia/state")`)
	if want := `{"state":"ON","brightness":40,"color_mode":"rgbw","color":{"r":1,"g":2,"b":3,"w":9}}`; state != want {
		t.Errorf("state after the command = %s, want %s", state, want)
	}
	got = mustExec(t, engine, `(function()
		_sent = {}
		HACommands.handleExposedMessage("vclu/exposed/t/light/salon/set", '{"state":"OFF"}')
		HACommands.handleExposedMessage("vclu/exposed/t/light/salon/set", '{"state":"ON"}')
		return table.concat(_sent, ";")
	end)()`)
	if want := `LED1:execute(10, 0);LED1:execute(9, 0)`; got != want {
		t.Errorf("on/off sent %s, want %s", got, want)
	}
}

// The colour comes through the subscription as the CLU sends it: a quoted string in the
// report, next to the white and the value. The first answer and a later report both land.
func TestHAExposedRGBColourFromReport(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedRGBSetup + `
		registerObject("CLU1.LED2", _rgbw)
		_rgbw._registryPath = "CLU1.LED2"
		-- the CLU reports the colour scaled by the value: #FF5500 at 0.5
		__go_send_command = function(ip, script) return 'clientReport:1:{0.5,"#7F2A00",64}' end
		_sid = RemoteSubscription.subscribe("192.168.0.3", {
			{ objId = "LED2", featureIndex = 0 }, { objId = "LED2", featureIndex = 6 }, { objId = "LED2", featureIndex = 15 },
		}, "CLU1")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `_last("vclu/exposed/t/light/kuchnia/state")`)
	if want := `{"state":"ON","brightness":50,"color_mode":"rgbw","color":{"r":254,"g":84,"b":0,"w":64}}`; got != want {
		t.Errorf("after the subscribe answer = %s, want %s", got, want)
	}
	got = mustExec(t, engine, `(function()
		RemoteSubscription.handleReport("192.168.0.3", _sid, '{1,"#0000ff",128}')
		return _last("vclu/exposed/t/light/kuchnia/state")
	end)()`)
	if want := `{"state":"ON","brightness":100,"color_mode":"rgbw","color":{"r":0,"g":0,"b":255,"w":128}}`; got != want {
		t.Errorf("after a report = %s, want %s", got, want)
	}
}

// The colour reported is the output, scaled by the value: dimmed it keeps the light's
// colour, off (#000000) keeps the last one, and a colour changed on the CLU shows.
func TestRGBColourScaledByValue(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	got := mustExec(t, engine, `(function()
		local clu = { executeNoReply = function() end }
		local l = RemoteRGBLight:new(clu, "LED1", "tasma")
		local out = {}
		local function report(hex, v) -- in report order: the value, then the colour
			l:updateState(l:fromFeature(0, v))
			l:fromFeature(RemoteRGBLight.FEATURE_RGB, hex)
			local r, g, b = l:getColor()
			out[#out + 1] = tostring(r) .. "," .. tostring(g) .. "," .. tostring(b)
		end
		report('"#ff0000"', 1)    -- red, full
		report('"#7c0000"', 0.49) -- dimmed: still red
		report('"#000000"', 0)    -- off: the colour stays
		report('"#00007f"', 0.5)  -- blue set on the CLU, at half
		l:setColor(255, 128, 0)   -- set from vCLU
		report('"#7f4000"', 0.5)  -- its report at half: kept exactly
		return table.concat(out, " ") .. " " .. table.concat({ l:getColor() }, ",")
	end)()`)
	if want := "255,0,0 255,0,0 255,0,0 0,0,254 255,128,0 255,128,0"; got != want {
		t.Errorf("colours\n got: %s\nwant: %s", got, want)
	}
}

// Before the first colour report the state has no color_mode (HA warns about a mode
// without a colour); a read-only RGB light is a binary_sensor and gets ON/OFF.
func TestHAExposedRGBWithoutColourAndReadonly(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedRGBSetup + `
		_ro = RemoteRGBLight:new({ executeNoReply = function() end }, "LED3", "sypialnia")
		expose(_ro, "light", { name = "Sypialnia", path = "t:sypialnia", readonly = true })
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		_rgb:updateState(0.5)
		return _last("vclu/exposed/t/light/salon/state")
	end)()`)
	if want := `{"state":"ON","brightness":50}`; got != want {
		t.Errorf("state before a colour = %s, want %s", got, want)
	}
	got = mustExec(t, engine, `(function()
		_ro:fromFeature(6, "#ff0000")
		_ro:updateState(1)
		return _last("homeassistant/binary_sensor/t_sypialnia/config"):match('"state_topic":"[^"]+"') .. "|" .. _last("vclu/exposed/t/light/sypialnia/state") .. "|" .. tostring(_last("homeassistant/light/t_sypialnia/config"):find("schema"))
	end)()`)
	if want := `"state_topic":"vclu/exposed/t/light/sypialnia/state"|ON|nil`; got != want {
		t.Errorf("read-only RGB light = %s, want %s", got, want)
	}
}

// Picking a colour on a light that is off: HA sends ON with the colour and no
// brightness, and the light must come on, not only change colour.
func TestHAExposedRGBColourOnOffLightSwitchesOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedRGBSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		_rgb:updateState(0)
		_sent = {}
		HACommands.handleExposedMessage("vclu/exposed/t/light/salon/set", '{"state":"ON","color":{"r":255,"g":0,"b":12}}')
		local off = table.concat(_sent, ";")
		_rgb:updateState(0.5)
		_sent = {}
		HACommands.handleExposedMessage("vclu/exposed/t/light/salon/set", '{"state":"ON","color":{"r":0,"g":255,"b":0}}')
		return off .. "|" .. table.concat(_sent, ";")
	end)()`)
	// off: colour, then SwitchOn; already on: the colour alone
	if want := `LED1:execute(6, "#ff000c");LED1:execute(9, 0)|LED1:execute(6, "#00ff00")`; got != want {
		t.Errorf("sent %s, want %s", got, want)
	}
}
