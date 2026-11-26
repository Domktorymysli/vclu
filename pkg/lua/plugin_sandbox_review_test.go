package lua

import (
	"strings"
	"testing"
)

// Review of #206: what a plugin can reach by walking every field it is given.

// walkLua walks the plugin's globals, the registry entries in roots and what they lead to,
// through fields and pairs only (no method calls); found strings and functions are kept.
const walkLua = `
	local seen, queue, strs, fns = {}, {}, {}, {}
	local function push(v, path)
		if type(v) == "table" and not seen[__srcid(v)] then
			seen[__srcid(v)] = path
			queue[#queue + 1] = { v, path }
		elseif type(v) == "string" then
			strs[#strs + 1] = path .. "=" .. v
		elseif type(v) == "function" then
			fns[#fns + 1] = { v, path }
		end
	end
	for _, name in ipairs(__roots) do push(_registry[name], "_registry." .. name) end
	push(Plugin.getPlugin("@t/b"), "getPlugin(b)")
	push(_:get("@t/b"), "_:get(b)")
	for _, name in ipairs({ "string", "table", "math", "os", "datetime", "Crypto", "bit", "Telnet",
		"Logger", "JSON", "Plugin", "EventBus", "_", "_registry", "Modbus" }) do
		local ok, v = pcall(function() return _ENVX[name] end)
		if ok then push(v, name) end
	end
	local i = 1
	while i <= #queue and i < 20000 do
		local t, path = queue[i][1], queue[i][2]
		i = i + 1
		local ok = pcall(function()
			for k, v in pairs(t) do
				push(k, path .. ".<key>")
				push(v, path .. "." .. tostring(k))
			end
		end)
	end
	__walk = { strs = strs, fns = fns, tables = queue }
	return #queue
`

func TestReviewExecPluginWalkFindsNoSecretOrRawShared(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`
		PluginSandbox:create("@t/b", nil)
		PluginSandbox:setSecrets("@t/b", { token = "TOPSECRET-b-123" })
		PluginSandbox:execute("@t/b", [[
			local p = Plugin:new("b", {name = "B"})
			p:sensor("temp", function() return 21 end)
			p.config = { botToken = "TOPSECRET-cfg-456" }
			local h = p:expose(p:get("temp"), "temperature", { id = "t" })
		]])
		_registry["mod.thing"] = {
			value = 1, getValue = function(self) return self.value end,
			bus = EventBus:getShared(), exposed = ExposedObjects, plugin = Plugin,
		}
	`); err != nil {
		t.Fatal(err)
	}
	if _, e := runPlugin(t, engine, "@t/a", `p = Plugin:new("a", {name = "A"})`); e != "nil" {
		t.Fatal(e)
	}
	if _, err := engine.Execute(`
		local env = PluginSandbox.sandboxes["@t/a"].environment
		local roots = {}
		for k in pairs(_registry) do if type(k) == "string" then roots[#roots + 1] = k end end
		env.__roots = roots
		env._ENVX = env
		env.__srcid = function(t)
			local mt = getmetatable(t)
			return type(mt) == "table" and rawget(mt, "__sandboxSource") or t
		end
	`); err != nil {
		t.Fatal(err)
	}
	r, e := runPlugin(t, engine, "@t/a", walkLua)
	if e != "nil" {
		t.Fatalf("walk: %s", e)
	}
	t.Logf("walked %s tables", r)

	// secrets: the sandbox's and the other plugin's config
	out, err := engine.Execute(`
		local w = PluginSandbox.sandboxes["@t/a"].environment.__walk
		local bad = {}
		for _, s in ipairs(w.strs) do
			if s:find("TOPSECRET", 1, true) then bad[#bad + 1] = s end
		end
		-- shared tables and dangerous functions must not be reachable raw
		local shared = { [_G] = "_G", [string] = "string", [table] = "table", [Plugin] = "Plugin",
			[PluginSandbox] = "PluginSandbox", [PluginSandbox.sandboxes] = "sandboxes",
			[Plugin.instances] = "Plugin.instances", [_registry] = "_registry",
			[EventBus] = "EventBus", [ExposedObjects] = "ExposedObjects", [JSON] = "JSON", [Logger] = "Logger" }
		for _, sb in pairs(PluginSandbox.sandboxes) do shared[sb] = "a sandbox"; shared[sb.api] = "a sandbox api" end
		for _, inst in pairs(Plugin.instances) do shared[inst] = "instance " .. inst.id end
		for _, q in ipairs(w.tables) do
			if shared[q[1]] and q[1] == PluginSandbox.sandboxes["@t/a"].environment.__srcid(q[1]) then bad[#bad + 1] = "raw " .. shared[q[1]] .. " at " .. q[2] end
		end
		local danger = {}
		for name, f in pairs({ rawset = rawset, rawget = rawget, setmetatable = setmetatable,
			getmetatable = getmetatable, loadstring = loadstring, load = load, require = require,
			getfenv = getfenv, setfenv = setfenv, dofile = dofile }) do danger[f] = name end
		if os.execute then danger[os.execute] = "os.execute" end
		if io and io.open then danger[io.open] = "io.open" end
		if debug then for k, f in pairs(debug) do danger[f] = "debug." .. k end end
		for _, f in ipairs(w.fns) do
			if danger[f[1]] then bad[#bad + 1] = danger[f[1]] .. " at " .. f[2] end
		end
		return table.concat(bad, "\n")
	`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("reachable from another plugin:\n%s", out)
	}
}

