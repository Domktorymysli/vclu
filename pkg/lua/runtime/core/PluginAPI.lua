--- Plugin API for vCLU.
-- API for creating and managing plugins in vCLU.
-- @module PluginAPI
-- @usage
--   local plugin = Plugin:new("weather", {
--       name = "Weather Plugin",
--       version = "1.0.0"
--   })
--   plugin:onInit(function(config)
--       -- initialization code
--   end)

-- Load PluginHelpers
local PluginHelpers = require("PluginHelpers")

--- Plugin class.
-- Base class for all vCLU plugins.
-- @table Plugin
Plugin = {}
Plugin.__index = Plugin
Plugin.instances = {}
Plugin.apiVersion = 1

--- Create a new plugin instance
-- @param id string Plugin identifier (short id). If __plugin_id is set by loader, it will be used as fullId.
-- @param metadata table Plugin metadata (name, version, description)
-- @return Plugin New plugin instance
-- @usage local myPlugin = Plugin:new("my-plugin", {name = "My Plugin", version = "1.0.0"})
function Plugin:new(id, metadata)
    local instance = setmetatable({}, Plugin)

    -- Use fullId from loader if available, otherwise use provided id
    local fullId = __plugin_id or id
    instance.id = fullId
    instance.shortId = id

    -- Parse namespace from fullId (@namespace/id)
    if fullId:sub(1, 1) == "@" then
        local slashPos = fullId:find("/")
        if slashPos then
            instance.namespace = fullId:sub(2, slashPos - 1)
            instance.shortId = fullId:sub(slashPos + 1)
        end
    end

    instance.name = metadata.name or id
    instance.version = metadata.version or "0.0.0"
    instance.description = metadata.description or ""
    instance.config = {}
    instance.timers = {}
    instance.subscriptions = {}
    instance._initCallback = nil
    instance._cleanupCallback = nil
    instance._exposedHandles = {}  -- Track exposed objects for cleanup

    -- Get sandbox reference if running in sandbox
    instance._sandbox = nil
    if PluginSandbox and PluginSandbox.sandboxes and PluginSandbox.sandboxes[fullId] then
        instance._sandbox = PluginSandbox.sandboxes[fullId]
    end

    -- Mark as plugin for inspect/type detection
    instance._isPlugin = true

    -- Store instance by fullId
    Plugin.instances[fullId] = instance

    -- Register in global _registry so _:get("@vclu/weather") works
    if _registry then
        _registry[fullId] = instance
    end

    Logger:info("[PLUGIN] Registered: " .. instance.name .. " v" .. instance.version .. " (" .. fullId .. ")")
    return instance
end

--- Set initialization callback
-- @param callback function Function called when plugin is loaded with config
-- @return Plugin self for chaining
-- @usage plugin:onInit(function(config) print("Loaded with config:", config) end)
function Plugin:onInit(callback)
    self._initCallback = callback
    return self
end

--- Set cleanup callback (called when plugin is unloaded)
-- @param callback function Function called before plugin is unloaded
-- @return Plugin self for chaining
-- @usage plugin:onCleanup(function() print("Cleaning up...") end)
function Plugin:onCleanup(callback)
    self._cleanupCallback = callback
    return self
end

--- Initialize the plugin with config
-- @param config table Configuration values from user
function Plugin:init(config)
    self.config = config or {}
    if self._initCallback then
        self._initCallback(self.config)
    end
end

