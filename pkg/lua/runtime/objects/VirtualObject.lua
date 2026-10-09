--- Base class for all virtual objects.
-- Provides self-contained state management, onChange with unsubscribe,
-- event system, and tagging. No CLU/UDP dependency.
--
-- IMPORTANT: Virtual* and Remote* must expose identical public APIs.
-- When adding/changing methods on Remote*, update the Virtual* counterpart.
-- _type values must match exactly (DOUT, DIMMER, ROLLER, DIN, AnalogOUT) —
-- TypeMap, expose, HomeKit, MQTT, MCP all depend on this.
-- @module objects.VirtualObject

VirtualObject = {}
VirtualObject.__index = VirtualObject
VirtualObject._selfFilter = true -- updateState applies the expose filter

-- KV namespace of persisted virtual objects: one file, one debounced write
local PERSIST_NS = "virtual-objects"
local RESERVED_KEYS = { get = true, set = true, save = true, toTable = true }

-- NaN/Inf cannot be saved as JSON: one such value would fail every write
-- of the shared namespace
local function storable(v)
    -- v - v is 0 only for finite numbers (gopher-lua's math.huge is not inf)
    return type(v) ~= "number" or v - v == 0
end

--- Applies the constructor options shared by all virtual objects.
-- @param opts table|nil {initial, persist}: persist = true keeps the value
--   under the object's name, persist = "key" under that key; the saved value
--   replaces initial after a reload or restart
-- @param default any State when opts.initial is nil
-- @param normalize function|nil (self, value) -> value, e.g. clamping
function VirtualObject:_setup(opts, default, normalize)
    opts = opts or {}
    local value = opts.initial
    if value == nil then value = default end
    if opts.persist then
        if opts.persist == true and self._name == "Virtual" then
            error("persist = true needs a name (or give persist a key)", 3)
        end
        local key = opts.persist == true and self._name or opts.persist
        if type(key) ~= "string" or key == "" or RESERVED_KEYS[key] then
            error("persist must be true or a string key (not get/set/save/toTable)", 3)
        end
        self._persistKey = key
        local saved = persist(PERSIST_NS):get(key)
        -- a value of another type (the object changed kind) is ignored
        if saved ~= nil and storable(saved) and (value == nil or type(saved) == type(value)) then value = saved end
    end
    if normalize then value = normalize(self, value) end
    self._state = value
end

--- Creates a new VirtualObject instance.
-- @api public
-- @param name string Optional name for the object
-- @return VirtualObject The new virtual object instance
function VirtualObject:new(name)
    local obj = setmetatable({
        _name = name or "Virtual",
        _type = "UNKNOWN",
        _state = nil,
        _cbs = {},
        _eventHandlers = {},
        _tags = {},
        _allowedMethods = {},
    }, self)
    return obj
end

-- ==========================================================================
-- Core state
-- ==========================================================================

--- Gets a value by index. Index 0 returns the primary state.
-- @param index number The property index to retrieve
-- @return any The value
function VirtualObject:get(index)
    if index == 0 then return self._state end
    return nil
end

--- Sets a value by index. Index 0 updates the primary state.
-- @param index number The property index to set
-- @param value any The value to set
function VirtualObject:set(index, value)
    if index == 0 then self:updateState(value) end
end

--- Updates the primary state and notifies all onChange callbacks.
-- Only fires if the value actually changed.
-- @param newValue any The new state value
function VirtualObject:updateState(newValue)
    local old = self._state
    if old == newValue then return end
    local mark = StateChange and StateChange.mark(self)
    self._state = newValue
    if self._persistKey and storable(newValue) then persist(PERSIST_NS):set(self._persistKey, newValue) end
    if StateChange then
        local report
        report, old = StateChange.filter(self, old, newValue)
        if not report then return end
    end
    for _, cb in ipairs(self._cbs) do cb(newValue, old) end
    self:emit("OnChange", newValue, old)
    -- Publish unless an expose callback already did
    local path = mark and StateChange.pathOf(self, self._registryPath)
    if path and not StateChange.publishedSince(self, mark) then
        StateChange.publish(path, self, old, newValue)
    end
end

--- Gets the current state value.
-- @api public
-- @return any Current state
function VirtualObject:getValue()
    return self._state
end

-- ==========================================================================
-- onChange (returns unsubscribe function, required by ExposedObjects)
-- ==========================================================================

--- Registers a callback for state changes.
-- @api public
-- @param cb function Called with (newValue, oldValue)
-- @return function Unsubscribe function
function VirtualObject:onChange(cb)
    table.insert(self._cbs, cb)
    return function()
        for i, c in ipairs(self._cbs) do
            if c == cb then table.remove(self._cbs, i) break end
        end
    end
end

-- ==========================================================================
-- Event system (same contract as RemoteObject)
-- ==========================================================================

--- Registers an event handler for a named event.
-- @api public
-- @param eventName string The name of the event
-- @param callback function The callback function
-- @return VirtualObject Self for method chaining
function VirtualObject:onEvent(eventName, callback)
    if not self._eventHandlers[eventName] then
        self._eventHandlers[eventName] = {}
    end
    table.insert(self._eventHandlers[eventName], callback)
    return self
end

--- Emits an event, invoking all registered handlers.
-- @api public
-- @param eventName string The name of the event
-- @param ... any Arguments to pass to handlers
function VirtualObject:emit(eventName, ...)
    local handlers = self._eventHandlers[eventName]
    if handlers then
        for _, callback in ipairs(handlers) do
            callback(...)
        end
    end
end

-- ==========================================================================
-- Tag system (same contract as RemoteObject)
-- ==========================================================================

--- Adds one or more tags to this object.
-- @api public
-- @param ... string One or more tag names
-- @return VirtualObject Self for method chaining
function VirtualObject:addTag(...)
    local tags = {...}
    for _, tag in ipairs(tags) do
        self._tags[tag] = true
        if _tagIndex then
            if not _tagIndex[tag] then
                _tagIndex[tag] = {}
            end
            table.insert(_tagIndex[tag], self)
        end
    end
    return self
end

--- Removes a tag from this object.
-- @api public
-- @param tag string The tag name to remove
-- @return VirtualObject Self for method chaining
function VirtualObject:removeTag(tag)
    self._tags[tag] = nil
    if _tagIndex and _tagIndex[tag] then
        for i, obj in ipairs(_tagIndex[tag]) do
            if obj == self then
                table.remove(_tagIndex[tag], i)
                break
            end
        end
    end
    return self
end

--- Checks if this object has a specific tag.
-- @api public
-- @param tag string The tag name to check
-- @return boolean True if the object has the tag
function VirtualObject:hasTag(tag)
    return self._tags[tag] == true
end

--- Gets all tags assigned to this object.
-- @api public
-- @return table Array of tag names
function VirtualObject:getTags()
    local tags = {}
    for tag, _ in pairs(self._tags) do
        table.insert(tags, tag)
    end
    return tags
end

-- ==========================================================================
-- Introspection (same contract as RemoteObject)
-- ==========================================================================

--- Checks if a method can be executed on this object.
-- @param method string The method name to check
-- @return boolean True if the method exists and is callable
function VirtualObject:canExecute(method)
    return type(self[method]) == "function"
end

--- Dynamically calls a method on this object.
-- @param method string The method name to call
-- @param ... any Arguments to pass to the method
-- @return any The method's return value, or nil and error message
function VirtualObject:call(method, ...)
    if self:canExecute(method) then
        return self[method](self, ...)
    end
    return nil, "unknown method: " .. tostring(method)
end

--- Gets list of allowed methods.
-- @return table Array of method names
function VirtualObject:getMethods()
    if self._allowedMethods then
        local result = {}
        for _, method in ipairs(self._allowedMethods) do
            table.insert(result, method)
        end
        return result
    end
    return {}
end

--- Gets comprehensive information about this object.
-- @return table Object information including name, type, tags, methods
function VirtualObject:getInfo()
    return {
        name = self._name,
        type = self._type,
        tags = self:getTags(),
        methods = self:getMethods(),
        state = self._state,
    }
end

--- Returns a formatted string for debugging.
-- @return string Multi-line formatted string representation
function VirtualObject:toString()
    local lines = {}
    table.insert(lines, "=== " .. (self._name or "Virtual") .. " ===")
    table.insert(lines, "  name:  " .. tostring(self._name))
    table.insert(lines, "  type:  " .. tostring(self._type))
    table.insert(lines, "  state: " .. tostring(self._state))

    local tags = self:getTags()
    if #tags > 0 then
        table.insert(lines, "  tags:  " .. table.concat(tags, ", "))
    end

    if _registry then
        for path, obj in pairs(_registry) do
            if obj == self then
                table.insert(lines, "  path:  " .. path)
                break
            end
        end
    end

    return table.concat(lines, "\n")
end

-- ==========================================================================
-- Expose API (delegates to global expose / ExposedObjects)
-- ==========================================================================

local TypeMap = require("TypeMap")

--- Gets the expose type for this object based on its _type
-- @return string The expose type
function VirtualObject:_getExposeType()
    return TypeMap.getExposeType(self._type, "switch")
end

--- Gets the registry path for this object
-- @return string|nil The registry path or nil
function VirtualObject:_getRegistryPath()
    if _registry then
        for path, obj in pairs(_registry) do
            if obj == self then
                return path
            end
        end
    end
    return nil
end

--- Exposes this object to MQTT and HomeKit.
-- Returns an ExposedHandle for fluent configuration.
-- @api public
-- @param options table Optional settings
-- @return ExposedHandle Handle for configuring the exposure
function VirtualObject:expose(options)
    if self._exposeHandle and not self._exposeHandle:isExposed() then
        self._exposeHandle = nil
    end

    if self._exposeHandle then
        if options then
            self._exposeHandle:update(options)
        end
        return self._exposeHandle
    end

    if not ExposedObjects then
        error("ExposedObjects not available - expose API requires ExposedObjects module")
    end

    options = options or {}

    local path = self:_getRegistryPath()
    if path then
        options.path = options.path or path
    end

    options.name = options.name or self._name

    local exposeType = self:_getExposeType()
    self._exposeHandle = ExposedObjects:expose(self, exposeType, options)
    return self._exposeHandle
end

--- Unexposes this object.
-- @api public
-- @return boolean True if was exposed and now unexposed
function VirtualObject:unexpose()
    if self._exposeHandle then
        self._exposeHandle:unexpose()
        self._exposeHandle = nil
        return true
    end
    return false
end

--- Checks if this object is currently exposed.
-- @api public
-- @return boolean True if exposed
function VirtualObject:isExposed()
    if self._exposeHandle and not self._exposeHandle:isExposed() then
        self._exposeHandle = nil
    end
    return self._exposeHandle ~= nil
end

--- Gets the expose handle if exposed.
-- @return ExposedHandle|nil
function VirtualObject:getExposeHandle()
    if self._exposeHandle and not self._exposeHandle:isExposed() then
        self._exposeHandle = nil
    end
    return self._exposeHandle
end

return VirtualObject
