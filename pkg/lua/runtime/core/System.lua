--- SYSTEM module for CLU runtime environment
-- Manages the core system functionality including module registration,
-- event bus coordination, client subscriptions, and lifecycle management.
-- This module implements a singleton pattern using a shared instance.
-- @module SYSTEM
SYSTEM = {}

--- Shared instance of SYSTEM (singleton pattern)
local shared = nil

--- Default TTL for client subscriptions in seconds (5 minutes)
-- Used as fallback when CLU.FEATURE_VCLU_CLIENT_TTL is not available
local DEFAULT_CLIENT_TTL = 300

--- Create a new SYSTEM instance
-- Initializes a new SYSTEM object with default values for all properties.
-- @return SYSTEM New SYSTEM instance
function SYSTEM:new()
    system = {
        eventBus = nil,
        logger = nil,
        nextModuleId = 0,
        clu = nil,
        modules = {},
        -- Client subscriptions for push notifications
        -- Key: "ip:port:sessionID", Value: { ip, port, sessionID, subscriptions = {{object, featureIndex}, ...} }
        clientSubscriptions = {},
    }
    setmetatable(system, self)
    self.__index = self

    return system
end

--- Get the shared SYSTEM instance (singleton)
-- @return SYSTEM The shared SYSTEM instance
function SYSTEM:getShared()
    return shared
end

--- Start the SYSTEM and initialize CLU
-- This method is called by the bridge to start the system.
-- Fires the CLU EVENT_ON_INIT event to initialize the CLU module.
function SYSTEM:Start()
    if (self.clu == nil) then
        error("No Clu in the system");
    end

    EventBus:getShared():fire(self.clu, CLU.EVENT_ON_INIT) -- hardcodowane jedno clu
end

--- Register a module in the SYSTEM
-- Assigns a unique ID to the module and stores it in the modules table.
-- If the module is a CLU, it is stored as the primary CLU instance.
-- @param module table The module object to register
function SYSTEM:registerModule(module)
    local moduleData = ModuleData:new(module)

    local id = moduleData:getId()
    local moduleAddress = moduleData:getAddress()
    local description = moduleData:getDescription()

    self.modules[id] = module;

    if (description == "CLU") then
        self.clu = module
    end

    Logger:getShared():debug('SYSTEM:registerModule: ' .. description .. ', moduleAddress: ' .. moduleAddress .. ', id: ' .. id)
end

--- Get a module by its ID
-- Each module has a sequential ID. Sometimes from the frontend, only the module ID is known.
-- @param id number The unique module ID
-- @return table The module object
function SYSTEM:getModuleById(id)
    if (self.modules[id] ~= nil) then
        return self.modules[id]
    end

    error("SYSTEM: missing module with id: " .. id)
end

--- Get the primary CLU instance
-- @return table The CLU module object
function SYSTEM:getClu()
    return self.clu
end

--- Get the next available module ID
-- Global variable used by Object to assign unique IDs
-- @return number The next sequential module ID
function SYSTEM:getNextModuleId()
    self.nextModuleId = self.nextModuleId + 1
    return self.nextModuleId
end

--- Get the EventBus instance
-- Returns EventBus for JavaScript runtime or EventBusRecorder for tests
-- @return EventBus|EventBusRecorder The event bus instance
function SYSTEM:getEventBus()
    if _CLU ~= nil then
        -- przypadek gdy kod działa w javascript
        return EventBus:getShared()
    end
    -- w przypadku gdy to "testy"
    return EventBusRecorder:getShared()
end

--- Get the Logger instance
-- @return Logger The shared logger instance
function SYSTEM:getLogger()
    return Logger:getShared()
end

--- System loop tick
-- Called periodically to process system events
function SYSTEM:Loop()
end

--- Wait function placeholder
-- Logs wait operation with the specified parameter
-- @param param number Wait duration or parameter
function SYSTEM:Wait(param)
    Logger:getShared():info("SYSTEM:Wait(" .. param .. ")")
end

--- Initialize the SYSTEM
-- Called from main.lua to start the system if CLU is registered
function SYSTEM.Init()
    local sys = SYSTEM:getShared()
    if sys.clu then
        sys:Start()
    else
        Logger:getShared():warn("SYSTEM.Init() called but no CLU registered")
    end
end

--- Fetch values of user variables (cechy użytkownika)
-- Takes a table of variable names, returns values in format: values:{"val1",nil}
-- Used by Object Manager to read user-defined global variables
-- @param names table Array of variable names to fetch
-- @return string Formatted string: values:{"val1","val2",nil,...}
function SYSTEM:fetchValues(names)
    local results = {}
    for i, name in ipairs(names) do
        local value = _G[name]
        if value == nil then
            table.insert(results, "nil")
        elseif type(value) == "string" then
            table.insert(results, '"' .. tostring(value) .. '"')
        else
            table.insert(results, tostring(value))
        end
    end
    return "values:{" .. table.concat(results, ",") .. "}"
end