// What is missing from a plugin's environment stays missing, also under other names.
func TestReviewExecPluginEnvHasNoEscapeHatches(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`
		local env = PluginSandbox.sandboxes["@t/a"].environment
		env._ENV_PROBE = function(n) return env[n] end
	`); err != nil {
		t.Fatal(err)
	}
	r, e := runPlugin(t, engine, "@t/a", `
		local out = {}
		for _, n in ipairs({ "load", "loadstring", "dofile", "loadfile", "require", "rawset", "rawget",
			"rawequal", "getmetatable", "setmetatable", "getfenv", "setfenv", "debug", "coroutine",
			"io", "module", "newproxy", "_G", "collectgarbage", "gcinfo", "channel" }) do
			if _ENV_PROBE(n) ~= nil then out[#out + 1] = n end
		end
		if os.execute or os.exit or os.remove or os.rename or os.getenv or os.setenv then out[#out + 1] = "os.*" end
		if string.dump then
			local ok = pcall(string.dump, function() end)
			if ok then out[#out + 1] = "string.dump" end
		end
		return table.concat(out, ",")
	`)
	if e != "nil" || r != "" {
		t.Errorf("reachable: %q (e=%s)", r, e)
	}
}

// The plugin's copy of a standard library holds no table of the real one.
func TestReviewExecPluginStdlibCopyHoldsNoSharedTable(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	r, e := runPlugin(t, engine, "@t/a", `
		local found = {}
		for lib, t in pairs({ string = string, table = table, math = math, os = os, Crypto = Crypto,
			bit = bit, Telnet = Telnet, datetime = datetime }) do
			for k, v in pairs(t) do
				if type(v) == "table" then
					found[#found + 1] = lib .. "." .. tostring(k)
					pcall(function() v.rep = function() return "pwned" end end)
				end
			end
		end
		return table.concat(found, ",")
	`)
	if e != "nil" || r != "" {
		t.Errorf("tables in the plugin's libraries: %q (e=%s)", r, e)
	}
	mustLua(t, engine, `string.rep("a", 2)`, "aa")
	mustLua(t, engine, `("a"):rep(2)`, "aa")
}

