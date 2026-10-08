--- Base class for all remote objects.
-- Provides core functionality for remote object communication, event handling,
-- state management, and tagging system.
-- @module objects.RemoteObject

RemoteObject = {}
RemoteObject.__index = RemoteObject
RemoteObject._selfFilter = true -- updateState applies the expose filter

--- Creates a new RemoteObject instance.
-- @param clu CLU_REMOTE The CLU instance this object belongs to
-- @param objId string The unique identifier of the object
-- @param name string Optional name for the object (defaults to objId)
-- @return RemoteObject The new remote object instance
function RemoteObject:new(clu, objId, name)
    local obj = {
        _clu = clu,
        _objId = objId,
        _name = name or objId,
        _type = "UNKNOWN",
        _eventHandlers = {},
        _lastState = nil,
        _tags = {}
    }
    setmetatable(obj, self)
    return obj
end

--- Adds one or more tags to this object.
-- @param ... string One or more tag names to add
-- @return RemoteObject Self for method chaining
function RemoteObject:addTag(...)
    local tags = {...}
    for _, tag in ipairs(tags) do
        self._tags[tag] = true
        if not _tagIndex[tag] then
            _tagIndex[tag] = {}
        end
        table.insert(_tagIndex[tag], self)
    end
    return self
end

--- Removes a tag from this object.
-- @param tag string The tag name to remove
-- @return RemoteObject Self for method chaining
function RemoteObject:removeTag(tag)
    self._tags[tag] = nil
    if _tagIndex[tag] then
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
-- @param tag string The tag name to check
-- @return boolean True if the object has the tag
function RemoteObject:hasTag(tag)
    return self._tags[tag] == true
end

--- Gets all tags assigned to this object.
-- @return table Array of tag names
function RemoteObject:getTags()
    local tags = {}
    for tag, _ in pairs(self._tags) do
        table.insert(tags, tag)
    end
    return tags
end

--- Checks if a method can be executed on this object.
-- @param method string The method name to check
-- @return boolean True if the method exists and is callable
function RemoteObject:canExecute(method)
    return type(self[method]) == "function"
end

--- Dynamically calls a method on this object.
-- @param method string The method name to call
-- @param ... any Arguments to pass to the method
-- @return any The method's return value, or nil and error message
function RemoteObject:call(method, ...)
    if self:canExecute(method) then
        return self[method](self, ...)
    end
    return nil, "unknown method: " .. tostring(method)
end

--- Gets list of all callable methods on this object.
-- @return table Array of method names
function RemoteObject:getMethods()
    if self._allowedMethods then
        local result = {}
        for _, method in ipairs(self._allowedMethods) do
            table.insert(result, method)
        end
        return result
    end

    local methods = {}
    local seen = {}

    local function collectFrom(t)
        if not t or seen[t] then return end
        seen[t] = true
        for k, v in pairs(t) do
            if type(v) == "function" and type(k) == "string" and not k:match("^_") then
                methods[k] = true
            end
        end
        local mt = getmetatable(t)
        if mt and mt.__index and type(mt.__index) == "table" then
            collectFrom(mt.__index)
        end
    end

    collectFrom(getmetatable(self))

    local result = {}
    for method, _ in pairs(methods) do
        table.insert(result, method)
    end
    table.sort(result)
    return result
end

--- Gets comprehensive information about this object.
-- @return table Object information including id, name, type, tags, methods
function RemoteObject:getInfo()
    return {
        id = self._objId,
        name = self._name,
        type = self._type,
        tags = self:getTags(),
        methods = self:getMethods()
    }
end