--- Get list of registered CLUs (local and remote)
-- Scans all registered modules and returns information about CLU instances
-- @return string JSON array: [{"name":"CLU123","ip":"192.168.1.4","type":"CLU_REMOTE"},...]
function SYSTEM:getRegisteredCLUs()
    local results = {}

    -- Build index of global CLU variables by address
    local cluByAddress = {}
    for varName, varValue in pairs(_G) do
        if type(varValue) == "table" and type(varName) == "string" and varName:match("^CLU%d+$") then
            local desc = rawget(varValue, "description")
            if desc == "CLU" or desc == "CLU_REMOTE" then
                local addr = rawget(varValue, "address")
                if addr then
                    cluByAddress[addr] = varName
                end
            end
        end
    end

    for id, module in pairs(self.modules) do
        if module.description == "CLU" or module.description == "CLU_REMOTE" then
            local ip = "0.0.0.0"
            local addr = module.address
            if addr and type(addr) == "number" then
                local b1 = math.floor(addr / 16777216) % 256
                local b2 = math.floor(addr / 65536) % 256
                local b3 = math.floor(addr / 256) % 256
                local b4 = addr % 256
                ip = b1 .. "." .. b2 .. "." .. b3 .. "." .. b4
            end
            -- Find CLU name by matching address
            local name = cluByAddress[addr] or "Unknown"
            -- Build JSON entry
            local entry = string.format('{"name":"%s","ip":"%s","type":"%s"}', name, ip, module.description)
            table.insert(results, entry)
        end
    end
    return "[" .. table.concat(results, ",") .. "]"
end

-- ==========================================================================
-- Client Registration for Push Notifications (clientReport)
-- Used by Object Manager and mobile apps to subscribe to feature value changes
-- ==========================================================================

-- Format a Lua value for clientReport response.
-- Strings are quoted, nil becomes "nil", numbers/booleans as-is.
-- @local
local function formatValue(value)
    if value == nil then
        return "nil"
    elseif type(value) == "string" then
        return '"' .. tostring(value) .. '"'
    elseif type(value) == "boolean" then
        return tostring(value)
    else
        return tostring(value)
    end
end

