--- Modbus Gateway Plugin for vCLU
-- Reads Modbus RTU devices sitting behind an RS485-to-Ethernet gateway
-- (Waveshare RS485 TO ETH, USR-TCP232, Elfin EW11 ...) over Modbus TCP.
--
-- The gateway must run in "TCP Server" mode with "Modbus TCP to RTU" enabled,
-- and its serial settings must match the bus (SDM120M ships as 9600 8N1).
--
-- @module plugins.modbus-gateway
--
-- ## Usage
--
-- Gateways are declared in code rather than in plugin config, so one vCLU can
-- drive several gateways at once. Each call to create() is an independent
-- gateway with its own poller and its own socket to the Go side.
--
-- ```lua
-- local modbus = Plugin.getPlugin("@vclu/modbus-gateway")
--
-- local garaz = modbus:create({
--     id = "garaz",
--     host = "192.168.0.9",
--     port = 4196,
--     interval = 30,
--     devices = {
--         { id = "ladowarka", unit = 1, name = "Ladowarka auta" },
--         { id = "klima",     unit = 2, name = "Klimatyzacja" },
--         { id = "pralka",    unit = 3, name = "Pralka i suszarka" }
--     }
-- })
--
-- expose(modbus:get("pralka_power"),  "number", { name = "Pralka moc", unit = "W" })
-- expose(modbus:get("pralka_online"), "binary_sensor", { name = "Pralka online" })
-- ```
--
-- A device may also be given as a bare unit address when the defaults fit:
-- `devices = { 1, 2, 3 }` yields meter1, meter2 and meter3 on the sdm120 profile.
--
-- ## Sensors
--
-- One set per device, prefixed with the device id:
--
-- | Suffix      | Unit | Description                        |
-- |-------------|------|------------------------------------|
-- | _voltage    | V    | Napiecie                           |
-- | _current    | A    | Prad                               |
-- | _power      | W    | Moc czynna                         |
-- | _apparent   | VA   | Moc pozorna                        |
-- | _reactive   | var  | Moc bierna                         |
-- | _pf         |      | Wspolczynnik mocy                  |
-- | _frequency  | Hz   | Czestotliwosc                      |
-- | _energy     | kWh  | Energia pobrana                    |
-- | _exported   | kWh  | Energia oddana                     |
-- | _total      | kWh  | Energia lacznie                    |
-- | _online     | 0/1  | Czy ostatni odczyt sie powiodl     |
--
-- Plus `gateway_<id>_online` per gateway.

--------------------------------------------------------------------------------
-- PLUGIN REGISTRATION
--------------------------------------------------------------------------------

local plugin = Plugin:new("modbus-gateway", {
    name = "Modbus Gateway",
    version = "2.1.0",
    description = "Fabryka bramek Modbus RTU po RS485-Ethernet (Modbus TCP)"
})

--------------------------------------------------------------------------------
-- DEVICE PROFILES
--------------------------------------------------------------------------------

-- Register maps are split into blocks of contiguous registers, so a device is
-- read in a few transactions instead of one per field.
-- Eastron SDM120/SDM220/SDM230 all expose 32-bit big-endian floats via FC04.
local PROFILES = {
    sdm120 = {
        label = "Eastron SDM120 / VCX SDM120M",
        fc = Modbus.READ_INPUT,
        blocks = {
            { base = 0x0000, qty = 32 },
            { base = 0x0046, qty = 10 },
            { base = 0x0156, qty = 4 }
        },
        fields = {
            { id = "voltage",   addr = 0x0000, unit = "V" },
            { id = "current",   addr = 0x0006, unit = "A" },
            { id = "power",     addr = 0x000C, unit = "W" },
            { id = "apparent",  addr = 0x0012, unit = "VA" },
            { id = "reactive",  addr = 0x0018, unit = "var" },
            { id = "pf",        addr = 0x001E, unit = "" },
            { id = "frequency", addr = 0x0046, unit = "Hz" },
            { id = "energy",    addr = 0x0048, unit = "kWh" },
            { id = "exported",  addr = 0x004A, unit = "kWh" },
            { id = "total",     addr = 0x0156, unit = "kWh" }
        }
    }
}

-- SDM220 and SDM230 share the layout for the fields we care about.
PROFILES.sdm220 = PROFILES.sdm120
PROFILES.sdm230 = PROFILES.sdm120

--------------------------------------------------------------------------------
-- MODULE STATE
--------------------------------------------------------------------------------

