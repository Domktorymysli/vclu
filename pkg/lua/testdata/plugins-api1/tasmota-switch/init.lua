--[[
    Tasmota Switch Plugin dla vCLU

    Fabryka obiektów do sterowania urządzeniami Tasmota przez HTTP.

    Użycie:
        local tasmota = Plugin.getPlugin("@vclu/tasmota-switch")

        local lamp = tasmota:create({
            ip = "192.168.1.100",
            name = "Lampka Biurko",
            password = "admin"  -- opcjonalne
        })

        lamp:on()
        lamp:off()
        lamp:toggle()

        expose(lamp, "switch", { name = "Lampka Biurko" })
]]

--------------------------------------------------------------------------------
-- PLUGIN REGISTRATION
--------------------------------------------------------------------------------

local plugin = Plugin:new("tasmota-switch", {
    name = "Tasmota Switch",
    version = "1.0.3",
    description = "Factory for Tasmota switch objects"
})

--------------------------------------------------------------------------------
-- TASMOTA DEVICE CLASS
--------------------------------------------------------------------------------

-- Method table copied onto each instance. The plugin sandbox exposes no
-- setmetatable, so inheritance through __index is not available here.
local TasmotaSwitch = {}

function TasmotaSwitch:new(options)
    local obj = {
        _type = "switch",
        _id = options.id,
        _name = options.name or "Tasmota",
        _ip = options.ip,
        _password = options.password,
        _value = 0,
        _online = false,
        _lastUpdate = 0,
        _lastError = nil,
        _eventHandlers = {},
        _polling = options.polling ~= false,  -- default true
        _pollInterval = options.pollInterval or 30,
        _poller = nil
    }
    for name, fn in pairs(TasmotaSwitch) do obj[name] = fn end

    -- Start polling if enabled
    if obj._polling and obj._ip then
        obj:_startPolling()
    end

    return obj
end

--------------------------------------------------------------------------------
-- HTTP COMMANDS
--------------------------------------------------------------------------------

function TasmotaSwitch:_buildUrl(cmd)
    local url = string.format("http://%s/cm?cmnd=%s", self._ip, cmd)
    if self._password then
        url = url .. "&user=admin&password=" .. self._password
    end
    return url
end

function TasmotaSwitch:_command(cmd, callback)
    if not self._ip then
        if callback then callback(nil, "No IP configured") end
        return
    end

    local url = self:_buildUrl(cmd)

    plugin:httpRequest({
        url = url,
        timeout = 5000,
        parseJson = "success"
    }, function(resp)
        if resp.err then
            self._online = false
            self._lastError = resp.err
            if callback then callback(nil, resp.err) end
            return
        end

        self._online = true
        self._lastError = nil
        self._lastUpdate = os.time()

        if callback then callback(resp.json, nil) end
    end)
end

function TasmotaSwitch:_updateState(json)
    if not json then return end

    local power = json.POWER or json.POWER1
    if power then
        local newValue = (power == "ON") and 1 or 0
        local oldValue = self._value
        self._value = newValue

        if newValue ~= oldValue then
            self:emit("OnChange", newValue, oldValue)
        end
    end
end

function TasmotaSwitch:_startPolling()
    if self._poller then return end

    self._poller = plugin:poller("poll_" .. self._ip, {
        interval = self._pollInterval * 1000,
        immediate = true,
        timeout = 10000,
        retry = { maxAttempts = 2, backoff = 1000 },

        onTick = function(done)
            self:_command("Power", function(json, err)
                if err then
                    done(nil, err)
                    return
                end
                self:_updateState(json)
                done()
            end)
        end,

        onError = function(err)
            self._online = false
            plugin:log("warn", self._name .. ": poll failed - " .. tostring(err))
        end
    })

    self._poller:start()
end

function TasmotaSwitch:_stopPolling()
    if self._poller then
        self._poller:stop()
        self._poller = nil
    end
end

--------------------------------------------------------------------------------
-- PUBLIC API - Control
--------------------------------------------------------------------------------

function TasmotaSwitch:on()
    self:_command("Power%20ON", function(json, err)
        if json then
            self:_updateState(json)
            plugin:log("debug", self._name .. ": ON")
        end
    end)
end

