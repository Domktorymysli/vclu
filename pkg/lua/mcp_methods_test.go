package lua

import (
	"strings"
	"testing"
)

// Every public method of the built-in classes (not an on* event registration, not a raw
// or internal one) is callable by vclu_device_execute, whatever _allowedMethods lists; a
// method added to an exposed instance too.
func TestMCPExecutePublicMethodsOfBuiltInClasses(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, true)
	got := mustExec(t, engine, `
		local internal = { new = true, get = true, set = true, execute = true, call = true,
			updateState = true, emit = true, onEvent = true }
		local remote = { "RemoteSwitch", "RemoteSensor", "RemoteLight", "RemoteRGBLight",
			"RemoteLedChannel", "RemoteCover", "RemoteNumber", "RemoteThermometer",
			"RemoteValueSensor", "RemoteThermostat", "RemotePanel", "RemotePanelButton" }
		local virtual = { "VirtualSwitch", "VirtualDimmer", "VirtualCover", "VirtualSensor",
			"VirtualNumber", "VirtualText", "VirtualThermostat", "VirtualPresence" }
		local function publicOf(class)
			local names, seen, t = {}, {}, class
			while type(t) == "table" do
				for k, v in pairs(t) do
					if type(k) == "string" and type(v) == "function" and not seen[k]
					   and not k:match("^_") and not k:match("^on") and not internal[k] then
						seen[k] = true
						table.insert(names, k)
					end
				end
				local mt = getmetatable(t)
				t = mt and mt.__index
			end
			table.sort(names)
			return names
		end
		-- counters in a table: gopher-lua detaches upvalues after a caught error
		local R = { bad = {}, n = 0 }
		local function check(name, class, obj)
			local path = "t:" .. name:lower()
			expose(obj, "switch", { path = path })
			for _, m in ipairs(publicOf(class)) do
				R.n = R.n + 1
				local out = MCP.execute_method(path, m, nil)
				if out:find("method not found", 1, true) then table.insert(R.bad, name .. "." .. m) end
			end
		end
		for i, name in ipairs(remote) do
			local class = _G[name]
			check(name, class, class:new(CLU510000001, "OBJ" .. i, name))
		end
		local opts = { VirtualThermostat = {
			source = { getValue = function() return 20 end },
			output = { switchOn = function() end, switchOff = function() end } } }
		for _, name in ipairs(virtual) do
			local class = _G[name]
			check(name, class, class:new(name:lower() .. "_obj", opts[name] or {}))
		end
		return R.n .. " " .. table.concat(R.bad, ",")`)
	if fields := strings.SplitN(got, " ", 2); fields[0] == "0" || fields[1] != "" {
		t.Errorf("checked %s methods; not callable: %s", fields[0], fields[1])
	}
	sent.take()

	// a pulse switch (a gate) and a method added to an instance
	mustExec(t, engine, `G = RemoteSwitch:new(CLU510000001, "DOU0009", "gate")
		expose(G, "switch", { path = "t:gate" })
		G.ping = function() return "pong" end`)
	if got := mustExec(t, engine, `return MCP.execute_method("t:gate", "pulse", "[500]")`); !strings.Contains(got, `"ok":true`) {
		t.Errorf("pulse: %s", got)
	}
	if got := sent.take(); !strings.Contains(got, "DOU0009:execute(1, 500)") {
		t.Errorf("pulse sent %q", got)
	}
	if got := mustExec(t, engine, `return MCP.execute_method("t:gate", "ping", nil)`); !strings.Contains(got, `"result":"pong"`) {
		t.Errorf("instance method: %s", got)
	}
}
