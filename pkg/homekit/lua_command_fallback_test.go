package homekit

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/brutella/hap/characteristic"
)

// An object with only set/get: brightness and ON/OFF of a dimmer, and a cover
// target, fall back to the handle's setValue (lua/command.lua).
func TestCommandFallbackToSetValue(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_writes = {}
		local function plain(name)
			local o = { _v = 0 }
			function o:get() return self._v end
			function o:getValue() return self._v end
			function o:set(_, v) self._v = v; table.insert(_writes, name .. "=" .. tostring(v)) end
			return o
		end
		expose(plain("dim"), "dimmer", { name = "Kinkiet", path = "hk:plaindim" })
		expose(plain("cov"), "cover", { name = "Roleta", path = "hk:plaincov" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}
	writes := func() string {
		got, err := engine.Execute(`local w = table.concat(_writes, ";"); _writes = {}; return w`)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	dim := s.createExposedAccessory(ExposedObject{Path: "hk:plaindim", Type: "dimmer", Name: "Kinkiet", Dimmable: true, Value: float64(0)}, false)
	findChar(dim.Accessory, characteristic.TypeBrightness).SetValueRequest(33, homeAppRequest())
	if got := writes(); got != "dim=0.33" {
		t.Errorf("slider 33%% wrote %q, want dim=0.33", got)
	}
	findChar(dim.Accessory, characteristic.TypeOn).SetValueRequest(false, homeAppRequest())
	if got := writes(); got != "dim=0" {
		t.Errorf("OFF wrote %q, want dim=0", got)
	}
	findChar(dim.Accessory, characteristic.TypeOn).SetValueRequest(true, homeAppRequest())
	if got := writes(); got != "dim=1" {
		t.Errorf("ON wrote %q, want dim=1", got)
	}

	cov := s.createExposedAccessory(ExposedObject{Path: "hk:plaincov", Type: "cover", Name: "Roleta", Value: float64(0)}, false)
	findChar(cov.Accessory, characteristic.TypeTargetPosition).SetValueRequest(70, homeAppRequest())
	if got := writes(); got != "cov=70" {
		t.Errorf("target 70 wrote %q, want cov=70", got)
	}
}

// A failing write is an error (the tile reverts), as before the move to lua/command.lua.
func TestCommandErrorReported(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		local o = {}
		function o:get() return 0 end
		function o:getValue() return 0 end
		function o:set() end
		function o:setPosition() error("motor stuck") end
		expose(o, "cover", { name = "Roleta", path = "hk:stuck" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}
	if s.command("hk:stuck", "hk.setPosition(h, 10)", nil) {
		t.Fatal("a write that raised an error reported success")
	}
}
