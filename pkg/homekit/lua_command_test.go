package homekit

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/brutella/hap/characteristic"
)

// The write script runs on the real Lua engine: an exposed object is switched,
// a missing one is reported (and the tile reverted), not silently ignored.
func TestCommandOnRealLua(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		sw = VirtualSwitch:new("hk")
		expose(sw, "switch", { name = "Lampa", path = "hk:lamp" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}

	w := s.createExposedAccessory(ExposedObject{Path: "hk:lamp", Type: "switch", Name: "Lampa", Value: float64(0)}, false)
	findChar(w.Accessory, characteristic.TypeOn).SetValueRequest(true, homeAppRequest())
	if v, err := engine.Execute(`return tostring(sw:getValue())`); err != nil || v != "1" {
		t.Fatalf("exposed object not switched: %q %v", v, err)
	}

	gone := s.createExposedAccessory(ExposedObject{Path: "hk:gone", Type: "switch", Name: "Nie ma", Value: float64(0)}, false)
	on := findChar(gone.Accessory, characteristic.TypeOn)
	if s.command("hk:gone", "h:setValue(1)", func(v float64) { on.SetValueRequest(v > 0, nil) }) {
		t.Fatal("write to a missing object reported success")
	}
}
