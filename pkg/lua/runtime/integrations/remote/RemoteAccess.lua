--- RemoteAccess - access from outside the local network
-- The exposed objects filtered by the "remote" access profile, and a command entry
-- that checks the profile where the command executes. The remote side is not trusted.
-- @module integrations.remote.RemoteAccess

RemoteAccess = {}

local INTEGRATION = "remote"

-- Commands each exposed type takes: name -> HA subpath and payload
local COMMANDS = {
    switch = { on = { nil, "ON" }, off = { nil, "OFF" } },
    light = { on = { nil, "ON" }, off = { nil, "OFF" }, brightness = { "brightness" } },
    dimmer = { on = { nil, "ON" }, off = { nil, "OFF" }, brightness = { "brightness" } },
    cover = { open = { nil, "OPEN" }, close = { nil, "CLOSE" }, stop = { nil, "STOP" }, position = { "position" } },
    climate = { target = { "target_temp" } },
    number = { value = { nil } },
    scene = { run = { nil, "ON" } },
}

-- The level for remote access: readonly also when expose() says so; an unknown level is hidden
local function level(handle)
    local l = AccessControl.get(INTEGRATION, handle.path)
    if l == "full" and handle.options and handle.options.readonly then
        return "readonly"
    end
    if l ~= "full" and l ~= "readonly" then
        return "hidden"
    end
    return l
end

--- The exposed objects remote access may see, sorted by path.
-- @return table list of {path, name, type, area, value, access ("full" or "readonly")}
function RemoteAccess.objects()
    local out = {}
    for path, handle in pairs(ExposedObjects:list()) do
        local l = level(handle)
        if l ~= "hidden" then
            table.insert(out, {
                path = path,
                name = handle.options.name or path,
                type = handle.type,
                area = handle.options.area,
                value = handle:getValue(),
                access = l,
            })
        end
    end
    table.sort(out, function(a, b) return a.path < b.path end)
    return out
end

local function run(handle, command, value)
    local spec = (COMMANDS[handle.type] or {})[command]
    if not spec then
        return false, "unsupported command: " .. tostring(command)
    end
    local subpath, payload = spec[1], spec[2]
    if not payload then
        local n = tonumber(value)
        if not n or n - n ~= 0 then
            return false, "invalid value"
        end
        -- a number stays in the range its expose() options give
        if handle.type == "number" then
            local min, max = tonumber(handle.options.min), tonumber(handle.options.max)
            if min and n < min then n = min end
            if max and n > max then n = max end
        end
        payload = tostring(n)
    end
    -- a switch has its own methods; the HA path would write its feature 0
    local obj = handle.object
    if handle.type == "switch" and type(obj) == "table" then
        local method = obj[command == "on" and "switchOn" or "switchOff"]
        if type(method) == "function" then
            method(obj)
            return true
        end
    end
    return HACommands.applyExposed(handle, subpath, payload)
end

--- Runs a command from remote access on an exposed object.
-- @param path string Path of the exposed object
-- @param command string on, off, brightness, position, open, close, stop, target, value, run
-- @param value number|nil brightness and position 0..100, target in °C, value for a number
-- @param actor string Who sent it: "user:<login>"
-- @return boolean, string|nil
function RemoteAccess.command(path, command, value, actor)
    if type(actor) ~= "string" or not actor:match("^user:%S+$") then
        return false, "actor must be user:<login>"
    end
    local handle = type(path) == "string" and ExposedObjects:get(path)
    -- hidden reads as not found: the remote side learns nothing about it
    if not handle or not handle._exposed or level(handle) == "hidden" then
        return false, "object not found: " .. tostring(path)
    end
    if level(handle) ~= "full" then
        return false, "access denied: " .. path .. " is readonly"
    end
    local ctx = { origin = "remote", actor = actor, correlationId = StateContext.newCorrelationId() }
    local ok, res, err = pcall(StateContext.with, ctx, run, handle, command, value)
    if not ok then
        Logger:getShared():warn("[RemoteAccess] %s %s failed: %s", path, tostring(command), tostring(res))
        return false, "command failed"
    end
    if res then
        Logger:getShared():info("[RemoteAccess] %s %s by %s", path, tostring(command), actor)
    end
    return res and true or false, err
end

return RemoteAccess
