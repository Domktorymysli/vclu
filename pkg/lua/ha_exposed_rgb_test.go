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
	// the subscription asks for the colour (and the white) before the value
	if got := mustExec(t, engine, `table.concat(_rgb:subscribeFeatures(), ",") .. "|" .. table.concat(_rgbw:subscribeFeatures(), ",")`); got != "6,0|6,15,0" {
		t.Errorf("subscribed features = %s, want 6,0|6,15,0", got)
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