--- Cleanup the plugin
function Plugin:cleanup()
    -- Cancel all timers
    for _, timerId in ipairs(self.timers) do
        if clearInterval then clearInterval(timerId) end
        if clearTimeout then clearTimeout(timerId) end
    end
    self.timers = {}

    -- Remove subscriptions
    for _, sub in ipairs(self.subscriptions) do
        -- Unsubscribe logic if available
    end
    self.subscriptions = {}

    -- Unexpose all exposed objects
    for _, handle in ipairs(self._exposedHandles) do
        if handle and handle.unexpose then
            handle:unexpose()
        end
    end
    self._exposedHandles = {}

    -- Alternative: use ExposedObjects batch cleanup
    if ExposedObjects and ExposedObjects.unexposeByPlugin then
        ExposedObjects:unexposeByPlugin(self.id)
    end

    if self._cleanupCallback then
        self._cleanupCallback()
    end

    -- Remove from global _registry
    if _registry and self.id then
        _registry[self.id] = nil
    end

    Logger:info("[PLUGIN] Unloaded: " .. self.name)
end

--- Get config value
-- @param key string Config key
-- @param default any Default value if key not found
-- @return any Config value or default
-- @usage local apiKey = plugin:getConfig("apiKey", "")
function Plugin:getConfig(key, default)
    if self.config[key] ~= nil then
        return self.config[key]
    end
    return default
end

--- Create a repeating timer for this plugin
-- Uses sandbox API if available for automatic resource tracking
-- @param intervalMs number Interval in milliseconds
-- @param callback function Function to call on each interval
-- @return number Timer ID
-- @usage local timerId = plugin:setInterval(60000, function() fetchData() end)
function Plugin:setInterval(intervalMs, callback)
    local timerId
    if self._sandbox and self._sandbox.api then
        -- Use sandbox API (automatic tracking + pcall protection)
        timerId = self._sandbox.api.setInterval(intervalMs, callback)
    else
        -- Fallback to global function
        timerId = setInterval(intervalMs, callback)
        table.insert(self.timers, timerId)
    end
    return timerId
end

--- Create a one-shot timer for this plugin
-- Uses sandbox API if available for automatic resource tracking
-- @param delayMs number Delay in milliseconds
-- @param callback function Function to call after delay
-- @return number Timer ID
-- @usage plugin:setTimeout(5000, function() print("5 seconds later") end)
function Plugin:setTimeout(delayMs, callback)
    local timerId
    if self._sandbox and self._sandbox.api then
        -- Use sandbox API (automatic tracking + pcall protection)
        timerId = self._sandbox.api.setTimeout(delayMs, callback)
    else
        -- Fallback to global function
        timerId = setTimeout(delayMs, callback)
        table.insert(self.timers, timerId)
    end
    return timerId
end

--- Clear a timer
-- @param timerId number Timer ID to clear
function Plugin:clearTimer(timerId)
    if self._sandbox and self._sandbox.api then
        self._sandbox.api.clearTimer(timerId)
    else
        if clearInterval then clearInterval(timerId) end
        if clearTimeout then clearTimeout(timerId) end
    end

    -- Remove from tracked timers (for non-sandbox case)
    for i, id in ipairs(self.timers) do
        if id == timerId then
            table.remove(self.timers, i)
            break
        end
    end
end

--- Register an event handler
-- Uses sandbox API if available for automatic resource tracking
-- @param eventName string Name of the event to listen for
-- @param callback function Handler function
-- @return Plugin self for chaining
-- @usage plugin:on("registry:changed", function(path, value) ... end)
function Plugin:on(eventName, callback)
    if self._sandbox and self._sandbox.api then
        -- Use sandbox API (automatic tracking + pcall protection)
        self._sandbox.api.on(eventName, callback)
    elseif EventBus then
        EventBus:on(eventName, callback)
        table.insert(self.subscriptions, {type = "event", name = eventName, callback = callback})
    end
    return self
end

--- Emit an event
-- @param eventName string Name of the event
-- @param ... any Event arguments
-- @usage plugin:emit("weather:updated", data)
function Plugin:emit(eventName, ...)
    if self._sandbox and self._sandbox.api then
        self._sandbox.api.emit(eventName, ...)
    elseif EventBus then
        EventBus:emit(eventName, ...)
    end
end

