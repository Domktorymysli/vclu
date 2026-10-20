-- ============================================
-- Helper functions for vCLU Lua scripting
-- ============================================

-- ============================================
-- TIMERS wrapper (uses Go timer functions)
-- ============================================
TIMERS = {}

--- Set a one-shot timeout
-- @param ms number Delay in milliseconds
-- @param callback function Function to execute
-- @return number Timer ID
function TIMERS:setTimeout(ms, callback)
    if type(__go_setTimeout) == "function" then
        return __go_setTimeout(callback, ms)
    else
        error("Timer support not available - Go runtime required")
    end
end

--- Set a repeating interval
-- @param ms number Interval in milliseconds
-- @param callback function Function to execute
-- @return number Timer ID
function TIMERS:setInterval(ms, callback)
    if type(__go_setInterval) == "function" then
        return __go_setInterval(callback, ms)
    else
        error("Timer support not available - Go runtime required")
    end
end

--- Clear a timer
-- @param id number Timer ID
function TIMERS:clearTimer(id)
    if type(__go_clearTimer) == "function" then
        __go_clearTimer(id)
    end
end

-- ============================================
-- Math helpers
-- ============================================

--- Clamp value to min-max range
-- @api public
-- @param value number Value to clamp
-- @param min number Minimum value
-- @param max number Maximum value
-- @return number Clamped value
function clamp(value, min, max)
    if value < min then return min end
    if value > max then return max end
    return value
end

--- Linear interpolation between two values
-- @api public
-- @param a number Start value
-- @param b number End value
-- @param t number Progress (0-1)
-- @return number Interpolated value
function lerp(a, b, t)
    return a + (b - a) * clamp(t, 0, 1)
end

--- Map value from one range to another
-- @api public
-- @param value number Input value
-- @param inMin number Input range minimum
-- @param inMax number Input range maximum
-- @param outMin number Output range minimum
-- @param outMax number Output range maximum
-- @return number Mapped value
function map(value, inMin, inMax, outMin, outMax)
    if inMax == inMin then return outMin end
    local t = (value - inMin) / (inMax - inMin)
    return lerp(outMin, outMax, t)
end

--- Round number to given precision
-- @api public
-- @param value number Value to round
-- @param decimals number Number of decimal places (default 0)
-- @return number Rounded value
function round(value, decimals)
    local mult = 10 ^ (decimals or 0)
    return math.floor(value * mult + 0.5) / mult
end

-- ============================================
-- Timer helpers (requires Go timer support)
-- ============================================

--- Execute function after delay (with slow callback detection)
-- @api public
-- @param ms number Delay in milliseconds
-- @param callback function Function to execute
-- @return number Timer ID
function after(ms, callback)
    local info = debug and debug.getinfo and debug.getinfo(callback, "Sl")
    local src = info and ((info.short_src or "?") .. ":" .. (info.linedefined or "?")) or "unknown"

    return TIMERS:setTimeout(ms, function()
        local t0 = os.clock()
        callback()
        local elapsed = os.clock() - t0
        if elapsed > 2.0 then
            Logger:getShared():error("Slow after(%d) callback took %.1fs at %s", ms, elapsed, src)
        elseif elapsed > 0.5 then
            Logger:getShared():warn("Slow after(%d) callback took %.1fs at %s", ms, elapsed, src)
        end
    end)
end

--- Execute function repeatedly (with overlap warning)
-- @api public
-- @param ms number Interval in milliseconds
-- @param callback function Function to execute
-- @return number Timer ID
function every(ms, callback)
    local info = debug and debug.getinfo and debug.getinfo(callback, "Sl")
    local src = info and ((info.short_src or "?") .. ":" .. (info.linedefined or "?")) or "unknown"

    return TIMERS:setInterval(ms, function()
        local t0 = os.clock()
        callback()
        local elapsedMs = (os.clock() - t0) * 1000
        if elapsedMs > ms then
            Logger:getShared():warn("every(%d): callback took %.0fms (exceeds interval) at %s", ms, elapsedMs, src)
        elseif elapsedMs > 2000 then
            Logger:getShared():error("Slow every(%d) callback took %.0fms at %s", ms, elapsedMs, src)
        elseif elapsedMs > 500 then
            Logger:getShared():warn("Slow every(%d) callback took %.0fms at %s", ms, elapsedMs, src)
        end
    end)
end

--- Cancel timer
-- @api public
-- @param id number Timer ID
function cancel(id)
    TIMERS:clearTimer(id)
end

-- ============================================
-- EventBus helpers
-- ============================================