function TasmotaSwitch:off()
    self:_command("Power%20OFF", function(json, err)
        if json then
            self:_updateState(json)
            plugin:log("debug", self._name .. ": OFF")
        end
    end)
end

function TasmotaSwitch:toggle()
    self:_command("Power%20TOGGLE", function(json, err)
        if json then
            self:_updateState(json)
            plugin:log("debug", self._name .. ": TOGGLE -> " .. (self._value == 1 and "ON" or "OFF"))
        end
    end)
end

function TasmotaSwitch:refresh()
    self:_command("Power", function(json, err)
        if json then
            self:_updateState(json)
        end
    end)
end

--------------------------------------------------------------------------------
-- PUBLIC API - Configuration
--------------------------------------------------------------------------------

function TasmotaSwitch:setId(id)
    self._id = id
    return self
end

function TasmotaSwitch:setIp(ip)
    self._ip = ip
    return self
end

function TasmotaSwitch:setPassword(password)
    self._password = password
    return self
end

function TasmotaSwitch:setName(name)
    self._name = name
    return self
end

function TasmotaSwitch:setPolling(enabled, intervalSec)
    self._polling = enabled
    if intervalSec then
        self._pollInterval = intervalSec
    end

    if enabled and self._ip then
        self:_startPolling()
    else
        self:_stopPolling()
    end
    return self
end

--------------------------------------------------------------------------------
-- PUBLIC API - State
--------------------------------------------------------------------------------

function TasmotaSwitch:getId()
    return self._id
end

function TasmotaSwitch:isOn()
    return self._value == 1
end

function TasmotaSwitch:isOnline()
    return self._online
end

function TasmotaSwitch:getValue()
    return self._value
end

function TasmotaSwitch:getState()
    return {
        id = self._id,
        name = self._name,
        ip = self._ip,
        value = self._value,
        online = self._online,
        lastUpdate = self._lastUpdate,
        lastError = self._lastError
    }
end

--------------------------------------------------------------------------------
-- EXPOSE INTERFACE (required by expose())
--------------------------------------------------------------------------------

function TasmotaSwitch:get(feature)
    return self._value
end

function TasmotaSwitch:set(feature, value)
    if value == 1 or value == true or value == "ON" then
        self:on()
    else
        self:off()
    end
end

function TasmotaSwitch:switchOn()
    self:on()
end

function TasmotaSwitch:switchOff()
    self:off()
end

--------------------------------------------------------------------------------
-- EVENT SYSTEM
--------------------------------------------------------------------------------

function TasmotaSwitch:on_event(event, callback)
    if not self._eventHandlers[event] then
        self._eventHandlers[event] = {}
    end
    local handlers = self._eventHandlers[event]
    table.insert(handlers, callback)
    -- expose() keeps this and calls it on unexpose().
    return function()
        for i, cb in ipairs(handlers) do
            if cb == callback then
                table.remove(handlers, i)
                return
            end
        end
    end
end

function TasmotaSwitch:emit(event, ...)
    local handlers = self._eventHandlers[event]
    if handlers then
        for _, cb in ipairs(handlers) do
            pcall(cb, ...)
        end
    end
end

function TasmotaSwitch:onChange(callback)
    return self:on_event("OnChange", callback)
end

--------------------------------------------------------------------------------
-- PLUGIN FACTORY
--------------------------------------------------------------------------------

local devices = {}

plugin:onInit(function(config)
    plugin:log("info", "Tasmota Switch factory ready")
end)

plugin:onCleanup(function()
    -- Stop all pollers
    for _, device in pairs(devices) do
        device:_stopPolling()
    end
    devices = {}
    plugin:log("info", "Tasmota Switch cleanup")
end)

--- Create a new Tasmota switch object
-- @param options table { ip, name, password, polling, pollInterval }
-- @return TasmotaSwitch
function plugin:create(options)
    if type(options) == "string" then
        options = { ip = options }
    end

    local device = TasmotaSwitch:new(options)
    table.insert(devices, device)

    plugin:log("info", "Created: " .. device._name .. " @ " .. (device._ip or "no-ip"))

    return device
end

--- Get all created devices
-- @return table
function plugin:getDevices()
    return devices
end

return plugin
