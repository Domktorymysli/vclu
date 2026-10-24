-- Defined in every loaded state: step operations and a warning when a draft
-- looks up an object the sandbox does not have.
local function resolve(path)
	if rawget(_registry, path) == nil and _nameIndex[path] then return _nameIndex[path] end
	return path
end
-- a set step, and what the automation then does to an object the scenario
-- has not seen yet, is a change: watchers fire, while the history still
-- skips a first reading
local stepPath
local record = __go_history_record
if record then
	__go_history_record = function(e)
		local id, atMs, change, known, first = record(e)
		if e.path == stepPath or first then change = true end
		return id, atMs, change, known
	end
end
function __sandbox_set(path, value)
	path = resolve(path)
	local o = rawget(_registry, path)
	if o == nil then error("no object '" .. path .. "'", 0) end
	stepPath = StateChange.pathOf(o, path)
	local ok, err = pcall(_.sync, _, path, value)
	stepPath = nil
	if not ok then error(err, 0) end
end
function __sandbox_mqtt(topic, payload)
	local seen = {}
	for _, c in pairs(MQTT._instances or {}) do
		if not seen[c] then seen[c] = true; c:_handleMessage(topic, payload, 0, false) end
	end
	if not seen[MQTT] then MQTT:_handleMessage(topic, payload, 0, false) end
end
function __sandbox_history_path(path)
	path = resolve(path)
	return StateChange.pathOf(rawget(_registry, path), path)
end
function __sandbox_state(path)
	local o = rawget(_registry, resolve(path))
	if o == nil then return nil, false end
	return (StateChange.read(o)), true
end
local methods = getmetatable(_registry).__index
local get, warned = methods.get, {}
methods.get = function(self, p)
	local o = get(self, p)
	if o == nil and type(p) == "string" and not warned[p] then
		warned[p] = true
		__sandbox_log("[WARN] sandbox: no object '" .. p .. "' (CLU objects come from imports/proxy_*.lua, " ..
			"integration objects from the live snapshot or fixtures; supported kinds: satel_zone)")
	end
	return o
end
local value = methods.value
methods.value = function(self, p)
	if type(p) == "string" then methods.get(self, p) end -- the same warning for a wrong path
	return value(self, p)
end
