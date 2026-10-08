--- MCP (Model Context Protocol) safe Lua API
-- Provides validated functions for MCP DeviceProvider to call from Go.
-- All functions are called via LuaEngine.Call() — no string interpolation.
-- @module MCP

local MCP = {}

--- List all exposed devices, optionally filtered.
-- @param filterJson string|nil JSON filter object or legacy type string
-- @return string JSON array of devices
function MCP.list_devices(filterJson)
    if not ExposedObjects then
        return "[]"
    end

    -- Parse filter: either JSON object or legacy plain type string
    local filter = {}
    if filterJson and filterJson ~= "" then
        if filterJson:sub(1, 1) == "{" then
            filter = JSON:decode(filterJson) or {}
        else
            -- Legacy: plain string = type filter
            filter.type = filterJson
        end
    end

    local results = {}
    for path, handle in pairs(ExposedObjects:list()) do
        if handle:isExposed() then
            local include = true

            -- Filter by type
            if filter.type and filter.type ~= "" and handle.type ~= filter.type then
                include = false
            end

            -- Filter by area
            if include and filter.area and filter.area ~= "" then
                if not handle.options.area or handle.options.area ~= filter.area then
                    include = false
                end
            end

            -- Filter by group
            if include and filter.group and filter.group ~= "" then
                if not handle.options.group or handle.options.group ~= filter.group then
                    include = false
                end
            end

            -- Filter by tag (any match)
            if include and filter.tag and filter.tag ~= "" then
                local found = false
                if handle.options.tags then
                    for _, t in ipairs(handle.options.tags) do
                        if t == filter.tag then found = true; break end
                    end
                end
                if not found then include = false end
            end

            -- Filter by text (search in name, description, path)
            if include and filter.text and filter.text ~= "" then
                local needle = filter.text:lower()
                local haystack = (path .. " " .. (handle.options.name or "") .. " " .. (handle.options.description or "")):lower()
                if not haystack:find(needle, 1, true) then
                    include = false
                end
            end

            if include then
                -- Collect methods from object (instance + metatable)
                local methods = {}
                local seen = {}
                if handle.object then
                    -- Instance methods
                    for k, v in pairs(handle.object) do
                        if type(v) == "function" and not k:match("^_") then
                            seen[k] = true
                            table.insert(methods, k)
                        end
                    end
                    -- Metatable methods (e.g., DOUT:toggle, switchOn, switchOff)
                    local mt = getmetatable(handle.object)
                    if mt and mt.__index and type(mt.__index) == "table" then
                        for k, v in pairs(mt.__index) do
                            if type(v) == "function" and not k:match("^_") and not seen[k] then
                                seen[k] = true
                                table.insert(methods, k)
                            end
                        end
                    end
                end

                local entry = {
                    path = path,
                    type = handle.type,
                    name = handle.options.name or path,
                    state = handle:getValue(),
                    readonly = handle.options.readonly or false,
                    methods = methods,
                    lastChanged = handle:getLastChanged(),
                }
                -- Include metadata if present
                if handle.options.area then entry.area = handle.options.area end
                if handle.options.group then entry.group = handle.options.group end
                if handle.options.tags then entry.tags = handle.options.tags end
                if handle.options.description then entry.description = handle.options.description end
                table.insert(results, entry)
            end
        end
    end

    return JSON:encode(results)
end

--- Get a single device's state and details.
-- @param path string Device path
-- @return string JSON object with device details, or error
function MCP.get_device(path)
    if not path or path == "" then
        return JSON:encode({ error = "path is required" })
    end

    if not ExposedObjects then
        return JSON:encode({ error = "ExposedObjects not available" })
    end

    local handle = ExposedObjects:get(path)
    if not handle or not handle:isExposed() then
        return JSON:encode({ error = "device not found: " .. path })
    end

    -- Collect methods (instance + metatable)
    local methods = {}
    local events = {}
    local seen = {}
    if handle.object then
        for k, v in pairs(handle.object) do
            if type(v) == "function" and not k:match("^_") then
                seen[k] = true
                table.insert(methods, k)
            end
        end
        -- Metatable methods
        local mt = getmetatable(handle.object)
        if mt and mt.__index and type(mt.__index) == "table" then
            for k, v in pairs(mt.__index) do
                if type(v) == "function" and not k:match("^_") and not seen[k] then
                    seen[k] = true
                    table.insert(methods, k)
                end
            end
        end
        -- Check for events
        if handle.object._events then
            for k in pairs(handle.object._events) do
                table.insert(events, k)
            end
        end
    end

    local caps = ExposedObjects:getCapabilities(handle.type) or {}

    local result = {
        path = path,
        type = handle.type,
        name = handle.options.name or path,
        state = handle:getValue(),
        readonly = handle.options.readonly or false,
        methods = methods,
        events = events,
        caps = caps,
        lastChanged = handle:getLastChanged(),
    }
    -- Include metadata if present
    if handle.options.area then result.area = handle.options.area end
    if handle.options.group then result.group = handle.options.group end
    if handle.options.tags then result.tags = handle.options.tags end
    if handle.options.description then result.description = handle.options.description end
    return JSON:encode(result)
