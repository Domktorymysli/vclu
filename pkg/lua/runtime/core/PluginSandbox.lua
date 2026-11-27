--- Plugin Sandbox for vCLU.
-- Provides isolated execution environment for plugins with automatic resource tracking.
-- @module PluginSandbox

-- Load PluginHelpers
local PluginHelpers = require("PluginHelpers")

PluginSandbox = {}
PluginSandbox.sandboxes = {}  -- pluginId -> sandbox data
PluginSandbox.ioHandles = {}  -- Go handle id -> {call} of a plugin's socket, port or transfer

-- Data for plugins' sockets, ports and transfers (pkg/lua/plugin_io.go): udp (bytes, ip,
-- port), serial (bytes), either (nil, err) once when it fails; tftp (bytes, err) once;
-- tcp ("connect" | "data" | "close", bytes | reason); ws ("open" | "message" | "close",
-- protocol | message | reason, binary | code).
function __vclu_plugin_io(id, kind, data, second, port)
    local h = PluginSandbox.ioHandles[id]
    if not h then return end
    if kind == "tcp" or kind == "ws" then
        if second == "close" then
            PluginSandbox.ioHandles[id] = nil
            h.sandbox.ioHandles[id] = nil
        end
        if h.call then h.call(second, data, port) end
        return
    end
    if kind == "tftp" or data == nil then -- a transfer's answer, or a socket or port that failed
        PluginSandbox.ioHandles[id] = nil
        h.sandbox.ioHandles[id] = nil
    end
    if h.call then
        h.call(data, second, port)
    end
end

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

-- A library as a plugin gets it: its functions and constants, no table of the real one
-- (string.__index is the string library itself)
local function libCopy(t)
    local c = {}
    for k, v in pairs(t) do
        if type(v) ~= "table" then c[k] = v end
    end
    return c
end

-- The string library as a plugin gets it: each call's time is the plugin's, also a pattern
-- that backtracks for seconds in one Go call (as ("").find is for everyone)
local function timedLib(t)
    local c = libCopy(t)
    if type(__go_plugin_timed) == "function" then
        for k, f in pairs(c) do
            if type(f) == "function" then c[k] = __go_plugin_timed(f) end
        end
    end
    return c
end

-- Taken at load: a module's own global bit or a wrapped Crypto does not reach plugins
local CRYPTO, BIT = libCopy(Crypto), libCopy(bit)
-- lib/telnet: pure functions over the connection a plugin gives it
local TELNET = require("lib/telnet")

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

