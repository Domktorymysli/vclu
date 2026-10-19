--- Tasmota devices over MQTT: a device is one line in a module.
-- The library creates objects bound to the device; expose stays in the module.
--
--   local tasmota = require("lib/tasmota")
--   local lamp = tasmota.switch("tasmota_75088A")            -- { relay = 2 } for POWER2
--   expose(lamp, "light", { path = "tasmota.biurko", name = "Lampka biurko", area = "Biuro" })
--
-- The topic is the device's Topic (Configuration -> MQTT). State comes from stat/<topic>/POWER<n>,
-- RESULT and tele/<topic>/STATE, sensors from tele/<topic>/SENSOR; commands go to cmnd/<topic>/...
-- A state from the device is never sent back as a command. The state is asked for on start, when
-- the MQTT client connects and when the device comes back (LWT Online). A change from the device
-- is recorded with the device as the actor (device:<topic>).
-- The library subscribes stat/<topic>/+ and tele/<topic>/+; MQTT keeps one callback per
-- pattern, so a module must not subscribe those patterns itself (subscribe e.g.
-- tele/<topic>/SENSOR). It needs the default client without a topic prefix.
-- @module lib.tasmota

local M = {}

-- per topic: { topic, online, sensors, listeners = { {power=, json=, sensor=} } }
local devices = {}
-- from tasmota/discovery/<mac>/config, by topic
local discovered = {}
local started = false

local function client() return MQTT end

local function onOff(v)
    if v == "ON" or v == "1" or v == 1 then return 1 end
    if v == "OFF" or v == "0" or v == 0 then return 0 end
    return nil -- TOGGLE, BLINK...
end

local function decode(payload)
    local ok, d = pcall(JSON.decode, payload)
    if ok and type(d) == "table" then return d end
    return nil
end

-- "ENERGY.Power" -> d.ENERGY.Power
local function field(d, path)
    for part in path:gmatch("[^%.]+") do
        if type(d) ~= "table" then return nil end
        d = d[part]
    end
    return d
end

local function ask(dev)
    local c = client()
    if not c then return end
    c:publish("cmnd/" .. dev.topic .. "/STATE", "")
    if dev.sensors then c:publish("cmnd/" .. dev.topic .. "/Status", "10") end
end

-- each listener's handler runs with the device as the actor
local function deliver(dev, kind, a, b)
    local cur = StateContext and StateContext.current()
    local ctx = { origin = "mqtt", actor = "device:" .. dev.topic,
        correlationId = cur and cur.correlationId }
    for _, l in ipairs(dev.listeners) do
        local fn = l[kind]
        if fn then
            local ok, err
            if StateContext then
                ok, err = pcall(StateContext.with, ctx, fn, a, b)
            else
                ok, err = pcall(fn, a, b)
            end
            if not ok and Logger then
                Logger:error("[tasmota] %s: %s", dev.topic, tostring(err))
            end
        end
    end
end

local function onMessage(dev, topic, payload)
    local prefix, name = topic:match("^(%a+)/[^/]+/([^/]+)$")
    if prefix == "stat" and name:match("^POWER%d*$") then
        deliver(dev, "power", name, payload)
    elseif (prefix == "stat" and name == "RESULT") or (prefix == "tele" and name == "STATE") then
        local d = decode(payload)
        if d then deliver(dev, "json", d) end
    elseif prefix == "tele" and name == "SENSOR" then
        local d = decode(payload)
        if d then deliver(dev, "sensor", d) end
    elseif prefix == "stat" and name == "STATUS10" then
        local d = decode(payload)
        if d and type(d.StatusSNS) == "table" then deliver(dev, "sensor", d.StatusSNS) end
    elseif prefix == "tele" and name == "LWT" then
        dev.online = payload == "Online"
        if dev.online then ask(dev) end
    end
end

local function start()
    if started then return end
    started = true
    local c = client()
    if not c then return end
    if c:getTopicPrefix() ~= "" and Logger then
        Logger:warn("[tasmota] MQTT has the topic prefix %q: Tasmota topics are stat/..., cmnd/..., "
            .. "they will not match", c:getTopicPrefix())
    end
    c:onConnect(function()
        for _, dev in pairs(devices) do ask(dev) end
    end)
    c:subscribe("tasmota/discovery/+/config", function(_, payload)
        local d = decode(payload)
        if not d or type(d.t) ~= "string" then return end
        local relays = 0
        for _, r in ipairs(type(d.rl) == "table" and d.rl or {}) do
            if r ~= 0 then relays = relays + 1 end
        end
        local names = type(d.fn) == "table" and d.fn or {}
        discovered[d.t] = { topic = d.t, name = names[1] or d.dn, device = d.dn,
            relays = relays, ip = d.ip, mac = d.mac }
    end)
end

-- the device record; subscribes once per topic (one callback per MQTT pattern)
local function device(topic)
    if type(topic) ~= "string" or topic == "" then
        error("tasmota: the device's Topic is needed (Configuration -> MQTT)", 3)
    end
    start()
    local dev = devices[topic]
    if dev then return dev end
    dev = { topic = topic, online = false, sensors = false, listeners = {} }
    devices[topic] = dev
    local c = client()
    if c then
        local handler = function(t, payload) onMessage(dev, t, payload) end
        c:subscribe("stat/" .. topic .. "/+", handler)
        c:subscribe("tele/" .. topic .. "/+", handler)
    end
    ask(dev)
    return dev