--- Returns a formatted string with all object data for debugging.
-- @return string Multi-line formatted string representation
function RemoteObject:toString()
    local lines = {}
    table.insert(lines, "=== " .. (self._name or self._objId) .. " ===")
    table.insert(lines, "  id:    " .. tostring(self._objId))
    table.insert(lines, "  name:  " .. tostring(self._name))
    table.insert(lines, "  type:  " .. tostring(self._type))
    table.insert(lines, "  state: " .. tostring(self._lastState))

    if self._clu then
        table.insert(lines, "  clu:")
        local serial = self._clu._serial or self._clu.id or "?"
        local address = self._clu._address or self._clu.address or "?"
        if type(address) == "number" then
            local b1 = math.floor(address / 16777216) % 256
            local b2 = math.floor(address / 65536) % 256
            local b3 = math.floor(address / 256) % 256
            local b4 = address % 256
            address = string.format("%d.%d.%d.%d", b1, b2, b3, b4)
        end
        table.insert(lines, "    serial:  " .. tostring(serial))
        table.insert(lines, "    address: " .. tostring(address))
    end

    local tags = self:getTags()
    if #tags > 0 then
        table.insert(lines, "  tags:  " .. table.concat(tags, ", "))
    end

    if self._mqttAccess then
        table.insert(lines, "  mqtt:  " .. tostring(self._mqttAccess))
    end

    for path, obj in pairs(_registry or {}) do
        if obj == self then
            table.insert(lines, "  path:  " .. path)
            break
        end
    end

    return table.concat(lines, "\n")
end

--- Gets a value from the remote object by index.
-- @param index number The property index to retrieve
-- @return any The value from the remote object
function RemoteObject:get(index)
    return self._clu:execute(0, self._objId .. ":get(" .. index .. ")")
end

--- Sets a value on the remote object by index (fire-and-forget).
-- Optimistic: updates local state immediately, subscription will confirm.
-- @param index number The property index to set
-- @param value any The value to set
function RemoteObject:set(index, value)
    -- a new command overrides what an earlier timed one would do
    if StateChange then StateChange.forgetExpected(self) end
    local valStr = type(value) == "string" and ('"' .. value .. '"') or tostring(value)
    self._clu:executeNoReply(0, self._objId .. ":set(" .. index .. ", " .. valStr .. ")")
    if index == 0 and self.updateState then
        self:updateState(value)
    end
end

--- Executes a method on the remote object by index (fire-and-forget).
-- @param index number The method index to execute
-- @param ... any Arguments to pass to the remote method
function RemoteObject:execute(index, ...)
    if StateChange then StateChange.forgetExpected(self) end
    local args = {...}
    local argsStr = ""
    for i, v in ipairs(args) do
        if i > 1 then argsStr = argsStr .. ", " end
        argsStr = argsStr .. tostring(v)
    end
    if argsStr ~= "" then
        self._clu:executeNoReply(0, self._objId .. ":execute(" .. index .. ", " .. argsStr .. ")")
    else
        self._clu:executeNoReply(0, self._objId .. ":execute(" .. index .. ")")
    end
end

--- Registers an event handler for a named event.
-- Named onEvent() to avoid collision with device control methods like
-- RemoteSwitch:on() (turn on) and RemoteLight:on() (turn on).
-- @param eventName string The name of the event to handle
-- @param callback function The callback function
-- @return RemoteObject Self for method chaining
function RemoteObject:onEvent(eventName, callback)
    if not self._eventHandlers[eventName] then
        self._eventHandlers[eventName] = {}
    end
    table.insert(self._eventHandlers[eventName], callback)
    return self
end

--- Emits an event, invoking all registered handlers.
-- @param eventName string The name of the event to emit
-- @param ... any Arguments to pass to event handlers
function RemoteObject:emit(eventName, ...)
    local handlers = self._eventHandlers[eventName]
    if handlers then
        for _, callback in ipairs(handlers) do
            callback(...)
        end
    end
end

--- Registers a callback for when the object's state changes.
-- @param callback function Function called with (newState, oldState)
-- @return function Unsubscribe function (required by ExposedObjects cleanup)
function RemoteObject:onChange(callback)
    self:onEvent("OnChange", callback)
    return function()
        local handlers = self._eventHandlers["OnChange"]
        if handlers then
            for i, cb in ipairs(handlers) do
                if cb == callback then
                    table.remove(handlers, i)
                    return
                end
            end
        end
    end
end