--- Listen for an event on the shared EventBus
-- @api public
-- @param eventName string Event name (e.g. "weather:changed")
-- @param callback function Handler function
-- @return function Unsubscribe function
-- @usage on("weather:changed", function(data) print(data.temp) end)
function on(eventName, callback)
    local bus = EventBus:getShared()
    bus:on(eventName, callback)
    return function()
        bus:off(eventName, callback)
    end
end

--- Emit an event on the shared EventBus
-- @api public
-- @param eventName string Event name
-- @param ... any Arguments to pass to listeners
-- @usage emit("weather:changed", { temp = 22.5 })
function emit(eventName, ...)
    EventBus:getShared():emit(eventName, ...)
end

-- ============================================
-- Global JS/Arduino-style aliases
-- ============================================

--- One-shot timer (JS-style: setTimeout(fn, ms))
-- @api public
-- @param callback function Function to execute
-- @param ms number Delay in milliseconds
-- @return number Timer ID
function setTimeout(callback, ms)
    return after(ms, callback)
end

--- Repeating timer (JS-style: setInterval(fn, ms))
-- @api public
-- @param callback function Function to execute
-- @param ms number Interval in milliseconds
-- @return number Timer ID
function setInterval(callback, ms)
    return every(ms, callback)
end

--- Clear any timer by ID
-- @api public
-- @param id number Timer ID from setTimeout or setInterval
function clearTimeout(id)
    TIMERS:clearTimer(id)
end

--- Alias for clearTimeout
clearInterval = clearTimeout

-- ============================================
-- Scenes
-- ============================================
_scenes = {}

--- Define a scene
-- @api public
-- @param name string Scene name
-- @param fn function Scene function
function scene(name, fn)
    local s = {
        name = name,
        fn = fn,
        _enabled = true
    }

    function s:run(...)
        if self._enabled then
            self.fn(...)
        end
        return self
    end

    function s:enable()
        self._enabled = true
        return self
    end

    function s:disable()
        self._enabled = false
        return self
    end

    function s:isEnabled()
        return self._enabled
    end

    _scenes[name] = s
    return s
end

--- Get scene by name
-- @api public
-- @param name string Scene name
-- @return table Scene object
function getScene(name)
    return _scenes[name]
end

--- Run scene by name
-- @api public
-- @param name string Scene name
function runScene(name, ...)
    local s = _scenes[name]
    if s then
        s:run(...)
    end
end

--- Get all scene names
-- @api public
-- @return table Array of scene names
function getAllScenes()
    local names = {}
    for name, _ in pairs(_scenes) do
        table.insert(names, name)
    end
    table.sort(names)
    return names
end

-- Scenes namespace for dot access: scenes.kino:run()
scenes = setmetatable({}, {
    __index = function(_, name)
        return _scenes[name]
    end
})

-- ============================================
-- Sequence builder
-- ============================================

--- Create a sequence of actions with delays.
-- Returns a builder with methods: seq:add(fn), seq:wait(ms), seq:run(onComplete), seq:stop()
-- @api public
-- @return table Sequence builder
function sequence()
    local seq = {
        _steps = {},
        _running = false,
        _currentStep = 0
    }

    --- Add action to sequence
    function seq:add(fn)
        table.insert(self._steps, {type = "action", fn = fn})
        return self
    end

    --- Add delay to sequence
    function seq:wait(ms)
        table.insert(self._steps, {type = "wait", ms = ms})
        return self
    end

    --- Run the sequence
    function seq:run(onComplete)
        if self._running then return self end
        self._running = true
        self._currentStep = 0

        local function runNext()
            self._currentStep = self._currentStep + 1
            local step = self._steps[self._currentStep]

            if not step then
                self._running = false
                if onComplete then onComplete() end
                return
            end

            if step.type == "action" then
                step.fn()
                runNext()
            elseif step.type == "wait" then
                after(step.ms, runNext)
            end
        end

        runNext()
        return self
    end

    --- Stop the sequence
    function seq:stop()
        self._running = false
        return self
    end

    return seq
end

-- ============================================
-- Cascade helper for groups
-- ============================================

--- Execute function on each object with delay between
-- @api public
-- @param group table RemoteGroup
-- @param delayMs number Delay between objects in ms
-- @param fn function Function to call on each object
function cascade(group, delayMs, fn)
    local objects = group._objects
    for i, obj in ipairs(objects) do
        after((i - 1) * delayMs, function()
            fn(obj, i)
        end)
    end
end

-- Add cascade to RemoteGroup
if RemoteGroup then
    function RemoteGroup:cascade(delayMs, fn)
        cascade(self, delayMs, fn)
        return self
    end
end

-- ============================================
-- State save/restore
-- ============================================