local gateways = {}    -- ordered list of Gateway instances
local byId = {}        -- gateway id -> Gateway
local ownerOfDevice = {} -- device id -> gateway id
-- Sensors of every gateway share one flat namespace, so an id registered twice
-- would let the second getter silently replace the first. Nothing is registered
-- before its ids are claimed here.
local sensorOwner = {} -- sensor id -> human readable owner

--------------------------------------------------------------------------------
-- HELPERS
--------------------------------------------------------------------------------

local function profileFor(device)
    if device.registers then
        -- Custom map: { {id="temp", addr=0x0010, type="float"}, ... }
        return {
            label = "custom",
            fc = device.fc or Modbus.READ_INPUT,
            blocks = nil,
            fields = device.registers
        }
    end
    return PROFILES[device.profile or "sdm120"]
end

-- 16-bit fields occupy one register; reading two can run off the end of the map.
local function registerCount(field)
    local kind = field.type or "float"
    if kind == "int16" or kind == "raw" then return 1 end
    return 2
end

local function decodeField(field, registers, base)
    local kind = field.type or "float"
    local i = field.addr - base + 1
    if kind == "float" then
        return Modbus.toFloat32(registers[i], registers[i + 1])
    elseif kind == "int32" then
        return Modbus.toInt32(registers[i], registers[i + 1])
    elseif kind == "uint32" then
        return Modbus.toUint32(registers[i], registers[i + 1])
    elseif kind == "int16" then
        return Modbus.toInt16(registers[i])
    end
    return registers[i]
end

local function fieldsInBlock(profile, block)
    local out = {}
    for _, field in ipairs(profile.fields) do
        if field.addr >= block.base and field.addr < block.base + block.qty then
            table.insert(out, field)
        end
    end
    return out
end

-- A device may be given as a bare unit address, since one gateway usually
-- carries several identical meters and only the address differs.
local function normalizeDevice(entry)
    if type(entry) == "number" then
        entry = { unit = entry }
    elseif type(entry) ~= "table" then
        return nil
    end

    local unit = plugin:coerceNumber(entry.unit, 0)
    if unit < 1 or unit > 247 then return nil end

    local id = plugin:coerceString(entry.id, "")
    if id == "" then id = "meter" .. tostring(unit) end

    return {
        id = id,
        unit = unit,
        name = plugin:coerceString(entry.name, id),
        profile = plugin:coerceString(entry.profile, "sdm120"),
        registers = entry.registers,
        fc = entry.fc
    }
end

local function notify(id)
    local s = plugin:get(id)
    if s and s.emit then s:emit("OnChange", s:get()) end
end

-- Gateway sensors get their own prefix so a gateway named like a device cannot
-- collide with that device's own _online sensor.
local function gatewaySensorId(gatewayId)
    return "gateway_" .. gatewayId .. "_online"
end

