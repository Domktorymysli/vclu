package lua

import (
	"strings"
	"testing"
)

// MCP vclu_device_set (MCP.set_value -> ExposedHandle:setValue) writes by the type: a cover
// moves by SetPosition (execute 10), not by its feature 0; a light by its own setValue.
func TestMCPSetValueByType(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_sent = {}
		local clu = { address = "192.168.0.3", executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
		local rol = RemoteCover:new(clu, "ROL1", "salon")
		rol._hasPosition = true
		expose(rol, "cover", { path = "t:rol" })
		expose(RemoteCover:new(clu, "ROL2", "strych"), "cover", { path = "t:old" })
		expose(RemoteLight:new(clu, "DIM1", "kuchnia"), "dimmer", { path = "t:dim" })
		expose(RemoteRGBLight:new(clu, "LED1", "biuro"), "light", { path = "t:led" })
		expose(RemoteSwitch:new(clu, "DOU1", "lampa"), "switch", { path = "t:sw" })
		_vcov = VirtualCover:new("vcov")
		expose(_vcov, "cover", { path = "t:vcov" })
		_vsw = VirtualSwitch:new("vsw")
		expose(_vsw, "switch", { path = "t:vsw" })
		-- a plugin control: set(0, v) is its write
		_ctl = { set = function(self, i, v) self.got = tostring(i) .. "=" .. tostring(v) end }
		expose(_ctl, "number", { path = "t:ctl" })
		function set(path, v)
			_sent = {}
			local r = JSON.decode(MCP.set_value(path, v))
			return tostring(r.ok) .. " " .. table.concat(_sent, ";")
		end
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	for _, c := range []struct{ call, want string }{
		{`set("t:rol", 40)`, "true ROL1:execute(10, 40, 91)"},
		{`set("t:rol", 140.6)`, "true ROL1:execute(10, 100, 91)"},
		{`set("t:rol", "abc")`, "false "},
		// a roller without a position opens or closes
		{`set("t:old", 80)`, "true ROL2:execute(0, 0)"},
		{`set("t:old", 10)`, "true ROL2:execute(1, 0)"},
		{`set("t:dim", 0.5)`, "true DIM1:set(0, 0.5)"},
		// LED RGB: Value is read-only, SetValue is execute 0
		{`set("t:led", 0.25)`, "true LED1:execute(0, 0.25)"},
		{`set("t:sw", 1)`, "true DOU1:set(0, 1)"},
		{`set("t:vcov", 30) .. _vcov:getPosition()`, "true 30"},
		{`set("t:vsw", 1) .. _vsw:getValue()`, "true 1"},
		{`set("t:ctl", 7) .. _ctl.got`, "true 0=7"},
	} {
		if got := strings.TrimSpace(mustExec(t, engine, c.call)); got != strings.TrimSpace(c.want) {
			t.Errorf("%s\n got: %s\nwant: %s", c.call, got, c.want)
		}
	}
}
