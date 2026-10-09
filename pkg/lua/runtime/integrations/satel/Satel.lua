---
--- Satel - Satel Integra alarm integration module.
--- Wraps the Go __go_satel_* bridge: state reads hit the Go-side cache
--- (non-blocking), control commands are queued in Go (fire-and-forget,
--- results arrive as events), and pollEvents() drains panel events from
--- a 100ms timer set up by SetSatelManager / bootstrap.
-- @module Satel
---

local Satel = {}

-- Created objects: kind -> number -> object
Satel._objects = { zone = {}, partition = {}, partitionAlarm = {}, output = {}, temp = {} }
Satel._config = nil
Satel._connected = false
Satel.panelType = nil

local function decode(jsonStr)
    if not jsonStr or jsonStr == "" then return nil end
    return JSON:decode(jsonStr)
end

--- Whether the Go Satel bridge is available (integration enabled).
function Satel.isAvailable()
    return type(__go_satel_status) == "function"
end

--- Integration config from Go: zones/partitions/outputs/tempZones lists,
--- allowDisarm, armMode.
function Satel.getConfig()
    if Satel._config then return Satel._config end
    if type(__go_satel_config) ~= "function" then return nil end
    Satel._config = decode(__go_satel_config())
    return Satel._config
end

--- Whether the panel session is up.
function Satel.isConnected()
    if not Satel.isAvailable() then return false end
    local st = decode(__go_satel_status())
    return (st and st.connected) or false
end

-- ==========================================================================
-- State reads (Go-side cache, instant)
-- ==========================================================================

--- Zone state: { number, violated, tamper, alarm, bypass, name }
function Satel.getZone(n)
    if type(__go_satel_get_zone) ~= "function" then return nil end
    return decode(__go_satel_get_zone(n))
end

--- Partition state: { number, armed, alarm, fireAlarm, entryTime, exitTime, name }
function Satel.getPartition(n)
    if type(__go_satel_get_partition) ~= "function" then return nil end
    return decode(__go_satel_get_partition(n))
end

--- Output state: { number, active, name }
function Satel.getOutput(n)
    if type(__go_satel_get_output) ~= "function" then return nil end
    return decode(__go_satel_get_output(n))
end

--- Zone temperature in °C, or nil when no reading yet.
function Satel.getTemperature(n)
    if type(__go_satel_get_temp) ~= "function" then return nil end
    local t = __go_satel_get_temp(n) -- bridge returns no values when unknown
    return t
end

-- ==========================================================================
-- Control (async — queued in Go, result arrives as "satel.result" event)
-- ==========================================================================

local function control(fn, ...)
    if type(fn) ~= "function" then return false, "satel: not available" end
    return fn(...)
end

--- Arm a partition (mode 0-3, default from config). Safe operation.
function Satel.armPartition(n, mode)
    return control(__go_satel_arm, n, mode)
end

--- Force-arm a partition (mode 0-3).
function Satel.forceArm(n, mode)
    return control(__go_satel_force_arm, n, mode)
end

--- Disarm a partition. Blocked unless allowDisarm in config.
function Satel.disarmPartition(n)
    return control(__go_satel_disarm, n)
end

--- Clear a partition alarm. Blocked unless allowDisarm in config.
function Satel.clearAlarm(n)
    return control(__go_satel_clear_alarm, n)
end

--- Switch an output on/off.
function Satel.setOutput(n, on)
    return control(__go_satel_set_output, n, on and true or false)
end

-- ==========================================================================
-- Object accessors
-- ==========================================================================

function Satel.zone(n) return Satel._objects.zone[n] end
function Satel.partition(n) return Satel._objects.partition[n] end
function Satel.partitionAlarm(n) return Satel._objects.partitionAlarm[n] end
function Satel.output(n) return Satel._objects.output[n] end
function Satel.temp(n) return Satel._objects.temp[n] end

-- ==========================================================================
-- Setup: create objects from config ranges
-- ==========================================================================

