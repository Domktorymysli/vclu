--- Plugin Sandbox for vCLU.
-- Provides isolated execution environment for plugins with automatic resource tracking.
-- @module PluginSandbox

-- Load PluginHelpers
local PluginHelpers = require("PluginHelpers")

PluginSandbox = {}
PluginSandbox.sandboxes = {}  -- pluginId -> sandbox data

-- Read-only proxy for external registry objects (prevents plugin mutation)
local readOnlyProxies = setmetatable({}, {__mode = "k"})  -- weak-key: proxy -> source

local function readOnlyProxy(obj)
    if type(obj) ~= "table" then return obj end
    local proxy = setmetatable({}, {
        __index = function(_, key)
            local val = obj[key]
            if type(val) == "table" then
                return readOnlyProxy(val)
            end
            return val
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify external registry objects")
        end,
        __len = function() return #obj end,
    })
    readOnlyProxies[proxy] = obj
    return proxy
end

-- Overrides for pairs/ipairs/next to support read-only proxies in Lua 5.1
local originalPairs = pairs
local originalIpairs = ipairs
local originalNext = next

local function sandboxPairs(t)
    local source = readOnlyProxies[t]
    if source then return originalPairs(source) end
    return originalPairs(t)
end

local function sandboxIpairs(t)
    local source = readOnlyProxies[t]
    if source then return originalIpairs(source) end
    return originalIpairs(t)
end

local function sandboxNext(t, key)
    local source = readOnlyProxies[t]
    if source then return originalNext(source, key) end
    return originalNext(t, key)
end

--- Create a new sandbox for a plugin
-- @param pluginId string The full plugin ID (e.g., "@vclu/weather")
-- @return table The created sandbox
function PluginSandbox:create(pluginId)
    -- Pre-compute plugin namespace prefix (e.g., "plugins.vclu.weather")
    local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
    local pluginKey = shortId or pluginId
    if namespace then
        pluginKey = namespace .. "." .. pluginKey
    end

    local sandbox = {
        id = pluginId,
        pluginPrefix = "plugins." .. pluginKey,
        timers = {},              -- timerId -> true (for cleanup)
        eventSubscriptions = {},  -- {name, callback} pairs
        mqttSubscriptions = {},   -- {topic, callback} pairs
        registryObjects = {},     -- path -> true (for cleanup)
        dataObjects = {},         -- shortPath -> data (plugin-local state, not in global _registry)
        environment = nil,        -- will be set by buildEnvironment
    }

    -- Build wrapped API with automatic tracking
    sandbox.api = self:buildAPI(sandbox)

    -- Build isolated Lua environment
    sandbox.environment = self:buildEnvironment(sandbox)

    self.sandboxes[pluginId] = sandbox
    Logger:info("[SANDBOX] Created sandbox for: " .. pluginId)

    return sandbox
end