// A string's methods come from the real library: reaching it through a string changes nothing.
func TestReviewExecPluginStringMethodsReadOnly(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	for _, code := range []string{
		`local s = ("").__index; s.rep = function() return "pwned" end`,
		`local s = ("x")["__index"]; s.format = nil`,
	} {
		runPlugin(t, engine, "@t/a", code)
	}
	mustLua(t, engine, `("a"):rep(2)`, "aa")
	mustLua(t, engine, `string.format("%d", 1)`, "1")
	r, _ := runPlugin(t, engine, "@t/a", `return ("a"):rep(2) .. ("%d"):format(3) .. ("abc"):sub(2)`)
	if r != "aa3bc" {
		t.Errorf("string methods in a plugin: %q", r)
	}
}

// A plugin without grenton_raw reading a remote CLU object: open question whether the read
// may go to the CLU (any index, waiting for its answer)
func TestReviewExecRemoteReadFromPlugin(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`
		__calls = {}
		local clu = { execute = function(_, m, s) __calls[#__calls + 1] = s; return 0 end }
		_registry["CLU2.DOU1"] = RemoteObject:new(clu, "DOU1", "DOU1")
	`); err != nil {
		t.Fatal(err)
	}
	_, e := runPlugin(t, engine, "@t/a", `return _:get("CLU2.DOU1"):get(27)`)
	if !strings.Contains(e, "reading a CLU needs the grenton_raw permission") {
		t.Errorf("error = %q", e)
	}
	mustLua(t, engine, `table.concat(__calls, ";")`, "")
	if _, e := runPlugin(t, engine, "@t/a", `local v = _:get("CLU2.DOU1"):get(1); return v`); !strings.Contains(e, "grenton_raw") {
		t.Errorf("not a tail call: %q", e)
	}
	// the value vclu already knows stays readable
	if _, e := runPlugin(t, engine, "@t/a", `return _:get("CLU2.DOU1"):getInfo()`); e != "nil" {
		t.Errorf("getInfo: %s", e)
	}
	// with grenton_raw the request goes
	mustLua(t, engine, `PluginSandbox:create("@t/raw", {api = 2, grenton_raw = true}) and "ok"`, "ok")
	if _, e := runPlugin(t, engine, "@t/raw", `return _:get("CLU2.DOU1"):get(27)`); e != "nil" {
		t.Errorf("grenton_raw: %s", e)
	}
	mustLua(t, engine, `table.concat(__calls, ";")`, "DOU1:get(27)")
}

// API 1 keeps reading a CLU, with a warning; module code is not a plugin's.
func TestCluRequestAPI1AndModules(t *testing.T) {
	engine := sandboxEngine(t, "@t/old", `{api = 1, lan = true}`)
	if _, err := engine.Execute(`
		__calls = {}
		local clu = { execute = function(_, m, s) __calls[#__calls + 1] = s; return 0 end }
		_registry["CLU2.DOU1"] = RemoteObject:new(clu, "DOU1", "DOU1")
	`); err != nil {
		t.Fatal(err)
	}
	if _, e := runPlugin(t, engine, "@t/old", `return _:get("CLU2.DOU1"):get(3)`); e != "nil" {
		t.Errorf("API 1: %s", e)
	}
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@t/old"].warnedCluRequest)`, "true")
	mustLua(t, engine, `_registry["CLU2.DOU1"]:get(4) and table.concat(__calls, ";")`, "DOU1:get(3);DOU1:get(4)")
}