--- Log a message with plugin prefix
-- @param level string Log level (info, warn, error, debug)
-- @param message string Message to log
function Plugin:log(level, message)
    -- Use shortId for cleaner log output
    local prefix = "[" .. (self.shortId or self.id):upper() .. "] "
    if level == "error" then
        Logger:error(prefix .. message)
    elseif level == "warn" then
        Logger:warn(prefix .. message)
    elseif level == "debug" then
        Logger:debug(prefix .. message)
    else
        Logger:info(prefix .. message)
    end
end

--- Create a data object for this plugin (stored locally, not in global _registry)
-- @param path string Object path (relative to plugin)
-- @param objectData table Object data
-- @return table The created object
-- @usage local sensor = plugin:createObject("temperature", {value = 0, unit = "C"})
function Plugin:createObject(path, objectData)
    if self._sandbox and self._sandbox.api then
        return self._sandbox.api.createObject(path, objectData)
    end
    -- Fallback: store in _dataObjects
    self._dataObjects = self._dataObjects or {}
    self._dataObjects[path] = objectData
    return objectData
end

--- Get a data object by path
-- @param path string Object path (relative to plugin, or full registry path)
-- @return table|nil The object or nil if not found
-- @usage local sensor = plugin:getObject("temperature")
function Plugin:getObject(path)
    if self._sandbox and self._sandbox.api then
        return self._sandbox.api.getObject(path)
    end
    -- Fallback: check local data objects
    if self._dataObjects and self._dataObjects[path] then
        return self._dataObjects[path]
    end
    return nil
end

--- Make an HTTP GET request
-- Uses sandbox API if available for pcall protection
-- @param url string URL to fetch
-- @param callback function Callback with (response, error)
-- @usage plugin:httpGet("https://api.example.com/data", function(resp, err)
--     if err then print("Error:", err) else print("Data:", resp) end
-- end)
function Plugin:httpGet(url, callback)
    if self._sandbox and self._sandbox.api then
        self._sandbox.api.httpGet(url, callback)
        return
    end

    -- Fallback
    if HttpClient then
        HttpClient:new()
            :get(url)
            :onResponse(function(response)
                callback(response, nil)
            end)
            :onError(function(err)
                callback(nil, err)
            end)
            :send()
    else
        callback(nil, "HttpClient not available")
    end
end

--- Make an HTTP POST request
-- Uses sandbox API if available for pcall protection
-- @param url string URL to post to
-- @param data table Data to send (will be JSON encoded)
-- @param callback function Callback with (response, error)
function Plugin:httpPost(url, data, callback)
    if self._sandbox and self._sandbox.api then
        self._sandbox.api.httpPost(url, data, callback)
        return
    end

    -- Fallback
    if HttpClient then
        HttpClient:new()
            :post(url, data)
            :onResponse(function(response)
                callback(response, nil)
            end)
            :onError(function(err)
                callback(nil, err)
            end)
            :send()
    else
        callback(nil, "HttpClient not available")
    end
end

--- Make an HTTP request with full options (method, headers, body, timeout)
-- Uses sandbox API if available for pcall protection
-- @param options table Request options {method, url, headers, body, timeout}
-- @param callback function Callback with (response, error)
-- @usage plugin:httpRequest({
--     method = "GET",
--     url = "https://api.example.com/data",
--     headers = { ["Authorization"] = "Basic xxx" },
--     timeout = 5000
-- }, function(resp, err) end)
function Plugin:httpRequest(options, callback)
    if self._sandbox and self._sandbox.api and self._sandbox.api.httpRequest then
        self._sandbox.api.httpRequest(options, callback)
        return
    end

    -- Fallback - use httpGet/httpPost without custom headers
    local method = (options.method or "GET"):upper()
    if method == "POST" then
        self:httpPost(options.url, options.body, callback)
    else
        self:httpGet(options.url, callback)
    end
end