--- Build wrapped API functions with automatic resource tracking
-- @param sandbox table The sandbox instance
-- @return table API functions
function PluginSandbox:buildAPI(sandbox)
    local pluginId = sandbox.id
    local api = {}

    -- Wrapped setTimeout with tracking and pcall protection
    api.setTimeout = function(ms, callback)
        if type(__go_setTimeout) ~= "function" then
            Logger:error("[" .. pluginId .. "] setTimeout not available")
            return nil
        end
        local wrappedCallback = function(...)
            local ok, err = pcall(callback, ...)
            if not ok then
                Logger:error("[" .. pluginId .. "] Timer error: " .. tostring(err))
            end
        end

        local id = __go_setTimeout(wrappedCallback, ms)
        sandbox.timers[id] = true
        return id
    end

    -- Wrapped setInterval with tracking and pcall protection
    api.setInterval = function(ms, callback)
        if type(__go_setInterval) ~= "function" then
            Logger:error("[" .. pluginId .. "] setInterval not available")
            return nil
        end
        local wrappedCallback = function(...)
            local ok, err = pcall(callback, ...)
            if not ok then
                Logger:error("[" .. pluginId .. "] Interval error: " .. tostring(err))
            end
        end

        local id = __go_setInterval(wrappedCallback, ms)
        sandbox.timers[id] = true
        return id
    end

    -- Wrapped clearTimer
    api.clearTimer = function(id)
        if type(__go_clearTimer) == "function" then
            __go_clearTimer(id)
        end
        sandbox.timers[id] = nil
    end

    -- Wrapped EventBus:on with tracking
    api.on = function(eventName, callback)
        local wrappedCallback = function(...)
            local ok, err = pcall(callback, ...)
            if not ok then
                Logger:error("[" .. pluginId .. "] Event '" .. eventName .. "' error: " .. tostring(err))
            end
        end

        local bus = EventBus and EventBus:getShared() or nil
        if bus then
            bus:on(eventName, wrappedCallback)
            table.insert(sandbox.eventSubscriptions, {
                name = eventName,
                callback = wrappedCallback
            })
        end

        return wrappedCallback
    end

    -- Wrapped EventBus:emit (no tracking needed, just pcall protection)
    api.emit = function(eventName, ...)
        local bus = EventBus and EventBus:getShared() or nil
        if bus then
            bus:emit(eventName, ...)
        end
    end

    -- Wrapped MQTT subscribe with tracking
    api.mqttSubscribe = function(topic, callback)
        local wrappedCallback = function(t, payload)
            local ok, err = pcall(callback, t, payload)
            if not ok then
                Logger:error("[" .. pluginId .. "] MQTT '" .. topic .. "' error: " .. tostring(err))
            end
        end

        if MQTT and MQTT.subscribe then
            MQTT:subscribe(topic, wrappedCallback)
            table.insert(sandbox.mqttSubscriptions, {
                topic = topic,
                callback = wrappedCallback
            })
        end

        return wrappedCallback
    end

    -- Wrapped MQTT publish (no tracking needed)
    api.mqttPublish = function(topic, payload, retain, qos)
        if MQTT and MQTT.publish then
            local payloadStr = payload
            if type(payload) == "table" then
                payloadStr = JSON:encode(payload)
            end
            MQTT:publish(topic, payloadStr, retain or false, qos or 0)
        end
    end

    -- Create a plugin-local data object (stored in sandbox, not in global _registry)
    api.createObject = function(path, data)
        sandbox.dataObjects[path] = data
        Logger:debug("[SANDBOX] Created data object: " .. path)
        return data
    end

    -- Get a data object: sandbox-local first, then global _registry (read-only)
    api.getObject = function(path)
        -- Try sandbox-local data objects first (own namespace — writable)
        if sandbox.dataObjects[path] then
            return sandbox.dataObjects[path]
        end
        -- Try global _registry (external objects — read-only proxy). rawget and the table
        -- check: a name such as "list" is a registry method, not an object.
        local obj = _registry and rawget(_registry, path)
        if type(obj) == "table" then
            return readOnlyProxy(obj)
        end
        -- Try Registry:get (always external scope)
        if _ and _.get then
            obj = _:get(path)
            if type(obj) == "table" then return readOnlyProxy(obj) end
        end
        return nil
    end

    -- HTTP GET with pcall protection
    api.httpGet = function(url, callback)
        if HTTP then
            HTTP:asyncGet(url, function(response, err)
                local ok, callErr = pcall(callback, response, err)
                if not ok then
                    Logger:error("[" .. pluginId .. "] HTTP GET callback error: " .. tostring(callErr))
                end
            end)
        else
            callback(nil, "HTTP not available")
        end
    end

    -- HTTP POST with pcall protection
    api.httpPost = function(url, data, callback)
        if HTTP then
            HTTP:asyncPost(url, data, function(response, err)
                local ok, callErr = pcall(callback, response, err)
                if not ok then
                    Logger:error("[" .. pluginId .. "] HTTP POST callback error: " .. tostring(callErr))
                end
            end)
        else
            callback(nil, "HTTP not available")
        end
    end

    -- HTTP Request with full options (method, headers, body, timeout)
    api.httpRequest = function(options, callback)
        if not HTTP then
            callback(nil, "HTTP not available")
            return
        end

        local method = (options.method or "GET"):upper()
        local url = options.url
        local headers = options.headers or {}
        local body = options.body
        local timeout = options.timeout or 30000

        -- Create a new HTTP client instance for this request
        local client = HTTP:new(pluginId .. "-request")
        client:setTimeout(timeout)

        -- Set SSL verification (default true, can be disabled for self-signed certs)
        if options.verifySSL == false then
            client:setVerifySSL(false)
        end

        -- Set Content-Type via dedicated method (required for proper header handling)
        if headers["Content-Type"] then
            client:setContentType(headers["Content-Type"])
            headers["Content-Type"] = nil  -- Remove from headers to avoid duplicate
        end

        -- Set other custom headers
        for name, value in pairs(headers) do
            client:setHeader(name, value)
        end

        -- Make async request
        local requestMethod = method == "POST" and client.asyncPost or client.asyncGet
        local requestArgs = method == "POST" and {client, url, body} or {client, url}

        local asyncFunc = method == "POST"
            and function() client:asyncPost(url, body, function(response, err)
                    local ok, callErr = pcall(callback, response, err)
                    if not ok then
                        Logger:error("[" .. pluginId .. "] HTTP request callback error: " .. tostring(callErr))
                    end
                end)
            end
            or function() client:asyncGet(url, function(response, err)
                    local ok, callErr = pcall(callback, response, err)
                    if not ok then
                        Logger:error("[" .. pluginId .. "] HTTP request callback error: " .. tostring(callErr))
                    end
                end)
            end

        asyncFunc()
    end

    -- Logging helper
    api.log = function(level, message)
        local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
        local prefix = "[" .. (shortId or pluginId):upper() .. "] "

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

    -- ============================================
    -- NEW API: Helpers from PluginHelpers
    -- ============================================

    -- HTTP request with full options
    api.httpRequest = function(opts, callback)
        PluginHelpers.httpRequest(pluginId, opts, callback)
    end

    -- URL builder
    api.url = function(base, params)
        return PluginHelpers.buildUrl(base, params)
    end

    -- Auth helpers
    api.basicAuth = function(username, password)
        return PluginHelpers.basicAuth(username, password)
    end

    api.bearerAuth = function(token)
        return PluginHelpers.bearerAuth(token)
    end

    -- JSON helpers
    api.decodeJson = function(str)
        return PluginHelpers.decodeJson(str)
    end

    api.encodeJson = function(data)
        return PluginHelpers.encodeJson(data)
    end

    -- Coercion helpers
    api.coerceNumber = function(val, default)
        return PluginHelpers.coerceNumber(val, default)
    end

    api.coerceString = function(val, default)
        return PluginHelpers.coerceString(val, default)
    end

    api.coerceBool = function(val, default)
        return PluginHelpers.coerceBool(val, default)
    end

    -- Poller
    api.createPoller = function(name, opts)
        return PluginHelpers.createPoller(pluginId, name, opts)
    end

    -- Update a plugin-local data object (stored in sandbox, not in global _registry)
    api.updateObject = function(path, patch)
        local obj = sandbox.dataObjects[path]
        if not obj then
            return false, "updateObject: object '" .. path .. "' does not exist. Use createObject() first."
        end
        for k, v in pairs(patch) do
            obj[k] = v
        end
        obj._lastUpdate = os.time()
        return true, nil
    end

    api.upsertObject = function(path, data)
        local obj = sandbox.dataObjects[path]
        if obj then
            for k, v in pairs(data) do
                obj[k] = v
            end
            obj._lastUpdate = os.time()
        else
            data._lastUpdate = os.time()
            data._created = os.time()
            sandbox.dataObjects[path] = data
            obj = data
        end
        return obj
    end

    -- Event emit with throttling
    api.emitThrottled = function(event, data, opts)
        local emitFn = function(ev, payload)
            if EventBus and EventBus.emit then
                EventBus:emit(ev, payload)
            end
        end
        return PluginHelpers.emitThrottled(pluginId, emitFn, event, data, opts)
    end

    -- Log with redaction
    api.logSafe = function(level, msg, fields, redact)
        local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
        PluginHelpers.logSafe(Logger, shortId or pluginId, level, msg, fields, redact)
    end

    return api