end

-- binds obj to a device: a value from the device is not sent back; any other change of obj,
-- also one made by a callback reacting to the device's value, goes to send(value)
local function bind(dev, obj, send)
    local b = {}
    obj:onChange(function(v)
        if b.applying ~= nil and v == b.applying then
            b.applying = nil
            return
        end
        send(v)
    end)
    local function apply(v)
        if v == nil or v == obj:getValue() then return end
        b.applying = v
        local ok, err = pcall(function() obj:updateState(v) end)
        b.applying = nil
        if not ok then error(err, 0) end
    end
    return apply
end

local function publish(topic, cmd, payload)
    local c = client()
    if c then c:publish("cmnd/" .. topic .. "/" .. cmd, payload) end
end

--- A relay as a VirtualSwitch (1 on, 0 off).
-- @api public
-- @param topic string The device's Topic, e.g. "tasmota_75088A"
-- @param opts table Optional: relay (1 = POWER, 2 = POWER2...), name (object name)
-- @return VirtualSwitch
function M.switch(topic, opts)
    opts = opts or {}
    local dev = device(topic)
    local key = (opts.relay and opts.relay > 1) and ("POWER" .. opts.relay) or "POWER"
    local sw = VirtualSwitch:new(opts.name or (topic .. "_" .. key))
    local apply = bind(dev, sw, function(v)
        publish(topic, key, (v == 1 or v == true) and "ON" or "OFF")
    end)
    -- a single relay reports POWER1 or POWER depending on SetOption26
    local alt = key == "POWER" and "POWER1" or nil
    dev.listeners[#dev.listeners + 1] = {
        power = function(name, payload)
            if name == key or name == alt then apply(onOff(payload)) end
        end,
        json = function(d) apply(onOff(d[key] or (alt and d[alt]))) end,
    }
    return sw
end

--- A dimmer (Tasmota Dimmer 0..100) as a VirtualDimmer (0..1; 0 = off).
-- @api public
-- @param topic string The device's Topic
-- @param opts table Optional: name (object name)
-- @return VirtualDimmer
function M.dimmer(topic, opts)
    opts = opts or {}
    local dev = device(topic)
    local dim = VirtualDimmer:new(opts.name or (topic .. "_dimmer"))
    local st = { power = nil, level = nil }
    local apply = bind(dev, dim, function(v)
        if not v or v <= 0 then
            publish(topic, "POWER", "OFF")
        else
            -- Dimmer turns the light on as well
            publish(topic, "Dimmer", tostring(math.max(1, math.floor(v * 100 + 0.5))))
        end
    end)
    local function update()
        -- switchOn after the device switched off restores the device's level
        if st.level and st.level > 0 then dim._lastBrightness = st.level / 100 end
        if st.power == 0 then apply(0)
        elseif st.power == 1 and st.level then apply(st.level / 100) end
    end
    dev.listeners[#dev.listeners + 1] = {
        power = function(name, payload)
            if name == "POWER" or name == "POWER1" then
                local p = onOff(payload)
                if p ~= nil then st.power = p; update() end
            end
        end,
        json = function(d)
            local p = onOff(d.POWER or d.POWER1)
            if p ~= nil then st.power = p end
            if type(d.Dimmer) == "number" then st.level = d.Dimmer end
            update()
        end,
    }
    return dim
end

--- A sensor value as a VirtualNumber, from tele/<topic>/SENSOR and the answer to Status 10.
-- @api public
-- @param topic string The device's Topic
-- @param path string Field in the SENSOR JSON, e.g. "ENERGY.Power" or "DS18B20.Temperature"
-- @param opts table Optional: unit, name (object name)
-- @return VirtualNumber
function M.sensor(topic, path, opts)
    opts = opts or {}
    if type(path) ~= "string" or path == "" then
        error("tasmota.sensor: a field of the SENSOR JSON is needed, e.g. \"ENERGY.Power\"", 2)
    end
    local dev = device(topic)
    local num = VirtualNumber:new(opts.name or (topic .. "_" .. path:gsub("%.", "_")), { unit = opts.unit })
    -- read-only: the value is the device's (expose it with readonly = true as well)
    num._allowedMethods = { "getValue" }
    local function refuse()
        if Logger then Logger:warn("[tasmota] %s %s is read-only, the value comes from the device", topic, path) end
    end
    num.setValue = refuse
    num.set = refuse
    local apply = bind(dev, num, function() end)
    dev.listeners[#dev.listeners + 1] = {
        sensor = function(d)
            local v = field(d, path)
            if type(v) == "number" then apply(v) end
        end,
    }
    local first = not dev.sensors
    dev.sensors = true
    if first then publish(topic, "Status", "10") end
    return num
end

--- Whether the device is online (its LWT is Online).
-- @api public
-- @param topic string The device's Topic
-- @return boolean
function M.online(topic)
    local dev = devices[topic]
    return dev ~= nil and dev.online
end

--- Tasmota devices seen on MQTT discovery (tasmota/discovery/<mac>/config), sorted by topic.
-- Nothing is created from them; add a device with a line in a module.
-- @api public
-- @return table list of { topic, name, device, relays, ip, mac }
function M.devices()
    start()
    local out = {}
    for _, d in pairs(discovered) do out[#out + 1] = d end
    table.sort(out, function(a, b) return a.topic < b.topic end)
    return out
end

return M