--- Creates SatelZone/SatelOutput/SatelPartition/SatelThermometer objects for
--- all configured numbers and registers them in _registry (satel.zone.N, ...).
--- Idempotent — safe to call again after Reinitialize.
function Satel.setup()
    local cfg = Satel.getConfig()
    if not cfg then return end

    for _, n in ipairs(cfg.zones or {}) do
        if not Satel._objects.zone[n] then
            local obj = SatelZone:new(n)
            Satel._objects.zone[n] = obj
            if registerObject then registerObject("satel.zone." .. n, obj) end
        end
    end

    for _, n in ipairs(cfg.outputs or {}) do
        if not Satel._objects.output[n] then
            local obj = SatelOutput:new(n)
            Satel._objects.output[n] = obj
            if registerObject then registerObject("satel.output." .. n, obj) end
        end
    end

    for _, n in ipairs(cfg.partitions or {}) do
        if not Satel._objects.partition[n] then
            local obj = SatelPartition:new(n, nil, { armMode = cfg.armMode or 0 })
            Satel._objects.partition[n] = obj
            if registerObject then registerObject("satel.partition." .. n, obj) end

            -- Companion read-only alarm sensor
            local alarm = VirtualSensor:new("Satel Partition " .. n .. " Alarm")
            alarm._satel = true
            Satel._objects.partitionAlarm[n] = alarm
            if registerObject then registerObject("satel.partition." .. n .. ".alarm", alarm) end
        end
    end

    for _, n in ipairs(cfg.tempZones or {}) do
        if not Satel._objects.temp[n] then
            local obj = SatelThermometer:new(n)
            Satel._objects.temp[n] = obj
            if registerObject then registerObject("satel.temp." .. n, obj) end
        end
    end

    Logger:info("[satel] objects created: %d zones, %d partitions, %d outputs, %d thermometers",
        #(cfg.zones or {}), #(cfg.partitions or {}), #(cfg.outputs or {}), #(cfg.tempZones or {}))
end

-- ==========================================================================
-- Event polling (drained from a 100ms timer)
-- ==========================================================================

-- Panel events and refreshes are physical changes seen by the alarm panel
local SOURCE = { origin = "physical", actor = "device:satel" }

--- Updates obj from the panel; its handlers keep their own context.
local function apply(obj, method, ...)
    StateChange.applyFrom(obj, SOURCE, method, obj, ...)
end

--- Drains pending panel events (max 20 per tick to avoid blocking the loop).
function Satel.pollEvents()
    if type(__go_satel_poll_event) ~= "function" then return end
    for _ = 1, 20 do
        local evJson = __go_satel_poll_event()
        if not evJson then break end
        local ev = decode(evJson)
        if ev then
            local ok, err = pcall(Satel._dispatch, ev)
            if not ok then
                Logger:warn("[satel] event dispatch failed: %s", tostring(err))
            end
        end
    end
end

--- Re-reads all object states from the Go cache (after (re)connect).
function Satel._refreshAll()
    for n, obj in pairs(Satel._objects.zone) do
        local s = Satel.getZone(n)
        if s then apply(obj, obj._applyState, s) end
    end
    for n, obj in pairs(Satel._objects.partition) do
        local s = Satel.getPartition(n)
        if s then
            apply(obj, obj._applyState, s)
            local alarm = Satel._objects.partitionAlarm[n]
            if alarm then apply(alarm, alarm.updateState, s.alarm and 1 or 0) end
        end
    end
    for n, obj in pairs(Satel._objects.output) do
        local s = Satel.getOutput(n)
        if s then apply(obj, obj._applyState, s) end
    end
    for n, obj in pairs(Satel._objects.temp) do
        local t = Satel.getTemperature(n)
        if t then apply(obj, obj.updateState, t) end
    end
end

--- Applies a panel name to an object (unless the user renamed it).
local function applyName(obj, name)
    if not obj or obj._customName or not name or name == "" then return end
    local prev = obj._name
    obj._name = name
    if obj.isExposed and obj:isExposed() then
        local handle = obj:getExposeHandle()
        -- a name given in expose() or rename() wins over the panel's
        local cur = handle and handle.options and handle.options.name
        if handle and handle.update and (cur == nil or cur == handle.path or cur == prev) then
            handle:update({ name = name })
        end
    end
end

--- Dispatches one decoded panel event to objects and EventBus.
function Satel._dispatch(ev)
    local bus = EventBus:getShared()

    if ev.type == "connection" then
        Satel._connected = ev.connected or false
        if ev.panelType and ev.panelType ~= "" then Satel.panelType = ev.panelType end
        bus:emit("satel.connection", {
            connected = Satel._connected,
            panelType = Satel.panelType,
            message = ev.message,
        })
        if Satel._connected then
            Logger:info("[satel] connected to %s", tostring(Satel.panelType))
            Satel._refreshAll()
        end

    elseif ev.type == "zone" and ev.zone then
        local obj = Satel._objects.zone[ev.number]
        if obj then apply(obj, obj._applyState, ev.zone) end
        bus:emit("satel.zone." .. ev.number .. ".OnChange", ev.zone)
        bus:emit("satel.OnZoneChange", ev.zone)

    elseif ev.type == "partition" and ev.partition then
        local obj = Satel._objects.partition[ev.number]
        if obj then apply(obj, obj._applyState, ev.partition) end
        local alarm = Satel._objects.partitionAlarm[ev.number]
        if alarm then apply(alarm, alarm.updateState, ev.partition.alarm and 1 or 0) end
        bus:emit("satel.partition." .. ev.number .. ".OnChange", ev.partition)
        bus:emit("satel.OnPartitionChange", ev.partition)

    elseif ev.type == "output" and ev.output then
        local obj = Satel._objects.output[ev.number]
        if obj then apply(obj, obj._applyState, ev.output) end
        bus:emit("satel.output." .. ev.number .. ".OnChange", ev.output)

    elseif ev.type == "temperature" then
        local obj = Satel._objects.temp[ev.number]
        if obj and ev.temp then apply(obj, obj.updateState, ev.temp) end
        bus:emit("satel.temp." .. ev.number .. ".OnChange", { zone = ev.number, value = ev.temp })

    elseif ev.type == "name" then
        local kind = Satel._objects[ev.kind]
        applyName(kind and kind[ev.number], ev.message)

    elseif ev.type == "result" then
        bus:emit("satel.result", ev)
        if not ev.ok then
            Logger:warn("[satel] %s %s failed: %s", tostring(ev.op), tostring(ev.number), tostring(ev.message))
        end
    end
end

--- Status summary for dashboards / inspect().
function Satel.stats()
    local count = function(t) local c = 0 for _ in pairs(t) do c = c + 1 end return c end
    return {
        available = Satel.isAvailable(),
        connected = Satel.isConnected(),
        panelType = Satel.panelType,
        zones = count(Satel._objects.zone),
        partitions = count(Satel._objects.partition),
        outputs = count(Satel._objects.output),
        thermometers = count(Satel._objects.temp),
    }
end

return Satel