end

--- Build isolated Lua environment for the plugin
-- @param sandbox table The sandbox instance
-- @return table The environment table
function PluginSandbox:buildEnvironment(sandbox)
    local api = sandbox.api

    -- Safe subset of os library
    local safeOs = {
        time = os.time,
        date = os.date,
        difftime = os.difftime,
        clock = os.clock,
    }

    local env = {
        -- Standard Lua functions (safe)
        print = print,
        pairs = sandboxPairs,
        ipairs = sandboxIpairs,
        next = sandboxNext,
        type = type,
        tostring = tostring,
        tonumber = tonumber,
        select = select,
        unpack = unpack or table.unpack,
        pcall = pcall,
        xpcall = xpcall,
        error = error,
        assert = assert,

        -- Standard libraries (safe, immutable copies)
        string = string,
        table = table,
        math = math,
        os = safeOs,

        -- vCLU API (wrapped with tracking)
        setTimeout = api.setTimeout,
        setInterval = api.setInterval,
        clearTimeout = api.clearTimer,
        clearInterval = api.clearTimer,

        -- Global references (read-only)
        Logger = Logger,
        JSON = JSON,
        HttpClient = HTTP,  -- HTTP is the global from require("HttpClient")
        HTTP = HTTP,
        MQTT = MQTT,
        Modbus = Modbus,

        -- Wrapped Plugin that injects sandbox's fullId
        Plugin = setmetatable({}, {
            __index = function(_, key)
                if key == "new" then
                    return function(_, shortId, metadata)
                        -- Create instance with forced fullId from sandbox
                        local instance = setmetatable({}, Plugin)
                        instance.id = sandbox.id  -- Force fullId from sandbox
                        instance.shortId = shortId

                        -- Parse namespace from fullId
                        if sandbox.id:sub(1, 1) == "@" then
                            local slashPos = sandbox.id:find("/")
                            if slashPos then
                                instance.namespace = sandbox.id:sub(2, slashPos - 1)
                                instance.shortId = sandbox.id:sub(slashPos + 1)
                            end
                        end

                        instance.name = metadata.name or shortId
                        instance.version = metadata.version or "0.0.0"
                        instance.description = metadata.description or ""
                        instance.config = {}
                        instance.timers = {}
                        instance.subscriptions = {}
                        instance._initCallback = nil
                        instance._cleanupCallback = nil
                        instance._sandbox = sandbox
                        instance._isPlugin = true

                        -- Store in global Plugin.instances
                        Plugin.instances[sandbox.id] = instance

                        -- Register in global _registry so _:get("@vclu/weather") works
                        if _registry then
                            _registry[sandbox.id] = instance
                        end

                        Logger:info("[PLUGIN] Registered: " .. instance.name .. " v" .. instance.version .. " (" .. sandbox.id .. ")")
                        return instance
                    end
                end
                -- Delegate other calls to global Plugin
                return Plugin[key]
            end
        }),
        EventBus = setmetatable({}, {
            __index = function(_, key)
                if key == "on" then return api.on end
                if key == "emit" then return api.emit end
                return nil
            end
        }),

        -- Plugin-specific variables (set by loader)
        __plugin_id = sandbox.id,
        __plugin_config = {},
        __plugin_path = "",

        -- BLOCKED - dangerous functions
        -- os.execute, io.*, loadfile, dofile, load, rawset, rawget, debug, getfenv, setfenv
    }

    -- Registry access with write protection
    env._ = setmetatable({}, {
        __index = function(_, key)
            if key == "get" then
                return function(_, path) return api.getObject(path) end
            end
            if key == "value" then
                return function(_, path)
                    if type(path) ~= "string" then
                        error("_:value: path must be a string, got " .. type(path), 2)
                    end
                    local obj = api.getObject(path)
                    if type(obj) ~= "table" or type(obj.getValue) ~= "function" then return nil end
                    return obj:getValue()
                end
            end
            -- Other registry methods are not given to plugins: sync changes state, and
            -- byTag/find hand out writable objects.
            if _registry and type(_registry[key]) == "function" then
                return function()
                    error("_:" .. key .. " is not available in a plugin; use _:get or _:value", 2)
                end
            end
            return nil
        end
    })

    -- Read-only access to global _registry (all objects wrapped in read-only proxy)
    env._registry = setmetatable({}, {
        __index = function(_, key)
            if not _registry then return nil end
            local obj = _registry[key]
            if obj == nil then return nil end
            if type(obj) ~= "table" then return obj end
            return readOnlyProxy(obj)
        end,
        __newindex = function(_, key, value)
            error("Read-only: plugins cannot modify _registry directly. Use createObject/upsertObject instead.")
        end
    })

    -- Metatable to block access to undefined globals
    setmetatable(env, {
        __index = function(_, key)
            -- Allow access to explicitly defined items only
            return nil
        end,
        __newindex = function(t, key, value)
            -- Allow creating local variables in the environment
            rawset(t, key, value)
        end
    })

    return env