-- Add to RemoteObject (only if exists)
if RemoteObject then
    --- Save object state
    function RemoteObject:saveState()
        return {
            value = self.getValue and self:getValue() or nil,
            tags = self:getTags(),
            _objId = self._objId,
            _type = self._type
        }
    end

    --- Restore object state
    function RemoteObject:restoreState(state)
        if state and state.value ~= nil and self.setValue then
            self:setValue(state.value)
        end
        return self
    end

    --- Chainable delay for objects
    function RemoteObject:after(ms, fn)
        local self_ref = self
        after(ms, function()
            fn(self_ref)
        end)
        return self
    end
end

-- Add to RemoteGroup (only if exists)
if RemoteGroup then
    -- states in the group's order: the same id can be on two CLUs
    function RemoteGroup:saveState()
        local states = {}
        for i, obj in ipairs(self._objects) do
            if type(obj.saveState) == "function" then states[i] = obj:saveState() end
        end
        return states
    end

    function RemoteGroup:restoreState(states)
        for i, obj in ipairs(self._objects) do
            local state = states and states[i]
            if state and state._objId == obj._objId and type(obj.restoreState) == "function" then
                obj:restoreState(state)
            end
        end
        return self
    end
end

-- ============================================
-- Inspect - universal debug helper
-- ============================================

--- Inspect any value and return human-readable string
-- Recognizes: Plugin, RemoteObject, RemoteGroup, Registry objects, tables, primitives
-- @api public
-- @param value any Value to inspect
-- @param depth number Max recursion depth (default 2)
-- @return string Human-readable representation
function inspect(value, depth)
    local isTopLevel = (depth == nil)
    depth = depth or 2
    local result = _inspect(value, depth)
    if isTopLevel then
        print(result)
    end
    return result
end

