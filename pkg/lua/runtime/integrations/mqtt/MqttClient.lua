--
-- MQTT Module for Virtual CLU
--

local MQTT = {}
MQTT.__index = MQTT

-- Instance registry (for Go callbacks)
MQTT._instances = {}
MQTT._nextInstanceId = 1

--- Create new MQTT instance.
-- @api public
function MQTT:new(id)
    -- Generate unique instance ID if not provided
    local instanceId = id or ("mqtt_" .. MQTT._nextInstanceId)
    MQTT._nextInstanceId = MQTT._nextInstanceId + 1

    local instance = {
        _id = instanceId,
        _instanceId = instanceId,  -- For Go backend
        _type = "MQTT",

        -- Properties storage
        _props = {
            url = "tcp://localhost",
            port = 1883,
            clientId = "vclu_" .. instanceId,
            username = nil,
            password = nil,
            connected = false,
            keepAlive = 60,
            cleanSession = true,
            autoReconnect = true,
            reconnectInterval = 5000,
            topicPrefix = "",
            qos = 0,
            retain = false,
        },

        -- Event handlers
        _eventHandlers = {},

        -- Subscriptions
        _subscriptions = {},

        -- Message queue (for testing without Go)
        _outbox = {},
        _inbox = {},

        -- HA Discovery config (per instance)
        -- Messages are loaded from Go via loadDiscoveryMessages()
        _discovery = {
            messages = {},       -- Pre-generated messages from Go
            autoPublish = true,  -- Auto-publish on connect
        },
    }
    setmetatable(instance, self)

    -- Register instance for Go callbacks
    MQTT._instances[instanceId] = instance

    return instance
end

-- Get instance by ID (used by Go callbacks)
function MQTT.getInstance(instanceId)
    return MQTT._instances[instanceId]
end

-- Get all instance IDs
function MQTT.getInstanceIds()
    local ids = {}
    for id, _ in pairs(MQTT._instances) do
        table.insert(ids, id)
    end
    return ids
end

--- Get instance ID.
-- @api public
function MQTT:getId()
    return self._instanceId
end

-- ============================================
-- Named methods
-- ============================================

--- Set broker URL
-- @param url string Broker URL (e.g., "tcp://192.168.1.100" or "ssl://broker.example.com")
-- @return self for chaining
-- @api public
function MQTT:setUrl(url)
    self._props.url = url
    return self
end

