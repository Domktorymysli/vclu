--- Plugin Sandbox for vCLU.
-- Provides isolated execution environment for plugins with automatic resource tracking.
-- @module PluginSandbox

-- Load PluginHelpers
local PluginHelpers = require("PluginHelpers")

PluginSandbox = {}
PluginSandbox.sandboxes = {}  -- pluginId -> sandbox data

-- Nothing a plugin can reach may be a shared mutable table: it gets copies, read-only
-- proxies of objects it did not create, and facades whose methods are bound to the
-- real object. A proxy keeps its source in the metatable, which a plugin cannot read.
local unpackArgs = unpack or table.unpack

local function sourceOf(t)
    local mt = type(t) == "table" and getmetatable(t)
    return type(mt) == "table" and rawget(mt, "__sandboxSource") or nil
end

-- Applies f to every result of a call, keeping trailing nils.
local function mapResults(f, ...)
    local n = select("#", ...)
    if n == 0 then return end
    local out = { ... }
    for i = 1, n do out[i] = f(out[i]) end
    return unpackArgs(out, 1, n)
end

local function shallowCopy(t)
    local c = {}
    for k, v in pairs(t) do c[k] = v end
    return c
end

-- Methods a plugin may call on an object it did not create: reads only.
local READ_DENY = {
    getShared = true, getInstance = true, getInstanceIds = true, getConfig = true,
    getKey = true, getOutbox = true, getRequestLog = true,
}
local function isReadMethod(name)
    if type(name) ~= "string" or READ_DENY[name] then return false end
    return name == "get" or name:match("^get%u") ~= nil or name:match("^is%u") ~= nil
        or name:match("^has%u") ~= nil
end

local function isPluginInstance(obj)
    return type(obj) == "table" and rawget(obj, "_isPlugin") == true and getmetatable(obj) == Plugin
end

local readOnlyProxy

-- Another plugin as a plugin sees it: its sensors and controls, read-only. Not its
-- config, KV store or sandbox.
local function pluginFacade(obj)
    return {
        id = obj.id, shortId = obj.shortId, namespace = obj.namespace,
        name = obj.name, version = obj.version,
        get = function(_, id) return readOnlyProxy(obj:get(id)) end,
        listSensorsAndControls = function() return obj:listSensorsAndControls() end,
    }
end

-- Read-only proxy for external registry objects: fields read, tables proxied in turn,
-- read methods called on the real object, every other method refused.
readOnlyProxy = function(obj)
    if type(obj) ~= "table" or sourceOf(obj) then return obj end
    if isPluginInstance(obj) then return pluginFacade(obj) end
    local proxy
    local methods = {}
    proxy = setmetatable({}, {
        __sandboxSource = obj,
        __index = function(_, key)
            local val = obj[sourceOf(key) or key]
            if type(val) == "function" then
                local m = methods[key]
                if m then return m end
                if isReadMethod(key) then
                    m = function(self, ...)
                        if self ~= proxy then
                            error("Read-only: call " .. tostring(key) .. " as a method of its object", 2)
                        end
                        self = obj
                        for i = 1, select("#", ...) do
                            local t = type((select(i, ...)))
                            if t ~= "number" and t ~= "string" and t ~= "boolean" and t ~= "nil" then
                                error("Read-only: " .. tostring(key) .. " takes numbers, strings and booleans from a plugin", 2)
                            end
                        end
                        return mapResults(readOnlyProxy, val(self, ...))
                    end
                else
                    m = function()
                        error("Read-only: a plugin cannot call " .. tostring(key)
                            .. " on an object it did not create (only get, getX, isX, hasX)", 2)
                    end
                end
                methods[key] = m
                return m
            end
            return readOnlyProxy(val)
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify external registry objects", 2)
        end,
        __len = function() return #obj end,
    })
    return proxy
end

