--
-- MQTT Command Handler for Home Assistant integration
-- Translates HA payloads (ON/OFF/OPEN/CLOSE) to Lua method calls
--

local MqttHandler = {}
MqttHandler.__index = MqttHandler

-- Command mappings: HA payload -> Lua method
MqttHandler.CommandMap = {
    switch = {
        ON = "switchOn",
        OFF = "switchOff",
    },
    light = {
        ON = "on",
        OFF = "off",
    },
    cover = {
        OPEN = "open",
        CLOSE = "close",
        STOP = "stop",
    },
    binary_sensor = {
        -- Read-only, no commands
    },
    sensor = {
        -- Read-only, no commands
    },
    number = {
        -- Uses value directly
    },
}

-- State mappings: Lua value -> HA state
MqttHandler.StateMap = {
    switch = {
        [0] = "OFF",
        [1] = "ON",
        ["off"] = "OFF",
        ["on"] = "ON",
        [false] = "OFF",
        [true] = "ON",
    },
    light = {
        [0] = "OFF",
        [1] = "ON",
        ["off"] = "OFF",
        ["on"] = "ON",
        [false] = "OFF",
        [true] = "ON",
    },
    cover = {
        [0] = "closed",
        [1] = "open",
        [2] = "opening",
        [3] = "closing",
        ["closed"] = "closed",
        ["open"] = "open",
        ["opening"] = "opening",
        ["closing"] = "closing",
    },
    binary_sensor = {
        [0] = "OFF",
        [1] = "ON",
        [false] = "OFF",
        [true] = "ON",
    },
}

function MqttHandler:new(mqtt, registry)
    local instance = {
        _mqtt = mqtt,           -- MQTT module instance
        _registry = registry,   -- Object registry (_registry table)
        _topicPrefix = nil,     -- Cached topic prefix
        _subscriptions = {},    -- Active subscriptions
        _caseIndex = nil,       -- Lazy case-insensitive index

        -- Rate limiting
        _rateLimit = {
            enabled = true,
            minIntervalMs = 100,    -- Minimum 100ms between publishes per topic
            lastPublish = {},       -- topic -> timestamp
        },
    }
    setmetatable(instance, self)
    return instance
end

-- ============================================
-- Setup and configuration
-- ============================================

--- Initialize handler with MQTT instance
-- @param mqtt table MQTT module instance
-- @return self
function MqttHandler:setMqtt(mqtt)
    self._mqtt = mqtt
    return self
end

--- Set object registry
-- @param registry table Object registry (_registry)
-- @return self
function MqttHandler:setRegistry(registry)
    self._registry = registry
    self._caseIndex = nil  -- Rebuild on next lookup
    return self
end

--- Configure rate limiting
-- @param enabled boolean Enable/disable rate limiting
-- @param minIntervalMs number Minimum interval between publishes in ms (default: 100)
-- @return self
function MqttHandler:setRateLimit(enabled, minIntervalMs)
    self._rateLimit.enabled = enabled ~= false
    if minIntervalMs then
        self._rateLimit.minIntervalMs = minIntervalMs
    end
    return self
end

--- Start listening for commands
-- Subscribes to command topics
-- @return self
function MqttHandler:start()
    if not self._mqtt then
        error("MQTT instance not set")
    end

    local prefix = self._mqtt:getTopicPrefix()
    self._topicPrefix = prefix

    -- Subscribe to command topics
    -- grenton/{clu}/{component}/{object}/set
    -- grenton/{clu}/{component}/{object}/brightness/set
    -- grenton/{clu}/{component}/{object}/position/set

    self._mqtt:subscribe("+/+/+/set", function(topic, payload)
        self:_handleSetCommand(topic, payload)
    end)

    self._mqtt:subscribe("+/+/+/brightness/set", function(topic, payload)
        self:_handleBrightnessCommand(topic, payload)
    end)

    self._mqtt:subscribe("+/+/+/position/set", function(topic, payload)
        self:_handlePositionCommand(topic, payload)
    end)

    self._mqtt:subscribe("+/+/+/tilt/set", function(topic, payload)
        self:_handleTiltCommand(topic, payload)
    end)

    if Logger then
        Logger:info("MqttHandler started, prefix: %s", prefix)
    end

    return self
end

-- ============================================
-- Command handlers
-- ============================================