--- Publish to MQTT
-- Uses sandbox API if available
-- @param topic string MQTT topic
-- @param payload string|table Payload (tables will be JSON encoded)
-- @param options table Optional publish options (retain, qos)
-- @usage plugin:mqttPublish("home/weather/temperature", {value = 22.5})
function Plugin:mqttPublish(topic, payload, options)
    options = options or {}

    if self._sandbox and self._sandbox.api then
        self._sandbox.api.mqttPublish(topic, payload, options.retain, options.qos)
        return
    end

    -- Fallback
    local payloadStr = payload
    if type(payload) == "table" then
        payloadStr = JSON:encode(payload)
    end

    if MQTT and MQTT.publish then
        MQTT:publish(topic, payloadStr, options.retain or false, options.qos or 0)
    end
end

--- Subscribe to MQTT topic
-- Uses sandbox API if available for automatic resource tracking
-- @param topic string MQTT topic pattern
-- @param callback function Handler function(topic, payload)
-- @usage plugin:mqttSubscribe("home/+/set", function(topic, payload) ... end)
function Plugin:mqttSubscribe(topic, callback)
    if self._sandbox and self._sandbox.api then
        self._sandbox.api.mqttSubscribe(topic, callback)
        return
    end

    -- Fallback
    if MQTT and MQTT.subscribe then
        MQTT:subscribe(topic, callback)
        table.insert(self.subscriptions, {type = "mqtt", topic = topic, callback = callback})
    end
end

-- ============================================
-- NEW API: HTTP, URL, Auth helpers
-- ============================================

--- Make an HTTP request with full options (NEW API)
-- @param opts table Request options (see PluginHelpers.httpRequest)
-- @param callback function Callback(response, err)
function Plugin:httpRequest(opts, callback)
    PluginHelpers.httpRequest(self.id, opts, callback)
end

--- Build URL with sorted query parameters
-- @param base string Base URL
-- @param params table Query parameters
-- @return string Full URL
function Plugin:url(base, params)
    return PluginHelpers.buildUrl(base, params)
end

--- Build Basic Auth header value
-- @param username string
-- @param password string
-- @return string "Basic xxx"
function Plugin:basicAuth(username, password)
    return PluginHelpers.basicAuth(username, password)
end

--- Build Bearer Auth header value
-- @param token string
-- @return string "Bearer xxx"
function Plugin:bearerAuth(token)
    return PluginHelpers.bearerAuth(token)
end

--- Safely decode JSON
-- @param str string JSON string
-- @return any|nil Decoded value
-- @return string|nil Error message
function Plugin:decodeJson(str)
    return PluginHelpers.decodeJson(str)
end

--- Safely encode JSON
-- @param data any Data to encode
-- @return string|nil JSON string
-- @return string|nil Error message
function Plugin:encodeJson(data)
    return PluginHelpers.encodeJson(data)
end

-- ============================================
-- NEW API: Coercion helpers
-- ============================================

--- Coerce value to number
-- @param val any Value to coerce
-- @param default number Default if coercion fails
-- @return number
function Plugin:coerceNumber(val, default)
    return PluginHelpers.coerceNumber(val, default)
end

--- Coerce value to string
-- @param val any Value to coerce
-- @param default string Default if coercion fails
-- @return string
function Plugin:coerceString(val, default)
    return PluginHelpers.coerceString(val, default)
end

--- Coerce value to boolean
-- @param val any Value to coerce
-- @param default boolean Default if coercion fails
-- @return boolean
function Plugin:coerceBool(val, default)
    return PluginHelpers.coerceBool(val, default)
end

-- ============================================
-- NEW API: Poller
-- ============================================

--- Create a poller for periodic data fetching
-- @param name string Poller name
-- @param opts table Poller options
-- @return table Poller object
function Plugin:poller(name, opts)
    return PluginHelpers.createPoller(self.id, name, opts)
end

-- ============================================
-- NEW API: Registry with validation
-- ============================================