-- A shared runtime object (Logger, JSON, an expose handle) whose methods a plugin may
-- call but whose fields it may not replace.
local function boundFacade(obj)
    local facade
    local methods = {}
    local function out(r)
        if r == obj then return facade end
        if type(r) == "table" and getmetatable(r) ~= nil then return readOnlyProxy(r) end
        return r
    end
    facade = setmetatable({}, {
        __sandboxSource = obj,
        __index = function(_, key)
            local val = obj[key]
            if type(val) == "function" then
                local m = methods[key]
                if not m then
                    m = function(self, ...)
                        if self ~= facade then
                            error("call " .. tostring(key) .. " as a method of its object", 2)
                        end
                        return mapResults(out, val(obj, ...))
                    end
                    methods[key] = m
                end
                return m
            end
            return readOnlyProxy(val)
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify a shared object", 2)
        end,
    })
    return facade
end

-- Only the named functions of a shared object, bound to it: no fields, no constructors
local function boundFunctions(obj, names)
    local t = {}
    local fns = {}
    for _, name in ipairs(names) do
        local f = obj[name]
        if type(f) == "function" then
            fns[name] = function(self, ...)
                if self == t then return f(obj, ...) end
                return f(obj, self, ...)
            end
        end
    end
    return setmetatable(t, {
        __index = fns,
        __newindex = function()
            error("Read-only: plugins cannot modify a shared object", 2)
        end,
    })
end

-- pairs/ipairs/next over a proxy go through the proxy, never hand out raw values
local originalPairs = pairs
local originalIpairs = ipairs
local originalNext = next

-- A key leaves like a value: a table as a read-only proxy, a function not at all
local function sandboxNext(t, key)
    local source = sourceOf(t)
    if not source then return originalNext(t, key) end
    local k = originalNext(source, sourceOf(key) or key)
    while type(k) == "function" do k = originalNext(source, k) end
    if k == nil then return nil end
    return readOnlyProxy(k), t[k]
end

local function sandboxPairs(t)
    if sourceOf(t) then return sandboxNext, t, nil end
    return originalPairs(t)
end

local function proxyIpairsStep(t, i)
    i = i + 1
    if sourceOf(t)[i] == nil then return nil end
    return i, t[i]
end

local function sandboxIpairs(t)
    if sourceOf(t) then return proxyIpairsStep, t, 0 end
    return originalIpairs(t)
end

-- What leaves the sandbox for Go (JSON) or a module (an event) must not carry proxies,
-- which look empty there: the sources go instead, copied into the plugin's own tables.
local function plain(v, seen)
    if type(v) ~= "table" then return v end
    local src = sourceOf(v)
    if src then return src end
    seen = seen or {}
    if seen[v] then return seen[v] end
    seen[v] = v
    local copy
    for k, val in originalPairs(v) do
        local pv = plain(val, seen)
        if pv ~= val and not copy then
            copy = shallowCopy(v)
            seen[v] = copy
        end
        if copy then copy[k] = pv end
    end
    return copy or v
end

-- The plugin's table library: raw functions read through a proxy, and do not write one
local function sandboxTable()
    local t = shallowCopy(table)
    t.concat = function(list, ...)
        return table.concat(sourceOf(list) or list, ...)
    end
    for _, name in ipairs({ "insert", "remove", "sort" }) do
        local f = table[name]
        t[name] = function(list, ...)
            if sourceOf(list) then
                error("Read-only: table." .. name .. " on an object the plugin did not create", 2)
            end
            return f(list, ...)
        end
    end
    return t
end

local function sandboxUnpack(list, ...)
    local src = sourceOf(list)
    if not src then return unpackArgs(list, ...) end
    return mapResults(readOnlyProxy, unpackArgs(src, ...))
end