--- Handle set command (ON/OFF/OPEN/CLOSE/STOP)
function MqttHandler:_handleSetCommand(topic, payload)
    local parts = self:_parseTopic(topic)
    if not parts then return end

    local obj = self:_findObject(parts.objectId)
    if not obj then
        if Logger then
            Logger:warn("MqttHandler: object not found: %s", parts.objectId)
        end
        return
    end

    -- Get method from command map
    local commands = self.CommandMap[parts.component]
    if commands then
        local method = commands[payload]
        if method then
            if obj:canExecute(method) then
                if Logger then
                    Logger:debug("MqttHandler: %s:%s()", parts.objectId, method)
                end
                obj:call(method)
                return
            end
        end
    end

    -- For number type, payload is the value directly
    if parts.component == "number" then
        local value = tonumber(payload)
        if value and obj:canExecute("setValue") then
            if Logger then
                Logger:debug("MqttHandler: %s:setValue(%s)", parts.objectId, value)
            end
            obj:call("setValue", value)
        end
    end
end

--- Handle brightness command (0-255)
function MqttHandler:_handleBrightnessCommand(topic, payload)
    local parts = self:_parseTopic(topic)
    if not parts then return end

    local obj = self:_findObject(parts.objectId)
    if not obj then return end

    local brightness = tonumber(payload)
    if not brightness then return end

    -- Convert 0-255 to 0-100%
    local percent = math.floor(brightness * 100 / 255)

    if obj:canExecute("setBrightness") then
        if Logger then
            Logger:debug("MqttHandler: %s:setBrightness(%d)", parts.objectId, percent)
        end
        obj:call("setBrightness", percent)
    end
end

--- Handle position command (0-100)
function MqttHandler:_handlePositionCommand(topic, payload)
    local parts = self:_parseTopic(topic)
    if not parts then return end

    local obj = self:_findObject(parts.objectId)
    if not obj then return end

    local position = tonumber(payload)
    if not position then return end

    if obj:canExecute("setPosition") then
        if Logger then
            Logger:debug("MqttHandler: %s:setPosition(%d)", parts.objectId, position)
        end
        obj:call("setPosition", position)
    end
end

--- Handle tilt command (0-100)
function MqttHandler:_handleTiltCommand(topic, payload)
    local parts = self:_parseTopic(topic)
    if not parts then return end

    local obj = self:_findObject(parts.objectId)
    if not obj then return end

    local tilt = tonumber(payload)
    if not tilt then return end

    if obj:canExecute("setTilt") then
        if Logger then
            Logger:debug("MqttHandler: %s:setTilt(%d)", parts.objectId, tilt)
        end
        obj:call("setTilt", tilt)
    end
end

-- ============================================
-- State publishing
-- ============================================

--- Publish state for an object (with rate limiting)
-- @param obj table Remote object
-- @param component string HA component type
-- @param force boolean Skip rate limiting (default: false)
-- @return boolean Success
function MqttHandler:publishState(obj, component, force)
    if not self._mqtt then return false end

    local objectId = obj._objId
    local state = self:_getObjectState(obj, component)

    if state == nil then return false end

    local topic = string.format("%s/%s/state", objectId, component)

    -- Rate limiting check
    if not force and not self:_canPublish(topic) then
        if Logger then
            Logger:debug("MqttHandler: rate limited %s", topic)
        end
        return false
    end

    if Logger then
        Logger:debug("MqttHandler: publish %s = %s", topic, tostring(state))
    end

    return self._mqtt:publish(topic, tostring(state), 0, true)
end

--- Check if publish is allowed (rate limiting)
-- @param topic string Topic to check
-- @return boolean True if publish allowed
function MqttHandler:_canPublish(topic)
    if not self._rateLimit.enabled then
        return true
    end

    local now = os.clock() * 1000  -- Convert to ms
    local lastTime = self._rateLimit.lastPublish[topic] or 0

    if (now - lastTime) >= self._rateLimit.minIntervalMs then
        self._rateLimit.lastPublish[topic] = now
        return true
    end

    return false
end

--- Publish brightness for a light
-- @param obj table Remote light object
-- @return boolean Success
function MqttHandler:publishBrightness(obj)
    if not self._mqtt then return false end

    local objectId = obj._objId
    local brightness = 0

    if obj.getBrightness then
        brightness = obj:getBrightness() or 0
    elseif obj.getValue then
        -- Convert 0-1 to 0-255
        local value = obj:getValue() or 0
        brightness = math.floor(value * 255)
    end

    local topic = string.format("%s/light/brightness", objectId)
    return self._mqtt:publish(topic, tostring(brightness), 0, true)