-- A plugin's instance proxy -> its real instance: another plugin gets the facade of it
local instanceOf = setmetatable({}, { __mode = "k" })
-- What a module holds of a plugin (a handle, the instance's facade) -> { obj, inner }
local handleInfo = setmetatable({}, { __mode = "k" })
local handleOf = setmetatable({}, { __mode = "k" })     -- plugin object -> the module's handle of it
-- a plugin's object the core met (exposed, read by another plugin) -> its plugin
local objectOwner = setmetatable({}, { __mode = "k" })
local toModule, toPlugin, moduleHandle

local function ownerOf(obj)
    local h = handleOf[obj]
    if h then return handleInfo[h].inner end
    return objectOwner[obj]
end

-- Another plugin as a plugin sees it: its sensors and controls, read-only. Not its
-- config, KV store or sandbox.
local function pluginFacade(obj, owner)
    return {
        id = obj.id, shortId = obj.shortId, namespace = obj.namespace,
        name = obj.name, version = obj.version,
        get = function(_, id)
            local s = obj:get(id)
            if type(s) == "table" and not objectOwner[s] then objectOwner[s] = obj end
            return readOnlyProxy(s, owner)
        end,
        listSensorsAndControls = function() return obj:listSensorsAndControls() end,
    }
end

-- A function of an object the plugin did not create never leaves raw (through a proxy it
-- is a method, refused unless a read)
local function keptFunction()
    error("Read-only: a function of an object the plugin did not create cannot be taken out of it", 2)
end

-- Results of a read as the plugin gets them: tables proxied for the same owner
local function ownedResults(owner, ...)
    local n = select("#", ...)
    if n == 0 then return end
    local out = { ... }
    for i = 1, n do
        if type(out[i]) == "function" then out[i] = keptFunction else out[i] = readOnlyProxy(out[i], owner) end
    end
    return unpackArgs(out, 1, n)
end

-- A read method of a proxy. A request to a CLU under it is owner's (false: not known): Go finds
-- this function on the stack and reads owner (__go_clu_caller, PluginSandbox.checkCluRequest)
local function readMethod(proxy, obj, key, val, owner)
    return function(self, ...)
        if self ~= proxy then
            error("Read-only: call " .. tostring(key) .. " as a method of its object", 2)
        end
        for i = 1, select("#", ...) do
            local t = type((select(i, ...)))
            if t ~= "number" and t ~= "string" and t ~= "boolean" and t ~= "nil" then
                error("Read-only: " .. tostring(key) .. " takes numbers, strings and booleans from a plugin", 2)
            end
        end
        return ownedResults(owner, val(obj, ...))
    end
end
if type(__go_proxy_read_method) == "function" then
    __go_proxy_read_method(readMethod(nil, nil, nil, nil, false))
end

-- Read-only proxy for external registry objects: fields read, tables proxied in turn,
-- read methods called on the real object, every other method refused. owner: the plugin
-- it was handed to, when known.
readOnlyProxy = function(obj, owner)
    if type(obj) ~= "table" then return obj end
    owner = owner or false
    if instanceOf[obj] then return pluginFacade(instanceOf[obj], owner) end
    local held = handleInfo[obj]
    if held then
        if held.obj == held.inner then return pluginFacade(held.inner, owner) end
        obj = held.obj -- a module's handle of a plugin's object: read-only here
    end
    local src = sourceOf(obj)
    if src then
        local mt = getmetatable(obj)
        -- another plugin's proxy reads as the plugin that reached it
        if rawget(mt, "__sandboxReadOnly") and rawget(mt, "__sandboxOwner") == owner then return obj end
        obj = src -- a facade with methods of another plugin's object: read-only here
    end
    if isPluginInstance(obj) then return pluginFacade(obj, owner) end
    local proxy
    local methods = {}
    proxy = setmetatable({}, {
        __sandboxSource = obj,
        __sandboxReadOnly = true,
        __sandboxOwner = owner,
        __index = function(_, key)
            -- an object's _fields are its own state, not for another plugin
            if type(key) == "string" and key:sub(1, 1) == "_" then return nil end
            local val = obj[sourceOf(key) or key]
            if type(val) == "function" then
                local m = methods[key]
                if m then return m end
                if isReadMethod(key) then
                    m = readMethod(proxy, obj, key, val, owner)
                else
                    m = function()
                        error("Read-only: a plugin cannot call " .. tostring(key)
                            .. " on an object it did not create (only get, getX, isX, hasX)", 2)
                    end
                end
                methods[key] = m
                return m
            end
            return readOnlyProxy(val, owner)
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify external registry objects", 2)
        end,
        __len = function() return #obj end,
    })
    return proxy
end

-- A shared runtime object (Logger, JSON, an expose handle) whose methods a plugin may
-- call but whose fields it may not replace. deny: method name -> function that refuses;
-- owner: the plugin it was handed to.
local function boundFacade(obj, deny, owner)
    local facade
    local methods = {}
    local function out(r)
        if r == obj then return facade end
        if type(r) == "table" and getmetatable(r) ~= nil then return readOnlyProxy(r, owner) end
        return r
    end
    facade = setmetatable({}, {
        __sandboxSource = obj,
        __index = function(_, key)
            if deny and deny[key] then return deny[key] end
            -- its _fields are the core's (what the user set on a handle, a poller's timers)
            if type(key) == "string" and key:sub(1, 1) == "_" then return nil end
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
            return readOnlyProxy(val, owner)
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

-- The values of the plugin's secrets become *** in what it logs; shorter than 4 characters
-- they would mask ordinary words
-- a short secret (a PIN) masked where it stands alone, not inside a longer word or number
local function maskShort(v, secret)
    local out, from = {}, 1
    while true do
        local i, j = v:find(secret, from, true)
        if not i then break end
        local before, after = v:sub(i - 1, i - 1), v:sub(j + 1, j + 1)
        if before:match("%w") or after:match("%w") then
            out[#out + 1] = v:sub(from, j)
        else
            out[#out + 1] = v:sub(from, i - 1) .. "***"
        end
        from = j + 1
    end
    out[#out + 1] = v:sub(from)
    return table.concat(out)
end

local function maskSecrets(sandbox, v)
    if type(v) ~= "string" or not sandbox.secrets then return v end
    for _, secret in pairs(sandbox.secrets) do
        if type(secret) == "string" and #secret >= 4 then
            v = v:gsub(secret:gsub("[%^%$%(%)%%%.%[%]%*%+%-%?]", "%%%0"), "***")
        elseif type(secret) == "string" and secret ~= "" then
            v = maskShort(v, secret)
        end
    end
    return v
end

local function maskedLogger(sandbox)
    local out = {}
    for _, level in ipairs({ "debug", "info", "warn", "error" }) do
        -- formatted first: a secret among the arguments of "%s" is masked too
        out[level] = function(_, msg, ...)
            return Logger[level](Logger, maskSecrets(sandbox, Logger:format(msg, ...)))
        end
    end
    return out
end

local function topicLevels(topic)
    local levels = {}
    for level in (topic .. "/"):gmatch("([^/]*)/") do levels[#levels + 1] = level end
    return levels
end

-- filterCovers: every topic the requested filter matches, the allowed one matches too
-- (shelly/# covers shelly/+/status and shelly itself; + does not cover #)
local function filterCovers(allowed, requested)
    local a, r = topicLevels(allowed), topicLevels(requested)
    for i = 1, #a do
        if a[i] == "#" then return true end
        local level = r[i]
        if level == nil or level == "#" then return false end
        if a[i] ~= "+" and a[i] ~= level then return false end
    end
    return #r == #a
end

-- topicAllowed: the topic (or filter) is covered by one of the allowed filters
local function topicAllowed(allowed, topic)
    if type(topic) ~= "string" or topic == "" then return false end
    for _, f in ipairs(allowed) do
        if type(f) == "string" and filterCovers(f, topic) then return true end
    end
    return false
end

-- The callback the shared MQTT client calls for topic, nil when none
local function mqttHolder(topic)
    local subs = MQTT and MQTT._subscriptions
    if type(subs) ~= "table" or type(topic) ~= "string" then return nil end
    local full = MQTT._applyPrefix and MQTT:_applyPrefix(topic) or topic
    return subs[full] and subs[full].callback
end

local function ownsMqtt(sandbox, callback)
    for _, sub in ipairs(sandbox.mqttSubscriptions) do
        if sub.callback == callback then return true end
    end
    return false
end

-- A shared class (HTTP, MQTT) as plugin API 1 holds it: fns, callable with or without the
-- class as self, nothing else of the real one
local function legacyClass(name, fns)
    local facade
    facade = setmetatable({}, {
        __index = function(_, key)
            local f = fns[key]
            if f then
                return function(self, ...)
                    if self == facade then return f(...) end
                    return f(self, ...)
                end
            end
            error(name .. "." .. tostring(key) .. " is not available in a plugin; use the plugin API (plugin:httpRequest, plugin:mqttSubscribe)", 2)
        end,
        __newindex = function()
            error("Read-only: plugins cannot modify a shared object", 2)
        end,
    })
    return facade
end

-- pairs/ipairs/next over a proxy go through the proxy, never hand out raw values
local originalPairs = pairs
local originalIpairs = ipairs
local originalNext = next

-- A key leaves like a value: a table as a read-only proxy, a function or a _field not at all
local function sandboxNext(t, key)
    local source = sourceOf(t)
    if not source then return originalNext(t, key) end
    local function hidden(k)
        return type(k) == "function" or type(k) == "string" and k:sub(1, 1) == "_"
    end
    local k = originalNext(source, sourceOf(key) or key)
    while hidden(k) do k = originalNext(source, k) end
    if k == nil then return nil end
    return readOnlyProxy(k, rawget(getmetatable(t), "__sandboxOwner")), t[k]
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
-- JSON of an object the plugin did not create: a copy without its _fields, as the proxy shows it
local function visible(t, seen)
    if seen[t] then return seen[t] end
    local c = {}
    seen[t] = c
    for k, v in originalPairs(t) do
        if type(k) ~= "string" or k:sub(1, 1) ~= "_" then
            c[k] = type(v) == "table" and visible(sourceOf(v) or v, seen) or v
        end
    end
    return c
end

-- vis: a table for JSON, which the plugin can read back (so without the _fields of a proxy
-- or a facade). Always a copy: an event's payload is the plugin's no more once emitted.
-- inner: the emitting plugin, whose functions in a payload cross as a module's boundary functions
local function plain(v, seen, vis, inner)
    if type(v) == "function" and inner then return toModule(v, inner) end
    if type(v) ~= "table" then return v end
    local src = sourceOf(v)
    if src then
        if vis then return visible(src, vis) end
        return src
    end
    seen = seen or {}
    if seen[v] then return seen[v] end
    local copy = {}
    seen[v] = copy
    for k, val in originalPairs(v) do copy[k] = plain(val, seen, vis, inner) end
    return copy
end

-- The plugin's table library: raw functions read through a proxy, and do not write one
local function sandboxTable()
    local t = libCopy(table)
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
    return ownedResults(rawget(getmetatable(list), "__sandboxOwner"), unpackArgs(src, ...))
end

-- Plugin class functions that are not instance methods
local PLUGIN_STATIC = { new = true, getPlugin = true, _lookup = true, list = true, allStats = true, initFromConfig = true }
-- Fields of its own instance a plugin may not see (the sandbox, raw handles) or replace
local INSTANCE_HIDDEN = { _sandbox = true, _exposedHandles = true, timers = true, subscriptions = true }
local INSTANCE_PROTECTED = {
    id = true, shortId = true, namespace = true, _sandbox = true, _isPlugin = true, _exposedHandles = true,
    timers = true, subscriptions = true, init = true, cleanup = true, stats = true,
    getExposed = true, -- the user's module sets remote access on what it returns
    dashboard = true, -- in a module it is the access builder of the core (moduleFacade)
}
-- Called by the loader and the sandbox on the real instance, not by the plugin
local INSTANCE_CORE_ONLY = { init = true, cleanup = true }

-- The plugin's own instance as the plugin holds it. The real instance (in
-- Plugin.instances, for modules) stays out of its reach: methods are called on it,
-- and its identity cannot be changed to act as another plugin.
-- On API 2 who reaches a plugin's device from the cloud is set by the user's module
local HANDLE_USER_ONLY = {}
for _, name in ipairs({ "remote", "roles", "pin", "confirm" }) do
    HANDLE_USER_ONLY[name] = function()
        error("expose:" .. name .. " is the user's to set, not the plugin's: a module calls it on the handle "
            .. "(Plugin.getPlugin(id):getExposed() or its own expose of the plugin's object)", 2)
    end
end

local function instanceProxy(inner)
    local proxy = {}
    instanceOf[proxy] = inner
    local methods = {}
    local function handleFacade(h)
        local api2 = not inner._sandbox or inner._sandbox.apiVersion >= 2
        return boundFacade(h, api2 and HANDLE_USER_ONLY or nil, inner.id)
    end
    local function out(r)
        if r == inner then return proxy end
        if type(r) == "table" and getmetatable(r) ~= nil and not sourceOf(r) then
            return boundFacade(r, nil, inner.id)
        end
        return r
    end
    local overrides = {
        getExposed = function()
            local result = {}
            for i, h in ipairs(Plugin.getExposed(inner)) do result[i] = handleFacade(h) end
            return result
        end,
        -- a path only under the plugin's id, not a CLU's or another plugin's
        expose = function(obj, objType, options)
            local prefix = inner.id .. ":"
            local paths = ExposedObjects and ExposedObjects._objectToPath
            -- a module exposes the plugin's object as its handle
            local existing = paths and (paths[obj] or (handleOf[obj] and paths[handleOf[obj]]))
            if existing and existing:sub(1, #prefix) ~= prefix then
                error("Read-only: " .. existing .. " was exposed by a module; a plugin cannot change it", 2)
            end
            local opts = {}
            for k, v in originalPairs(options or {}) do opts[k] = v end
            if inner._sandbox and inner._sandbox.apiVersion >= 2 and opts.path ~= nil and (type(opts.path) ~= "string" or opts.path:sub(1, #prefix) ~= prefix) then
                opts.path = prefix .. tostring(opts.path)
            end
            local handle = Plugin.expose(inner, obj, objType, opts)
            -- its own object under its path: a module reaches it as a handle (Registry.get)
            if type(obj) == "table" and getmetatable(obj) == nil and not objectOwner[obj] then objectOwner[obj] = inner end
            if type(handle) == "table" then return handleFacade(handle) end
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

-- ============================================
-- What a module gets of a plugin (API 2): plain data as copies, objects with functions as
-- handles, its functions called back later. One Lua state per plugin keeps this contract.
-- ============================================

local BOUNDARY_DEPTH = 32
local facadeOf = setmetatable({}, { __mode = "k" })     -- instance -> its facade
local callbackOf = setmetatable({}, { __mode = "k" })   -- instance -> { module function -> plugin side }
local boundaryFnOf = setmetatable({}, { __mode = "k" }) -- instance -> { plugin function -> module side }
-- a plugin object's fields the core reads (a sensor's type, id and name for expose)
local HANDLE_FIELDS = { _type = true, _id = true, _name = true, _isPlugin = true }
local handleSeq = 0

-- A handle of a stopped plugin (disabled, suspended, reloaded) does nothing
local function stoppedCheck(inner, level)
    local sb = inner._sandbox
    if (sb and sb.dead) or Plugin.instances[inner.id] ~= inner then
        error(tostring(inner.id) .. ": stopped", level or 3)
    end
end

-- Module functions a plugin calls run after it, in order. A plugin may have this many
-- waiting: a loop of calls stops at the plugin, not in the modules' time.
local LATER_LIMIT = 10000
local laterQueue, laterArmed, laterPending = {}, false, {}
local function runLater()
    laterArmed = false
    local q = laterQueue
    laterQueue, laterPending = {}, {}
    for i = 1, #q do
        local ok, err = pcall(q[i].fn, unpackArgs(q[i].args, 1, q[i].args.n))
        if not ok and Logger then Logger:warn("[PLUGIN] a module callback given to " .. q[i].from .. ": " .. tostring(err)) end
    end
end
local function later(from, fn, args)
    if type(__go_setTimeout) ~= "function" then -- a state without timers: now
        pcall(fn, unpackArgs(args, 1, args.n))
        return
    end
    local pending = (laterPending[from] or 0) + 1
    if pending > LATER_LIMIT then
        error(tostring(from) .. ": " .. LATER_LIMIT .. " calls to modules are pending; let them run first", 3)
    end
    laterPending[from] = pending
    laterQueue[#laterQueue + 1] = { fn = fn, args = args, from = from }
    if not laterArmed then
        laterArmed = true
        __go_setTimeout(runLater, 0)
    end
end

local function copyTable(v, inner, depth, seen, conv)
    if depth > BOUNDARY_DEPTH then error("data between a module and " .. tostring(inner.id) .. " is nested deeper than " .. BOUNDARY_DEPTH, 0) end
    if seen[v] then error("data between a module and " .. tostring(inner.id) .. " has a cycle", 0) end
    seen[v] = true
    local c = {}
    for k, val in originalPairs(v) do c[conv(k, inner, depth + 1, seen)] = conv(val, inner, depth + 1, seen) end
    seen[v] = nil
    return c
end

-- numbers, strings, booleans and nil cross as they are: no table for the common call
local function allSimple(...)
    for i = 1, select("#", ...) do
        local t = type((select(i, ...)))
        if t == "table" or t == "function" then return false end
    end
    return true
end

local function toPluginArgs(inner, ...)
    if allSimple(...) then return ... end
    local n = select("#", ...)
    local out = { ... }
    for i = 1, n do out[i] = toPlugin(out[i], inner, 0) end
    return unpackArgs(out, 1, n)
end

local function toModuleResults(inner, ...)
    if allSimple(...) then return ... end
    local n = select("#", ...)
    local out = { ... }
    for i = 1, n do out[i] = toModule(out[i], inner, 0) end
    return unpackArgs(out, 1, n)
end

-- A value from a module (or the core, through a handle) as the plugin gets it
toPlugin = function(v, inner, depth, seen)
    local t = type(v)
    if t == "function" then
        local byFn = callbackOf[inner]
        if not byFn then
            byFn = setmetatable({}, { __mode = "k" })
            callbackOf[inner] = byFn
        end
        local cb = byFn[v]
        if not cb then
            local fn = v
            cb = function(...)
                local n = select("#", ...)
                local args = { n = n }
                for i = 1, n do args[i] = toModule((select(i, ...)), inner, 0) end
                later(inner.id, fn, args)
            end
            byFn[v] = cb
        end
        return cb
    end
    if t ~= "table" then return v end
    local held = handleInfo[v]
    if held then
        if held.inner ~= inner then
            error("an object of " .. tostring(held.inner.id) .. " cannot be given to " .. tostring(inner.id)
                .. ": give its path or plain data", 0)
        end
        if held.obj == inner then return (inner._sandbox and inner._sandbox.instanceProxy) or inner end
        return held.obj
    end
    -- a house object (registry, CLU) reaches the plugin read-only: give a path to act on it
    if getmetatable(v) ~= nil then return readOnlyProxy(v, inner.id) end
    return copyTable(v, inner, depth or 0, seen or {}, toPlugin)
end

-- A value from the plugin as a module gets it
toModule = function(v, inner, depth, seen)
    local t = type(v)
    if t == "function" then
        -- per plugin: a library function (math, Telnet) is the same value in every sandbox
        local byFn = boundaryFnOf[inner]
        if not byFn then
            byFn = setmetatable({}, { __mode = "k" })
            boundaryFnOf[inner] = byFn
        end
        local f = byFn[v]
        if not f then
            local fn = v
            f = function(...)
                stoppedCheck(inner)
                return toModuleResults(inner, fn(toPluginArgs(inner, ...)))
            end
            byFn[v] = f
        end
        return f
    end
    if t ~= "table" then return v end
    if handleInfo[v] then return v end
    if v == inner or instanceOf[v] == inner then return PluginSandbox.moduleFacade(inner) end
    local src = sourceOf(v)
    if src then
        -- a house object the plugin read: the module's own; a plugin's object: that plugin's handle
        local owner = ownerOf(src)
        if owner then return moduleHandle(src, owner) end
        return src
    end
    if getmetatable(v) ~= nil then return moduleHandle(v, inner) end
    for _, val in originalPairs(v) do
        if type(val) == "function" then return moduleHandle(v, inner) end
    end
    return copyTable(v, inner, depth or 0, seen or {}, toModule)
end

-- A plugin's object as a module holds it: its methods, its plain fields as copies, never its
-- _fields; the same object is the same handle
moduleHandle = function(obj, inner)
    local h = handleOf[obj]
    if h then return h end
    handleSeq = handleSeq + 1
    local label = "<" .. tostring(inner.id) .. " handle " .. handleSeq .. ">"
    -- methods found once are a plain lookup; fields are read each time
    local methods = setmetatable({}, {
        __index = function(cache, key)
            local val = obj[key]
            local private = type(key) == "string" and key:sub(1, 1) == "_"
            if type(val) == "function" then
                if private and key ~= "_notify" then return nil end
                local m = function(self, ...)
                    stoppedCheck(inner)
                    local fn = obj[key]
                    if self == h then return toModuleResults(inner, fn(obj, toPluginArgs(inner, ...))) end
                    return toModuleResults(inner, fn(toPluginArgs(inner, self, ...)))
                end
                rawset(cache, key, m)
                return m
            end
            if HANDLE_FIELDS[key] then return val end
            if private then return nil end
            stoppedCheck(inner)
            return toModule(val, inner)
        end,
    })
    h = setmetatable({}, {
        __index = methods,
        -- what a module (or the core: history, its path) sets stays on the handle, never in the
        -- plugin's object; only the core's note of a sensor's reported value goes through
        __newindex = function(t, key, value)
            if key == "_notified" then
                obj._notified = value
                return
            end
            rawset(t, key, value)
        end,
        __tostring = function() return label end,
    })
    handleOf[obj] = h
    handleInfo[h] = { obj = obj, inner = inner }
    return h
end

-- The plugin as Plugin.getPlugin gives it to a module: its own methods, the core's a module
-- may call, its name and version; no other field (config, sandbox, store, timers, its state)
local FACADE_FIELDS = { id = true, shortId = true, namespace = true, name = true, version = true,
    description = true, _isPlugin = true }
-- the core's methods: "raw" results are the core's (expose handles), "handle" go through toModule
local FACADE_CORE = { get = "handle", on = "handle", getExposed = "raw",
    listSensorsAndControls = "raw", stats = "raw" }

-- A plugin's own code never grants a dashboard: no frame on the stack runs in a plugin's
-- environment (every frame, so pcall or a callback in between hides none); level is the caller's
local function refuseFromPlugin(level, where)
    local envs = {}
    for _, sb in pairs(PluginSandbox.sandboxes or {}) do
        if sb.environment then envs[sb.environment] = true end
    end
    for i = 2, 200 do
        local ok, env = pcall(getfenv, i + 1) -- +1: pcall's own frame
        if not ok then break end
        if envs[env] then
            error(where .. ": who opens a plugin's dashboard is the user's: a module sets it, not a plugin", level + 1)
        end
    end
end

-- Plugin.getPlugin(id):dashboard(id) in a module: the access builder of a declared dashboard
local function moduleDashboard(inner, id)
    refuseFromPlugin(3, "dashboard")
    local declared = inner._sandbox and inner._sandbox.declaredDashboards or {}
    if type(id) ~= "string" or not declared[id] then
        error(tostring(inner.id) .. ":dashboard: the plugin declares no dashboard " .. tostring(id)
            .. ' (plugin.json "dashboards")', 3)
    end
    return Dashboard._accessBuilder(inner.id, id, function(where) refuseFromPlugin(4, where) end)
end

function PluginSandbox.moduleFacade(inner)
    local f = facadeOf[inner]
    if f then return f end
    local function asMethod(key, self)
        if self ~= f then error("call " .. tostring(key) .. " as a method: plugin:" .. tostring(key) .. "(...)", 3) end
        stoppedCheck(inner, 4)
    end
    -- the core's error as the module's: at the line that called it
    local function coreCall(core, ...)
        local r = { n = select("#", ...), pcall(core, inner, ...) }
        if not r[1] then
            local msg = r[2]
            if type(msg) == "string" then msg = msg:gsub("^[^\n]-%.lua:%d+: ", "", 1) end
            error(msg, 3)
        end
        return unpackArgs(r, 2, table.maxn(r))
    end
    local methods = setmetatable({}, {
        __index = function(cache, key)
            if FACADE_FIELDS[key] then return rawget(inner, key) end
            stoppedCheck(inner)
            local m
            local own = rawget(inner, key)
            local private = type(key) == "string" and key:sub(1, 1) == "_"
            if key == "dashboard" then
                m = function(self, id)
                    asMethod(key, self)
                    return moduleDashboard(inner, id)
                end
            elseif type(own) == "function" and not private then
                m = function(self, ...)
                    asMethod(key, self)
                    return toModuleResults(inner, rawget(inner, key)(inner, toPluginArgs(inner, ...)))
                end
            elseif own == nil and FACADE_CORE[key] then
                local how, core = FACADE_CORE[key], Plugin[key]
                m = function(self, ...)
                    asMethod(key, self)
                    if how == "raw" then
                        return mapResults(function(r) if r == inner then return f end return r end, coreCall(core, ...))
                    end
                    return toModuleResults(inner, coreCall(core, toPluginArgs(inner, ...)))
                end
            elseif own ~= nil then
                return nil
            elseif type(Plugin[key]) == "function" then
                m = function()
                    error(tostring(inner.id) .. ":" .. tostring(key) .. " is the plugin's own; a module uses its own "
                        .. "(EventBus:getShared(), its KV, its timers)", 2)
                end
            else
                return nil
            end
            rawset(cache, key, m)
            return m
        end,
    })
    f = setmetatable({}, {
        __index = methods,
        __newindex = function()
            error("Read-only: Plugin.getPlugin(\"" .. tostring(inner.id) .. "\") is the plugin's; a module sets nothing on it", 2)
        end,
        __tostring = function() return "<plugin " .. tostring(inner.id) .. ">" end,
    })
    facadeOf[inner] = f
    handleInfo[f] = { obj = inner, inner = inner }
    return f
end

local function api2(inner)
    local sb = inner._sandbox
    return sb and sb.apiVersion and sb.apiVersion >= 2
end

-- What Plugin.getPlugin and _:get give a module: on API 2 a plugin's facade and, of an
-- object a plugin exposed under its path, its handle; else the object itself
function PluginSandbox.moduleView(v)
    if type(v) ~= "table" then return v end
    if rawget(v, "_isPlugin") then
        if v._sandbox and api2(v) then return PluginSandbox.moduleFacade(v) end
        return v
    end
    local owner = ownerOf(v)
    if owner and api2(owner) then return moduleHandle(v, owner) end
    return v
end

-- The object behind a module's facade or handle (the core's own lookups)
function PluginSandbox.instanceBehind(v)
    local held = handleInfo[v]
    if held then return held.obj end
    return v
end

-- ============================================
-- Apps: a plugin's own dashboards and named actions
-- ============================================

local function pack(...) return { n = select("#", ...), ... } end

-- Widget methods that reach the house by name (areas, covers by path, the house calendar and
-- its schedules, metrics): a plugin's dashboard shows only what the plugin gives it
local DASH_DENY = { new = true, area = true, floor = true, room = true, covers = true, calendar = true,
    schedule = true, chart = true, bars = true }

-- An error raised in the core names the plugin's call, not the core's file
local function coreError(e)
    return (tostring(e):gsub("^[^\n:]-%.lua:%d+: ", ""))
end

-- Who may open a plugin's dashboard is the user's, granted from a module (Plugin.getPlugin(id):dashboard(id))
local DASH_USER = { pin = true, remote = true, roles = true }
local DASH_PLUGIN_AUTH = { session = true, pin = true }

-- Refuses an id held by a module's dashboard or another plugin's
local function dashboardFree(pluginId, id)
    local d = _dashboards and _dashboards[id]
    if d == nil or d._plugin == pluginId then return end
    local owner = d._plugin and ("plugin " .. d._plugin) or "a module"
    error("plugin:dashboard: dashboard " .. id .. " already exists (" .. owner .. ")", 0)
end

-- The builder as a plugin holds it: the chain API of Dashboard.new, functions it passes run
-- as its callbacks (errors in its log), the raw builder and sections out of its reach.
local function dashboardFacade(sandbox, raw, log)
    local pluginId = sandbox.id
    local facades = {}
    local wrapArg, out

    local function toFacade(v)
        if type(v) ~= "table" then return v end
        if facades[v] then return facades[v] end
        if rawget(v, "_builder") == raw then return out(v) end
        return v
    end

    local function wrapFn(fn)
        local last
        return function(...)
            if sandbox.dead then error("plugin " .. pluginId .. " is disabled", 0) end
            local res = pack(pcall(fn, mapResults(toFacade, ...)))
            if not res[1] then
                local msg = maskSecrets(sandbox, tostring(res[2]))
                if msg ~= last then -- a getter failing on every poll is logged once
                    last = msg
                    log:error("[" .. pluginId .. "] Dashboard " .. raw._id .. " error: " .. msg)
                end
                error(msg, 0)
            end
            last = nil
            return unpackArgs(res, 2, res.n)
        end
    end

    local function holdsFn(v, depth)
        if type(v) == "function" then return true end
        if type(v) ~= "table" or getmetatable(v) ~= nil or depth > 3 then return false end
        for _, x in originalPairs(v) do
            if holdsFn(x, depth + 1) then return true end
        end
        return false
    end

    -- tables holding functions (opts, fns) are copied with them wrapped; data stays the plugin's
    wrapArg = function(v, depth)
        depth = depth or 0
        if type(v) == "function" then return wrapFn(v) end
        if not holdsFn(v, depth) then return v end
        local copy = {}
        for k, x in originalPairs(v) do copy[k] = wrapArg(x, depth + 1) end
        return copy
    end

    local function finish()
        dashboardFree(pluginId, raw._id)
        Dashboard._applyAccess(raw)
        local ok, err = pcall(raw.build, raw)
        if not ok then error(coreError(err), 3) end
        sandbox.dashboards[raw._id] = true
        return facades[raw]
    end

    local function userOnly(name)
        local how = name == "pin" and ':pin("1234")' or name == "roles" and ':remote():roles(...)' or ":remote()"
        error("dashboard:" .. name .. " is the user's to grant, not the plugin's: a module calls "
            .. 'Plugin.getPlugin("' .. pluginId .. '"):dashboard("' .. raw._pluginDash .. '")' .. how, 3)
    end

    -- auth and name are the plugin's defaults; what the user granted stays over them
    local function ownSetting(target, name, v)
        if name == "auth" then
            if not DASH_PLUGIN_AUTH[v] then
                error('dashboard:auth: a plugin sets "session" or "pin"; "public" and the app are the user\'s to grant '
                    .. '(Plugin.getPlugin("' .. pluginId .. '"):dashboard(id):auth(...) in a module)', 3)
            end
            raw._pluginOwn.auth = v
        else
            if type(v) ~= "string" or v == "" then error("dashboard:" .. name .. ": a text", 3) end
            raw._pluginOwn[name] = v
        end
        Dashboard._applyAccess(raw)
        return facades[target]
    end

    local function methodsOf(target)
        local f = {}
        local meta = getmetatable(target)
        for name, fn in originalPairs(type(meta) == "table" and meta or {}) do
            if type(fn) == "function" and type(name) == "string" and name:sub(1, 1) ~= "_" and not DASH_DENY[name] then
                f[name] = function(self, ...)
                    if self ~= facades[target] then
                        error("call " .. name .. " as a method of the dashboard (:" .. name .. ")", 2)
                    end
                    if sandbox.dead then error("plugin " .. pluginId .. " is disabled", 2) end
                    if name == "build" then return finish() end
                    if DASH_USER[name] then userOnly(name) end
                    if name == "auth" or name == "name" or name == "icon" then
                        local r = ownSetting(target, name, ...) -- no tail call: errors count the levels
                        return r
                    end
                    local args = pack(...)
                    for i = 1, args.n do args[i] = wrapArg(args[i]) end
                    local res = pack(pcall(fn, target, unpackArgs(args, 1, args.n)))
                    if not res[1] then error(coreError(res[2]), 2) end
                    return mapResults(out, unpackArgs(res, 2, res.n))
                end
            end
        end
        for name in originalPairs(DASH_DENY) do
            if name ~= "new" then
                f[name] = function()
                    error("dashboard:" .. name .. " is not available in a plugin's dashboard: it reaches the house's objects", 2)
                end
            end
        end
        return f
    end

    -- the builder and its sections leave as facades; any other table not at all
    out = function(r)
        if type(r) ~= "table" then return r end
        if facades[r] then return facades[r] end
        if r == raw or rawget(r, "_builder") == raw then
            facades[r] = methodsOf(r)
            return facades[r]
        end
        return nil
    end

    return out(raw)
end

local ACTION_TYPES = { string = true, number = true, boolean = true }
local MAX_ACTIONS, MAX_ACTION_STRING = 32, 1000

-- A text for people and agents: a string or {pl = ..., en = ...}
local function actionText(v, where)
    if type(v) == "string" and v ~= "" and #v <= 500 then return v end
    if type(v) == "table" and getmetatable(v) == nil then
        local t = {}
        for k, x in originalPairs(v) do
            if (k ~= "pl" and k ~= "en") or type(x) ~= "string" or x == "" or #x > 500 then t = nil break end
            t[k] = x
        end
        if t and next(t) then return t end
    end
    error("plugin:action: " .. where .. " is a text (up to 500 characters) or {pl = ..., en = ...}", 0)
end

local function actionSpec(name, spec)
    if type(name) ~= "string" or #name > 64 or not name:match("^[a-z][a-z0-9_]*$") then
        error("plugin:action: name " .. tostring(name) .. " is a lower-case letter, then letters, digits and _", 0)
    end
    if type(spec) ~= "table" or getmetatable(spec) ~= nil then
        error("plugin:action(" .. name .. "): spec is a table {description, params}", 0)
    end
    for k in originalPairs(spec) do
        if k ~= "description" and k ~= "params" then
            error("plugin:action(" .. name .. "): unknown spec field " .. tostring(k) .. " (description, params)", 0)
        end
    end
    local out = { description = actionText(spec.description, name .. " description"), params = {} }
    if spec.params ~= nil and (type(spec.params) ~= "table" or getmetatable(spec.params) ~= nil) then
        error("plugin:action(" .. name .. "): params is a table {name = {type, description, required}}", 0)
    end
    local count = 0
    for pname, p in originalPairs(spec.params or {}) do
        local where = name .. " param " .. tostring(pname)
        if type(pname) ~= "string" or #pname > 64 or not pname:match("^[%a][%w_]*$") then
            error("plugin:action: " .. where .. ": a name is a letter, then letters, digits and _", 0)
        end
        if type(p) ~= "table" or not ACTION_TYPES[p.type] then
            error("plugin:action: " .. where .. ": type is \"string\", \"number\" or \"boolean\"", 0)
        end
        for k in originalPairs(p) do
            if k ~= "type" and k ~= "description" and k ~= "required" then
                error("plugin:action: " .. where .. ": unknown field " .. tostring(k) .. " (type, description, required)", 0)
            end
        end
        if p.required ~= nil and type(p.required) ~= "boolean" then
            error("plugin:action: " .. where .. ": required is true or false", 0)
        end
        count = count + 1
        if count > 32 then error("plugin:action(" .. name .. "): at most 32 params", 0) end
        out.params[pname] = {
            type = p.type, required = p.required == true,
            description = p.description ~= nil and actionText(p.description, where .. " description") or nil,
        }
    end
    return out
end

-- An action's answer as plain data JSON can carry; nil and an error when it is not
local function actionResult(sandbox, v, depth, seen)
    local t = type(v)
    if t == "string" then return maskSecrets(sandbox, v) end
    if t == "boolean" or t == "nil" then return v end
    if t == "number" then
        if v - v ~= 0 then return nil, "a number that is not finite" end
        return v
    end
    if t ~= "table" or getmetatable(v) ~= nil then return nil, "a " .. t end
    seen = seen or {}
    if seen[v] or depth > 16 then return nil, "a table nested too deep or in a cycle" end
    seen[v] = true
    local copy = {}
    for k, x in originalPairs(v) do
        if type(k) ~= "string" and type(k) ~= "number" then return nil, "a table with a " .. type(k) .. " key" end
        local c, err = actionResult(sandbox, x, depth + 1, seen)
        if err then return nil, err end
        copy[k] = c
    end
    seen[v] = nil
    return copy
end

--- Create a new sandbox for a plugin
-- @param pluginId string The full plugin ID (e.g., "@vclu/weather")
-- @param permissions table|nil {api, lan, mqtt, modbus, official} from the loader; nil grants none
-- @return table The created sandbox
function PluginSandbox:create(pluginId, permissions)
    -- a plugin started again without a stop: its old timers, sockets and handlers go first
    if self.sandboxes[pluginId] then
        Logger:warn("[SANDBOX] " .. pluginId .. " started again while running; stopping the old one")
        self:cleanup(pluginId)
    end
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
        ioHandles = {},           -- Go handle id -> true: sockets, ports, transfers
        dashboards = {},          -- dashboard id -> true, removed on cleanup
        actions = {},             -- name -> {description, params, confirm, fn}
        dead = false,             -- set by cleanup
    }
    permissions = type(permissions) == "table" and permissions or {}
    -- plugin API version (pkg/plugins/permissions.go): 1 keeps the contract from before
    -- permissions, 2 adds prefixes and drops the HTTP and MQTT globals; the loader always
    -- passes it, any other caller gets the strict one
    sandbox.apiVersion = tonumber(permissions.api) or 2
    -- dashboards in plugin.json: id -> {name}; on API 2 plugin:dashboard builds only these
    sandbox.declaredDashboards = {}
    if type(permissions.dashboards) == "table" then
        for id, d in pairs(permissions.dashboards) do
            if type(id) == "string" and type(d) == "table" and type(d.name) == "string" then
                sandbox.declaredDashboards[id] = { name = d.name }
            end
        end
    end
    -- actions in plugin.json (API 2): name -> {description, params, confirm, allowed}
    sandbox.declaredActions = {}
    if type(permissions.actions) == "table" then
        for name, a in pairs(permissions.actions) do
            if type(name) == "string" and type(a) == "table" then sandbox.declaredActions[name] = a end
        end
    end
    sandbox.permissions = {
        lan = permissions.lan == true,
        udp = permissions.udp == true,
        tcp = permissions.tcp == true,
        mqtt = permissions.mqtt == true,
        modbus = permissions.modbus == true,
        grentonRaw = permissions.grenton_raw == true,
        tftp = permissions.tftp == true,
        serial = permissions.serial == true,
    }
    -- How the user narrowed them (grants in .vclu.json): lists of hosts and topic filters,
    -- nil allows any, an empty list none
    local limits = type(permissions.limits) == "table" and permissions.limits or {}
    local function list(v) return type(v) == "table" and v or nil end
    sandbox.limits = {
        lanHosts = list(limits.lanHosts),
        udpHosts = list(limits.udpHosts),
        tcpHosts = list(limits.tcpHosts),
        mqttSubscribe = list(limits.mqttSubscribe),
        mqttPublish = list(limits.mqttPublish),
        grentonRawHosts = list(limits.cluHosts),
        cluDiscovery = limits.cluDiscovery == true,
        modbusHosts = list(limits.modbusHosts),
        lanPorts = list(permissions.lanPorts), -- nil: any local port
        tftpHosts = list(limits.tftpHosts),
        serialPorts = list(limits.serialPorts),
        -- internet hosts on API 2 (from the loader; any other caller: none), API 1 any
        internetHosts = sandbox.apiVersion >= 2 and (list(limits.internetHosts) or {}) or nil,
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
        __go_http_set_lan(pluginId, sandbox.permissions.lan, sandbox.limits.lanHosts, sandbox.limits.lanPorts)
    end
    if type(__go_http_set_internet) == "function" then
        __go_http_set_internet(pluginId, sandbox.limits.internetHosts)
    end
    if type(__go_pluginio_grant) == "function" then
        -- the ports of plugin.json's permissions: udp sends and broadcasts, tcp connects, lan's
        -- WebSockets reach (nil: any local port)
        __go_pluginio_grant(pluginId, sandbox.permissions.grentonRaw, sandbox.permissions.tftp,
            sandbox.permissions.serial, sandbox.limits.grentonRawHosts, sandbox.limits.tftpHosts,
            sandbox.limits.serialPorts, sandbox.permissions.udp, sandbox.limits.udpHosts,
            list(permissions.udpBroadcastPorts) or {}, sandbox.permissions.tcp, sandbox.limits.tcpHosts,
            list(permissions.tcpPorts) or {}, sandbox.limits.lanPorts, list(permissions.udpPorts) or {},
            sandbox.limits.cluDiscovery)
    end

    -- Build wrapped API with automatic tracking
    sandbox.api = self:buildAPI(sandbox)

    -- Build isolated Lua environment
    sandbox.environment = self:buildEnvironment(sandbox)
    -- its frames are told by this environment: plugin code gets its own time budget
    if type(__go_plugin_budget_register) == "function" then
        __go_plugin_budget_register(pluginId, sandbox.environment)
    end

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
    local log = maskedLogger(sandbox)
    sandbox.log = log -- PluginHelpers logs a plugin's requests and pollers through it

    -- Wrapped setTimeout with tracking and pcall protection
    api.setTimeout = function(ms, callback)
        if type(__go_setTimeout) ~= "function" then
            Logger:error("[" .. pluginId .. "] setTimeout not available")
            return nil
        end
        local id
        local wrappedCallback = function(...)
            sandbox.timers[id] = nil -- fired: nothing left to clear
            local ok, err = pcall(callback, ...)
            if not ok then
                log:error("[" .. pluginId .. "] Timer error: " .. tostring(err))
            end
        end

        id = __go_setTimeout(wrappedCallback, ms, "plugin " .. pluginId)
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
                log:error("[" .. pluginId .. "] Interval error: " .. tostring(err))
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
            local ok, err = pcall(callback, mapResults(function(v) return readOnlyProxy(v, pluginId) end, ...))
            if not ok then
                log:error("[" .. pluginId .. "] Event '" .. eventName .. "' error: " .. tostring(err))
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
        if sandbox.apiVersion < 2 then return end
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
            bus:emit(eventName, mapResults(function(v) return plain(v, nil, nil, sandbox.instance) end, ...))
        end
    end

    local function requireMqtt()
        if not sandbox.permissions.mqtt then
            error("MQTT needs \"mqtt\" in the permissions of " .. pluginId .. "'s plugin.json", 2)
        end
    end

    local function requireTopic(allowed, topic, what)
        if allowed and not topicAllowed(allowed, topic) then
            error("MQTT " .. what .. " " .. tostring(topic) .. " is not among the topics allowed in " .. pluginId .. "'s settings", 3)
        end
    end

    -- Wrapped MQTT subscribe with tracking
    api.mqttSubscribe = function(topic, callback)
        requireMqtt()
        requireTopic(sandbox.limits.mqttSubscribe, topic, "subscription to")
        local wrappedCallback = function(t, payload)
            local ok, err = pcall(callback, t, payload)
            if not ok then
                log:error("[" .. pluginId .. "] MQTT '" .. topic .. "' error: " .. tostring(err))
            end
        end

        if MQTT and MQTT.subscribe then
            -- one callback per topic: a module's (or another plugin's) is not replaced
            local held = mqttHolder(topic)
            if held and not ownsMqtt(sandbox, held) then
                error("MQTT " .. tostring(topic) .. " is subscribed already by a module or another plugin", 2)
            end
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
        requireTopic(sandbox.limits.mqttPublish, topic, "publishing on")
        if MQTT and MQTT.publish then
            local payloadStr = payload
            if type(payload) == "table" then
                payloadStr = JSON:encode(plain(payload, nil, {}))
            end
            MQTT:publish(topic, payloadStr, qos or 0, retain or false)
        end
    end

    -- udp, tcp, clu, tftp and serial: handles live in Go, which checks the grant again
    local function requireIO(name, key)
        if not sandbox.permissions[key] then
            error(name .. " needs \"" .. name .. "\" in the permissions of " .. pluginId
                .. "'s plugin.json, allowed in the plugin's settings", 3)
        end
    end

    local function ioCallback(what, fn)
        if type(fn) ~= "function" then return nil end
        return function(...)
            local ok, err = pcall(fn, ...)
            if not ok then
                log:error("[" .. pluginId .. "] " .. what .. " error: " .. tostring(err))
            end
        end
    end

    local function track(id)
        local entry = { sandbox = sandbox }
        PluginSandbox.ioHandles[id] = entry
        sandbox.ioHandles[id] = true
        return entry
    end

    local function closeIO(id)
        __go_pluginio_close(id, pluginId)
        PluginSandbox.ioHandles[id] = nil
        sandbox.ioHandles[id] = nil
    end

    -- A UDP socket of the plugin's own, under udp or clu; nil, err when the port cannot be bound
    local function udpSocket(opts, perm)
        local port = type(opts) == "table" and tonumber(opts.port) or 0
        local id, err = __go_pluginio_udp_open(pluginId, port, perm)
        if not id then return nil, err end
        local entry = track(id)
        local socket = {}
        function socket:send(ip, toPort, bytes)
            return __go_pluginio_udp_send(id, pluginId, tostring(ip), tonumber(toPort) or 0, tostring(bytes))
        end
        function socket:broadcast(toPort, bytes)
            return __go_pluginio_udp_send(id, pluginId, "255.255.255.255", tonumber(toPort) or 0, tostring(bytes))
        end
        function socket:onReceive(fn) entry.call = ioCallback("UDP", fn) end
        function socket:close() closeIO(id) end
        return socket
    end

    -- Devices on the local network; broadcast only to the ports in plugin.json's "udp"
    api.udpSocket = function(opts)
        requireIO("udp", "udp")
        return udpSocket(opts, "udp")
    end

    -- A TCP connection to a device; it comes up (or fails) in the callbacks, nil, err when refused
    api.tcpConnect = function(opts)
        requireIO("tcp", "tcp")
        if type(opts) ~= "table" then
            error("plugin:tcpConnect: opts is a table {host = ip, port = n, timeout = seconds}", 2)
        end
        local id, err = __go_pluginio_tcp_open(pluginId, tostring(opts.host), tonumber(opts.port) or 0,
            tonumber(opts.timeout) or 0)
        if not id then return nil, err end
        local on, state = {}, "connecting"
        track(id).call = function(what, data)
            if what == "connect" then
                state = "open"
                if on.connect then on.connect() end
            elseif what == "close" then
                state = "closed"
                if on.close then on.close(data) end
            elseif on.receive then
                on.receive(data)
            end
        end
        local conn = {}
        function conn:send(bytes) return __go_pluginio_tcp_send(id, pluginId, tostring(bytes)) end
        function conn:onConnect(fn) on.connect = ioCallback("TCP", fn) end
        function conn:onReceive(fn) on.receive = ioCallback("TCP", fn) end
        function conn:onClose(fn) on.close = ioCallback("TCP", fn) end
        function conn:isOpen() return state == "open" end
        function conn:close()
            state = "closed"
            closeIO(id)
        end
        return conn
    end

    -- A WebSocket client where the plugin's HTTP may go (permissions.internet, lan); it
    -- opens (or fails) in the callbacks, nil, err when refused
    api.websocket = function(url, opts)
        if type(url) ~= "string" then
            error("plugin:websocket: url is a ws:// or wss:// string", 2)
        end
        if opts ~= nil and type(opts) ~= "table" then
            error("plugin:websocket: opts is a table {headers = {...}, protocols = {...}, timeout = seconds}", 2)
        end
        opts = opts or {}
        local id, err = __go_pluginio_ws_open(pluginId, url, type(opts.headers) == "table" and opts.headers or nil,
            type(opts.protocols) == "table" and opts.protocols or nil, tonumber(opts.timeout) or 0)
        if not id then return nil, err end
        local on, state = {}, "connecting"
        track(id).call = function(what, data, extra)
            if what == "open" then
                state = "open"
                if on.open then on.open(data) end
            elseif what == "close" then
                state = "closed"
                if on.close then on.close(extra, data) end
            elseif on.message then
                on.message(data, extra)
            end
        end
        local ws = {}
        function ws:send(data, sendOpts)
            local binary = type(sendOpts) == "table" and sendOpts.binary == true
            return __go_pluginio_ws_send(id, pluginId, tostring(data), binary)
        end
        function ws:onOpen(fn) on.open = ioCallback("WebSocket", fn) end
        function ws:onMessage(fn) on.message = ioCallback("WebSocket", fn) end
        function ws:onClose(fn) on.close = ioCallback("WebSocket", fn) end
        function ws:isOpen() return state == "open" end
        function ws:close(code, reason)
            local ok, cerr = __go_pluginio_ws_close(id, pluginId, tonumber(code) or 1000, reason and tostring(reason) or "")
            if not ok then return nil, cerr end
            state = "closed"
            PluginSandbox.ioHandles[id] = nil
            sandbox.ioHandles[id] = nil
            return true
        end
        return ws
    end

    -- Frames to CLUs: their port, broadcast for discovery
    api.grentonSocket = function(opts)
        requireIO("clu", "grentonRaw")
        return udpSocket(opts, "grenton_raw")
    end

    -- The CLU cipher with a key and IV the plugin brings (raw or base64)
    api.grentonCipher = function(key, iv)
        requireIO("clu", "grentonRaw")
        key, iv = tostring(key), tostring(iv)
        local _, err = __go_pluginio_cipher(true, key, iv, "")
        if err then error(err, 2) end
        return {
            encrypt = function(_, bytes) return __go_pluginio_cipher(true, key, iv, tostring(bytes)) end,
            decrypt = function(_, bytes) return __go_pluginio_cipher(false, key, iv, tostring(bytes)) end,
        }
    end

    -- A TFTP client: get answers (bytes, err), put (true, err), both in a callback
    api.tftp = function(ip, opts)
        requireIO("tftp", "tftp")
        local port = type(opts) == "table" and tonumber(opts.port) or 69
        local function transfer(name, bytes, done)
            local id, err = __go_pluginio_tftp(pluginId, tostring(ip), port, tostring(name), bytes)
            if not id then
                api.setTimeout(0, function() done(nil, err) end)
                return
            end
            track(id).call = ioCallback("TFTP", done)
        end
        return {
            get = function(_, name, callback)
                transfer(name, nil, function(data, e)
                    if callback then callback(data, e) end
                end)
            end,
            put = function(_, name, bytes, callback)
                transfer(name, tostring(bytes), function(_, e)
                    if callback then callback(e == nil or nil, e) end
                end)
            end,
        }
    end

    -- A serial port assigned to the plugin; returns nil, err when it cannot be opened
    api.serialOpen = function(path, opts)
        requireIO("serial", "serial")
        opts = type(opts) == "table" and opts or {}
        local id, err = __go_pluginio_serial_open(pluginId, tostring(path), tonumber(opts.baud) or 9600,
            tonumber(opts.data) or 8, opts.parity and tostring(opts.parity) or "none", tonumber(opts.stop) or 1)
        if not id then return nil, err end
        local entry = track(id)
        local port = {}
        function port:write(bytes) return __go_pluginio_serial_write(id, pluginId, tostring(bytes)) end
        function port:onData(fn) entry.call = ioCallback("serial", fn) end
        function port:close() closeIO(id) end
        return port
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
        obj = PluginSandbox.instanceBehind(obj)
        if obj == sandbox.instance then return sandbox.instanceProxy end
        return readOnlyProxy(obj, pluginId)
    end

    -- GET/POST go through httpRequest like every other plugin request: the plugin's own
    -- HTTP instance, with its LAN permission
    local function alive() return not sandbox.dead end

    local function simpleRequest(opts, callback)
        PluginHelpers.httpRequest(pluginId, opts, function(response, err)
            local ok, callErr = pcall(callback, response, err)
            if not ok then
                log:error("[" .. pluginId .. "] HTTP " .. opts.method .. " callback error: " .. tostring(callErr))
            end
        end, alive)
    end

    -- httpGet and httpPost stay for older plugins; plugin:httpRequest is the one to use
    local function deprecated(name)
        if sandbox.deprecatedLogged then return end
        sandbox.deprecatedLogged = true
        log:warn("[" .. pluginId .. "] plugin:" .. name .. " is deprecated: use plugin:httpRequest")
    end

    api.httpGet = function(url, callback)
        deprecated("httpGet")
        simpleRequest({ method = "GET", url = url }, callback)
    end

    api.has = function(name) return PluginHelpers.has(name) end

    api.httpPost = function(url, data, callback)
        deprecated("httpPost")
        local opts = { method = "POST", url = url }
        if type(data) == "table" then opts.json = data else opts.body = data end
        simpleRequest(opts, callback)
    end

    -- Logging helper
    api.log = function(level, message)
        local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
        local prefix = "[" .. (shortId or pluginId):upper() .. "] "

        if level == "error" then
            log:error(prefix .. message)
        elseif level == "warn" then
            log:warn(prefix .. message)
        elseif level == "debug" then
            log:debug(prefix .. message)
        else
            log:info(prefix .. message)
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
        return PluginHelpers.encodeJson(plain(data, nil, {}))
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
            if bus then bus:emit(ev, plain(payload, nil, nil, sandbox.instance)) end
        end
        return PluginHelpers.emitThrottled(pluginId, emitFn, event, data, opts)
    end

    -- Log with redaction
    api.logSafe = function(level, msg, fields, redact)
        local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
        PluginHelpers.logSafe(log, shortId or pluginId, level, msg, fields, redact)
    end

    -- A dashboard of the plugin's own at /d/<shortId>_<id>, "session" auth unless it sets one
    local dashPrefix = ((pluginId:match("^@[^/]+/(.+)$") or pluginId):gsub("-", "_"))
    api.dashboard = function(id)
        if type(id) ~= "string" or #id > 64 or not id:match("^[a-z0-9_]+$") then
            error("plugin:dashboard: id " .. tostring(id) .. " is lower-case letters, digits and _", 0)
        end
        if not (Dashboard and Dashboard.new) then error("plugin:dashboard: dashboards are not available", 0) end
        local declared = sandbox.declaredDashboards[id]
        if not declared and sandbox.apiVersion >= 2 then
            error("plugin:dashboard: " .. id .. ' is not declared: declare it in plugin.json "dashboards"', 0)
        end
        local realId = dashPrefix .. "_" .. id
        dashboardFree(pluginId, realId)
        local raw = Dashboard.new(realId)
        raw._plugin, raw._pluginDash = pluginId, id
        raw._pluginOwn = { auth = "session", name = declared and declared.name or realId }
        Dashboard._applyAccess(raw)
        return dashboardFacade(sandbox, raw, log)
    end

    -- A named action agents and voice call: fn(params) returns the result, or nil, err. On API 2
    -- plugin.json declares it (description, params, confirm): plugin:action(name, fn)
    api.action = function(name, spec, fn)
        local a
        if sandbox.apiVersion >= 2 or next(sandbox.declaredActions) ~= nil then
            if fn ~= nil or type(spec) ~= "function" then
                error("plugin:action(" .. tostring(name) .. ", fn): on plugin API 2 the action is declared in plugin.json's \"actions\"", 0)
            end
            fn = spec
            local d = type(name) == "string" and sandbox.declaredActions[name]
            if not d then
                error("plugin:action: " .. tostring(name) .. " is not among plugin.json's \"actions\"", 0)
            end
            -- checked by the loader when it read plugin.json
            a = { description = d.description, params = {}, confirm = d.confirm == true, allowed = d.allowed == true }
            for pname, p in pairs(type(d.params) == "table" and d.params or {}) do
                a.params[pname] = { type = p.type, required = p.required == true, description = p.description }
            end
        else
            a = actionSpec(name, spec)
            a.allowed = true
        end
        if type(fn) ~= "function" then error("plugin:action(" .. name .. "): fn is a function", 0) end
        if not sandbox.actions[name] then
            local n = 0
            for _ in originalPairs(sandbox.actions) do n = n + 1 end
            if n >= MAX_ACTIONS then error("plugin:action: at most " .. MAX_ACTIONS .. " actions per plugin", 0) end
        end
        a.fn = fn
        sandbox.actions[name] = a
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

    -- API 1 globals, only for a plugin allowed lan or mqtt in full: HTTP:new() is a client whose
    -- requests are the plugin's (its policy, not vclu's own ports), MQTT the shared client
    -- through the sandbox (its topics tracked, a module's never taken)
    local limits = sandbox.limits
    local legacyHTTP, legacyMQTT
    if sandbox.apiVersion < 2 then
        if sandbox.permissions.lan and limits.lanHosts == nil and HTTP then
            legacyHTTP = legacyClass("HTTP", {
                new = function()
                    local client = HTTP:new()
                    client._instanceId = pluginId
                    return boundFacade(client, nil, pluginId)
                end,
            })
        end
        if sandbox.permissions.mqtt and limits.mqttSubscribe == nil and limits.mqttPublish == nil and MQTT then
            legacyMQTT = legacyClass("MQTT", {
                subscribe = api.mqttSubscribe,
                publish = function(topic, payload, qos, retain) return api.mqttPublish(topic, payload, retain, qos) end,
                unsubscribe = function(topic)
                    local held = mqttHolder(topic)
                    if not held or not ownsMqtt(sandbox, held) then return false end
                    for i = #sandbox.mqttSubscriptions, 1, -1 do
                        if sandbox.mqttSubscriptions[i].callback == held then table.remove(sandbox.mqttSubscriptions, i) end
                    end
                    return MQTT:unsubscribe(topic)
                end,
                isConnected = function() return MQTT:isConnected() end,
                getTopicPrefix = function() return MQTT:getTopicPrefix() end,
            })
        end
    end

    -- Modbus only with the permission; without it a clear error instead of nil
    local modbus
    if sandbox.permissions.modbus and Modbus then
        -- requests stop with the plugin, as its timers do; conversions keep working
        modbus = shallowCopy(Modbus)
        for name, f in originalPairs(Modbus) do
            if type(f) == "function" and (name == "request" or name:match("^read") or name:match("^write")) then
                modbus[name] = function(opts, ...)
                    if sandbox.dead then error("plugin " .. pluginId .. " is disabled", 2) end
                    -- the gateways the user narrowed modbus to (grants): an IP address among them
                    local hosts = sandbox.limits.modbusHosts
                    if hosts and type(__go_plugin_hosts_allow) == "function" then
                        local host = type(opts) == "table" and opts.host or nil
                        if not __go_plugin_hosts_allow(hosts, tostring(host)) then
                            error("Modbus to " .. tostring(host) .. ": not among the hosts allowed in "
                                .. pluginId .. "'s settings", 2)
                        end
                    end
                    return f(opts, ...)
                end
            end
        end
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
        obj = PluginSandbox.instanceBehind(obj)
        if obj == sandbox.instance then return sandbox.instanceProxy end
        return readOnlyProxy(obj, pluginId)
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
            return external(Plugin._lookup(id))
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
        print = function(...)
            local args = { ... }
            for i = 1, select("#", ...) do args[i] = maskSecrets(sandbox, args[i]) end
            print(unpackArgs(args, 1, select("#", ...)))
        end,
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
        string = timedLib(string),
        table = sandboxTable(),
        math = libCopy(math),
        Crypto = libCopy(CRYPTO),
        bit = libCopy(BIT),
        Telnet = libCopy(TELNET),
        os = safeOs,
        datetime = safeDatetime,

        -- vCLU API (wrapped with tracking)
        setTimeout = api.setTimeout,
        setInterval = api.setInterval,
        clearTimeout = api.clearTimer,
        clearInterval = api.clearTimer,

        -- Shared objects: methods callable, fields not replaceable. HTTP and MQTT go
        -- through the plugin API (plugin:httpRequest, plugin:mqttSubscribe).
        Logger = boundFunctions(maskedLogger(sandbox), { "debug", "info", "warn", "error" }),
        JSON = boundFunctions({
            encode = function(_, data, ...) return JSON:encode(plain(data, nil, {}), ...) end,
            decode = function(_, ...) return JSON:decode(...) end,
        }, { "encode", "decode" }),
        Modbus = modbus,

        Plugin = pluginClass,
        HTTP = legacyHTTP,
        HttpClient = legacyHTTP,
        MQTT = legacyMQTT,
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
        -- setmetatable, getmetatable, require; HTTP and MQTT from plugin API 2
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
        result = maskSecrets(sandbox, tostring(result))
        Logger:error("[SANDBOX] Runtime error in " .. pluginId .. ": " .. result)
        return nil, "Runtime error: " .. result
    end

    return result, nil
end

--- text with the plugin's secrets as *** (an error the core passes on)
function PluginSandbox:mask(pluginId, text)
    local sandbox = self.sandboxes[pluginId]
    return sandbox and maskSecrets(sandbox, text) or text
end

--- Called before a request goes to a CLU: plugin code needs clu on API 2; an API 1
-- plugin is warned once. Module code passes.
function PluginSandbox.checkCluRequest()
    if type(__go_clu_caller) ~= "function" then return end
    local viaProxy, id = __go_clu_caller()
    if not id then
        -- a plugin's read through a proxy it got from somewhere else
        if viaProxy then error("a plugin reading a CLU needs the clu permission", 3) end
        return
    end
    local sandbox = PluginSandbox.sandboxes[id]
    if not sandbox or sandbox.permissions.grentonRaw then return end
    if sandbox.apiVersion < 2 then
        if not sandbox.warnedCluRequest then
            sandbox.warnedCluRequest = true
            Logger:warn("[SANDBOX] " .. id .. " reads a CLU; on plugin API 2 that needs the clu permission")
        end
        return
    end
    error(id .. ": reading a CLU needs the clu permission", 3)
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

--- The secret fields of the plugin's form, read by plugin:secret(name)
function PluginSandbox:setSecrets(pluginId, secrets)
    local sandbox = self.sandboxes[pluginId]
    if sandbox then sandbox.secrets = secrets or {} end
end

-- ============================================
-- Wizard: discover (step 2) and module code (step 3), called by the loader
-- ============================================

PluginSandbox.discovered = {}

local function copyPlain(v, depth)
    if type(v) ~= "table" then return v end
    if (depth or 0) > 8 then return nil end
    local out = {}
    for k, x in originalPairs(v) do out[k] = copyPlain(x, (depth or 0) + 1) end
    return out
end

local function wizardInstance(pluginId, field)
    local sandbox = PluginSandbox.sandboxes[pluginId]
    local instance = sandbox and not sandbox.dead and sandbox.instance
    if not instance then return nil, nil, "the plugin is not running" end
    local fn = rawget(instance, field)
    if type(fn) ~= "function" then return nil, nil, "the plugin has no " .. field:sub(2) end
    return instance, fn
end

--- Starts the plugin's discover; the result waits in PluginSandbox.discovered[reqId]
function PluginSandbox:discover(pluginId, reqId)
    local instance, fn, err = wizardInstance(pluginId, "_discover")
    if not instance then
        self.discovered[reqId] = { err = err }
        return
    end
    local answered = false
    local function done(rows, e)
        if answered then return end
        answered = true
        if e ~= nil or type(rows) ~= "table" then
            self.discovered[reqId] = { err = maskSecrets(PluginSandbox.sandboxes[pluginId] or {}, tostring(e or "discover gave no rows")) }
        else
            self.discovered[reqId] = { rows = copyPlain(rows) }
        end
    end
    local ok, e = pcall(fn, copyPlain(instance.config), done)
    if not ok then done(nil, e) end
end

--- The discover result as JSON once it is there, else nil
function PluginSandbox:takeDiscovered(reqId)
    local r = self.discovered[reqId]
    if not r then return nil end
    self.discovered[reqId] = nil
    return JSON:encode(r)
end

function PluginSandbox:forgetDiscover(reqId)
    self.discovered[reqId] = nil
end

--- The module code the plugin proposes for its saved devices, as JSON {code} or {err}
function PluginSandbox:moduleCode(pluginId)
    local instance, fn, err = wizardInstance(pluginId, "_moduleCode")
    if not instance then return JSON:encode({ err = err }) end
    local cfg = copyPlain(instance.config) or {}
    local ok, code = pcall(fn, cfg, copyPlain(cfg.devices) or {})
    if not ok then return JSON:encode({ err = maskSecrets(PluginSandbox.sandboxes[pluginId] or {}, tostring(code)) }) end
    if type(code) ~= "string" then return JSON:encode({ err = "moduleCode did not return text" }) end
    return JSON:encode({ code = code })
end

--- The actions of running plugins (of one when pluginId is given), as JSON
-- [{plugin, name, description, params}]
function PluginSandbox:listActions(pluginId)
    local ids = {}
    for id, sb in pairs(self.sandboxes) do
        if not sb.dead and (pluginId == nil or pluginId == "" or id == pluginId) then ids[#ids + 1] = id end
    end
    table.sort(ids)
    local out = {}
    for _, id in ipairs(ids) do
        local names = {}
        for name, a in pairs(self.sandboxes[id].actions) do
            if a.allowed then names[#names + 1] = name end -- a declared one the user has not allowed waits
        end
        table.sort(names)
        for _, name in ipairs(names) do
            local a = self.sandboxes[id].actions[name]
            out[#out + 1] = { plugin = id, name = name, description = a.description,
                params = next(a.params) and a.params or nil, confirm = a.confirm or nil }
        end
    end
    return JSON:encode(out)
end

local function actionAnswer(err, code) return JSON:encode({ error = err, code = code }) end

--- Runs a plugin's action with params (a JSON object): JSON {result} or {error, code}, code
-- "not_found", "invalid" (params) or "failed" (the action's own error)
function PluginSandbox:callAction(pluginId, name, paramsJSON, confirmed)
    local sb = self.sandboxes[pluginId]
    local a = sb and not sb.dead and sb.actions[name]
    if not a or not a.allowed then
        return actionAnswer("plugin " .. tostring(pluginId) .. " has no action " .. tostring(name)
            .. (a and " allowed yet: it waits for the user in the plugin's settings" or ""), "not_found")
    end
    if a.confirm and confirmed ~= true then
        return actionAnswer("action " .. name .. " asks for confirmation: ask the user, then call it again confirmed", "confirm")
    end
    local ok, params = pcall(JSON.decode, JSON, paramsJSON or "{}")
    if not ok or (params ~= nil and type(params) ~= "table") then
        return actionAnswer("params are a JSON object", "invalid")
    end
    local given = {}
    for k, v in pairs(params or {}) do
        local p = a.params[k]
        if not p then
            local known = {}
            for n in pairs(a.params) do known[#known + 1] = n end
            table.sort(known)
            return actionAnswer("unknown param " .. tostring(k) .. " (params: "
                .. (#known > 0 and table.concat(known, ", ") or "none") .. ")", "invalid")
        end
        if type(v) ~= p.type or (p.type == "number" and v - v ~= 0) then
            return actionAnswer("param " .. k .. " is a " .. p.type .. ", got " .. type(v), "invalid")
        end
        if p.type == "string" and select(2, v:gsub("[^\128-\191]", "")) > MAX_ACTION_STRING then
            return actionAnswer("param " .. k .. " is longer than " .. MAX_ACTION_STRING .. " characters", "invalid")
        end
        given[k] = v
    end
    for k, p in pairs(a.params) do
        if p.required and given[k] == nil then return actionAnswer("param " .. k .. " is required", "invalid") end
    end
    local res = pack(pcall(a.fn, given))
    local err
    if not res[1] then
        err = maskSecrets(sb, tostring(res[2]))
        Logger:error("[" .. pluginId .. "] Action '" .. name .. "' error: " .. err)
    elseif res[2] == nil and res[3] ~= nil then
        err = maskSecrets(sb, tostring(res[3]))
    end
    if err then return actionAnswer(err, "failed") end
    local result, bad = actionResult(sb, res[2], 0)
    if bad then return actionAnswer("action " .. name .. " returned " .. bad .. " (text, number, true/false or a plain table)", "failed") end
    return '{"result":' .. JSON:encode(result) .. '}' -- nil stays null, not an empty table
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

    -- 0. Sockets, serial ports and transfers, then pollers, exposed objects and the
    -- registry entry of its instance
    if type(__go_plugin_budget_forget) == "function" then
        __go_plugin_budget_forget(pluginId)
    end
    if type(__go_pluginio_release) == "function" then
        __go_pluginio_release(pluginId)
    end
    for id in pairs(sandbox.ioHandles) do
        PluginSandbox.ioHandles[id] = nil
    end
    sandbox.ioHandles = {}
    for _, poller in ipairs(sandbox.pollers) do
        pcall(poller.stop, poller)
    end
    sandbox.pollers = {}
    -- its dashboards (still its own) and actions
    for id in pairs(sandbox.dashboards) do
        if _dashboards and _dashboards[id] and _dashboards[id]._plugin == pluginId then
            _dashboards[id] = nil
            for i = #_dashboardOrder, 1, -1 do
                if _dashboardOrder[i] == id then table.remove(_dashboardOrder, i) end
            end
        end
    end
    sandbox.dashboards = {}
    sandbox.actions = {}
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
        -- only while the topic is still its own: a module may have subscribed since
        if MQTT and MQTT.unsubscribe and mqttHolder(sub.topic) == sub.callback then
            MQTT:unsubscribe(sub.topic)
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