--- Update a data object (shallow merge, fails if not exists)
-- @param path string Object path
-- @param patch table Fields to update
-- @return boolean Success
-- @return string|nil Error message
function Plugin:updateObject(path, patch)
    if self._sandbox and self._sandbox.api then
        return self._sandbox.api.updateObject(path, patch)
    end
    -- Fallback: local data objects
    self._dataObjects = self._dataObjects or {}
    local obj = self._dataObjects[path]
    if not obj then
        local err = "updateObject: object '" .. path .. "' does not exist. Use createObject() first."
        Logger:error("[" .. (self.shortId or self.id):upper() .. "] " .. err)
        return false, err
    end
    for k, v in pairs(patch) do obj[k] = v end
    obj._lastUpdate = os.time()
    return true, nil
end

--- Upsert a data object (create or update)
-- @param path string Object path
-- @param data table Object data
-- @return table The object
function Plugin:upsertObject(path, data)
    if self._sandbox and self._sandbox.api then
        return self._sandbox.api.upsertObject(path, data)
    end
    -- Fallback: local data objects
    self._dataObjects = self._dataObjects or {}
    local obj = self._dataObjects[path]
    if obj then
        for k, v in pairs(data) do obj[k] = v end
        obj._lastUpdate = os.time()
    else
        data._lastUpdate = os.time()
        data._created = os.time()
        self._dataObjects[path] = data
        obj = data
    end
    return obj
end

-- ============================================
-- NEW API: Events with throttling
-- ============================================

--- Emit event with optional throttling and coalescing
-- @param eventName string Event name (format: "namespace:action")
-- @param data table Event data
-- @param opts table Options {throttle, key, coalesce, leading, trailing}
-- @return boolean|string true=emitted, false=dropped, "pending"=queued
function Plugin:emit(eventName, data, opts)
    if opts and (opts.throttle or opts.coalesce or opts.leading ~= nil or opts.trailing) then
        -- Use throttled emit
        local emitFn = function(event, payload)
            if self._sandbox and self._sandbox.api then
                self._sandbox.api.emit(event, payload)
            elseif EventBus then
                EventBus:emit(event, payload)
            end
        end
        return PluginHelpers.emitThrottled(self.id, emitFn, eventName, data, opts)
    else
        -- Direct emit (original behavior)
        if self._sandbox and self._sandbox.api then
            self._sandbox.api.emit(eventName, data)
        elseif EventBus then
            EventBus:emit(eventName, data)
        end
        return true
    end
end

-- ============================================
-- NEW API: Logging with redaction
-- ============================================

--- Log with optional secret redaction
-- @param level string Log level
-- @param msg string Message
-- @param fields table Optional fields (logged as key=value)
-- @param redact boolean Redact secrets (default true)
function Plugin:logSafe(level, msg, fields, redact)
    PluginHelpers.logSafe(Logger, self.shortId or self.id, level, msg, fields, redact)
end

-- ============================================
-- NEW API: Expose Objects (MQTT/HomeKit)
-- ============================================

--- Expose an object to MQTT and HomeKit
-- Creates a unified exposure that works with both MQTT discovery
-- and HomeKit accessories without requiring the plugin to know
-- about these integrations.
-- @param obj table Object to expose (must implement get(0) at minimum)
-- @param objType string Type: switch, light, dimmer, sensor, motion, temperature, humidity, cover, scene, number, button
-- @param options table Optional settings: name, path, id, readonly, hidden, mqtt, homekit, group
-- @return ExposedHandle Handle for managing the exposed object
-- @usage
--   local handle = plugin:expose(mySwitch, "switch", { name = "Living Room Light" })
--   handle:unexpose()  -- to remove later
function Plugin:expose(obj, objType, options)
    if not ExposedObjects then
        self:log("error", "ExposedObjects not available")
        return nil
    end

    options = options or {}

    -- Set plugin context for path generation
    ExposedObjects:setPluginContext(self.id)

    -- If no name provided, generate from path or plugin name
    if not options.name then
        options.name = (options.id or objType) .. " (" .. self.shortId .. ")"
    end

    -- Expose the object
    local handle = ExposedObjects:expose(obj, objType, options)

    -- Clear plugin context
    ExposedObjects:setPluginContext(nil)

    -- Track handle for cleanup
    if handle and handle:isExposed() then
        table.insert(self._exposedHandles, handle)
    end

    return handle