--- Set broker host (alias for setUrl with tcp:// prefix)
-- @param host string Broker host (e.g., "192.168.1.100" or "broker.example.com")
-- @return self for chaining
-- @api public
function MQTT:setHost(host)
    -- Add tcp:// prefix if not already present
    if not host:match("^%w+://") then
        host = "tcp://" .. host
    end
    self._props.url = host
    return self
end

--- Set broker port
-- @param port number Broker port (default 1883, or 8883 for SSL)
-- @return self for chaining
-- @api public
function MQTT:setPort(port)
    self._props.port = port
    return self
end

--- Set client ID
-- @param clientId string Unique client identifier
-- @return self for chaining
-- @api public
function MQTT:setClientId(clientId)
    self._props.clientId = clientId
    return self
end

--- Set authentication credentials
-- @param username string Username
-- @param password string Password
-- @return self for chaining
-- @api public
function MQTT:setCredentials(username, password)
    self._props.username = username
    self._props.password = password
    return self
end

--- Set topic prefix for all publish/subscribe operations
-- @param prefix string Prefix (e.g., "grenton", "home/grenton")
-- @return self for chaining
-- @api public
function MQTT:setTopicPrefix(prefix)
    self._props.topicPrefix = prefix
    return self
end

--- Set default QoS level
-- @param qos number QoS level (0, 1, or 2)
-- @return self for chaining
-- @api public
function MQTT:setQoS(qos)
    if qos < 0 or qos > 2 then
        error("QoS must be 0, 1, or 2")
    end
    self._props.qos = qos
    return self
end

--- Set keep alive interval
-- @param seconds number Keep alive interval in seconds
-- @return self for chaining
-- @api public
function MQTT:setKeepAlive(seconds)
    self._props.keepAlive = seconds
    return self
end

--- Enable/disable auto reconnect
-- @param enabled boolean Auto reconnect flag
-- @param intervalMs number Reconnect interval in milliseconds (optional)
-- @return self for chaining
-- @api public
function MQTT:setAutoReconnect(enabled, intervalMs)
    self._props.autoReconnect = enabled
    if intervalMs then
        self._props.reconnectInterval = intervalMs
    end
    return self
end

--- Get connection status (synced with Go backend)
-- @return boolean True if connected
-- @api public
function MQTT:isConnected()
    -- If Go backend available, get real connection state
    if __go_mqtt_is_connected then
        local connected = __go_mqtt_is_connected(self._instanceId)
        -- Update local state to stay in sync
        self._props.connected = connected
        return connected
    end
    -- Fallback to local state
    return self._props.connected == true
end

--- Get broker URL (full with port)
-- @return string Full broker URL
-- @api public
function MQTT:getUrl()
    return self._props.url
end

--- Get topic prefix
-- @return string Topic prefix
-- @api public
function MQTT:getTopicPrefix()
    return self._props.topicPrefix or ""
end

-- ============================================
-- Connection methods
-- ============================================

--- Connect to broker
-- @return boolean Success
-- @api public
function MQTT:connect()
    if Logger then
        Logger:info("MQTT[%s]:connect() to %s:%d as %s",
            self._instanceId,
            self._props.url,
            self._props.port,
            self._props.clientId)
    end

    -- Call Go backend if available (async — result delivered via pollEvents)
    if __go_mqtt_connect then
        __go_mqtt_connect(
            self._instanceId,
            self._props.url,
            self._props.port,
            self._props.clientId,
            self._props.username,
            self._props.password,
            self._props.keepAlive,
            self._props.cleanSession
        )
        -- Don't set connected=true here — async, wait for pollEvents
        return true -- pending
    end

    -- Stub for testing without Go
    self._props.connected = true
    self:_emit("connect")

    -- Auto-publish discovery after connect (if messages loaded from Go)
    if self._discovery.autoPublish and #self._discovery.messages > 0 then
        self:publishAllDiscovery()
    end

    return true
end

--- Disconnect from broker
-- @param removeInstance boolean Remove from instance registry (default: true)
-- @return boolean Success
-- @api public
function MQTT:disconnect(removeInstance)
    if Logger then
        Logger:info("MQTT[%s]:disconnect()", self._instanceId)
    end

    -- Call Go backend if available (with instanceId)
    if __go_mqtt_disconnect then
        __go_mqtt_disconnect(self._instanceId)
    end

    self._props.connected = false
    self:_emit("disconnect")

    -- Only remove from registry if explicitly requested (default: true for manual disconnect)
    -- Auto-reconnect scenarios should pass false to keep instance reference
    if removeInstance ~= false then
        MQTT._instances[self._instanceId] = nil
    end

    return true
end

-- ============================================
-- Publish/Subscribe methods
-- ============================================

--- Publish message to topic
-- @param topic string Topic (prefix will be added if set)
-- @param payload string|table Message payload (table will be JSON encoded)
-- @param qos number QoS level (optional, uses default)
-- @param retain boolean Retain flag (optional, uses default)
-- @return boolean Success
-- @api public
function MQTT:publish(topic, payload, qos, retain)
    -- Apply prefix
    local fullTopic = self:_applyPrefix(topic)

    -- Handle table payload (convert to JSON)
    local payloadStr = payload
    if type(payload) == "table" then
        -- Simple JSON encode (for complex use case, import json library)
        payloadStr = self:_simpleJsonEncode(payload)
    elseif type(payload) ~= "string" then
        payloadStr = tostring(payload)
    end

    -- Use defaults if not specified
    qos = qos or self._props.qos
    retain = retain ~= nil and retain or self._props.retain

    if Logger then
        Logger:debug("MQTT[%s]:publish(%s, %s, qos=%d, retain=%s)",
            self._instanceId, fullTopic, payloadStr, qos, tostring(retain))
    end

    -- Call Go backend if available
    if __go_mqtt_publish then
        __go_mqtt_publish(self._instanceId, fullTopic, payloadStr, qos, retain)
    end

    -- Queue to outbox for testing/debugging (skip when Go backend handles delivery)
    if not __go_mqtt_publish or #self._outbox < 100 then
        table.insert(self._outbox, {
            instanceId = self._instanceId,
            topic = fullTopic,
            payload = payloadStr,
            qos = qos,
            retain = retain,
            timestamp = os.time()
        })
    end

    -- Always return true after queueing (Go backend may fail if not connected)
    return true
end

--- Subscribe to topic
-- @param topic string Topic pattern (prefix will be added if set)
-- @param callback function Callback function(topic, payload, qos, retain)
-- @return boolean Success
-- @api public
function MQTT:subscribe(topic, callback)
    local fullTopic = self:_applyPrefix(topic)

    if Logger then
        Logger:info("MQTT[%s]:subscribe(%s)", self._instanceId, fullTopic)
    end

    -- Store subscription
    self._subscriptions[fullTopic] = {
        pattern = fullTopic,
        callback = callback
    }

    -- Call Go backend if available (with instanceId)
    if __go_mqtt_subscribe then
        return __go_mqtt_subscribe(self._instanceId, fullTopic)
    end

    return true
end

--- Unsubscribe from topic
-- @param topic string Topic pattern
-- @return boolean Success
-- @api public
function MQTT:unsubscribe(topic)
    local fullTopic = self:_applyPrefix(topic)

    if Logger then
        Logger:info("MQTT[%s]:unsubscribe(%s)", self._instanceId, fullTopic)
    end

    self._subscriptions[fullTopic] = nil

    -- Call Go backend if available (with instanceId)
    if __go_mqtt_unsubscribe then
        return __go_mqtt_unsubscribe(self._instanceId, fullTopic)
    end

    return true
end

-- ============================================
-- Event handlers
-- ============================================

--- Register OnConnect handler
-- @param callback function Callback function()
-- @return self for chaining
-- @api public
function MQTT:onConnect(callback)
    return self:_on("connect", callback)
end

--- Register OnDisconnect handler
-- @param callback function Callback function()
-- @return self for chaining
-- @api public
function MQTT:onDisconnect(callback)
    return self:_on("disconnect", callback)
end

--- Register OnMessage handler (global, for all subscriptions)
-- @param callback function Callback function(topic, payload, qos, retain)
-- @return self for chaining
-- @api public
function MQTT:onMessage(callback)
    return self:_on("message", callback)
end

--- Register OnError handler
-- @param callback function Callback function(error)
-- @return self for chaining
-- @api public
function MQTT:onError(callback)
    return self:_on("error", callback)
end

-- ============================================
-- HA Discovery (receives pre-generated messages from Go)
-- ============================================

--- Load discovery messages generated by Go (omparser.GenerateHADiscovery)
-- @param messages table Array of {topic, payload, retain} from Go
-- @return self
-- @api public
function MQTT:loadDiscoveryMessages(messages)
    self._discovery.messages = messages or {}
    if Logger then
        Logger:info("MQTT[%s]:loadDiscoveryMessages - loaded %d messages",
            self._instanceId, #self._discovery.messages)
    end
    return self
end

--- Add single discovery message
-- @param topic string Full topic (e.g., "homeassistant/switch/.../config")
-- @param payload string|table JSON payload
-- @return self
-- @api public
function MQTT:addDiscoveryMessage(topic, payload)
    table.insert(self._discovery.messages, {
        topic = topic,
        payload = payload,
        retain = true
    })
    return self
end

--- Configure discovery settings
-- @param config table {autoPublish}
-- @return self
-- @api public
function MQTT:configureDiscovery(config)
    if config.autoPublish ~= nil then
        self._discovery.autoPublish = config.autoPublish
    end
    return self
end

--- Publish all loaded discovery messages
-- @return number Number of messages published
-- @api public
function MQTT:publishAllDiscovery()
    local count = 0
    local messages = self._discovery.messages or {}

    if Logger then
        Logger:info("MQTT[%s]:publishAllDiscovery - %d messages",
            self._instanceId, #messages)
    end

    for _, msg in ipairs(messages) do
        if self:_publishRaw(msg.topic, msg.payload, 0, true) then
            count = count + 1
        end
    end

    return count
end

--- Remove all discovery (publish empty payloads)
-- @return number Number of messages published
-- @api public
function MQTT:removeAllDiscovery()
    local count = 0
    local messages = self._discovery.messages or {}

    for _, msg in ipairs(messages) do
        if self:_publishRaw(msg.topic, "", 0, true) then
            count = count + 1
        end
    end

    return count
end

--- Publish state update for object
-- @param topic string State topic (from discovery message)
-- @param state any State value
-- @return boolean Success
-- @api public
function MQTT:publishState(topic, state)
    return self:_publishRaw(topic, tostring(state), 0, true)
end

--- Internal publish without applying prefix
function MQTT:_publishRaw(topic, payload, qos, retain)
    local payloadStr = payload
    if type(payload) == "table" then
        payloadStr = self:_simpleJsonEncode(payload)
    end

    if Logger then
        Logger:debug("MQTT[%s]:_publishRaw(%s)", self._instanceId, topic)
    end

    -- Call Go backend if available
    if __go_mqtt_publish then
        __go_mqtt_publish(self._instanceId, topic, payloadStr, qos, retain)
    end

    -- Queue to outbox for testing/debugging (skip when Go backend handles delivery)
    if not __go_mqtt_publish or #self._outbox < 100 then
        table.insert(self._outbox, {
            instanceId = self._instanceId,
            topic = topic,
            payload = payloadStr,
            qos = qos,
            retain = retain,
            timestamp = os.time(),
            raw = true,
        })
    end

    -- Always return true after queueing (Go backend may fail if not connected)
    return true
end

function MQTT:_countTable(t)
    local count = 0
    for _ in pairs(t) do count = count + 1 end
    return count
end

-- ============================================
-- Internal methods
-- ============================================

function MQTT:_on(event, callback)
    if not self._eventHandlers[event] then
        self._eventHandlers[event] = {}
    end
    table.insert(self._eventHandlers[event], callback)
    return self
end

function MQTT:_emit(event, ...)
    local handlers = self._eventHandlers[event]
    if handlers then
        for _, callback in ipairs(handlers) do
            local ok, err = pcall(callback, ...)
            if not ok and Logger then
                Logger:error("MQTT event handler error: %s", tostring(err))
            end
        end
    end
end

function MQTT:_applyPrefix(topic)
    local prefix = self._props.topicPrefix
    if prefix and prefix ~= "" then
        -- Escape special regex characters in prefix for matching
        local escapedPrefix = prefix:gsub("([%^%$%(%)%%%.%[%]%*%+%-%?])", "%%%1")
        if not topic:match("^" .. escapedPrefix) then
            return prefix .. "/" .. topic
        end
    end
    return topic
end

function MQTT:_simpleJsonEncode(tbl)
    -- JSON encoder for tables (objects and arrays)
    if type(tbl) ~= "table" then
        return self:_jsonValue(tbl)
    end

    -- Check if table is array (sequential integer keys starting at 1)
    local isArray = true
    local maxIndex = 0
    for k, _ in pairs(tbl) do
        if type(k) ~= "number" or k < 1 or math.floor(k) ~= k then
            isArray = false
            break
        end
        if k > maxIndex then maxIndex = k end
    end
    -- Also check for holes in array
    if isArray and maxIndex > 0 then
        for i = 1, maxIndex do
            if tbl[i] == nil then
                isArray = false
                break
            end
        end
    end

    if isArray and maxIndex > 0 then
        -- Encode as JSON array
        local parts = {}
        for i = 1, maxIndex do
            table.insert(parts, self:_jsonValue(tbl[i]))
        end
        return "[" .. table.concat(parts, ",") .. "]"
    else
        -- Encode as JSON object
        local parts = {}
        for k, v in pairs(tbl) do
            if v ~= nil then  -- Skip nil values
                local key = '"' .. tostring(k) .. '"'
                local val = self:_jsonValue(v)
                table.insert(parts, key .. ":" .. val)
            end
        end
        return "{" .. table.concat(parts, ",") .. "}"
    end
end

function MQTT:_jsonValue(v)
    local t = type(v)
    if t == "nil" then
        return "null"
    elseif t == "boolean" then
        return v and "true" or "false"
    elseif t == "number" then
        -- Handle special float values
        if v ~= v then  -- NaN
            return "null"
        elseif v == math.huge or v == -math.huge then
            return "null"
        end
        return tostring(v)
    elseif t == "string" then
        -- Escape special characters
        local escaped = v
            :gsub("\\", "\\\\")
            :gsub('"', '\\"')
            :gsub("\n", "\\n")
            :gsub("\r", "\\r")
            :gsub("\t", "\\t")
            :gsub("\b", "\\b")
            :gsub("\f", "\\f")
        return '"' .. escaped .. '"'
    elseif t == "table" then
        return self:_simpleJsonEncode(v)
    else
        -- Function, userdata, thread -> null
        return "null"
    end
end

-- ============================================
-- Message handling (called from Go)
-- ============================================

--- Handle incoming message (called from Go backend)
-- @param topic string Full topic
-- @param payload string Message payload
-- @param qos number QoS level
-- @param retain boolean Retain flag
function MQTT:_handleMessage(topic, payload, qos, retain)
    -- Changes made by message handlers come in through MQTT
    if StateContext then
        return StateContext.with({ origin = "mqtt", actor = "service:mqtt",
            correlationId = StateContext.newCorrelationId() },
            self._dispatchMessage, self, topic, payload, qos, retain)
    end
    return self:_dispatchMessage(topic, payload, qos, retain)
end

function MQTT:_dispatchMessage(topic, payload, qos, retain)
    -- Call global OnMessage handlers
    self:_emit("message", topic, payload, qos, retain)

    -- Call subscription-specific callbacks
    for pattern, sub in pairs(self._subscriptions) do
        if self:_topicMatches(topic, pattern) then
            if sub.callback then
                local ok, err = pcall(sub.callback, topic, payload, qos, retain)
                if not ok and Logger then
                    Logger:error("MQTT subscription callback error: %s", tostring(err))
                end
            end
        end
    end
end

function MQTT:_topicMatches(topic, pattern)
    -- MQTT wildcard rules:
    -- + matches exactly one topic level (between /)
    -- # matches zero or more levels, but only at the end

    -- Exact match
    if topic == pattern then
        return true
    end

    -- Split into levels
    local topicLevels = {}
    for level in topic:gmatch("[^/]+") do
        table.insert(topicLevels, level)
    end

    local patternLevels = {}
    for level in pattern:gmatch("[^/]+") do
        table.insert(patternLevels, level)
    end

    -- Check each level
    for i, patLevel in ipairs(patternLevels) do
        if patLevel == "#" then
            -- # must be last level and matches rest
            return i == #patternLevels
        elseif patLevel == "+" then
            -- + matches exactly one level
            if not topicLevels[i] then
                return false
            end
        else
            -- Exact match required
            if topicLevels[i] ~= patLevel then
                return false
            end
        end
    end

    -- All pattern levels matched, check topic has same number of levels
    -- (unless last pattern was #, handled above)
    return #topicLevels == #patternLevels
end

-- ============================================
-- Testing helpers (for use without Go backend)
-- ============================================

--- Get outbox (messages queued for sending)
-- @return table Array of queued messages
function MQTT:getOutbox()
    return self._outbox
end

--- Clear outbox
function MQTT:clearOutbox()
    self._outbox = {}
end

--- Simulate incoming message (for testing)
-- @param topic string Topic
-- @param payload string Payload
function MQTT:simulateMessage(topic, payload)
    self:_handleMessage(topic, payload, 0, false)
end

-- ============================================
-- Message polling (called periodically to process incoming messages)
-- ============================================

--- Poll for incoming messages from Go backend and dispatch to handlers
-- This should be called periodically (e.g., every 100ms)
-- @return number Number of messages processed
function MQTT.pollMessages()
    -- Defensive check: __go_mqtt_poll must be a callable function
    if type(__go_mqtt_poll) ~= "function" then
        return 0
    end

    local count = 0
    local maxPerPoll = 100  -- Prevent infinite loop

    while count < maxPerPoll do
        local instanceId, topic, payload, qos, retained = __go_mqtt_poll()
        if instanceId == nil then
            break
        end

        count = count + 1

        -- Find the instance
        local instance = MQTT._instances[instanceId]
        if instance then
            instance:_handleMessage(topic, payload, qos, retained)
        else
            -- Log orphaned message (this shouldn't happen normally)
            if Logger then
                Logger:warn("MQTT message for unknown instance %s: %s", instanceId, topic)
            end
        end
    end

    return count
end

--- Poll for async lifecycle events (connect/subscribe results) from Go backend
-- Called periodically from the polling timer alongside pollMessages
-- @return number Number of events processed
function MQTT.pollEvents()
    if type(__go_mqtt_poll_event) ~= "function" then
        return 0
    end

    local maxPerPoll = 16 -- anti-starvation: don't hog the timer tick
    local count = 0

    while count < maxPerPoll do
        local eventType, instanceId, topic, errMsg = __go_mqtt_poll_event()
        if eventType == nil then break end
        count = count + 1

        local instance = MQTT._instances[instanceId]
        if not instance then goto continue end

        if eventType == "connect_result" then
            if errMsg == "" then
                instance._props.connected = true
                instance:_emit("connect")
                -- Re-subscribe all stored subscriptions (handles race with async connect + auto-reconnect)
                if __go_mqtt_subscribe then
                    for subTopic, _ in pairs(instance._subscriptions) do
                        __go_mqtt_subscribe(instance._instanceId, subTopic)
                    end
                end
                -- Auto-publish discovery after async connect
                if instance._discovery.autoPublish and #instance._discovery.messages > 0 then
                    instance:publishAllDiscovery()
                end
            else
                if Logger then
                    Logger:warn("MQTT[%s] connect failed: %s (auto-reconnect active)", instanceId, errMsg)
                end
            end
        elseif eventType == "connection_lost" then
            instance._props.connected = false
            instance:_emit("disconnect")
        elseif eventType == "subscribe_result" then
            if errMsg ~= "" and Logger then
                Logger:warn("MQTT[%s] subscribe '%s' failed: %s", instanceId, topic, errMsg)
            end
        end

        ::continue::
    end

    return count
end

-- Start polling timer (100ms interval)
local function startPolling()
    -- Defensive check: __go_setInterval must be a callable function
    if type(__go_setInterval) == "function" and not MQTT._pollTimerId then
        MQTT._pollTimerId = __go_setInterval(function()
            if MQTT and MQTT.pollMessages then
                MQTT.pollMessages()
            end
            if MQTT and MQTT.pollEvents then
                MQTT.pollEvents()
            end
        end, 100)
    end
end

-- Auto-start polling when module loads
startPolling()

-- ============================================
-- Module export
-- ============================================

-- Create default instance
local defaultInstance = MQTT:new("MQTT")

-- Return module with factory and default instance
return setmetatable({
    new = function(_, id) return MQTT:new(id) end,
    pollMessages = MQTT.pollMessages,
    pollEvents = MQTT.pollEvents,
}, {
    __index = defaultInstance,
    __newindex = function(_, k, v) rawset(defaultInstance, k, v) end,
    __call = function(_, ...) return MQTT:new(...) end
})