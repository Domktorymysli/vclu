--- Object Registry System for vCLU.
-- Provides a centralized registry for managing remote objects,
-- supporting lookups by path, name, or tags.
-- @module core.Registry

--- Main object registry table.
-- Stores all registered objects indexed by their full path.
_registry = {}

--- Name to path index.
-- Maps object names to their full paths for quick lookup.
_nameIndex = {}

--- Tag to objects index.
-- Maps tags to arrays of objects that have that tag.
_tagIndex = {}

--- Helper function to resolve path or name to actual path.
-- @param pathOrName string Object path or name
-- @return string The resolved full path
local function resolvePath(pathOrName)
    if pathOrName:find("%.") then
        return pathOrName
    end
    return _nameIndex[pathOrName] or pathOrName
end

-- Registry methods table
local _registry_methods = {
    --- Synchronize object state from external source (e.g., real CLU scripts).
    -- Updates the state via updateState() and publishes one state_changed.
    -- Used by real CLU om.lua scripts: VCLU:execute(0, "_:sync('CLU.DOUT1', value)")
    -- @api public
    -- @param self table The registry instance
    -- @param pathOrName string Object path or name
    -- @param value any New state value
    sync = function(self, pathOrName, value)
        local path = resolvePath(pathOrName)
        local obj = self[path]

        if obj then
            local numValue = tonumber(value)
            if numValue ~= nil then
                value = numValue
            end

            local oldState = StateChange.current(obj)
            local mark = StateChange.mark(obj)
            -- _:sync is how CLU scripts report state: the change comes from that CLU
            local ctx = { origin = "grenton", actor = "device:" .. (path:match("^([^.]+)%.") or path) }
            -- a first reading after a Lua reload (nil) is no change and must not drop an expectation
            if oldState ~= nil and oldState ~= value then ctx = StateChange.reportContext(obj, value, ctx) end
            if obj.updateState then
                StateChange.applyFrom(obj, ctx, obj.updateState, obj, value)
            else
                local report
                report, oldState = StateChange.filter(obj, oldState, value)
                obj._lastState = value
            end

            -- An onChange callback (expose) may have published already
            if not StateChange.publishedSince(obj, mark) then
                StateChange.publish(path, obj, oldState, StateChange.current(obj), ctx)
            end
        end
    end,

    --- Emit an event on an object.
    -- Used by real CLU scripts to forward events: _:event("CLU.DOUT_01.OnSwitchOn")
    -- @api public
    -- @param self table The registry instance
    -- @param pathOrName string Full event path (e.g., "CLU.DOUT_01.OnSwitchOn")
    event = function(self, pathOrName)
        local objPart, eventName = pathOrName:match("(.+)%.([^.]+)$")
        if not objPart then return end

        local path = resolvePath(objPart)
        local obj = self[path]
        if obj then
            obj:emit(eventName)
        end
    end,

    --- Get an object by name or path.
    -- @api public
    -- @param self table The registry instance
    -- @param pathOrName string Object path or name
    -- @return table|nil The object if found
    get = function(self, pathOrName)
        local path = resolvePath(pathOrName)
        return self[path]
    end,

    --- Get all objects with a specific tag.
    -- @api public
    -- @param self table The registry instance
    -- @param tag string The tag to search for
    -- @return RemoteGroup A group containing all matching objects
    byTag = function(self, tag)
        local group = RemoteGroup:new(_tagIndex[tag] or {})
        group._sourceTag = tag
        return group
    end,

    --- Get objects matching any of the specified tags.
    -- @api public
    -- @param self table The registry instance
    -- @param ... string Variable number of tags
    -- @return RemoteGroup A group containing all matching objects
    byTags = function(self, ...)
        local tags = {...}
        local result = {}
        local seen = {}
        for _, tag in ipairs(tags) do
            for _, obj in ipairs(_tagIndex[tag] or {}) do
                if not seen[obj] then
                    seen[obj] = true
                    table.insert(result, obj)
                end
            end
        end
        local group = RemoteGroup:new(result)
        group._sourceTags = tags
        return group
    end,

    --- Get all available tags in the registry.
    -- @api public
    -- @param self table The registry instance
    -- @return table Array of tag strings
    tags = function(self)
        local tags = {}
        for tag, _ in pairs(_tagIndex) do
            table.insert(tags, tag)
        end
        return tags
    end,

    --- Get registry information summary.
    -- @param self table The registry instance
    -- @return string Formatted summary of registry contents
    info = function(self)
        local lines = {"=== Object Registry ==="}
        local count = 0
        local byCLU = {}

        for path, obj in pairs(self) do
            if type(path) == "string" then
                count = count + 1
                local clu = obj._isPlugin and "Plugins" or path:match("^([^.]+)")
                local objType = obj._isPlugin and "plugin" or obj._type or "unknown"
                if clu then
                    if not byCLU[clu] then
                        byCLU[clu] = {_total = 0}
                    end
                    byCLU[clu]._total = byCLU[clu]._total + 1
                    byCLU[clu][objType] = (byCLU[clu][objType] or 0) + 1
                end
            end
        end

        local cluList = {}
        for clu in pairs(byCLU) do
            table.insert(cluList, clu)
        end
        table.sort(cluList)

        for _, clu in ipairs(cluList) do
            local data = byCLU[clu]
            table.insert(lines, string.format("\n  %s: %d objects", clu, data._total))
            local types = {}
            for t in pairs(data) do
                if t ~= "_total" then
                    table.insert(types, t)
                end
            end
            table.sort(types)
            for _, t in ipairs(types) do
                table.insert(lines, string.format("    - %s: %d", t, data[t]))
            end
        end

        table.insert(lines, string.format("\n=== Total: %d objects ===", count))
        return table.concat(lines, "\n")
    end,

    --- List all registered objects.
    -- @api public
    -- @param self table The registry instance
    -- @return table Array of tables with path, type, and name
    list = function(self)
        local result = {}
        for path, obj in pairs(self) do
            if type(path) == "string" then
                local objType = obj._isPlugin and "plugin" or obj._type or "unknown"
                local objName = obj._isPlugin and obj.name or obj._name
                table.insert(result, {path = path, type = objType, name = objName})
            end
        end
        table.sort(result, function(a, b) return a.path < b.path end)
        return result
    end,

    --- Find objects matching a pattern.
    -- @api public
    -- @param self table The registry instance
    -- @param pattern string Lua pattern to match against paths
    -- @return table Array of tables with path and obj fields
    find = function(self, pattern)
        local result = {}
        for path, obj in pairs(self) do
            if type(path) == "string" and path:match(pattern) then
                table.insert(result, {path = path, obj = obj})
            end
        end
        return result
    end,

    --- Export registry to JSON string.
    -- @param self table The registry instance
    -- @return string JSON array containing all objects
    toJSON = function(self)
        local results = {}
        for path, obj in pairs(self) do
            if type(path) == "string" and type(obj) == "table" then
                local cluId, objectId = path:match("^([^.]+)%.(.+)$")
                if obj._isPlugin then
                    -- Plugin entry — group by "Plugins"
                    local pluginCluId = "Plugins"
                    local pluginObjectId = path
                    local objName = tostring(obj.name or obj.shortId or path)
                    local version = tostring(obj.version or "")

                    -- Collect sensors
                    local sensorParts = {}
                    if obj._sensors then
                        for id, _ in pairs(obj._sensors) do
                            table.insert(sensorParts, '"' .. id .. '"')
                        end
                        table.sort(sensorParts)
                    end

                    -- Collect controls
                    local controlParts = {}
                    if obj._controls then
                        for id, _ in pairs(obj._controls) do
                            table.insert(controlParts, '"' .. id .. '"')
                        end
                        table.sort(controlParts)
                    end

                    local json = '{"path":"' .. path .. '","type":"plugin","name":"' .. objName .. '","state":null' ..
                        ',"cluId":"' .. pluginCluId .. '","objectId":"' .. pluginObjectId .. '"' ..
                        ',"version":"' .. version .. '"' ..
                        ',"sensors":[' .. table.concat(sensorParts, ",") .. ']' ..
                        ',"controls":[' .. table.concat(controlParts, ",") .. ']}'
                    table.insert(results, json)
                else
                    -- Regular object entry
                    local objType = tostring(obj._type or "unknown")
                    local objName = tostring(obj._name or objectId or "")
                    local state = obj._lastState
                    local stateStr = "null"
                    if type(state) == "number" then
                        stateStr = tostring(state)
                    elseif type(state) == "boolean" then
                        stateStr = state and "true" or "false"
                    elseif type(state) == "string" then
                        local escaped = state:gsub('\\', '\\\\'):gsub('"', '\\"'):gsub('\n', '\\n'):gsub('\r', '\\r')
                        stateStr = '"' .. escaped .. '"'
                    elseif type(state) == "table" then
                        local parts = {}
                        for i, v in ipairs(state) do
                            if type(v) == "number" then
                                table.insert(parts, tostring(v))
                            elseif type(v) == "string" then
                                table.insert(parts, '"' .. v:gsub('"', '\\"') .. '"')
                            elseif type(v) == "boolean" then
                                table.insert(parts, v and "true" or "false")
                            end
                        end
                        stateStr = "[" .. table.concat(parts, ",") .. "]"
                    end
                    local json = '{"path":"' .. path .. '","type":"' .. objType .. '","name":"' .. objName .. '","state":' .. stateStr .. ',"cluId":"' .. (cluId or "") .. '","objectId":"' .. (objectId or "") .. '"}'
                    table.insert(results, json)
                end
            end
        end
        table.sort(results)
        return "[" .. table.concat(results, ",") .. "]"
    end
}