end

--- Set a device value.
-- @param path string Device path
-- @param value any Value to set
-- @return string JSON result
function MCP.set_value(path, value)
    if not path or path == "" then
        return JSON:encode({ ok = false, error = "path is required" })
    end

    if not ExposedObjects then
        return JSON:encode({ ok = false, error = "ExposedObjects not available" })
    end

    local handle = ExposedObjects:get(path)
    if not handle or not handle:isExposed() then
        return JSON:encode({ ok = false, error = "device not found: " .. path })
    end

    if handle.options.readonly then
        return JSON:encode({ ok = false, error = "device is readonly: " .. path })
    end

    local ok = handle:setValue(value)
    if not ok then
        return JSON:encode({ ok = false, error = "setValue failed for: " .. path })
    end

    -- Read back current state
    local newState = handle:getValue()
    return JSON:encode({ ok = true, new_state = newState })
end

--- Execute a method on a device.
-- @param path string Device path
-- @param method string Method name
-- @param argsJson string|nil JSON-encoded array of arguments
-- @return string JSON result
function MCP.execute_method(path, method, argsJson)
    if not path or path == "" then
        return JSON:encode({ ok = false, error = "path is required" })
    end
    if not method or method == "" then
        return JSON:encode({ ok = false, error = "method is required" })
    end

    if not ExposedObjects then
        return JSON:encode({ ok = false, error = "ExposedObjects not available" })
    end

    local handle = ExposedObjects:get(path)
    if not handle or not handle:isExposed() then
        return JSON:encode({ ok = false, error = "device not found: " .. path })
    end

    if handle.options.readonly then
        return JSON:encode({ ok = false, error = "device is readonly: " .. path })
    end

    local obj = handle.object
    if not obj then
        return JSON:encode({ ok = false, error = "device object is nil" })
    end

    -- Validate method exists
    local fn = obj[method]
    if type(fn) ~= "function" then
        return JSON:encode({ ok = false, error = "method not found: " .. method })
    end

    -- Parse args
    local args = {}
    if argsJson and argsJson ~= "" then
        local decoded = JSON:decode(argsJson)
        if type(decoded) == "table" then
            args = decoded
        end
    end

    -- Call method safely
    local ok, result = pcall(function()
        return fn(obj, unpack(args))
    end)

    if not ok then
        return JSON:encode({ ok = false, error = "method call failed: " .. tostring(result) })
    end

    return JSON:encode({ ok = true, result = result })
end

--- List all exposed scenes.
-- @return string JSON array of scenes
function MCP.list_scenes()
    if not ExposedObjects then
        return "[]"
    end

    local results = {}
    for path, handle in pairs(ExposedObjects:list()) do
        if handle:isExposed() and handle.type == "scene" then
            table.insert(results, {
                path = path,
                name = handle.options.name or path,
            })
        end
    end

    return JSON:encode(results)
end

--- Execute a scene.
-- @param path string Scene path
-- @return string JSON result
function MCP.execute_scene(path)
    if not path or path == "" then
        return JSON:encode({ ok = false, error = "path is required" })
    end

    if not ExposedObjects then
        return JSON:encode({ ok = false, error = "ExposedObjects not available" })
    end

    local handle = ExposedObjects:get(path)
    if not handle or not handle:isExposed() then
        return JSON:encode({ ok = false, error = "scene not found: " .. path })
    end

    if handle.type ~= "scene" then
        return JSON:encode({ ok = false, error = "not a scene: " .. path })
    end

    local ok = handle:executeScene()
    if not ok then
        return JSON:encode({ ok = false, error = "scene execution failed or in cooldown" })
    end

    return JSON:encode({ ok = true })
end

return MCP