function _inspect(value, depth)
    -- Nil
    if value == nil then
        return "nil"
    end

    -- Primitives
    local t = type(value)
    if t == "string" then
        return string.format("%q", value)
    elseif t == "number" or t == "boolean" then
        return tostring(value)
    elseif t == "function" then
        return "<function>"
    elseif t == "thread" then
        return "<coroutine>"
    elseif t == "userdata" then
        return "<userdata>"
    end

    -- Tables
    if t ~= "table" then
        return tostring(value)
    end

    -- Check for known types

    -- Plugin instance
    if value._isPlugin and value.id then
        local status = value._initialized and "running" or "not initialized"
        local config = value.config or {}
        local configKeys = {}
        for k in pairs(config) do
            table.insert(configKeys, k)
        end
        table.sort(configKeys)
        -- Collect sensors/controls
        local sensors = {}
        if value._sensors then
            for id in pairs(value._sensors) do table.insert(sensors, id) end
            table.sort(sensors)
        end
        local controls = {}
        if value._controls then
            for id in pairs(value._controls) do table.insert(controls, id) end
            table.sort(controls)
        end
        -- Collect data objects from sandbox
        local objects = {}
        if value._sandbox and value._sandbox.dataObjects then
            for path, obj in pairs(value._sandbox.dataObjects) do
                table.insert(objects, "    " .. path .. ": " .. _inspect(obj, 1))
            end
            table.sort(objects)
        end
        local lines = {
            string.format("Plugin<%s> v%s [%s]", value.id, value.version or "?", status),
            "  name: " .. (value.name or "?"),
            "  config: {" .. table.concat(configKeys, ", ") .. "}",
        }
        if #sensors > 0 then
            table.insert(lines, "  sensors: [" .. table.concat(sensors, ", ") .. "]")
        end
        if #controls > 0 then
            table.insert(lines, "  controls: [" .. table.concat(controls, ", ") .. "]")
        end
        if #objects > 0 then
            table.insert(lines, "  objects:")
            for _, o in ipairs(objects) do table.insert(lines, o) end
        end
        return table.concat(lines, "\n")
    end

    -- RemoteObject
    if value._objId and value._cluId and value._type then
        local val = value.getValue and value:getValue() or "n/a"
        local state = value._lastState
        -- Collect methods
        local methods = {}
        if value.getMethods then
            methods = value:getMethods()
        else
            for k, v in pairs(getmetatable(value) or {}) do
                if type(v) == "function" and type(k) == "string" and not k:match("^_") then
                    table.insert(methods, k)
                end
            end
            table.sort(methods)
        end
        return string.format(
            "RemoteObject<%s>\n  type: %s\n  name: %s\n  value: %s\n  state: %s\n  path: %s.%s\n  methods: [%s]",
            value._objId,
            value._type,
            value._name or "?",
            tostring(val),
            tostring(state),
            value._cluId,
            value._objId,
            table.concat(methods, ", ")
        )
    end

    -- RemoteGroup
    if value._objects and value._name and type(value.on) == "function" then
        local count = #value._objects
        local objIds = {}
        for i, obj in ipairs(value._objects) do
            if i <= 5 then
                table.insert(objIds, obj._objId or "?")
            end
        end
        local suffix = count > 5 and string.format(" ... +%d more", count - 5) or ""
        return string.format(
            "RemoteGroup<%s> (%d objects)\n  objects: [%s%s]",
            value._name,
            count,
            table.concat(objIds, ", "),
            suffix
        )
    end

    -- Registry object (has _type but not _cluId)
    if value._type and not value._cluId then
        local state = value._lastState
        local val = value.getValue and value:getValue() or nil
        -- Collect methods
        local methods = {}
        if value.getMethods then
            methods = value:getMethods()
        else
            local methodSet = {}
            local mt = getmetatable(value)
            if mt and mt.__index then
                for k, v in pairs(mt.__index) do
                    if type(v) == "function" and type(k) == "string" and not k:match("^_") then
                        methodSet[k] = true
                    end
                end
            end
            for k, v in pairs(value) do
                if type(v) == "function" and type(k) == "string" and not k:match("^_") then
                    methodSet[k] = true
                end
            end
            -- Convert to sorted array
            for k in pairs(methodSet) do
                table.insert(methods, k)
            end
            table.sort(methods)
        end
        return string.format(
            "RegistryObject<%s>\n  type: %s\n  value: %s\n  state: %s\n  methods: [%s]",
            value._name or "?",
            value._type,
            tostring(val),
            tostring(state),
            table.concat(methods, ", ")
        )
    end

    -- Generic table
    if depth <= 0 then
        return "{...}"
    end

    local parts = {}
    local isArray = true
    local count = 0

    -- Check if array
    for k in pairs(value) do
        count = count + 1
        if type(k) ~= "number" or k < 1 or k > count or math.floor(k) ~= k then
            isArray = false
        end
    end

    if isArray and count > 0 then
        -- Array
        for i = 1, math.min(count, 10) do
            table.insert(parts, _inspect(value[i], depth - 1))
        end
        if count > 10 then
            table.insert(parts, string.format("... +%d more", count - 10))
        end
        return "[" .. table.concat(parts, ", ") .. "]"
    else
        -- Object/map
        local keys = {}
        for k in pairs(value) do
            if type(k) == "string" and not k:match("^_") then
                table.insert(keys, k)
            end
        end
        table.sort(keys)

        for i, k in ipairs(keys) do
            if i <= 10 then
                local v = value[k]
                local vStr
                if type(v) == "function" then
                    vStr = "<fn>"
                elseif type(v) == "table" then
                    vStr = depth > 1 and _inspect(v, depth - 1) or "{...}"
                else
                    vStr = _inspect(v, depth - 1)
                end
                table.insert(parts, string.format("%s: %s", k, vStr))
            end
        end
        if #keys > 10 then
            table.insert(parts, string.format("... +%d more keys", #keys - 10))
        end

        if #parts == 0 then
            return "{}"
        end
        return "{\n  " .. table.concat(parts, ",\n  ") .. "\n}"
    end
end

--- Print inspect result (convenience wrapper)
-- @api public
-- @param value any Value to inspect
-- @param label string Optional label
function dump(value, label)
    if label then
        print(label .. ": " .. inspect(value))
    else
        print(inspect(value))
    end
end

-- ============================================
-- Logging
-- ============================================
log = {
    level = "info",
    _levels = {debug = 1, info = 2, warn = 3, error = 4}
}

function log._shouldLog(level)
    return (log._levels[level] or 0) >= (log._levels[log.level] or 0)
end

function log.debug(fmt, ...)
    if log._shouldLog("debug") then
        print("[DEBUG] " .. string.format(fmt, ...))
    end
end

function log.info(fmt, ...)
    if log._shouldLog("info") then
        print("[INFO] " .. string.format(fmt, ...))
    end
end

function log.warn(fmt, ...)
    if log._shouldLog("warn") then
        print("[WARN] " .. string.format(fmt, ...))
    end
end

function log.error(fmt, ...)
    if log._shouldLog("error") then
        print("[ERROR] " .. string.format(fmt, ...))
    end
end

-- Return exported helpers
return {
    TIMERS = TIMERS,
    clamp = clamp,
    lerp = lerp,
    map = map,
    round = round,
    after = after,
    every = every,
    cancel = cancel,
    scene = scene,
    getScene = getScene,
    runScene = runScene,
    getAllScenes = getAllScenes,
    scenes = scenes,
    sequence = sequence,
    cascade = cascade,
    inspect = inspect,
    dump = dump,
    log = log,
    setTimeout = setTimeout,
    setInterval = setInterval,
    clearTimeout = clearTimeout,
    clearInterval = clearInterval
}