end

--- Unexpose an object by handle or path
-- @param handleOrPath ExposedHandle|string Handle returned by expose() or path string
-- @return boolean Success
function Plugin:unexpose(handleOrPath)
    if not ExposedObjects then
        return false
    end

    local handle
    if type(handleOrPath) == "string" then
        handle = ExposedObjects:get(handleOrPath)
    else
        handle = handleOrPath
    end

    if handle and handle.unexpose then
        handle:unexpose()

        -- Remove from tracked handles
        for i, h in ipairs(self._exposedHandles) do
            if h == handle then
                table.remove(self._exposedHandles, i)
                break
            end
        end

        return true
    end

    return false
end

--- Get list of exposed objects from this plugin
-- @return table Array of ExposedHandle
function Plugin:getExposed()
    local result = {}
    for _, handle in ipairs(self._exposedHandles) do
        if handle and handle:isExposed() then
            table.insert(result, handle)
        end
    end
    return result
end

-- ============================================
-- Sensor & Control Factory
-- ============================================

-- Internal storage for sensors/controls
Plugin._sensors = nil
Plugin._controls = nil

--- Create a read-only sensor object
-- @param id string Unique identifier for this sensor
-- @param getter function Function that returns current value
-- @return table Sensor object with get() interface
-- @usage
--   plugin:sensor("temperature", function() return state.temp end)
--   -- Later in user.lua:
--   expose(plugin:get("temperature"), "temperature", { name = "Temp" })
--- Emit OnChange(value, old) when the getter's value changed since last call.
-- Plugins call sensor:_notify() after updating their state.
local function notifyChange(self)
    local ok, value = pcall(self._getter)
    if not ok or value == self._notified then return end
    local old = self._notified
    self._notified = value
    self:emit("OnChange", value, old)
end

function Plugin:sensor(id, getter)
    if not self._sensors then self._sensors = {} end

    local sensor = {
        _type = "sensor",
        _id = self.id .. ":" .. id,
        _name = id,
        _getter = getter,
        _eventHandlers = {},

        get = function(self, feature)
            return self._getter()
        end,

        on = function(self, event, callback)
            if not self._eventHandlers[event] then
                self._eventHandlers[event] = {}
            end
            table.insert(self._eventHandlers[event], callback)
            return self
        end,

        emit = function(self, event, ...)
            local handlers = self._eventHandlers[event]
            if handlers then
                for _, cb in ipairs(handlers) do
                    pcall(cb, ...)
                end
            end
        end,

        onChange = function(self, callback)
            return self:on("OnChange", callback)
        end,

        _notify = notifyChange,
    }

    self._sensors[id] = sensor
    return sensor
end

--- Create a read-write control object
-- @param id string Unique identifier for this control
-- @param getter function Function that returns current value
-- @param setter function Function that sets value
-- @return table Control object with get()/set() interface
-- @usage
--   plugin:control("fanSpeed",
--       function() return state.fanSpeed end,
--       function(v) setFanSpeed(v) end
--   )
--   -- Later in user.lua:
--   expose(plugin:get("fanSpeed"), "number", { name = "Fan", min = 0, max = 4 })
function Plugin:control(id, getter, setter)
    if not self._controls then self._controls = {} end

    local control = {
        _type = "control",
        _id = self.id .. ":" .. id,
        _name = id,
        _getter = getter,
        _setter = setter,
        _eventHandlers = {},

        get = function(self, feature)
            return self._getter()
        end,

        set = function(self, feature, value)
            -- Support both set(value) and set(feature, value)
            -- If value is nil and feature is not a number, treat feature as the value
            if value == nil then
                value = feature
            end
            self._setter(value)
        end,

        on = function(self, event, callback)
            if not self._eventHandlers[event] then
                self._eventHandlers[event] = {}
            end
            table.insert(self._eventHandlers[event], callback)
            return self
        end,

        emit = function(self, event, ...)
            local handlers = self._eventHandlers[event]
            if handlers then
                for _, cb in ipairs(handlers) do
                    pcall(cb, ...)
                end
            end
        end,

        onChange = function(self, callback)
            return self:on("OnChange", callback)
        end,

        _notify = notifyChange,
    }

    self._controls[id] = control
    return control
