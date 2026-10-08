--- RemoteGroup class for bulk operations on multiple objects.
-- A collection wrapper that allows calling methods on multiple objects at once.
-- Supports method chaining and provides utility methods for filtering and iteration.
-- @module core.RemoteGroup

RemoteGroup = {}

--- Metatable __index function for RemoteGroup.
-- Enables dynamic method forwarding to all objects in the group.
-- @param self RemoteGroup The group instance
-- @param key string The method or property name being accessed
-- @return function|any Returns the actual property or a wrapper function
RemoteGroup.__index = function(self, key)
    if key == "_objects" or key == "each" or key == "filter" or key == "count" then
        return rawget(RemoteGroup, key)
    end
    return function(self, ...)
        local args = {...}
        for _, obj in ipairs(self._objects) do
            if type(obj[key]) == "function" then
                obj[key](obj, table.unpack(args))
            end
        end
        return self
    end
end

--- Create a new RemoteGroup instance.
-- @param objects table Array of objects to include in the group
-- @return RemoteGroup New group instance
function RemoteGroup:new(objects)
    local group = {
        _objects = objects or {}
    }
    setmetatable(group, RemoteGroup)
    return group
end

--- Iterate over each object in the group.
-- Calls the provided callback function for each object.
-- @param callback function Function to call for each object, receives (obj, index)
-- @return RemoteGroup Returns self for method chaining
function RemoteGroup:each(callback)
    for i, obj in ipairs(self._objects) do
        callback(obj, i)
    end
    return self
end

--- Filter objects in the group based on a predicate.
-- Creates a new RemoteGroup containing only objects for which the predicate returns true.
-- @param predicate function Function that receives an object and returns boolean
-- @return RemoteGroup New group containing filtered objects
function RemoteGroup:filter(predicate)
    local filtered = {}
    for _, obj in ipairs(self._objects) do
        if predicate(obj) then
            table.insert(filtered, obj)
        end
    end
    return RemoteGroup:new(filtered)
end

--- Get the number of objects in the group.
-- @return number Count of objects in this group
function RemoteGroup:count()
    return #self._objects
end

--- Expose all objects in the group to MQTT/HomeKit.
-- Auto-determines type from object's _type field if not specified.
-- @param exposeType string|nil Optional type override (switch, light, dimmer, etc.)
-- @param options table|nil Optional options (area, name prefix, etc.)
-- @return table Array of ExposedHandle objects
function RemoteGroup:expose(exposeType, options)
    options = options or {}
    local handles = {}

    -- Stats for logging
    local stats = {
        exposed = 0,   -- newly exposed
        updated = 0,   -- re-exposed (idempotent update)
        skipped = 0,   -- hidden or no get() method
        errors = 0,    -- failed to expose
    }

    -- Use central TypeMap for type mappings
    local TypeMap = require("TypeMap")

    for _, obj in ipairs(self._objects) do
        -- Skip objects without get() method
        if type(obj.get) ~= "function" then
            stats.skipped = stats.skipped + 1
        else
            -- Determine type
            local objType = exposeType
            if not objType then
                objType = TypeMap.getExposeType(obj._type, "switch")
            end

            -- Build options for this object
            local objOptions = {}
            for k, v in pairs(options) do
                objOptions[k] = v
            end

            -- Use object's name if not specified
            if not objOptions.name and obj._name then
                objOptions.name = obj._name
            end

            -- Check if already exposed (for stats)
            local wasExposed = ExposedObjects and ExposedObjects._objectToPath
                and ExposedObjects._objectToPath[obj] ~= nil

            -- Expose
            if expose and type(expose) == "function" then
                local ok, handle = pcall(function()
                    return expose(obj, objType, objOptions)
                end)
                if ok and handle then
                    table.insert(handles, handle)
                    if wasExposed then
                        stats.updated = stats.updated + 1
                    else
                        stats.exposed = stats.exposed + 1
                    end
                else
                    stats.errors = stats.errors + 1
                    if Logger then
                        Logger:getShared():warn("[RemoteGroup] Failed to expose %s: %s",
                            tostring(obj._name or "unknown"), tostring(handle))
                    end
                end
            end
        end
    end

    -- Log summary
    if Logger then
        local parts = {
            string.format("exposed=%d", stats.exposed),
            string.format("updated=%d", stats.updated),
        }
        if stats.skipped > 0 then
            table.insert(parts, string.format("skipped=%d", stats.skipped))
        end
        if stats.errors > 0 then
            table.insert(parts, string.format("errors=%d", stats.errors))
        end
        if options.area then
            table.insert(parts, string.format("area=%s", options.area))
        end
        if self._sourceTag then
            table.insert(parts, string.format("tag=%s", self._sourceTag))
        elseif self._sourceTags then
            table.insert(parts, string.format("tags=%s", table.concat(self._sourceTags, ",")))
        end

        Logger:getShared():info("[RemoteGroup:expose] %s", table.concat(parts, " "))
    end

    return handles
end

--- Unexpose all objects in the group.
-- @return RemoteGroup Returns self for method chaining
function RemoteGroup:unexpose()
    for _, obj in ipairs(self._objects) do
        -- Try object's unexpose method if available
        if type(obj.unexpose) == "function" then
            pcall(function() obj:unexpose() end)
        -- Or try to find handle in ExposedObjects
        elseif ExposedObjects and obj._exposeHandle then
            pcall(function() obj._exposeHandle:unexpose() end)
        end
    end
    return self
end

return RemoteGroup