end

--- Execute code in a plugin's sandbox
-- @param pluginId string The plugin ID
-- @param code string The Lua code to execute
-- @return any, string Result and error message (if any)
function PluginSandbox:execute(pluginId, code)
    local sandbox = self.sandboxes[pluginId]
    if not sandbox then
        return nil, "Sandbox not found for: " .. pluginId
    end

    -- Compile code (Lua 5.1 uses loadstring, Lua 5.2+ uses load)
    local loadFunc = loadstring or load
    local fn, compileErr = loadFunc(code, pluginId)
    if not fn then
        Logger:error("[SANDBOX] Compilation error in " .. pluginId .. ": " .. tostring(compileErr))
        return nil, "Compilation error: " .. tostring(compileErr)
    end

    -- Set isolated environment (Lua 5.1 uses setfenv, Lua 5.2+ uses load's 4th param)
    if setfenv then
        setfenv(fn, sandbox.environment)
    end

    -- Execute with pcall protection
    local ok, result = pcall(fn)
    if not ok then
        Logger:error("[SANDBOX] Runtime error in " .. pluginId .. ": " .. tostring(result))
        return nil, "Runtime error: " .. tostring(result)
    end

    return result, nil
end

--- Set environment variables for a plugin
-- @param pluginId string The plugin ID
-- @param config table The plugin configuration
-- @param path string The plugin's local path
function PluginSandbox:setConfig(pluginId, config, path)
    local sandbox = self.sandboxes[pluginId]
    if not sandbox then return end

    sandbox.environment.__plugin_config = config or {}
    sandbox.environment.__plugin_path = path or ""
end

--- Cleanup all resources for a plugin
-- @param pluginId string The plugin ID
function PluginSandbox:cleanup(pluginId)
    local sandbox = self.sandboxes[pluginId]
    if not sandbox then
        Logger:warn("[SANDBOX] No sandbox found for cleanup: " .. pluginId)
        return
    end

    Logger:info("[SANDBOX] Cleaning up plugin: " .. pluginId)

    -- 1. Clear all timers
    local timerCount = 0
    for timerId, _ in pairs(sandbox.timers) do
        if type(__go_clearTimer) == "function" then
            __go_clearTimer(timerId)
        end
        timerCount = timerCount + 1
    end
    if timerCount > 0 then
        Logger:debug("[SANDBOX] Cleared " .. timerCount .. " timers")
    end

    -- 2. Remove event subscriptions
    local eventCount = 0
    for _, sub in ipairs(sandbox.eventSubscriptions) do
        local bus = EventBus and EventBus:getShared() or nil
        if bus and bus.off then
            bus:off(sub.name, sub.callback)
            eventCount = eventCount + 1
        end
    end
    if eventCount > 0 then
        Logger:debug("[SANDBOX] Removed " .. eventCount .. " event subscriptions")
    end

    -- 3. Remove MQTT subscriptions
    local mqttCount = 0
    for _, sub in ipairs(sandbox.mqttSubscriptions) do
        if MQTT and MQTT.unsubscribe then
            MQTT:unsubscribe(sub.topic, sub.callback)
            mqttCount = mqttCount + 1
        end
    end
    if mqttCount > 0 then
        Logger:debug("[SANDBOX] Removed " .. mqttCount .. " MQTT subscriptions")
    end

    -- 4. Remove legacy registry objects + clear data objects
    local regCount = 0
    for path, _ in pairs(sandbox.registryObjects) do
        if _registry then
            _registry[path] = nil
            regCount = regCount + 1
        end
    end
    if regCount > 0 then
        Logger:debug("[SANDBOX] Removed " .. regCount .. " registry objects")
    end
    sandbox.dataObjects = {}

    -- 5. Clear throttle state for this plugin
    PluginHelpers.clearThrottleState(pluginId)

    -- 6. Clear environment reference
    sandbox.environment = nil

    -- 7. Remove sandbox
    self.sandboxes[pluginId] = nil

    Logger:info("[SANDBOX] Cleanup complete for: " .. pluginId)
end

--- Check if a sandbox exists for a plugin
-- @param pluginId string The plugin ID
-- @return boolean
function PluginSandbox:exists(pluginId)
    return self.sandboxes[pluginId] ~= nil
end

--- Get sandbox info for debugging
-- @param pluginId string The plugin ID
-- @return table Info about sandbox resources
function PluginSandbox:getInfo(pluginId)
    local sandbox = self.sandboxes[pluginId]
    if not sandbox then return nil end

    local timerCount = 0
    for _ in pairs(sandbox.timers) do timerCount = timerCount + 1 end

    return {
        id = sandbox.id,
        timers = timerCount,
        events = #sandbox.eventSubscriptions,
        mqtt = #sandbox.mqttSubscriptions,
        registryObjects = sandbox.registryObjects,
        dataObjects = sandbox.dataObjects
    }
end

Logger:info("[CORE] PluginSandbox loaded")
return PluginSandbox