end

--- Get a sensor or control by id
-- @param id string Sensor/control identifier
-- @return table Sensor or control object, or nil if not found
function Plugin:get(id)
    -- Plugin.get("@vclu/x") with a dot lands here with a string as self,
    -- because the class table doubles as the instance method table. Indexing a
    -- string yields nil rather than raising, so the mistake used to surface
    -- much later as a call on a nil plugin.
    if type(self) ~= "table" then
        error("Plugin.get is an instance method that returns a sensor. "
            .. "To fetch a plugin instance use Plugin.getPlugin(\""
            .. tostring(self) .. "\") instead.", 2)
    end
    if self._sensors and self._sensors[id] then
        return self._sensors[id]
    end
    if self._controls and self._controls[id] then
        return self._controls[id]
    end
    return nil
end

--- List all sensors and controls
-- @return table { sensors = {...}, controls = {...} }
function Plugin:listSensorsAndControls()
    local sensors = {}
    local controls = {}

    if self._sensors then
        for id, _ in pairs(self._sensors) do
            table.insert(sensors, id)
        end
    end
    if self._controls then
        for id, _ in pairs(self._controls) do
            table.insert(controls, id)
        end
    end

    return { sensors = sensors, controls = controls }
end

--- Get plugin statistics for monitoring
-- @return table Statistics including timers, sensors, controls, memory
-- @usage local stats = plugin:stats()
function Plugin:stats()
    -- Count timers (check both direct and sandbox)
    local timerCount = 0
    if self._sandbox and self._sandbox.timers then
        for _ in pairs(self._sandbox.timers) do
            timerCount = timerCount + 1
        end
    elseif self.timers then
        timerCount = #self.timers
    end

    -- Count subscriptions (check both direct and sandbox)
    local subCount = 0
    if self._sandbox then
        if self._sandbox.eventSubscriptions then
            subCount = subCount + #self._sandbox.eventSubscriptions
        end
        if self._sandbox.mqttSubscriptions then
            subCount = subCount + #self._sandbox.mqttSubscriptions
        end
    elseif self.subscriptions then
        subCount = #self.subscriptions
    end

    local stats = {
        id = self.id,
        name = self.name,
        version = self.version,
        timers = timerCount,
        sensors = 0,
        controls = 0,
        subscriptions = subCount,
        exposed = self._exposedHandles and #self._exposedHandles or 0,
    }

    -- Count sensors
    if self._sensors then
        for _ in pairs(self._sensors) do
            stats.sensors = stats.sensors + 1
        end
    end

    -- Count controls
    if self._controls then
        for _ in pairs(self._controls) do
            stats.controls = stats.controls + 1
        end
    end

    return stats
end

-- ============================================
-- NEW API: Key-Value Persistence
-- ============================================

--- Get a value from persistent storage
-- @param key string Key to retrieve
-- @param default any Default value if key not found or expired
-- @return any Value or default
-- @usage local cursor = plugin:kvGet("cursor", 0)
function Plugin:kvGet(key, default)
    if not __go_kv_get then
        self:log("warn", "KV store not available")
        return default
    end

    local value, exists = __go_kv_get(self.id, key)
    if not exists then
        return default
    end
    return value
end