-- Plugin class functions that are not instance methods
local PLUGIN_STATIC = { new = true, getPlugin = true, list = true, allStats = true, initFromConfig = true }
-- Fields of its own instance a plugin may not see (the sandbox, raw handles) or replace
local INSTANCE_HIDDEN = { _sandbox = true, _exposedHandles = true, timers = true, subscriptions = true }
local INSTANCE_PROTECTED = {
    id = true, shortId = true, namespace = true, _sandbox = true, _isPlugin = true, _exposedHandles = true,
    timers = true, subscriptions = true, init = true, cleanup = true, stats = true,
}
-- Called by the loader and the sandbox on the real instance, not by the plugin
local INSTANCE_CORE_ONLY = { init = true, cleanup = true }

-- The plugin's own instance as the plugin holds it. The real instance (in
-- Plugin.instances, for modules) stays out of its reach: methods are called on it,
-- and its identity cannot be changed to act as another plugin.
local function instanceProxy(inner)
    local proxy = {}
    local methods = {}
    local function out(r)
        if r == inner then return proxy end
        if type(r) == "table" and getmetatable(r) ~= nil and not sourceOf(r) then
            return boundFacade(r)
        end
        return r
    end
    local overrides = {
        getExposed = function()
            local result = {}
            for i, h in ipairs(Plugin.getExposed(inner)) do result[i] = boundFacade(h) end
            return result
        end,
        -- a path only under the plugin's id, not a CLU's or another plugin's
        expose = function(obj, objType, options)
            local prefix = inner.id .. ":"
            local existing = ExposedObjects and ExposedObjects._objectToPath[obj]
            if existing and existing:sub(1, #prefix) ~= prefix then
                error("Read-only: " .. existing .. " was exposed by a module; a plugin cannot change it", 2)
            end
            local opts = {}
            for k, v in originalPairs(options or {}) do opts[k] = v end
            if opts.path ~= nil and (type(opts.path) ~= "string" or opts.path:sub(1, #prefix) ~= prefix) then
                opts.path = prefix .. tostring(opts.path)
            end
            local handle = Plugin.expose(inner, obj, objType, opts)
            if type(handle) == "table" then return boundFacade(handle) end
            return handle
        end,
        -- only its own handles, never another module's expose path
        unexpose = function(handleOrPath)
            local wanted = sourceOf(handleOrPath) or handleOrPath
            for _, h in ipairs(inner._exposedHandles or {}) do
                if h == wanted or (type(wanted) == "string" and h:getPath() == wanted) then
                    return Plugin.unexpose(inner, h)
                end
            end
            return false
        end,
    }
    setmetatable(proxy, {
        __index = function(_, key)
            if INSTANCE_HIDDEN[key] then return nil end
            local val = rawget(inner, key)
            if val ~= nil then return val end
            local m = methods[key]
            if m then return m end
            local f = Plugin[key]
            if type(f) ~= "function" then
                if type(f) == "table" then return nil end
                return f
            end
            if PLUGIN_STATIC[key] or INSTANCE_CORE_ONLY[key] then return nil end
            local override = overrides[key]
            m = function(self, ...)
                if self ~= proxy then
                    error("Plugin method " .. key .. " called on another object", 2)
                end
                if inner._sandbox and inner._sandbox.dead then
                    error("plugin " .. tostring(inner.id) .. " is disabled", 2)
                end
                if override then return override(...) end
                return mapResults(out, f(inner, ...))
            end
            methods[key] = m
            return m
        end,
        __newindex = function(_, key, value)
            if INSTANCE_PROTECTED[key] then
                error("Plugin field " .. tostring(key) .. " is read-only", 2)
            end
            -- modules and the core call these on the real instance (create, init, get):
            -- the plugin's code gets its proxy as self
            if type(value) == "function" then
                local f = value
                value = function(s, ...)
                    if s == inner then s = proxy end
                    return f(s, ...)
                end
            end
            inner[key] = value
        end,
    })
    return proxy
end

--- Create a new sandbox for a plugin
-- @param pluginId string The full plugin ID (e.g., "@vclu/weather")
-- @param permissions table|nil {lan, mqtt, modbus} from plugin.json; nil grants none
-- @return table The created sandbox
function PluginSandbox:create(pluginId, permissions)
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
        instance = nil,           -- the real Plugin instance, set by Plugin:new
        instanceProxy = nil,      -- the instance as the plugin holds it
        pollers = {},             -- created through the API, stopped on cleanup
        dead = false,             -- set by cleanup
    }
    permissions = type(permissions) == "table" and permissions or {}
    sandbox.permissions = {
        lan = permissions.lan == true,
        mqtt = permissions.mqtt == true,
        modbus = permissions.modbus == true,
    }
    -- Events a plugin emits are "<prefix>:<name>". An official plugin's prefix is its
    -- short id or the id's first word (sun-position: "sun"), any other's its full id,
    -- so it cannot pass for another integration (telegram:command, gpio.*).
    if permissions.official == true and shortId then
        sandbox.eventPrefixes = { shortId .. ":", (shortId:match("^([^-]+)-") or shortId) .. ":" }
    else
        sandbox.eventPrefixes = { pluginId .. ":" }
    end
    -- requests of a plugin carry its id as the HTTP instance (PluginHelpers.httpRequest)
    if type(__go_http_set_lan) == "function" then
        __go_http_set_lan(pluginId, sandbox.permissions.lan)
    end

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

        local id = __go_setTimeout(wrappedCallback, ms, "plugin " .. pluginId)
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

        local id = __go_setInterval(wrappedCallback, ms, "plugin " .. pluginId)
        sandbox.timers[id] = true
        return id
    end

    -- Wrapped clearTimer: only the plugin's own timers
    api.clearTimer = function(id)
        if not sandbox.timers[id] then return end
        if type(__go_clearTimer) == "function" then
            __go_clearTimer(id)
        end
        sandbox.timers[id] = nil
    end

    -- Wrapped EventBus:on with tracking
    api.on = function(eventName, callback)
        -- modules' listeners get the same payload: the plugin may not change it
        local wrappedCallback = function(...)
            local ok, err = pcall(callback, mapResults(readOnlyProxy, ...))
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

    local function checkEventName(eventName)
        if type(eventName) == "string" then
            for _, prefix in ipairs(sandbox.eventPrefixes) do
                if eventName:sub(1, #prefix) == prefix then return end
            end
        end
        error("plugin " .. pluginId .. " may emit only events starting with "
            .. sandbox.eventPrefixes[1] .. ", not " .. tostring(eventName), 3)
    end

    api.checkEvent = function(eventName) checkEventName(eventName) end

    -- Wrapped EventBus:emit (no tracking needed, just pcall protection)
    api.emit = function(eventName, ...)
        checkEventName(eventName)
        local bus = EventBus and EventBus:getShared() or nil
        if bus then
            bus:emit(eventName, mapResults(plain, ...))
        end
    end

    local function requireMqtt()
        if not sandbox.permissions.mqtt then
            error("MQTT needs \"mqtt\" in the permissions of " .. pluginId .. "'s plugin.json", 2)
        end
    end

    -- Wrapped MQTT subscribe with tracking
    api.mqttSubscribe = function(topic, callback)
        requireMqtt()
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
        requireMqtt()
        if MQTT and MQTT.publish then
            local payloadStr = payload
            if type(payload) == "table" then
                payloadStr = JSON:encode(plain(payload))
            end
            MQTT:publish(topic, payloadStr, qos or 0, retain or false)
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
        if type(obj) ~= "table" and _ and _.get then
            -- Try Registry:get (always external scope)
            obj = _:get(path)
        end
        if type(obj) ~= "table" then return nil end
        if obj == sandbox.instance then return sandbox.instanceProxy end
        return readOnlyProxy(obj)
    end

    -- GET/POST go through httpRequest like every other plugin request: the plugin's own
    -- HTTP instance, with its LAN permission
    local function alive() return not sandbox.dead end

    local function simpleRequest(opts, callback)
        PluginHelpers.httpRequest(pluginId, opts, function(response, err)
            local ok, callErr = pcall(callback, response, err)
            if not ok then
                Logger:error("[" .. pluginId .. "] HTTP " .. opts.method .. " callback error: " .. tostring(callErr))
            end
        end, alive)
    end

    api.httpGet = function(url, callback)
        simpleRequest({ method = "GET", url = url }, callback)
    end

    api.httpPost = function(url, data, callback)
        local opts = { method = "POST", url = url }
        if type(data) == "table" then opts.json = data else opts.body = data end
        simpleRequest(opts, callback)
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
        PluginHelpers.httpRequest(pluginId, opts, callback, alive)
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
        return PluginHelpers.encodeJson(plain(data))
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

    -- Poller: its timers are its own, so the sandbox stops it on cleanup
    api.createPoller = function(name, opts)
        local poller = PluginHelpers.createPoller(pluginId, name, opts)
        table.insert(sandbox.pollers, poller)
        local start = poller.start
        poller.start = function(self, ...)
            if sandbox.dead then return self end
            return start(self, ...)
        end
        return boundFacade(poller)
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
        checkEventName(event)
        local emitFn = function(ev, payload)
            if sandbox.dead then return end
            checkEventName(ev)
            local bus = EventBus and EventBus:getShared() or nil
            if bus then bus:emit(ev, plain(payload)) end
        end
        return PluginHelpers.emitThrottled(pluginId, emitFn, event, data, opts)
    end

    -- Log with redaction
    api.logSafe = function(level, msg, fields, redact)
        local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
        PluginHelpers.logSafe(Logger, shortId or pluginId, level, msg, fields, redact)
    end

    -- A disabled plugin's code may still be called (a sensor getter, a pending
    -- callback); it starts nothing new
    for name, f in originalPairs(api) do
        api[name] = function(...)
            if sandbox.dead then
                error("plugin " .. pluginId .. " is disabled", 2)
            end
            return f(...)
        end
    end

    return api
end

--- Build isolated Lua environment for the plugin
-- @param sandbox table The sandbox instance
-- @return table The environment table
function PluginSandbox:buildEnvironment(sandbox)
    local api = sandbox.api
    local pluginId = sandbox.id

    -- Safe subset of os library
    local safeOs = {
        time = os.time,
        date = os.date,
        difftime = os.difftime,
        clock = os.clock,
    }

    -- datetime in the house's zone, without setTimezone: the zone is the house's, not a plugin's
    local safeDatetime = {}
    if datetime then
        for _, k in ipairs({ 'parse', 'now', 'fromTimestamp', 'format', 'diff', 'add', 'timezone', 'between' }) do
            safeDatetime[k] = datetime[k]
        end
    end

    -- Modbus only with the permission; without it a clear error instead of nil
    local modbus
    if sandbox.permissions.modbus and Modbus then
        modbus = shallowCopy(Modbus)
    else
        modbus = setmetatable({}, {
            __index = function()
                error("Modbus needs \"modbus\" in the permissions of " .. pluginId .. "'s plugin.json", 2)
            end,
        })
    end

    -- The plugin's view of the Plugin class: its own instance, other plugins read-only
    local function external(obj)
        if obj == nil then return nil end
        if obj == sandbox.instance then return sandbox.instanceProxy end
        return readOnlyProxy(obj)
    end
    local classFields = {
        apiVersion = Plugin.apiVersion,
        new = function(_, shortId, metadata)
            if sandbox.instanceProxy then
                Logger:warn("[PLUGIN] " .. pluginId .. " called Plugin:new again; returning its instance")
                return sandbox.instanceProxy
            end
            metadata = metadata or {}
            local instance = setmetatable({}, Plugin)
            instance.id = pluginId  -- Force fullId from sandbox
            instance.shortId = shortId

            -- Parse namespace from fullId
            if pluginId:sub(1, 1) == "@" then
                local slashPos = pluginId:find("/")
                if slashPos then
                    instance.namespace = pluginId:sub(2, slashPos - 1)
                    instance.shortId = pluginId:sub(slashPos + 1)
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
            instance._exposedHandles = {}
            instance._sandbox = sandbox
            instance._isPlugin = true

            sandbox.instance = instance
            sandbox.instanceProxy = instanceProxy(instance)

            -- Store in global Plugin.instances
            Plugin.instances[pluginId] = instance

            -- Register in global _registry so _:get("@vclu/weather") works
            if _registry then
                _registry[pluginId] = instance
            end

            Logger:info("[PLUGIN] Registered: " .. instance.name .. " v" .. instance.version .. " (" .. pluginId .. ")")
            return sandbox.instanceProxy
        end,
        getPlugin = function(id)
            return external(Plugin.getPlugin(id))
        end,
    }
    local pluginClass = setmetatable({}, {
        __index = function(_, key)
            local v = classFields[key]
            if v ~= nil then return v end
            error("Plugin." .. tostring(key) .. " is not available in a plugin", 2)
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify Plugin", 2)
        end,
    })

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
        unpack = sandboxUnpack,
        pcall = pcall,
        xpcall = xpcall,
        error = error,
        assert = assert,

        -- Standard libraries: the plugin's own copies, so a change stays in the plugin
        string = shallowCopy(string),
        table = sandboxTable(),
        math = shallowCopy(math),
        os = safeOs,
        datetime = safeDatetime,

        -- vCLU API (wrapped with tracking)
        setTimeout = api.setTimeout,
        setInterval = api.setInterval,
        clearTimeout = api.clearTimer,
        clearInterval = api.clearTimer,

        -- Shared objects: methods callable, fields not replaceable. HTTP and MQTT go
        -- through the plugin API (plugin:httpRequest, plugin:mqttSubscribe).
        Logger = boundFunctions(Logger, { "debug", "info", "warn", "error" }),
        JSON = boundFunctions({
            encode = function(_, data, ...) return JSON:encode(plain(data), ...) end,
            decode = function(_, ...) return JSON:decode(...) end,
        }, { "encode", "decode" }),
        Modbus = modbus,

        Plugin = pluginClass,
        EventBus = setmetatable({}, {
            __index = function(_, key)
                if key == "on" then return api.on end
                if key == "emit" then return api.emit end
                return nil
            end,
            __newindex = function()
                error("Read-only: plugins cannot modify EventBus", 2)
            end,
        }),

        -- Plugin-specific variables (set by loader)
        __plugin_id = pluginId,
        __plugin_config = {},
        __plugin_path = "",

        -- BLOCKED - dangerous functions
        -- os.execute, io.*, loadfile, dofile, load, rawset, rawget, debug, getfenv, setfenv,
        -- setmetatable, getmetatable, require, HTTP, MQTT
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
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify _", 2)
        end,
    })

    -- Read-only access to global _registry (objects proxied, registry functions withheld)
    env._registry = setmetatable({}, {
        __index = function(_, key)
            if not _registry then return nil end
            local obj = rawget(_registry, key)
            if type(obj) == "table" then return external(obj) end
            if type(obj) == "function" then return nil end
            return obj
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
    sandbox.dead = true

    -- 0. Pollers, exposed objects and the registry entry of its instance
    for _, poller in ipairs(sandbox.pollers) do
        pcall(poller.stop, poller)
    end
    sandbox.pollers = {}
    local instance = sandbox.instance
    if instance then
        for _, handle in ipairs(instance._exposedHandles or {}) do
            pcall(handle.unexpose, handle)
        end
        instance._exposedHandles = {}
        if ExposedObjects and ExposedObjects.unexposeByPlugin then
            pcall(ExposedObjects.unexposeByPlugin, ExposedObjects, pluginId)
        end
        if _registry and rawget(_registry, pluginId) == instance then
            _registry[pluginId] = nil
        end
        if Plugin.instances[pluginId] == instance then
            Plugin.instances[pluginId] = nil
        end
    end

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