--- Collect feature values for a list of subscriptions
-- Helper function that retrieves current values for subscribed features
-- @param subscriptions table Array of {object, featureIndex} pairs
-- @return string Lua table format: {val1,val2,"string",nil,...}
function SYSTEM:collectFeatureValues(subscriptions)
    local values = {}

    for i, sub in ipairs(subscriptions) do
        local obj = sub[1]
        local featureIndex = sub[2]
        local value = nil

        -- Try to get the value from the object (avoid closure allocation in pcall)
        if obj ~= nil and type(obj.get) == "function" then
            local ok, result = pcall(obj.get, obj, featureIndex)
            if ok then
                value = result
            else
                Logger:getShared():warn("SYSTEM:collectFeatureValues - failed to get feature " .. featureIndex .. ": " .. tostring(result))
            end
        else
            Logger:getShared():warn("SYSTEM:collectFeatureValues - object is nil or has no get() method")
        end

        values[#values + 1] = formatValue(value)
    end

    return "{" .. table.concat(values, ",") .. "}"
end

--- Register a client for push notifications
-- Subscribes a client to receive updates when feature values change
-- @param clientIP string IP address of the client (OM or mobile app)
-- @param destPort number Destination port for sending updates (typically 4344)
-- @param sessionID number Unique session identifier for this subscription
-- @param subscriptions table Array of {object, featureIndex} pairs to monitor
-- @return string Response format: clientReport:sessionID:{current values}
function SYSTEM:clientRegister(clientIP, destPort, sessionID, subscriptions)
    Logger:getShared():info("SYSTEM:clientRegister(" .. clientIP .. ", " .. destPort .. ", " .. sessionID .. ", [" .. #subscriptions .. " features])")

    -- Ensure clientSubscriptions table exists (for hot-reload compatibility)
    if self.clientSubscriptions == nil then
        self.clientSubscriptions = {}
    end

    -- Store the subscription
    local key = clientIP .. ":" .. destPort .. ":" .. sessionID
    local now = os.time()
    self.clientSubscriptions[key] = {
        ip = clientIP,
        port = destPort,
        sessionID = sessionID,
        subscriptions = subscriptions,
        registeredAt = now,
        lastActivityAt = now,
    }

    -- Collect current values
    local valuesStr = self:collectFeatureValues(subscriptions)

    -- Return immediate response with current values
    -- Format: clientReport:sessionID:{values}
    return "clientReport:" .. sessionID .. ":" .. valuesStr
end

--- Unregister a client subscription
-- Removes an active client subscription from the system
-- @param clientIP string IP address of the client
-- @param destPort number Destination port
-- @param sessionID number Session identifier to remove
-- @return string The sessionID as string
function SYSTEM:clientDestroy(clientIP, destPort, sessionID)
    Logger:getShared():info("SYSTEM:clientDestroy(" .. clientIP .. ", " .. destPort .. ", " .. sessionID .. ")")

    if self.clientSubscriptions == nil then
        return tostring(sessionID)
    end

    local key = clientIP .. ":" .. destPort .. ":" .. sessionID

    if self.clientSubscriptions[key] then
        self.clientSubscriptions[key] = nil
        Logger:getShared():debug("SYSTEM:clientDestroy - removed subscription " .. key)
    else
        Logger:getShared():warn("SYSTEM:clientDestroy - subscription not found: " .. key)
    end

    return tostring(sessionID)
end

--- Get all pending client reports for push notifications (diff-based)
-- Called periodically by Go to collect and send updates to subscribed clients.
-- Only includes subscriptions whose values actually changed since last call,
-- avoiding string allocation on quiet systems.
-- @return string JSON array: [{"ip":"...","port":...,"sessionID":...,"values":"..."},...]
function SYSTEM:getPendingClientReports()
    if self.clientSubscriptions == nil then
        return "[]"
    end

    local reports = {}

    for key, sub in pairs(self.clientSubscriptions) do
        local subs = sub.subscriptions
        local cache = sub._valueCache

        -- First call: no cache yet — always report
        if not cache then
            cache = {}
            sub._valueCache = cache
        end

        -- Read current values and detect changes (no string allocation)
        local changed = false
        local n = #subs
        for i = 1, n do
            local s = subs[i]
            local obj = s[1]
            local featureIndex = s[2]
            local value = nil
            if obj ~= nil and type(obj.get) == "function" then
                local ok, result = pcall(obj.get, obj, featureIndex)
                if ok then value = result end
            end
            -- Compare raw values — no string conversion
            if cache[i] ~= value then
                changed = true
                cache[i] = value
            end
        end

        -- First report (cache was empty) always sends
        if not changed and n > 0 and cache._sent then
            -- Values unchanged since last report — skip
        else
            cache._sent = true
            -- Build values string only when changed
            local values = {}
            for i = 1, n do
                values[i] = formatValue(cache[i])
            end
            local valuesStr = "{" .. table.concat(values, ",") .. "}"
            local escapedValues = valuesStr:gsub('"', '\\"')
            reports[#reports + 1] = string.format('{"ip":"%s","port":%d,"sessionID":%d,"values":"%s"}',
                sub.ip, sub.port, sub.sessionID, escapedValues)
        end
    end

    return "[" .. table.concat(reports, ",") .. "]"
end

--- Get count of active client subscriptions
-- @return number Count of currently active subscriptions
function SYSTEM:getClientSubscriptionCount()
    if self.clientSubscriptions == nil then
        return 0
    end
    local count = 0
    for _ in pairs(self.clientSubscriptions) do
        count = count + 1
    end
    return count
end

-- ==========================================================================
-- VCLU: Client TTL and cleanup functions
-- ==========================================================================

--- Get client Time-To-Live (TTL) configuration
-- Retrieves TTL from CLU feature (FEATURE_VCLU_CLIENT_TTL = 100) or uses default
-- @return number TTL in seconds
function SYSTEM:getClientTTL()
    if self.clu and type(self.clu.get) == "function" then
        local ok, ttl = pcall(self.clu.get, self.clu, 100) -- CLU.FEATURE_VCLU_CLIENT_TTL = 100
        if ok and type(ttl) == "number" and ttl > 0 then
            return ttl
        end
    end
    return DEFAULT_CLIENT_TTL
end

--- Cleanup expired client subscriptions
-- Called periodically by Go client_reporter to remove stale sessions
-- @return number Count of cleaned up subscriptions
function SYSTEM:cleanupExpiredClients()
    if self.clientSubscriptions == nil then
        return 0
    end

    local ttl = self:getClientTTL()
    local now = os.time()
    local expiredKeys = {}

    for key, sub in pairs(self.clientSubscriptions) do
        local age = now - (sub.lastActivityAt or sub.registeredAt or 0)
        if age > ttl then
            table.insert(expiredKeys, key)
        end
    end

    for _, key in ipairs(expiredKeys) do
        Logger:getShared():info("SYSTEM:cleanupExpiredClients - removing expired: " .. key)
        self.clientSubscriptions[key] = nil
    end

    return #expiredKeys
end

--- Get client subscription information
-- Returns detailed information about all active subscriptions for debugging purposes
-- @return string JSON array with subscription details including TTL status
function SYSTEM:getClientSubscriptionInfo()
    if self.clientSubscriptions == nil then
        return "[]"
    end

    local now = os.time()
    local ttl = self:getClientTTL()
    local results = {}

    for key, sub in pairs(self.clientSubscriptions) do
        local age = now - (sub.lastActivityAt or sub.registeredAt or 0)
        local remaining = ttl - age
        local entry = string.format(
            '{"key":"%s","age":%d,"remaining":%d,"features":%d}',
            key, age, remaining, #sub.subscriptions
        )
        table.insert(results, entry)
    end

    return "[" .. table.concat(results, ",") .. "]"
end

--- Initialize the shared SYSTEM instance (singleton pattern)
-- This creates the single shared instance used throughout the runtime
shared = SYSTEM:new()

return SYSTEM
