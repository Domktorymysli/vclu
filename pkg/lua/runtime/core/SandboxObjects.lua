--- SandboxObjects - integration objects for the MCP sandbox.
--
-- Production exports descriptors {path, kind, state, config} of objects whose
-- class has describe(); the sandbox rebuilds them through a closed set of
-- factories. Class names and metatables are never taken from the data.
--
-- @module core.SandboxObjects

SandboxObjects = { VERSION = 1 }

local factories = {
    satel_zone = SatelZone.fromDescriptor,
}

--- Descriptors of all registry objects that can describe themselves.
-- @return string JSON {version, timezone, objects}
function SandboxObjects.export()
    local list = {}
    for path, obj in pairs(_registry) do
        if type(path) == "string" and type(obj) == "table" and type(obj.describe) == "function" then
            local ok, d = pcall(obj.describe, obj)
            if ok and type(d) == "table" and factories[d.kind] then
                d.path = path
                table.insert(list, d)
            end
        end
    end
    table.sort(list, function(a, b) return a.path < b.path end)
    -- Go encoder escapes everything; an empty Lua table would encode as {}
    local encode = __go_json_encode or JSON.encode
    local objects = "[]"
    if #list > 0 then
        local err
        objects, err = encode(list)
        if not objects then error("SandboxObjects.export: " .. tostring(err)) end
    end
    local tz = datetime and datetime.timezone and datetime.timezone() or ""
    return string.format('{"version":%d,"timezone":%s,"objects":%s}', SandboxObjects.VERSION, encode(tz), objects)
end

--- Creates and registers objects from descriptors.
-- @param list table Array of {path, kind, state, config}
-- @return table Warnings for descriptors that could not be built
function SandboxObjects.load(list)
    local warnings = {}
    for i, d in ipairs(list or {}) do
        local factory = factories[d.kind]
        if type(d.path) ~= "string" or d.path == "" then
            table.insert(warnings, "fixture " .. i .. ": missing path")
        elseif not factory then
            table.insert(warnings, d.path .. ": unknown kind '" .. tostring(d.kind) .. "'")
        else
            local ok, obj = pcall(factory, d)
            if ok then
                registerObject(d.path, obj)
            else
                table.insert(warnings, d.path .. ": " .. tostring(obj))
            end
        end
    end
    return warnings
end

return SandboxObjects