--- Updates the object's state and triggers OnChange event if state changed.
-- @param newState any The new state value
function RemoteObject:updateState(newState)
    if type(newState) == "string" then
        local num = newState:match("^{?([%d%.%-]+)}?$")
        if num then
            local parsed = tonumber(num)
            if parsed then
                Logger:getShared():debug("[updateState] Fixed malformed value: '%s' -> %s", newState, parsed)
                newState = parsed
            end
        end
    end

    local oldState = self._lastState
    local mark = StateChange and StateChange.mark(self)
    self._lastState = newState
    if StateChange and newState ~= oldState then
        local report
        report, oldState = StateChange.filter(self, oldState, newState)
        if not report then return end
    end
    if oldState ~= nil and newState ~= oldState then
        self._lastEvent = os.time()
        self:emit("OnChange", newState, oldState)
    end
    -- Also an optimistic update from switchOn() etc. is a change; publish it
    -- unless an expose callback already did
    local path = mark and StateChange.pathOf(self, self._registryPath)
    if path and not StateChange.publishedSince(self, mark) then
        StateChange.publish(path, self, oldState, newState)
    end
end

-- ==========================================================================
-- Expose API
-- ==========================================================================

local TypeMap = require("TypeMap") -- central type mappings

--- Gets the expose type for this object based on its _type
-- @return string The expose type (switch, light, dimmer, etc.)
function RemoteObject:_getExposeType()
    return TypeMap.getExposeType(self._type, "switch")
end

--- Gets the registry path for this object
-- @return string|nil The registry path or nil if not in registry
function RemoteObject:_getRegistryPath()
    if _registry then
        for path, obj in pairs(_registry) do
            if obj == self then
                return path
            end
        end
    end
    return nil
end

--- Exposes this object to MQTT and HomeKit
-- Returns an ExposedHandle for fluent configuration.
-- Subsequent calls return the same handle (idempotent).
-- @param options table Optional settings (name, readonly, mqtt, homekit, etc.)
-- @return ExposedHandle Handle for configuring the exposure
-- @usage
--   CLU123.DOU_LAMPA:expose()
--       :rename("Lampa Salon")
--       :readonly()
--       :mqttOnly()
function RemoteObject:expose(options)
    -- Clean up dead handle reference (e.g., if handle:unexpose() was called directly)
    if self._exposeHandle and not self._exposeHandle:isExposed() then
        self._exposeHandle = nil
    end

    -- Return cached handle if already exposed (idempotent)
    if self._exposeHandle then
        -- Update options if provided
        if options then
            self._exposeHandle:update(options)
        end
        return self._exposeHandle
    end

    -- Check if ExposedObjects is available
    if not ExposedObjects then
        error("ExposedObjects not available - expose API requires ExposedObjects module")
    end

    options = options or {}

    -- Get registry path for stable identification
    local path = self:_getRegistryPath()
    if path then
        options.path = options.path or path
    end

    -- Use object name if no name provided
    options.name = options.name or self._name or self._objId

    -- Determine expose type
    local exposeType = self:_getExposeType()

    -- Expose and cache handle
    self._exposeHandle = ExposedObjects:expose(self, exposeType, options)

    return self._exposeHandle
end

--- Unexposes this object from MQTT and HomeKit
-- @return boolean True if was exposed and now unexposed
function RemoteObject:unexpose()
    if self._exposeHandle then
        self._exposeHandle:unexpose()
        self._exposeHandle = nil
        return true
    end
    return false
end

--- Checks if this object is currently exposed
-- @return boolean True if exposed
function RemoteObject:isExposed()
    -- Clean up dead handle reference
    if self._exposeHandle and not self._exposeHandle:isExposed() then
        self._exposeHandle = nil
    end
    return self._exposeHandle ~= nil
end

--- Gets the expose handle if this object is exposed
-- @return ExposedHandle|nil The handle or nil if not exposed
function RemoteObject:getExposeHandle()
    -- Clean up dead handle reference
    if self._exposeHandle and not self._exposeHandle:isExposed() then
        self._exposeHandle = nil
    end
    return self._exposeHandle
end

return RemoteObject