// An API 1 plugin keeps its expose paths, but not over an object a module or the CLU has.
func TestReviewExecAPI1ExposeOverRegistryObject(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", `{api = 1, lan = true}`)
	if _, err := engine.Execute(`
		__lamp = { get = function() return 0 end, set = function() end }
		_registry["CLU220000592.DOU9999"] = __lamp
	`); err != nil {
		t.Fatal(err)
	}
	r, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		local c = p:control("x", function() return 1 end, function(v) end)
		local h = p:expose(c, "switch", { path = "CLU220000592.DOU9999" })
		return h and h:getPath()
	`)
	t.Logf("r=%s e=%s", r, e)
	mustLua(t, engine, `tostring(_registry["CLU220000592.DOU9999"] == __lamp)`, "true")
}

// Another plugin's own instance and handles, reached through what it exposed or emitted:
// read-only like any object it did not create.
func TestReviewExecOtherPluginsInstanceThroughItsObjects(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`
		PluginSandbox:create("@t/b", nil)
		local _, e = PluginSandbox:execute("@t/b", [[
			local p = Plugin:new("b", {name = "B"})
			p.config = { botToken = "TOPSECRET-cfg" }
			local dev = { plugin = p, get = function() return 1 end, set = function() end }
			dev.handle = p:expose(dev, "switch", { id = "dev" })
			p:on("t:ask", function() EventBus.emit("@t/b:here", { plugin = p, handle = dev.handle }) end)
		]])
		assert(not e, e)
	`); err != nil {
		t.Fatal(err)
	}
	path, err := engine.Execute(`(function() for k in pairs(_registry) do if type(k) == "string" and k:find("@t/b:", 1, true) then return k end end end)()`)
	if err != nil || path == "" {
		t.Fatalf("exposed path: %q %v", path, err)
	}
	if _, e := runPlugin(t, engine, "@t/a", `
		got = {}
		EventBus.on("@t/b:here", function(m) got.m = m end)
	`); e != "nil" {
		t.Fatal(e)
	}
	if _, err := engine.Execute(`EventBus:getShared():emit("t:ask")`); err != nil {
		t.Fatal(err)
	}
	for what, code := range map[string]string{
		"config through registry":   `return _registry["` + path + `"].plugin.config.botToken`,
		"config through event":      `return got.m.plugin.config.botToken`,
		"unexpose through registry": `_registry["` + path + `"].handle:unexpose(); return "done"`,
		"unexpose through event":    `got.m.handle:unexpose(); return "done"`,
		"exposed list":              `local hs = got.m.plugin:getExposed(); hs[1]:unexpose(); return "done"`,
	} {
		r, e := runPlugin(t, engine, "@t/a", code)
		if e == "nil" && r != "nil" {
			t.Errorf("%s: reached %s", what, r)
		}
	}
	mustLua(t, engine, `tostring(_registry["`+path+`"] ~= nil)`, "true")
}

// Crypto and bit on bad input from a plugin: an error or nil, err, never a panic.
func TestReviewExecCryptoBadInput(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	k := `string.rep("k", 16)`
	for _, code := range []string{
		`return Crypto.aesCbcDecrypt(` + k + `, "short", string.rep("x", 16))`,
		`return Crypto.aesCbcDecrypt(` + k + `, string.rep("i", 16), string.rep("x", 16))`,
		`return Crypto.aesEcbDecrypt(` + k + `, "")`,
		`return Crypto.aesEcbDecrypt(` + k + `, string.rep("x", 15), "none")`,
		`return Crypto.aesGcmDecrypt(` + k + `, string.rep("n", 12), "x")`,
		`return Crypto.aesGcmEncrypt("short", string.rep("n", 12), "x")`,
		`return Crypto.hmac("sha512", "k", "x")`,
		`return Crypto.random(0/0)`,
		`return Crypto.random(1e300)`,
		`return Crypto.fromBase64("@@@")`,
		`return Crypto.fromHex("zz")`,
		`return bit.tohex(1, -2^31)`,
		`return bit.lshift(1, -1)`,
		`return bit.band(0/0, 1/0)`,
	} {
		r, e := runPlugin(t, engine, "@t/a", `local ok, a, b = pcall(function() `+code+` end) return tostring(ok) .. "|" .. tostring(a) .. "|" .. tostring(b)`)
		if e != "nil" || strings.Contains(r, "runtime error") || strings.Contains(r, "goroutine") {
			t.Errorf("%s: r=%s e=%s", code, r, e)
		}
	}
}