end

--- Publish position for a cover
-- @param obj table Remote cover object
-- @return boolean Success
function MqttHandler:publishPosition(obj)
    if not self._mqtt then return false end

    local objectId = obj._objId
    local position = 0

    if obj.getPosition then
        position = obj:getPosition() or 0
    end

    local topic = string.format("%s/cover/position", objectId)
    return self._mqtt:publish(topic, tostring(position), 0, true)
end

-- ============================================
-- Object change listener
-- ============================================

--- Register onChange listener for an object
-- Automatically publishes state when object changes
-- @param obj table Remote object
-- @param component string HA component type
function MqttHandler:watchObject(obj, component)
    local handler = self  -- Capture reference

    obj:onChange(function(newValue, oldValue)
        -- Wrap in pcall for error protection
        local ok, err = pcall(function()
            handler:publishState(obj, component)

            -- Also publish brightness/position if applicable
            if component == "light" and obj.getBrightness then
                handler:publishBrightness(obj)
            elseif component == "cover" and obj.getPosition then
                handler:publishPosition(obj)
            end
        end)

        if not ok and Logger then
            Logger:error("MqttHandler: onChange error for %s: %s", obj._objId, tostring(err))
        end
    end)

    if Logger then
        Logger:debug("MqttHandler: watching %s as %s", obj._objId, component)
    end
end

--- Watch all objects in registry
-- @param objectMap table Map of objectId -> {obj, component}
function MqttHandler:watchAll(objectMap)
    for objectId, info in pairs(objectMap) do
        self:watchObject(info.obj, info.component)
    end
end

-- ============================================
-- Internal helpers
-- ============================================

function MqttHandler:_parseTopic(topic)
    -- Remove prefix if present
    local topicWithoutPrefix = topic
    if self._topicPrefix and topic:sub(1, #self._topicPrefix + 1) == self._topicPrefix .. "/" then
        topicWithoutPrefix = topic:sub(#self._topicPrefix + 2)
    end

    -- Parse: {clu}/{component}/{object}/...
    local clu, component, objectId = topicWithoutPrefix:match("^([^/]+)/([^/]+)/([^/]+)")

    if not clu then return nil end

    return {
        clu = clu,
        component = component,
        objectId = objectId,
        fullTopic = topic,
    }
end

function MqttHandler:_findObject(objectId)
    if not self._registry then return nil end

    -- Try direct lookup first (MQTT topics are case-sensitive)
    -- rawget: an id such as "list" is a registry method, not an object
    local obj = rawget(self._registry, objectId)
    if obj then return obj end

    -- Build case-insensitive index on first miss (lazy initialization)
    if not self._caseIndex then
        self._caseIndex = {}
        for path, o in pairs(self._registry) do
            if type(path) == "string" then
                self._caseIndex[path:lower()] = o
            end
        end
    end

    -- Try case-insensitive lookup
    obj = self._caseIndex[objectId:lower()]
    if obj then
        if Logger then
            Logger:warn("MqttHandler: case mismatch for '%s', consider using exact case", objectId)
        end
        return obj
    end

    return nil
end

--- Rebuild case index (call after registry changes)
function MqttHandler:rebuildIndex()
    self._caseIndex = nil  -- Will be rebuilt on next lookup
end

function MqttHandler:_getObjectState(obj, component)
    local value = nil

    -- Get raw value
    if obj.getValue then
        value = obj:getValue()
    elseif obj.getState then
        value = obj:getState()
    end

    -- Map to HA state
    local stateMap = self.StateMap[component]
    if stateMap and stateMap[value] then
        return stateMap[value]
    end

    return value
end

-- ============================================
-- Custom command registration
-- ============================================

--- Register custom command mapping
-- @param component string Component type
-- @param payload string HA payload
-- @param method string Lua method name
function MqttHandler:registerCommand(component, payload, method)
    if not self.CommandMap[component] then
        self.CommandMap[component] = {}
    end
    self.CommandMap[component][payload] = method
end

--- Register custom state mapping
-- @param component string Component type
-- @param value any Lua value
-- @param state string HA state
function MqttHandler:registerState(component, value, state)
    if not self.StateMap[component] then
        self.StateMap[component] = {}
    end
    self.StateMap[component][value] = state
end

return MqttHandler
