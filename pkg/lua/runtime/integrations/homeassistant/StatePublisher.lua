--- Home Assistant State Publisher for vCLU.
-- Listens to StateBus and publishes state changes to MQTT.
-- Supports both legacy registry objects and ExposedObjects API.
-- @module HAStatePublisher

local HAStatePublisher = {}

-- Topic prefix (must match generator_ha.go)
HAStatePublisher.topicPrefix = "vclu"
HAStatePublisher.mqttInstance = nil
HAStatePublisher.enabled = false
HAStatePublisher.availabilityTopic = "vclu/status"  -- LWT topic

-- Use central TypeMap for type mappings
local TypeMap = require("TypeMap")

-- Readonly controllable expose types are remapped to a read-only HA component
-- (HA's switch/light/fan/... require a command_topic that a readonly object lacks).
-- Shared by onObjectExposed (discovery generation) and reconcile (stale cleanup).
local READONLY_REMAP = {
    switch = "binary_sensor", light = "binary_sensor",
    dimmer = "binary_sensor", fan = "binary_sensor", number = "sensor",
}

-- Effective readonly for an exposed object: the expose() option OR the "Readonly"
-- access level set per object in the dashboard. Honouring only the former let a
-- UI-readonly object be published as a controllable component with a command_topic.
local function isReadonly(path, options)
    if options and options.readonly then
        return true
    end
    if path and MqttAccess and MqttAccess.isReadonly then
        return MqttAccess.isReadonly(path)
    end
    return false
end

-- The HA discovery component an exposed handle currently uses (incl. readonly remap).
local function exposedComponent(exposedType, options, path)
    local component = TypeMap.getHAComponent(exposedType, "sensor")
    if isReadonly(path, options) and READONLY_REMAP[exposedType] then
        return READONLY_REMAP[exposedType]
    end
    return component
end

-- uniqueId derived from an object path (same rule as discovery generation).
local function pathToUniqueId(path)
    local base, id = path:match("^([^:]+):(.+)$")
    if not base or not id then
        base = "vclu"
        id = path
    end
    return string.lower(base .. "_" .. id):gsub("[^%w]", "_")
end

-- Clear ALL retained HA discovery configs for a path (empty retained payload across
-- every component). Used to remove an object from HA: on unexpose, and for objects
-- that are still exposed but became Hidden via AccessControl.
local function clearDiscovery(path)
    if not HAStatePublisher.mqttInstance then return end
    local uniqueId = pathToUniqueId(path)
    for _, component in ipairs(TypeMap.getHAComponents()) do
        HAStatePublisher.mqttInstance:publish(
            string.format("homeassistant/%s/%s/config", component, uniqueId), "", 0, true)
    end
end

-- A Grenton type that is a dimmer for HA (DIMMER, LED_CHANNEL): value 0..1, brightness
local function isDimmer(objType)
    return TypeMap.getExposeType(objType) == "dimmer"
end

-- The colour mode of an exposed light whose object has a colour (RemoteRGBLight): rgbw on
-- an RGBW module, rgb otherwise; nil for a plain light
local function colorMode(handle)
    local o = handle and handle.object
    if handle.type ~= "light" or type(o) ~= "table" or type(o.setColor) ~= "function" then return nil end
    return o._hasWhite and "rgbw" or "rgb"
end

-- schema json: the state, the brightness (0..100) and the colour in one payload
local function formatColorState(handle, value, mode)
    local o = handle.object
    local brightness = math.floor((tonumber(value) or 0) * 100 + 0.5)
    local parts = { string.format('"state":"%s","brightness":%d,"color_mode":"%s"',
        brightness > 0 and "ON" or "OFF", brightness, mode) }
    local r, g, b = o:getColor()
    if r then
        if mode == "rgbw" then
            parts[#parts + 1] = string.format('"color":{"r":%d,"g":%d,"b":%d,"w":%d}', r, g, b, o._white or 0)
        else
            parts[#parts + 1] = string.format('"color":{"r":%d,"g":%d,"b":%d}', r, g, b)
        end
    end
    return "{" .. table.concat(parts, ",") .. "}"
end

-- Format state value for MQTT based on type
local function formatState(value, objType)
    if objType == "DOUT" or objType == "DIN" then
        -- Binary: 0/1 -> OFF/ON
        if value == 1 or value == true or value == "1" then
            return "ON"
        else
            return "OFF"
        end

    elseif isDimmer(objType) then
        -- Dimmer: 0-1 float -> OFF/ON
        local numVal = tonumber(value) or 0
        if numVal > 0 then
            return "ON"
        else
            return "OFF"
        end

    elseif objType == "LEDRGB" then
        -- RGB Light: JSON format
        local numVal = tonumber(value) or 0
        local state = numVal > 0 and "ON" or "OFF"
        -- the colour is not reported (only Value is subscribed): HA keeps the last one
        return string.format(
            '{"state":"%s","brightness":%d}',
            state,
            math.floor(numVal * 100 + 0.5)
        )

    elseif objType == "ROLLER" or objType == "ROLLER_SH" then
        -- Cover: position to state
        local numVal = tonumber(value) or 0
        if numVal >= 100 then
            return "open"
        elseif numVal <= 0 then
            return "closed"
        else
            return "open"  -- partially open
        end

    else
        -- Default: return as string
        return tostring(value)
    end
end

-- Build MQTT topic from registry path
local function buildTopic(path, objType)
    -- path format: "CLU212345678.DOU1234"
    local cluId, objId = path:match("^([^.]+)%.(.+)$")
    if not cluId or not objId then
        return nil
    end

    local component = TypeMap.getHAComponentFromGreton(objType, "sensor")

    -- Topic: vclu/{clu_id}/{component}/{obj_id}/state
    return string.format(
        "%s/%s/%s/%s/state",
        HAStatePublisher.topicPrefix,
        string.lower(cluId),
        component,
        string.lower(objId)
    )
end

-- Build brightness topic for dimmers
local function buildBrightnessTopic(path, objType)
    if not isDimmer(objType) then
        return nil
    end

    local cluId, objId = path:match("^([^.]+)%.(.+)$")
    if not cluId or not objId then
        return nil
    end

    return string.format(
        "%s/%s/light/%s/brightness",
        HAStatePublisher.topicPrefix,
        string.lower(cluId),
        string.lower(objId)
    )
end

-- Build position topic for covers
local function buildPositionTopic(path, objType)
    if objType ~= "ROLLER" then  -- ROLLER_SH doesn't have position
        return nil
    end

    local cluId, objId = path:match("^([^.]+)%.(.+)$")
    if not cluId or not objId then
        return nil
    end

    return string.format(
        "%s/%s/cover/%s/position",
        HAStatePublisher.topicPrefix,
        string.lower(cluId),
        string.lower(objId)
    )
end

-- ==========================================================================
-- ExposedObjects support
-- ==========================================================================

-- Format state value for MQTT based on exposed type
local function formatExposedState(value, exposedType)
    if exposedType == "switch" or exposedType == "sensor" or exposedType == "motion" then
        -- Binary: 0/1/true/false -> OFF/ON
        if value == 1 or value == true or value == "1" or value == "ON" or value == "on" then
            return "ON"
        else
            return "OFF"
        end

    elseif exposedType == "light" or exposedType == "dimmer" then
        local numVal = tonumber(value) or 0
        if numVal > 0 then
            return "ON"
        else
            return "OFF"
        end

    elseif exposedType == "scene" then
        -- Scene is always OFF (stateless)
        return "OFF"

    elseif exposedType == "cover" then
        -- value is the position (see exposedCoverPosition)
        return (tonumber(value) or 0) > 0 and "open" or "closed"

    elseif exposedType == "lock" then
        -- Lock: 0/1 -> UNLOCKED/LOCKED
        if value == 1 or value == true or value == "1" or value == "LOCKED" then
            return "LOCKED"
        else
            return "UNLOCKED"
        end

    elseif exposedType == "fan" then
        -- Fan: 0 = OFF, >0 = ON
        local numVal = tonumber(value) or 0
        if numVal > 0 then
            return "ON"
        else
            return "OFF"
        end

    elseif exposedType == "garage_door" then
        -- Garage: 0/1 -> closed/open
        if value == 1 or value == true or value == "1" or value == "open" then
            return "open"
        else
            return "closed"
        end

    elseif exposedType == "climate" then
        -- Climate returns current temperature as number
        return tostring(value)

    elseif exposedType == "temperature" or exposedType == "humidity" or exposedType == "illuminance" or exposedType == "number" then
        return tostring(value)

    else
        return tostring(value)
    end
end

-- Build MQTT topic for ExposedObject
local function buildExposedTopic(path, exposedType)
    -- Path format: "plugin:name" or "vclu:hash"
    local base, id = path:match("^([^:]+):(.+)$")
    if not base or not id then
        -- Fallback: use whole path
        base = "vclu"
        id = path
    end

    local component = TypeMap.getHAComponent(exposedType, "sensor")

    -- Topic: vclu/exposed/{base}/{component}/{id}/state
    return string.format(
        "%s/exposed/%s/%s/%s/state",
        HAStatePublisher.topicPrefix,
        string.lower(base):gsub("[^%w]", "_"),
        component,
        string.lower(id):gsub("[^%w]", "_")
    )
end

-- Build brightness topic for exposed dimmer/light
local function buildExposedBrightnessTopic(path, exposedType)
    if exposedType ~= "dimmer" and exposedType ~= "light" then
        return nil
    end

    local base, id = path:match("^([^:]+):(.+)$")
    if not base or not id then
        base = "vclu"
        id = path
    end

    return string.format(
        "%s/exposed/%s/light/%s/brightness",
        HAStatePublisher.topicPrefix,
        string.lower(base):gsub("[^%w]", "_"),
        string.lower(id):gsub("[^%w]", "_")
    )
end

-- Build position topic for exposed covers
local function buildExposedPositionTopic(path, exposedType)
    if exposedType ~= "cover" then
        return nil
    end

    local base, id = path:match("^([^:]+):(.+)$")
    if not base or not id then
        base = "vclu"
        id = path
    end

    return string.format(
        "%s/exposed/%s/cover/%s/position",
        HAStatePublisher.topicPrefix,
        string.lower(base):gsub("[^%w]", "_"),
        string.lower(id):gsub("[^%w]", "_")
    )
end

--- Whether an exposed cover has a position. A Grenton roller without one (class 11, or a
-- proxy not regenerated) only moves up and down; other covers count their value as one.
-- @param handle ExposedHandle
-- @return boolean
function HAStatePublisher.coverHasPosition(handle)
    local obj = handle.object
    return not (type(obj) == "table" and obj._hasPosition == false)
end

--- Position of an exposed cover (0 = closed .. 100 = open, the same in vCLU and HA).
-- Read from getPosition() when the object has it, else from its value.
-- @param handle ExposedHandle
-- @return number|nil Position, nil when the object reports none (a roller without one)
function HAStatePublisher.exposedCoverPosition(handle)
    local obj = handle.object
    local position
    if type(obj) == "table" and type(obj.getPosition) == "function" then
        local ok, p = pcall(obj.getPosition, obj)
        if ok then position = p end
    else
        position = handle:getValue()
    end
    return tonumber(position)
end

--- State of an exposed cover for Home Assistant. A Grenton roller reports its movement
-- (getState: 1 up, 2 down), shown as opening/closing; otherwise the position decides, and
-- a roller without a position is stopped.
-- @param handle ExposedHandle
-- @param position number|nil Position (exposedCoverPosition)
-- @return string|nil open, closed, opening, closing or stopped; nil when nothing is known
function HAStatePublisher.exposedCoverState(handle, position)
    local obj = handle.object
    local movement
    if type(obj) == "table" and type(obj.getState) == "function" then
        local ok, m = pcall(obj.getState, obj)
        if ok then movement = tonumber(m) end
    end
    if movement == 1 then return "opening" end
    if movement == 2 then return "closing" end
    if position then return position > 0 and "open" or "closed" end
    if movement then return "stopped" end
    return nil
end

--- A position from Home Assistant (0 = closed .. 100 = open), clamped and rounded.
-- @param payload string|number Position from HA
-- @return number|nil Position 0..100, nil when not a number
function HAStatePublisher.coverPositionFromHA(payload)
    local n = tonumber(payload)
    if not n then return nil end
    return math.floor(math.max(0, math.min(100, n)) + 0.5)
end

-- Handle state change for ExposedObject
local function publishExposedState(handle)
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end

    -- Check if MQTT is enabled for this handle
    if handle.options.mqtt == false then
        return
    end

    -- Check AccessControl unless overridden
    if not handle.options.hidden and not handle.options.readonly then
        if MqttAccess and MqttAccess.isHidden and MqttAccess.isHidden(handle.path) then
            return
        end
    end

    local value = handle:getValue()
    local coverState
    if handle.type == "cover" then
        value = HAStatePublisher.exposedCoverPosition(handle)
        coverState = HAStatePublisher.exposedCoverState(handle, value)
    end
    -- Never publish a nil reading. HA rejects "nil" for numeric sensors
    -- (temperature/humidity/number) and there is nothing meaningful to report -
    -- the entity simply stays unavailable until a real value arrives.
    if value == nil and coverState == nil then
        return
    end

    local topic = buildExposedTopic(handle.path, handle.type)
    local mode = colorMode(handle)
    local payload = coverState or (mode and formatColorState(handle, value, mode))
        or formatExposedState(value, handle.type)

    local ok, err = HAStatePublisher.mqttInstance:publish(topic, payload, 0, true)
    if ok then
        if Logger then
            Logger:getShared():debug("[HA Publisher] Exposed: %s -> %s", topic, payload)
        end
    else
        if Logger then
            Logger:getShared():error("[HA Publisher] Exposed publish failed: %s", tostring(err))
        end
    end

    -- Publish brightness for dimmers/lights (a colour light has it in the JSON state)
    if not mode and ExposedObjects.hasBrightness(handle) then
        local brightnessTopic = buildExposedBrightnessTopic(handle.path, handle.type)
        if brightnessTopic then
            local numVal = tonumber(value) or 0
            local brightness = tostring(math.floor(numVal * 100 + 0.5))
            HAStatePublisher.mqttInstance:publish(brightnessTopic, brightness, 0, true)
        end
    end

    -- Publish position for covers that have one
    if handle.type == "cover" and value ~= nil then
        local positionTopic = buildExposedPositionTopic(handle.path, handle.type)
        if positionTopic then
            HAStatePublisher.mqttInstance:publish(positionTopic, tostring(value), 0, true)
        end
    end
end

-- Handle state change event
local function onStateChanged(data)
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end

    local path = data.path
    local value = data.value
    local objType = data.type

    -- Check if this is an ExposedObject
    if ExposedObjects then
        local handle = ExposedObjects:get(path)
        if handle then
            publishExposedState(handle)
            return  -- Handled by ExposedObjects
        end
    end

    -- Legacy registry path handling below

    -- Check MQTT access - skip if hidden
    if MqttAccess and MqttAccess.isHidden and MqttAccess.isHidden(path) then
        if Logger then
            Logger:getShared():debug("[HA Publisher] Skipping hidden object: %s", path)
        end
        return
    end

    -- Build main state topic
    local topic = buildTopic(path, objType)
    if not topic then
        if Logger then
            Logger:getShared():debug("[HA Publisher] Could not build topic for: " .. tostring(path))
        end
        return
    end

    -- A cover's value is its position (0 = closed). ROLLER_SH has no position: its value is not one.
    local position
    if objType == "ROLLER" then
        local obj = _registry and _registry[path]
        if not (obj and obj._hasPosition == false) then
            position = tonumber(value)
        end
    end

    -- Format and publish state
    local payload = formatState(value, objType)
    if position then
        payload = position > 0 and "open" or "closed"
    end

    local ok, err = HAStatePublisher.mqttInstance:publish(topic, payload, 0, true)  -- qos=0, retain=true
    if ok then
        if Logger then
            Logger:getShared():debug("[HA Publisher] Published: %s -> %s", topic, payload)
        end
    else
        if Logger then
            Logger:getShared():error("[HA Publisher] Publish failed: %s", tostring(err))
        end
    end

    -- Publish brightness for dimmers
    if isDimmer(objType) then
        local brightnessTopic = buildBrightnessTopic(path, objType)
        if brightnessTopic then
            local numVal = tonumber(value) or 0
            local brightness = tostring(math.floor(numVal * 100 + 0.5))
            HAStatePublisher.mqttInstance:publish(brightnessTopic, brightness, 0, true)
        end
    end

    -- Publish position for covers
    if position then
        local positionTopic = buildPositionTopic(path, objType)
        if positionTopic then
            HAStatePublisher.mqttInstance:publish(positionTopic, tostring(position), 0, true)
        end
    end
end

-- Handle new ExposedObject (publish discovery)
local function onObjectExposed(data)
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end

    local path = data.path
    local exposedType = data.type
    local name = data.name or path
    local options = data.options or {}

    -- Skip if MQTT disabled for this object
    if options.mqtt == false then
        return
    end

    -- AccessControl: a Hidden object must not be in HA. Actively CLEAR its discovery
    -- (it may have been published earlier, before it was hidden, and would otherwise
    -- linger as a retained config - even for manually expose()'d objects).
    if MqttAccess and MqttAccess.isHidden and MqttAccess.isHidden(path) then
        if Logger then
            Logger:getShared():debug("[HA Publisher] Hidden object, clearing discovery: %s", path)
        end
        clearDiscovery(path)
        return
    end

    if Logger then
        Logger:getShared():info("[HA Publisher] Object exposed: %s (%s)", path, exposedType)
    end

    -- Publish discovery message for Home Assistant
    local base, id = path:match("^([^:]+):(.+)$")
    if not base or not id then
        base = "vclu"
        id = path
    end

    local uniqueId = string.lower(base .. "_" .. id):gsub("[^%w]", "_")

    -- HA's controllable components (switch/light/fan/...) REQUIRE a command_topic.
    -- A readonly object has none, so HA rejects the discovery outright. exposedComponent()
    -- remaps such readonly objects to a read-only component (binary_sensor/sensor); the
    -- state topic and payload keep using exposedType, so nothing else changes.
    -- (lock/cover/garage_door use non-ON/OFF payloads and stay as-is.)
    local component = exposedComponent(exposedType, options, path)
    local readonlyRemapped = (component ~= TypeMap.getHAComponent(exposedType, "sensor"))

    -- Discovery topic: homeassistant/{component}/vclu_{uniqueId}/config
    local discoveryTopic = string.format(
        "homeassistant/%s/%s/config",
        component,
        uniqueId
    )

    -- Build discovery payload
    local stateTopic = buildExposedTopic(path, exposedType)

    -- Build device info. HA assigns areas per-device, not per-entity, so to get
    -- each object into its room we group entities into a device PER ROOM and set
    -- device.suggested_area. Without a room they fall under the main "Virtual CLU"
    -- device. Per-room devices link to it via via_device for a clean hierarchy.
    local area = options.area or options.room
    if area == "" then area = nil end

    local deviceId = "vclu"
    local deviceName = "Virtual CLU"
    local device = {
        identifiers = { deviceId },
        name = deviceName,
        model = "Virtual CLU",
        manufacturer = "Grenton"
    }
    if area then
        device.identifiers = { "vclu_" .. string.lower(area):gsub("[^%w]", "_") }
        device.name = "vCLU " .. area
        device.suggested_area = area
        device.via_device = "vclu"
    end

    local discoveryPayload = {
        name = name,
        unique_id = uniqueId,
        object_id = uniqueId,  -- Stable object ID
        state_topic = stateTopic,
        availability_topic = HAStatePublisher.availabilityTopic,
        payload_available = "online",
        payload_not_available = "offline",
        device = device
    }

    -- Add command topic for writable objects
    local caps = ExposedObjects and ExposedObjects:getCapabilities(exposedType)
    if caps and caps.write and not isReadonly(path, options) then
        discoveryPayload.command_topic = stateTopic:gsub("/state$", "/set")
    end

    -- Add brightness for dimmers/lights (not when remapped to a read-only component)
    local handle = ExposedObjects and ExposedObjects:get(path)
    local mode = not readonlyRemapped and handle and colorMode(handle)
    if mode then
        -- a colour light: state, brightness and colour travel in one JSON (Commands.lightJSON)
        discoveryPayload.schema = "json"
        discoveryPayload.brightness = true
        discoveryPayload.brightness_scale = 100
        discoveryPayload.supported_color_modes = { mode }
        local obj = handle.object
        if obj.onEvent and not obj._haColorHook then
            obj._haColorHook = true
            obj:onEvent("OnColorChange", function()
                local h = ExposedObjects:get(StateChange.pathOf(obj))
                if h and h.type == "light" then publishExposedState(h) end
            end)
        end
        -- drop a brightness retained by an earlier version, which announced a plain dimmer
        local brightnessTopic = buildExposedBrightnessTopic(path, exposedType)
        if brightnessTopic then
            HAStatePublisher.mqttInstance:publish(brightnessTopic, "", 0, true)
        end
    elseif not readonlyRemapped and handle and ExposedObjects.hasBrightness(handle) then
        local brightnessTopic = buildExposedBrightnessTopic(path, exposedType)
        discoveryPayload.brightness_state_topic = brightnessTopic
        discoveryPayload.brightness_command_topic = brightnessTopic .. "/set"
        discoveryPayload.brightness_scale = 100
    elseif handle and (exposedType == "light" or exposedType == "dimmer") and not ExposedObjects.hasBrightness(handle) then
        -- on/off light: drop a brightness value retained by older versions
        local brightnessTopic = buildExposedBrightnessTopic(path, exposedType)
        if brightnessTopic then
            HAStatePublisher.mqttInstance:publish(brightnessTopic, "", 0, true)
        end
    end

    -- Add position for covers that have one
    if exposedType == "cover" then
        -- A roller's movement is not its value: publish when it changes (once per object)
        local obj = handle and handle.object
        if type(obj) == "table" and obj.onEvent and not obj._haMovementHook then
            obj._haMovementHook = true
            obj:onEvent("OnMovementChange", function()
                local h = ExposedObjects:get(StateChange.pathOf(obj))
                if h and h.type == "cover" then publishExposedState(h) end
            end)
        end
        local positionTopic = buildExposedPositionTopic(path, exposedType)
        if handle and not HAStatePublisher.coverHasPosition(handle) then
            -- drop a position retained by older versions (the movement, 0-2)
            HAStatePublisher.mqttInstance:publish(positionTopic, "", 0, true)
        else
            discoveryPayload.position_topic = positionTopic
            discoveryPayload.set_position_topic = positionTopic .. "/set"
            discoveryPayload.position_open = 100
            discoveryPayload.position_closed = 0
        end
    end

    -- Add unit for sensors
    if exposedType == "temperature" then
        discoveryPayload.unit_of_measurement = options.unit or "°C"
        discoveryPayload.device_class = "temperature"
    elseif exposedType == "humidity" then
        discoveryPayload.unit_of_measurement = options.unit or "%"
        discoveryPayload.device_class = "humidity"
    elseif exposedType == "illuminance" then
        discoveryPayload.unit_of_measurement = options.unit or "lx"
        discoveryPayload.device_class = "illuminance"
    end

    -- Add min/max/step for number type (not when remapped to a read-only sensor)
    if not readonlyRemapped and exposedType == "number" then
        if options.min then discoveryPayload.min = options.min end
        if options.max then discoveryPayload.max = options.max end
        if options.step then discoveryPayload.step = options.step end
        if options.unit then discoveryPayload.unit_of_measurement = options.unit end
    elseif readonlyRemapped and exposedType == "number" and options.unit then
        -- Readonly number is shown as a plain sensor; keep its unit if any.
        discoveryPayload.unit_of_measurement = options.unit
    end

    -- Lock type
    if exposedType == "lock" then
        discoveryPayload.payload_lock = "LOCK"
        discoveryPayload.payload_unlock = "UNLOCK"
        discoveryPayload.state_locked = "LOCKED"
        discoveryPayload.state_unlocked = "UNLOCKED"
    end

    -- Fan type with speed (not when remapped to a read-only binary_sensor)
    if not readonlyRemapped and exposedType == "fan" then
        discoveryPayload.payload_on = "ON"
        discoveryPayload.payload_off = "OFF"
        -- Speed as percentage
        local speedTopic = buildExposedBrightnessTopic(path, exposedType)  -- reuse brightness topic builder
        if speedTopic then
            discoveryPayload.percentage_state_topic = speedTopic
            discoveryPayload.percentage_command_topic = speedTopic .. "/set"
            discoveryPayload.speed_range_min = options.min or 1
            discoveryPayload.speed_range_max = options.max or 100
        end
    end

    -- Climate type (thermostat)
    if exposedType == "climate" then
        local baseTopic = stateTopic:gsub("/state$", "")
        discoveryPayload.temperature_unit = options.unit or "C"
        discoveryPayload.temp_step = options.step or 0.5
        discoveryPayload.min_temp = options.min or 5
        discoveryPayload.max_temp = options.max or 35
        -- Temperature topics
        discoveryPayload.current_temperature_topic = baseTopic .. "/current_temp"
        discoveryPayload.temperature_state_topic = baseTopic .. "/target_temp"
        discoveryPayload.temperature_command_topic = baseTopic .. "/target_temp/set"
        -- Mode topics (optional)
        discoveryPayload.modes = {"off", "heat", "cool", "auto"}
        discoveryPayload.mode_state_topic = baseTopic .. "/mode"
        discoveryPayload.mode_command_topic = baseTopic .. "/mode/set"
    end

    -- Garage door (cover with device_class)
    if exposedType == "garage_door" then
        discoveryPayload.device_class = "garage"
        discoveryPayload.payload_open = "OPEN"
        discoveryPayload.payload_close = "CLOSE"
        discoveryPayload.payload_stop = "STOP"
        discoveryPayload.state_open = "open"
        discoveryPayload.state_closed = "closed"
        discoveryPayload.state_opening = "opening"
        discoveryPayload.state_closing = "closing"
    end

    -- (Area is handled via the per-room device.suggested_area above; HA ignores a
    -- top-level entity suggested_area, so it is intentionally not set here.)

    -- Passthrough for sensor metadata the type map does not set on its own.
    -- Lets an expose() opt into HA device/state classes, e.g. an energy meter:
    --   expose(o, "number", { readonly = true, unit = "kWh",
    --     device_class = "energy", state_class = "total_increasing" })
    -- The HA Energy dashboard needs both, and no built-in expose type sets them.
    if options.device_class then
        discoveryPayload.device_class = options.device_class
    end
    if options.state_class then
        discoveryPayload.state_class = options.state_class
    end

    -- Encode to JSON
    local payload = ""
    if JSON and JSON.encode then
        payload = JSON.encode(discoveryPayload)
    else
        -- Fallback: simple JSON encoding
        local parts = {}
        for k, v in pairs(discoveryPayload) do
            if type(v) == "string" then
                table.insert(parts, string.format('"%s":"%s"', k, v:gsub('"', '\\"')))
            elseif type(v) == "number" then
                table.insert(parts, string.format('"%s":%d', k, v))
            elseif type(v) == "table" then
                -- Skip nested for now
            end
        end
        payload = "{" .. table.concat(parts, ",") .. "}"
    end

    -- Publish discovery
    HAStatePublisher.mqttInstance:publish(discoveryTopic, payload, 0, true)

    -- Also publish initial state
    if ExposedObjects then
        local handle = ExposedObjects:get(path)
        if handle then
            publishExposedState(handle)
        end
    end
end

-- Handle ExposedObject removal (remove discovery)
local function onObjectUnexposed(data)
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end

    local path = data.path

    if Logger then
        Logger:getShared():info("[HA Publisher] Object unexposed: %s", path)
    end

    -- To remove from HA, publish empty payload to discovery topic
    local base, id = path:match("^([^:]+):(.+)$")
    if not base or not id then
        base = "vclu"
        id = path
    end

    local uniqueId = string.lower(base .. "_" .. id):gsub("[^%w]", "_")

    -- Try all possible component types
    for _, component in ipairs(TypeMap.getHAComponents()) do
        local discoveryTopic = string.format(
            "homeassistant/%s/%s/config",
            component,
            uniqueId
        )
        -- Empty payload removes the entity
        HAStatePublisher.mqttInstance:publish(discoveryTopic, "", 0, true)
    end
end

-- Handle object_updated event (republish discovery with new name/options)
local function onObjectUpdated(data)
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end

    local path = data.path
    if not path then return end

    -- Get the handle from ExposedObjects
    if not ExposedObjects then return end
    local handle = ExposedObjects:get(path)
    if not handle or not handle:isExposed() then return end

    if Logger then
        Logger:getShared():info("[HA Publisher] Object updated: %s (republishing discovery)", path)
    end

    -- Republish discovery with updated info
    onObjectExposed({
        path = path,
        type = handle.type,
        name = handle.options.name,
        options = handle.options,
    })
end

-- Reconcile HA discovery against the registry. For every object in the registry
-- that is NOT currently exposed, clear any stale retained discovery config it may
-- have left on the broker (empty retained payload, across all HA components). This
-- removes orphans from a previous run - e.g. a full-registry expose before modules/
-- restricted the set, or objects later set to Hidden in AccessControl.
--
-- Publish-only by design: it never calls expose()/unexpose(), never mutates
-- ExposedObjects, identity locks or StateBus, and never reads from the broker
-- (broker-side retained delivery proved unreliable under startup load). It only
-- publishes empty retained payloads - the same delete mechanism as onObjectUnexposed.
--
-- Safety: it only ever clears the discovery topics of objects that are in OUR
-- registry and NOT exposed by us. Exposed objects (and other integrations'
-- entities, which are not in our registry) are never touched.
function HAStatePublisher.reconcile()
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end

    local registry = _registry
    if type(registry) ~= "table" or type(registry.list) ~= "function" then
        if Logger then
            Logger:getShared():warn("[HA Publisher] reconcile skipped: registry unavailable")
        end
        return
    end

    local components = TypeMap.getHAComponents()
    local cleared = 0

    for _, info in ipairs(registry:list()) do
        local path = info.path

        -- Same uniqueId derivation as onObjectExposed.
        local base, id = path:match("^([^:]+):(.+)$")
        if not base or not id then
            base = "vclu"
            id = path
        end
        local uniqueId = string.lower(base .. "_" .. id):gsub("[^%w]", "_")

        -- Keep the component the object currently uses ONLY if it is exposed AND not
        -- Hidden via AccessControl; clear all the others (removes stale configs from a
        -- type change or readonly remap). Non-exposed OR Hidden objects (even manually
        -- expose()'d) keep keepComponent = nil, so every component is cleared - they are
        -- fully removed from HA.
        local keepComponent = nil
        local handle = ExposedObjects and ExposedObjects:get(path)
        local hidden = MqttAccess and MqttAccess.isHidden and MqttAccess.isHidden(path)
        if handle and not hidden then
            keepComponent = exposedComponent(handle.type, handle.options, path)
        end

        for _, component in ipairs(components) do
            if component ~= keepComponent then
                -- Empty payload to a non-existent retained topic is a harmless no-op.
                HAStatePublisher.mqttInstance:publish(
                    string.format("homeassistant/%s/%s/config", component, uniqueId),
                    "", 0, true)
            end
        end
        if keepComponent == nil then
            cleared = cleared + 1
        end
    end

    if Logger then
        Logger:getShared():info("[HA Publisher] reconcile: cleared stale discovery for %d non-exposed registry object(s) (+ stale components of exposed)", cleared)
    end
end

-- Re-evaluate HA discovery for every currently-exposed object against the CURRENT
-- AccessControl state, then reconcile. Re-runs onObjectExposed (which now clears
-- discovery for Hidden objects and publishes for visible ones), so changing an
-- object's access (e.g. to Hidden) in the panel applies live - without a restart.
function HAStatePublisher.refresh()
    if not HAStatePublisher.enabled or not HAStatePublisher.mqttInstance then
        return
    end
    if ExposedObjects then
        for path, handle in pairs(ExposedObjects:list()) do
            onObjectExposed({
                path = path,
                type = handle.type,
                name = handle.options.name,
                options = handle.options,
            })
        end
    end
    HAStatePublisher.reconcile()
    if Logger then
        Logger:getShared():info("[HA Publisher] refresh: re-evaluated discovery against current access")
    end
end

-- Setup state publisher
-- @param mqttInstance MQTT instance to use
-- @param options table Optional settings:
--   - autoExposeRegistry: boolean (default true) - auto-expose _registry objects
-- @return boolean Success
function HAStatePublisher.setup(mqttInstance, options)
    if not mqttInstance then
        if Logger then
            Logger:getShared():error("[HA Publisher] No MQTT instance provided")
        end
        return false
    end

    options = options or {}

    HAStatePublisher.mqttInstance = mqttInstance

    -- Subscribe to StateBus
    if StateBus and StateBus:getShared() then
        local bus = StateBus:getShared()

        -- State changes (both legacy and ExposedObjects)
        bus:on("state_changed", onStateChanged)

        -- ExposedObjects events
        bus:on("object_exposed", onObjectExposed)
        bus:on("object_unexposed", onObjectUnexposed)
        bus:on("object_updated", onObjectUpdated)

        HAStatePublisher.enabled = true

        if Logger then
            Logger:getShared():info("[HA Publisher] State publisher enabled, listening to StateBus (with ExposedObjects support)")
        end

        -- Publish availability "online"
        HAStatePublisher.mqttInstance:publish(HAStatePublisher.availabilityTopic, "online", 0, true)

        -- Publish discovery for any already-exposed objects
        if ExposedObjects then
            for path, handle in pairs(ExposedObjects:list()) do
                onObjectExposed({
                    path = path,
                    type = handle.type,
                    name = handle.options.name,
                    options = handle.options,
                })
            end
        end

        -- Clear stale discovery for registry objects we no longer expose (orphans
        -- left on the broker by previous runs). Publish-only, runs after the
        -- current exposed set has been republished above.
        HAStatePublisher.reconcile()

        return true
    else
        if Logger then
            Logger:getShared():error("[HA Publisher] StateBus not available")
        end
        return false
    end
end

-- Disable state publisher
function HAStatePublisher.disable()
    HAStatePublisher.enabled = false
    if Logger then
        Logger:getShared():info("[HA Publisher] State publisher disabled")
    end
end

-- Check if enabled
function HAStatePublisher.isEnabled()
    return HAStatePublisher.enabled
end

return HAStatePublisher