local function sensorIdsForDevice(device)
    local ids = { device.id .. "_online" }
    local profile = profileFor(device)
    if profile then
        for _, field in ipairs(profile.fields) do
            ids[#ids + 1] = device.id .. "_" .. field.id
        end
    end
    return ids
end

-- Returns false plus the offending id and its owner on the first clash.
local function sensorIdsFree(ids)
    for _, id in ipairs(ids) do
        if sensorOwner[id] then return false, id, sensorOwner[id] end
    end
    return true
end

local function claimSensorIds(ids, owner)
    for _, id in ipairs(ids) do sensorOwner[id] = owner end
end

--------------------------------------------------------------------------------
-- GATEWAY INSTANCE
--------------------------------------------------------------------------------

-- Method table copied onto each instance. The plugin sandbox exposes no
-- setmetatable, so inheritance through __index is not available here.
local Gateway = {}

function Gateway:_sensorId(device, fieldId)
    return device.id .. "_" .. fieldId
end

-- Reads one device and calls done(values, err). Blocks are chained so the RS485
-- bus carries one transaction at a time.
function Gateway:_readDevice(device, done)
    local profile = profileFor(device)
    if not profile then
        done(nil, "unknown profile: " .. tostring(device.profile))
        return
    end

    local ds = self.state.devices[device.id]
    local collected = {}

    -- Per-field reads: the fallback when a device rejects block reads.
    local function readFields(index)
        local field = profile.fields[index]
        if not field then
            done(collected, nil)
            return
        end
        Modbus.request({
            host = self.host, port = self.port,
            unit = device.unit, fc = profile.fc,
            addr = field.addr, qty = registerCount(field), timeout = self.timeout
        }, function(registers, err)
            if err then
                done(nil, err)
                return
            end
            collected[field.id] = decodeField(field, registers, field.addr)
            readFields(index + 1)
        end)
    end

    local function readBlocks(index)
        local block = profile.blocks[index]
        if not block then
            done(collected, nil)
            return
        end
        Modbus.request({
            host = self.host, port = self.port,
            unit = device.unit, fc = profile.fc,
            addr = block.base, qty = block.qty, timeout = self.timeout
        }, function(registers, err)
            if err then
                -- "illegal data address" means the device dislikes wide reads;
                -- drop to per-field mode for good rather than failing every tick.
                if tostring(err):find("illegal data address") and ds.blockMode then
                    ds.blockMode = false
                    plugin:log("warn", string.format(
                        "%s/%s (unit %d): block read rejected, switching to per-register reads",
                        self.id, device.id, device.unit))
                    readFields(1)
                    return
                end
                done(nil, err)
                return
            end
            for _, field in ipairs(fieldsInBlock(profile, block)) do
                collected[field.id] = decodeField(field, registers, block.base)
            end
            readBlocks(index + 1)
        end)
    end

    if profile.blocks and ds.blockMode then
        readBlocks(1)
    else
        readFields(1)
    end
end

function Gateway:_applyResult(device, values)
    local ds = self.state.devices[device.id]
    local wasOnline = ds.online
    ds.online = true
    ds.lastError = nil
    ds.values = values

    for fieldId, _ in pairs(values) do
        notify(self:_sensorId(device, fieldId))
    end
    if not wasOnline then notify(self:_sensorId(device, "online")) end

    plugin:updateObject("devices." .. device.id, {
        name = device.name or device.id,
        gateway = self.id,
        unit = device.unit,
        online = true,
        values = values,
        lastUpdate = os.time()
    })
end

function Gateway:_applyError(device, err)
    local ds = self.state.devices[device.id]
    local wasOnline = ds.online
    ds.online = false
    ds.lastError = err

    if wasOnline then
        notify(self:_sensorId(device, "online"))
        plugin:emit("modbus:device_offline", {
            gateway = self.id, device = device.id, unit = device.unit, error = err
        })
    end
    plugin:updateObject("devices." .. device.id, { online = false, lastError = err })
end

function Gateway:_registerSensors()
    for _, device in ipairs(self.devices) do
        self.state.devices[device.id] = { online = false, values = {}, blockMode = true }

        local profile = profileFor(device)
        if profile then
            for _, field in ipairs(profile.fields) do
                local deviceId, fieldId = device.id, field.id
                local this = self
                plugin:sensor(self:_sensorId(device, fieldId), function()
                    return this.state.devices[deviceId].values[fieldId] or 0
                end)
            end
        end

        local deviceId = device.id
        local this = self
        plugin:sensor(self:_sensorId(device, "online"), function()
            return this.state.devices[deviceId].online and 1 or 0
        end)

        plugin:upsertObject("devices." .. device.id, {
            name = device.name, gateway = self.id, unit = device.unit,
            online = false, values = {}, lastUpdate = 0
        })
    end

    local this = self
    plugin:sensor(gatewaySensorId(self.id), function()
        return this.state.online and 1 or 0
    end)
end

function Gateway:_buildPoller()
    local this = self
    self.poller = plugin:poller("read_" .. self.id, {
        interval = self.interval * 1000,
        immediate = true,
        -- Worst case: every device times out twice on the Go side.
        timeout = math.max(15000, #self.devices * self.timeout * 3),

        onTick = function(done)
            local index = 1
            local polled = 0
            local anySuccess = false
            local failures = {}

            local function step()
                local device = this.devices[index]
                if not device then
                    this.state.ready = true
                    -- Every device paused: nothing was asked, the link state stays as it was
                    if polled > 0 then
                        this.state.online = anySuccess
                        this.state.lastUpdate = os.time()
                    end
                    this.state.lastError = (#failures > 0) and table.concat(failures, "; ") or nil

                    plugin:updateObject("gateways." .. this.id, {
                        ready = true, online = this.state.online, lastUpdate = this.state.lastUpdate
                    })
                    notify(gatewaySensorId(this.id))

                    if anySuccess or polled == 0 then
                        plugin:emit("modbus:updated", {
                            gateway = this.id, devices = polled - #failures
                        }, { throttle = 30000, key = this.id })
                        -- A silent device is normal and often permanent, so it
                        -- must not back the poller off; _online carries the news.
                        if #failures > 0 then
                            plugin:log("warn", this.id .. " partial read: " .. this.state.lastError)
                        end
                        done({ read = polled - #failures, failed = #failures }, nil)
                    else
                        done(nil, this.state.lastError or "no device responded")
                    end
                    return
                end

                index = index + 1
                if this.state.paused[device.id] then return step() end
                polled = polled + 1
                this:_readDevice(device, function(values, err)
                    -- Paused while the read was in flight: the answer no longer counts
                    if this.state.paused[device.id] then
                        polled = polled - 1
                    elseif err then
                        this:_applyError(device, err)
                        table.insert(failures, device.id .. ": " .. tostring(err))
                    else
                        this:_applyResult(device, values)
                        anySuccess = true
                    end
                    step()
                end)
            end

            step()
        end,

        onError = function(err)
            this.state.online = false
            this.state.lastError = err
            plugin:log("warn", this.id .. " read cycle failed: " .. tostring(err))
            plugin:emit("modbus:error", { gateway = this.id, error = err })
        end
    })
end

--------------------------------------------------------------------------------
-- GATEWAY PUBLIC API
--------------------------------------------------------------------------------

function Gateway:isReady() return self.state.ready end
function Gateway:isOnline() return self.state.online end
function Gateway:getLastError() return self.state.lastError end
function Gateway:getLastUpdate() return self.state.lastUpdate end

--- Sensor object for expose(), e.g. gw:get("pralka_power")
function Gateway:get(id) return plugin:get(id) end

--- All decoded values for one device, e.g. gw:getDevice("pralka").values.power
function Gateway:getDevice(id)
    local ds = self.state.devices[id]
    if not ds then return nil end
    return { online = ds.online, lastError = ds.lastError, values = ds.values, paused = self.state.paused[id] == true }
end

--- Stop polling one device, e.g. a meter that loses power behind a contactor, so
-- each cycle does not wait for its timeouts. It reads offline until resume().
function Gateway:pause(id)
    if self.state.paused[id] then return true end
    for _, device in ipairs(self.devices) do
        if device.id == id then
            self.state.paused[id] = true
            -- Intentional, so no modbus:device_offline for alert listeners
            local ds = self.state.devices[id]
            ds.online = false
            ds.lastError = "paused"
            notify(self:_sensorId(device, "online"))
            plugin:updateObject("devices." .. id, { online = false, lastError = "paused" })
            return true
        end
    end
    return false
end

--- Poll a paused device again from the next cycle.
function Gateway:resume(id)
    if not self.state.paused[id] then return false end
    self.state.paused[id] = nil
    return true
end

function Gateway:isPaused(id) return self.state.paused[id] == true end

--- One decoded field, e.g. gw:getValue("pralka", "power")
function Gateway:getValue(deviceId, fieldId)
    local ds = self.state.devices[deviceId]
    if not ds then return nil end
    return ds.values[fieldId]
end

function Gateway:listDevices()
    local out = {}
    for _, device in ipairs(self.devices) do
        table.insert(out, {
            id = device.id, unit = device.unit,
            name = device.name, profile = device.profile
        })
    end
    return out
end

--- Raw read, for devices without a profile.
-- @param opts table unit, addr, qty, fc (defaults to FC04)
function Gateway:read(opts, callback)
    opts = opts or {}
    Modbus.request({
        host = self.host, port = self.port,
        unit = opts.unit or 1, fc = opts.fc or Modbus.READ_INPUT,
        addr = opts.addr or 0, qty = opts.qty or 2,
        timeout = opts.timeout or self.timeout
    }, callback)
end

--- Write a single holding register (FC06).
function Gateway:write(opts, callback)
    opts = opts or {}
    Modbus.writeSingle({
        host = self.host, port = self.port,
        unit = opts.unit or 1, addr = opts.addr or 0,
        value = opts.value or 0, timeout = opts.timeout or self.timeout
    }, callback)
end

function Gateway:refresh()
    if self.poller then self.poller:poll() end
end

function Gateway:start()
    if self.poller then self.poller:start() end
    return self
end

function Gateway:stop()
    if self.poller then self.poller:stop() end
    return self
end

function Gateway:getStats()
    local stats = Modbus.stats()
    if self.poller then stats.poller = self.poller:stats() end
    stats.gateway = self.id
    return stats
end

--------------------------------------------------------------------------------
-- FACTORY
--------------------------------------------------------------------------------

--- Create a gateway. Call once per physical RS485-to-Ethernet bridge.
-- @param opts table id, host, port, interval, timeout, devices, autostart
-- @return Gateway|nil, string|nil error
function plugin:create(opts)
    opts = opts or {}

    if not Modbus or not Modbus.isAvailable() then
        plugin:log("error", "Modbus backend not available - vCLU >= 1.1.0 required")
        return nil, "modbus backend not available"
    end

    local host = plugin:coerceString(opts.host, "")
    if host == "" then
        plugin:log("error", "create() needs a host (adres bramki, np. 192.168.0.9)")
        return nil, "host is required"
    end

    local id = plugin:coerceString(opts.id, "")
    if id == "" then id = "gw" .. tostring(#gateways + 1) end
    if byId[id] then
        plugin:log("error", "gateway id already taken: " .. id)
        return nil, "duplicate gateway id: " .. id
    end

    local gwSensor = gatewaySensorId(id)
    if sensorOwner[gwSensor] then
        plugin:log("error", string.format(
            "%s: sensor '%s' already registered by %s", id, gwSensor, sensorOwner[gwSensor]))
        return nil, "sensor id taken: " .. gwSensor
    end

    local self = {
        id = id,
        host = host,
        port = plugin:coerceNumber(opts.port, 502),
        timeout = plugin:coerceNumber(opts.timeout, 2000),
        interval = plugin:coerceNumber(opts.interval, 30),
        devices = {},
        poller = nil,
        state = { ready = false, online = false, lastUpdate = 0, lastError = nil, devices = {}, paused = {} }
    }
    for name, fn in pairs(Gateway) do self[name] = fn end

    if type(opts.devices) == "table" then
        for _, entry in ipairs(opts.devices) do
            local device = normalizeDevice(entry)
            if not device then
                plugin:log("warn", id .. ": skipping invalid device entry (need a unit address 1-247)")
            elseif ownerOfDevice[device.id] then
                plugin:log("error", string.format(
                    "%s: device id '%s' already used by gateway '%s', skipping",
                    id, device.id, ownerOfDevice[device.id]))
            else
                local ids = sensorIdsForDevice(device)
                local free, clash, owner = sensorIdsFree(ids)
                if not free then
                    plugin:log("error", string.format(
                        "%s: sensor '%s' already registered by %s, skipping device '%s'",
                        id, clash, owner, device.id))
                else
                    ownerOfDevice[device.id] = id
                    claimSensorIds(ids, "device " .. device.id .. " (gateway " .. id .. ")")
                    table.insert(self.devices, device)
                end
            end
        end
    end

    if #self.devices == 0 then
        plugin:log("error", id .. ": no devices given")
        return nil, "no devices"
    end

    plugin:logSafe("info", "Gateway created", {
        id = id, host = host, port = self.port,
        devices = #self.devices, interval = self.interval
    })

    plugin:upsertObject("gateways." .. id, {
        ready = false, online = false,
        host = host, port = self.port,
        deviceCount = #self.devices, lastUpdate = 0
    })

    claimSensorIds({ gwSensor }, "gateway " .. id)
    self:_registerSensors()
    self:_buildPoller()

    gateways[#gateways + 1] = self
    byId[id] = self

    if opts.autostart ~= false then self:start() end

    return self
end

--- Gateway by id, e.g. modbus:gateway("garaz")
function plugin:gateway(id) return byId[id] end

function plugin:getGateways() return gateways end

--- Profile names available to devices.
function plugin:listProfiles()
    local out = {}
    for name, profile in pairs(PROFILES) do
        out[name] = profile.label
    end
    return out
end

--------------------------------------------------------------------------------
-- LIFECYCLE
--------------------------------------------------------------------------------

plugin:onInit(function()
    if not Modbus or not Modbus.isAvailable() then
        plugin:log("error", "Modbus backend not available - vCLU >= 1.1.0 required")
        return
    end
    plugin:log("info", "Modbus gateway factory ready, declare gateways with create()")
end)

plugin:onCleanup(function()
    for _, gw in ipairs(gateways) do
        if gw.poller then gw.poller:stop() end
    end
    gateways = {}
    byId = {}
    ownerOfDevice = {}
    sensorOwner = {}
    plugin:log("info", "Modbus gateway plugin stopped")
end)

return plugin
