package lua

import "testing"

// Method and feature indexes of a Grenton dimmer (DIMMER, class 5) and an RGB(W) LED
// (LEDRGB, class 9) come from Object Manager's device-interfaces. LEDRGB numbers its
// methods unlike a dimmer: SwitchOn 9, SwitchOff 10, Switch 11, HoldValue 7, and its
// Value is read-only (SetValue is execute 0). A dimmer's execute 0 is Hold, not SetValue.

const grentonLightSetup = `
	_sent = {}
	local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
	_dim = RemoteLight:new(clu, "DIM1", "kuchnia")
	_led = RemoteRGBLight:new(clu, "LED1", "salon_led")
	registerObject("CLU1.DIM1", _dim)
	registerObject("CLU1.LED1", _led)

	function _run(f)
		_sent = {}
		f()
		return table.concat(_sent, ";")
	end
`

func TestGrentonLightMethods(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(grentonLightSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	for _, c := range []struct{ code, want string }{
		{`_dim:setValue(0.5)`, "DIM1:set(0, 0.5)"},
		{`_dim:setValue(0.5, 300)`, "DIM1:set(1, 300);DIM1:set(0, 0.5)"},
		{`_dim:setBrightness(30)`, "DIM1:set(0, 0.3)"},
		{`_dim:switchOn()`, "DIM1:execute(2, 0)"},
		{`_dim:switchOff()`, "DIM1:execute(3, 0)"},
		{`_dim:toggle()`, "DIM1:execute(1, 0)"},
		{`_dim:dim(500)`, "DIM1:execute(0, 500)"},

		{`_led:setValue(0.5)`, "LED1:execute(0, 0.5)"},
		{`_led:setBrightness(30, 200)`, "LED1:execute(0, 0.3, 200)"},
		{`_led:switchOn()`, "LED1:execute(9, 0)"},
		{`_led:switchOff(0, 400)`, "LED1:execute(10, 0, 400)"},
		{`_led:toggle()`, "LED1:execute(11, 0)"},
		{`_led:dim(500)`, "LED1:execute(7, 500)"},
		{`_led:setWhite(300)`, "LED1:execute(12, 255)"},
		{`_led:setWhite(127.6, 100)`, "LED1:execute(12, 128, 100)"},
		{`_led:setHue(120)`, "LED1:execute(1, 120)"},
		{`_led:setSaturation(0.5)`, "LED1:execute(2, 0.5)"},
		{`_led:setColor(255, 0, 10)`, `LED1:execute(6, "#ff000a")`},
	} {
		got := mustExec(t, engine, `_run(function() `+c.code+` end)`)
		if got != c.want {
			t.Errorf("%s sent %s, want %s", c.code, got, c.want)
		}
	}

	// SetValue on a LED stores the value at once, as set(0) does on a dimmer.
	if got := mustExec(t, engine, `(function() _led:setValue(0.25); return tostring(_led:getValue()) end)()`); got != "0.25" {
		t.Errorf("LED value after setValue = %s, want 0.25", got)
	}
}

func TestGrentonLightBrightnessRounds(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(grentonLightSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		local out = { tostring(_dim:getBrightness()) }
		for _, v in ipairs({ 0.29, 0.555, 1, 0 }) do
			_dim:updateState(v)
			table.insert(out, tostring(_dim:getBrightness()))
		end
		return table.concat(out, ",")
	end)()`)
	if got != "nil,29,56,100,0" {
		t.Errorf("getBrightness = %s, want nil,29,56,100,0", got)
	}

	// Home Assistant gets the rounded brightness, and no made-up colour for a LED.
	pub := mustExec(t, engine, `(function()
		local p = {}
		HAStatePublisher.setup({ publish = function(self, topic, payload) p[topic] = payload; return true end })
		_dim:updateState(0.29)
		_led:updateState(0.29)
		return tostring(p["vclu/clu1/light/dim1/brightness"]) .. "|" .. tostring(p["vclu/clu1/light/led1/state"])
	end)()`)
	if pub != `29|{"state":"ON","brightness":29}` {
		t.Errorf("published = %s", pub)
	}
}

// An RGB light from the OM import is announced with schema json, so Home Assistant sends
// one JSON command; plain ON/OFF reach the LED's own SwitchOn/SwitchOff.
func TestHALightJSONCommands(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(grentonLightSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	for _, c := range []struct{ topic, payload, want string }{
		{"led1/set", `{"state":"ON"}`, "LED1:execute(9, 0)"},
		{"led1/set", `{"state":"OFF"}`, "LED1:execute(10, 0)"},
		{"led1/set", `{"state":"ON","brightness":40}`, "LED1:execute(0, 0.4)"},
		{"led1/set", `{"state":"ON","brightness":150}`, "LED1:execute(0, 1)"},
		{"led1/set", `{"state":"ON","color":{"r":255,"g":0,"b":10}}`, `LED1:execute(6, "#ff000a")`},
		{"led1/set", `{"state":"ON","color":{"h":120,"s":50},"brightness":20}`,
			"LED1:execute(1, 120);LED1:execute(2, 0.5);LED1:execute(0, 0.2)"},
		{"led1/set", "ON", "LED1:execute(9, 0)"},
		{"led1/set", "OFF", "LED1:execute(10, 0)"},
		{"dim1/set", "ON", "DIM1:execute(2, 0)"},
		{"dim1/set", "OFF", "DIM1:execute(3, 0)"},
	} {
		got := mustExec(t, engine, `_run(function()
			HACommands.handleMessage("vclu/clu1/light/`+c.topic+`", [[`+c.payload+`]])
		end)`)
		if got != c.want {
			t.Errorf("%s %s sent %s, want %s", c.topic, c.payload, got, c.want)
		}
	}

	bad := mustExec(t, engine, `(function()
		_sent = {}
		local ok, err = HACommands.handleMessage("vclu/clu1/light/led1/set", "{bad")
		return tostring(ok) .. "|" .. tostring(err) .. "|" .. #_sent
	end)()`)
	if bad != "false|invalid JSON command|0" {
		t.Errorf("invalid JSON = %s, want false|invalid JSON command|0", bad)
	}
}