--- Set a value in persistent storage
-- @param key string Key to store
-- @param value any Value to store (string, number, boolean, or table)
-- @param opts table Optional settings {secure=bool, ttl=seconds}
-- @return boolean Success
-- @return string|nil Error message
-- @usage plugin:kvSet("token", "abc123", {secure=true, ttl=3600})
function Plugin:kvSet(key, value, opts)
    if not __go_kv_set then
        self:log("warn", "KV store not available")
        return false, "KV store not available"
    end

    opts = opts or {}
    local secure = opts.secure or false
    local ttl = opts.ttl or 0

    local err = __go_kv_set(self.id, key, value, secure, ttl)
    if err then
        return false, err
    end
    return true, nil
end

--- Delete a key from persistent storage
-- @param key string Key to delete
-- @return boolean Success
-- @return string|nil Error message
-- @usage plugin:kvDelete("cache")
function Plugin:kvDelete(key)
    if not __go_kv_delete then
        self:log("warn", "KV store not available")
        return false, "KV store not available"
    end

    local err = __go_kv_delete(self.id, key)
    if err then
        return false, err
    end
    return true, nil
end

--- Check if a key exists (and is not expired)
-- @param key string Key to check
-- @return boolean Exists
-- @usage if plugin:kvHas("token") then ... end
function Plugin:kvHas(key)
    if not __go_kv_has then
        return false
    end
    return __go_kv_has(self.id, key)
end

--- List keys in persistent storage
-- @param prefix string Optional prefix to filter keys
-- @return table List of keys
-- @usage local keys = plugin:kvList("cache:")
function Plugin:kvList(prefix)
    if not __go_kv_list then
        return {}
    end
    return __go_kv_list(self.id, prefix or "")
end

--- Get all values from persistent storage
-- Secure values are replaced with "[SECURE]" placeholder
-- @return table Key-value pairs
-- @usage local store = plugin:kvGetAll()
function Plugin:kvGetAll()
    if not __go_kv_get_all then
        return {}
    end
    return __go_kv_get_all(self.id)
end

-- Initialize plugin from config (called by Go loader)
-- pluginId is the fullId (e.g., "@vclu/weather")
function Plugin.initFromConfig(pluginId, config)
    local instance = Plugin.instances[pluginId]
    if instance then
        instance:init(config)
    else
        Logger:warn("[PLUGIN] Cannot init unknown plugin: " .. pluginId)
    end
end

-- Get plugin instance by fullId or shortId
-- Note: Use Plugin.getPlugin() instead of Plugin.get() to avoid conflict with instance method
function Plugin.getPlugin(pluginId)
    -- Try fullId first
    if Plugin.instances[pluginId] then
        return Plugin.instances[pluginId]
    end
    -- Try to find by shortId
    for id, instance in pairs(Plugin.instances) do
        if instance.shortId == pluginId then
            return instance
        end
    end
    return nil
end

-- List all registered plugins
function Plugin.list()
    local result = {}
    for id, instance in pairs(Plugin.instances) do
        table.insert(result, {
            id = id,  -- fullId
            shortId = instance.shortId,
            namespace = instance.namespace,
            name = instance.name,
            version = instance.version,
            description = instance.description
        })
    end
    return result
end

-- Get stats for all plugins (for monitoring/debugging)
function Plugin.allStats()
    local result = {
        plugins = {},
        totals = {
            count = 0,
            timers = 0,
            sensors = 0,
            controls = 0,
            subscriptions = 0,
            exposed = 0,
        },
        lua = {
            memoryKB = (collectgarbage and collectgarbage("count")) or 0,
        }
    }

    for id, instance in pairs(Plugin.instances) do
        local stats = instance:stats()
        result.plugins[id] = stats
        result.totals.count = result.totals.count + 1
        result.totals.timers = result.totals.timers + stats.timers
        result.totals.sensors = result.totals.sensors + stats.sensors
        result.totals.controls = result.totals.controls + stats.controls
        result.totals.subscriptions = result.totals.subscriptions + stats.subscriptions
        result.totals.exposed = result.totals.exposed + stats.exposed
    end

    return result
end

Logger:info("[CORE] PluginAPI loaded")
return Plugin