setmetatable(_registry, {__index = _registry_methods})

--- Global registry alias.
-- Convenient shorthand for accessing the registry.
_ = _registry

--- Register an object in the registry.
-- @api public
-- @param path string Full object path (e.g., "CLU.DOUT_01")
-- @param obj table The object instance to register
function registerObject(path, obj)
    _registry[path] = obj
    if StateChange then StateChange.attachHistory(obj, path) end
    if obj._name and obj._name ~= "" then
        _nameIndex[obj._name] = path
    end
end

--- Remove all objects whose path starts with the given prefix.
-- Used when an OM file is deleted to clean up its objects from the registry.
-- @param prefix string Path prefix (e.g., "CLU220000592")
-- @return number Count of removed objects
function removeByPrefix(prefix)
    local toRemove = {}
    for path in pairs(_registry) do
        if type(path) == "string" and path:sub(1, #prefix) == prefix then
            table.insert(toRemove, path)
        end
    end
    for _, path in ipairs(toRemove) do
        local obj = _registry[path]
        if obj and obj._name and _nameIndex[obj._name] == path then
            _nameIndex[obj._name] = nil
        end
        _registry[path] = nil
    end
    return #toRemove
end

return {
    _registry = _registry,
    _nameIndex = _nameIndex,
    _tagIndex = _tagIndex,
    registerObject = registerObject,
    removeByPrefix = removeByPrefix
}
