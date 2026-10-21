--- ExposedObjects module for unified MQTT/HomeKit exposure
-- Provides a simple API to expose objects to external integrations
-- without requiring knowledge of MQTT or HomeKit internals.
-- @module ExposedObjects

-- ==========================================================================
-- ExposedHandle class
-- ==========================================================================

--- Handle returned by expose() for managing exposed objects
-- @type ExposedHandle
local ExposedHandle = {}
local checkOptions -- defined after capabilities

-- Counts changes of what is exposed and how; dashboards reuse their rooms until it moves
local function changed()
    if ExposedObjects then ExposedObjects._generation = (ExposedObjects._generation or 0) + 1 end
end
ExposedHandle.__index = ExposedHandle

--- Create a new ExposedHandle
-- @param path string Unique path identifier
-- @param obj table Object being exposed
-- @param objType string Type of object (switch, light, sensor, etc.)
-- @param options table Options passed to expose()
-- @return ExposedHandle
-- a handle's own copy of the options (plain tables such as tags one level deep), so
-- objects exposed with one table do not share names, tags or later changes
local function copyOptions(options)
    local out = {}
    for k, v in pairs(options or {}) do
        if type(v) == "table" and getmetatable(v) == nil then
            local t = {}
            for k2, v2 in pairs(v) do t[k2] = v2 end
            v = t
        end
        out[k] = v
    end
    return out
end

function ExposedHandle:new(path, obj, objType, options)
    local h = {
        path = path,
        object = obj,
        type = objType,
        options = copyOptions(options),
        _exposed = true,
        _lastChanged = nil,  -- timestamp (epoch seconds), nil = never changed
        _lastExecute = 0,    -- for scene debounce
        _cooldownMs = 300,   -- 300ms cooldown for scenes
        _unsubscribe = nil,  -- unsubscribe function from object:onChange()
    }
    setmetatable(h, self)
    return h
end

--- Set the display name (fluent)
-- @api public
-- @param n string Display name
-- @return ExposedHandle self for chaining
function ExposedHandle:name(n)
    self.options.name = n
    changed()
    return self
end

--- Mark as read-only (fluent)
-- @api public
-- @return ExposedHandle self for chaining
function ExposedHandle:readonly()
    self.options.readonly = true
    changed()
    return self
end

--- Expose only to MQTT (fluent)
-- @api public
-- @return ExposedHandle self for chaining
function ExposedHandle:mqttOnly()
    self.options.homekit = false
    changed()
    return self
end

--- Expose only to HomeKit (fluent)
-- @api public
-- @return ExposedHandle self for chaining
function ExposedHandle:homekitOnly()
    self.options.mqtt = false
    changed()
    return self
end

--- Set group (fluent)
-- @api public
-- @param g string Group name
-- @return ExposedHandle self for chaining
function ExposedHandle:group(g)
    self.options.group = g
    changed()
    return self
end

--- Set physical area/room (fluent)
-- @api public
-- @param a string Area name (e.g., "Kuchnia", "Salon")
-- @return ExposedHandle self for chaining
function ExposedHandle:area(a)
    self.options.area = a
    changed()
    return self
end

--- Set tags (fluent)
-- @api public
-- @param t table Array of tag strings (e.g., {"critical", "noAutoOff"})
-- @return ExposedHandle self for chaining
function ExposedHandle:tags(t)
    self.options.tags = t
    changed()
    return self
end

--- Set description for AI agents (fluent)
-- @api public
-- @param d string Free-text description with rules and constraints
-- @return ExposedHandle self for chaining
function ExposedHandle:description(d)
    self.options.description = d
    changed()
    return self
end

--- Update options at runtime
-- @api public
-- @param opts table Options to merge (filter = false removes a filter)
-- @return boolean Success, false if handle is inactive
function ExposedHandle:update(opts)
    if not self._exposed then
        return false
    end
    checkOptions(self.type, opts)
    for k, v in pairs(opts) do
        self.options[k] = v
    end
    changed()
    if opts.filter ~= nil then StateChange.setFilter(self.object, opts.filter or nil) end
    if StateBus then
        StateBus:getShared():emit("object_updated", { path = self.path })
    end
    return true
end

--- Rename the exposed object
-- @api public
-- @param newName string New display name
-- @return boolean Success, false if handle is inactive
function ExposedHandle:rename(newName)
    if not self._exposed then
        return false
    end
    self.options.name = newName
    changed()
    if StateBus then
        StateBus:getShared():emit("object_updated", { path = self.path })
    end
    return true
end

--- Remove from exposed objects (idempotent - safe to call multiple times)
-- @api public
function ExposedHandle:unexpose()
    if not self._exposed then
        return -- already unexposed
    end
    self._exposed = false
    ExposedObjects:_remove(self.path)
end

--- Get the path identifier
-- @api public
-- @return string path
function ExposedHandle:getPath()
    return self.path
end

--- Check if still exposed
-- @api public
-- @return boolean
function ExposedHandle:isExposed()
    return self._exposed
end

--- Execute scene with debounce protection
-- Prevents double-execution from HomeKit echo
-- @api public
-- @return boolean Success, false if handle is inactive or in cooldown
function ExposedHandle:executeScene()
    if not self._exposed then
        return false
    end
    -- Use Go's wall clock time (ms precision) instead of os.clock() which is CPU time
    local now = __go_time_ms and __go_time_ms() or (os.time() * 1000)
    if now - self._lastExecute < self._cooldownMs then
        if Logger then
            Logger:getShared():debug("[ExposedObjects] Scene %s: ignoring echo (cooldown)", self.path)
        end
        return false -- ignored
    end
    self._lastExecute = now

    if self.object and type(self.object.execute) == "function" then
        self.object:execute(0)
        return true
    end
    return false
end

--- Get last changed timestamp (epoch seconds)
-- @api public
-- @return number|nil Epoch seconds when state last changed, or nil if never
function ExposedHandle:getLastChanged()
    return self._lastChanged
end

--- Get current value from object
-- @api public
-- @return any Current value, or nil if handle is inactive
function ExposedHandle:getValue()
    if not self._exposed then
        return nil
    end
    -- Prefer cached state (avoids blocking UDP call for Remote* objects)
    if self.object and self.object._lastState ~= nil then
        return self.object._lastState
    end
    if self.object and type(self.object.getValue) == "function" then
        local ok, val = pcall(function() return self.object:getValue() end)
        if ok then return val end
    end
    -- Fallback: raw get(0) for simple objects without getValue()
    if self.object and type(self.object.get) == "function" then
        local ok, val = pcall(function() return self.object:get(0) end)
        if ok then return val end
    end
    return nil
end

--- Set value on object
-- @api public
-- @param value any Value to set
-- @return boolean Success, false if handle is inactive or readonly
function ExposedHandle:setValue(value)
    if not self._exposed then
        return false
    end
    if self.options.readonly then
        return false
    end
    if self.object and type(self.object.set) == "function" then
        local obj = self.object
        local old, stateful = StateChange.read(obj)
        if stateful then self._lastSeen = old else old = self._lastSeen end
        local mark = StateChange.mark(obj)
        local ok = pcall(function() obj:set(0, value) end)
        if ok then
            self._lastChanged = os.time()
            if not StateChange.publishedSince(obj, mark) then
                -- Write-only controls report the requested value, others the real one
                local new = value
                if stateful then new = StateChange.read(obj) end
                self._lastSeen = new
                -- A plugin's next _notify() must not report this change again
                if obj._notify then obj._notified = new end
                StateChange.publish(self.path, obj, old, new, { type = self.type })
            end
        end
        return ok
    end
    return false
end

-- ==========================================================================
-- Capabilities definitions
-- ==========================================================================

local capabilities = {
    switch = { read = true, write = true, binary = true },
    -- brightness per object: a DOUT exposed as a light is on/off only
    light = { read = true, write = true },
    dimmer = { read = true, write = true, brightness = true, range = {0, 1} },
    sensor = { read = true, write = false, binary = true },
    binary_sensor = { read = true, write = false, binary = true },
    motion = { read = true, write = false, binary = true, event = true },
    temperature = { read = true, write = false, numeric = true, unit = "°C", range = {-40, 80} },
    humidity = { read = true, write = false, numeric = true, unit = "%", range = {0, 100} },
    illuminance = { read = true, write = false, numeric = true, unit = "lx", range = {0, 100000} },
    cover = { read = true, write = true, position = true, range = {0, 100} },
    scene = { read = false, write = false, execute = true, stateless = true },
    number = { read = true, write = true, range = {0, 100} },
    button = { read = false, write = false, execute = true, event = true },
    -- New types
    lock = { read = true, write = true, binary = true },
    fan = { read = true, write = true, speed = true, range = {0, 100} },
    climate = { read = true, write = true, temperature = true, range = {5, 35} },
    garage_door = { read = true, write = true, binary = true },
}

-- filter is only for read-only numeric sensors: on a control it would
-- swallow commands, on a binary sensor or a scene it makes no sense
local function checkFilterOption(objType, filter)
    if filter == nil or filter == false then return end
    local caps = capabilities[objType] or {}
    if not (caps.numeric and caps.read and not caps.write) then
        error("ExposedObjects:expose() - filter is for read-only numeric sensors, not '" .. tostring(objType) .. "'", 4)
    end
    local ok, err = pcall(StateChange.checkFilter, filter)
    if not ok then error("ExposedObjects:expose() - " .. tostring(err), 4) end
end

-- Raises for invalid expose options; nothing is changed before it passes
checkOptions = function(objType, opts)
    checkFilterOption(objType, opts.filter)
    if opts.history ~= nil and type(opts.history) ~= "boolean" then
        error("ExposedObjects:expose() - history must be true or false", 3)
    end
    if opts.dashboard ~= nil and type(opts.dashboard) ~= "boolean" then
        error("ExposedObjects:expose() - dashboard must be true or false", 3)
    end
end

-- ==========================================================================
-- hashStable - deterministic hash for path generation
-- ==========================================================================

local function hashStable(obj, objType)
    local parts = {}
    -- Use description and address if available
    if obj.description then table.insert(parts, obj.description) end
    if obj.address then table.insert(parts, tostring(obj.address)) end
    if obj.id then table.insert(parts, tostring(obj.id)) end
    table.insert(parts, objType)

    local str = table.concat(parts, "_")
    -- Simple hash: sum of ASCII codes modulo
    local h = 0
    for i = 1, #str do
        h = (h * 31 + string.byte(str, i)) % 2147483647
    end
    return string.format("%08x", h)
end

-- ==========================================================================
-- ExposedObjects module (singleton)
-- ==========================================================================

ExposedObjects = {
    _objects = {},        -- path -> ExposedHandle
    _objectToPath = {},   -- weak table: object_ref -> path (stability after restart)
    _pluginContext = nil, -- current plugin ID (set by plugin sandbox)
    _noUnsubscribeWarned = {}, -- weak set: objects already warned about onChange()
}

-- Make _objectToPath a weak table (keys are garbage collected)
setmetatable(ExposedObjects._objectToPath, { __mode = "k" })
setmetatable(ExposedObjects._noUnsubscribeWarned, { __mode = "k" })

--- Generate a stable path for an object
-- @param obj table Object to expose
-- @param objType string Object type
-- @param options table Options with optional path/id/name
-- @param pluginId string Optional plugin ID
-- @return string Generated path
local function generatePath(obj, objType, options, pluginId)
    -- Priority 1: explicit path
    if options.path then
        return options.path
    end

    -- Priority 2: check if object was already exposed (weak table lookup)
    if ExposedObjects._objectToPath[obj] then
        return ExposedObjects._objectToPath[obj]
    end

    -- Priority 3: check if object exists in _registry (same object reference)
    -- This ensures expose(dom1.DOU0789) uses registry path CLU220000592.DOU0789
    if _registry then
        for path, regObj in pairs(_registry) do
            if regObj == obj then
                return path
            end
        end
    end

    -- Priority 4: plugin_id:id or plugin_id:name or obj._id or obj._name
    local base = pluginId or "vclu"
    local id = options.id or obj._id or options.name or obj._name

    -- Priority 5: stable hash
    if not id then
        id = hashStable(obj, objType)
    end

    return base .. ":" .. id
end

--- Expose an object to MQTT and HomeKit
-- @param obj table Object to expose (must implement get(0))
-- @param objType string Type: switch, light, dimmer, sensor, temperature, etc.
-- @param options table Optional settings: name, path, id, readonly, hidden, mqtt, homekit, group,
--   filter = {delta, heartbeat} for noisy numeric sensors (see StateChange.setFilter),
--   history = false to keep the object's changes out of the history store
--   area, floor (the house model: lib/panel, section:area, dash:floor),
--   panel/dashboard = false keeps it off panels/dashboards, true puts it on them outside kinds
--   (a dashboard has widgets only for light, switch, dimmer, cover, temperature, humidity, lock)
-- @return ExposedHandle Handle for managing the exposed object
function ExposedObjects:expose(obj, objType, options)
    options = options or {}

    -- Validate object
    if obj == nil then
        error("ExposedObjects:expose() - object is nil")
    end

    -- Validate type
    if not capabilities[objType] then
        local validTypes = {}
        for k in pairs(capabilities) do table.insert(validTypes, k) end
        error("ExposedObjects:expose() - unknown type '" .. tostring(objType) ..
              "'. Valid types: " .. table.concat(validTypes, ", "))
    end

    checkOptions(objType, options)

    -- Check if hidden
    if options.hidden then
        if Logger then
            Logger:getShared():debug("[ExposedObjects] Object marked as hidden, not exposing")
        end
        -- Return a dummy handle that does nothing
        local dummy = ExposedHandle:new("_hidden_", obj, objType, options)
        dummy._exposed = false
        return dummy
    end

    -- Check if object was already exposed (identity is locked)
    local existingPath = self._objectToPath[obj]
    if existingPath then
        local existing = self._objects[existingPath]
        if existing and existing:isExposed() then
            -- Warn if user tries to change identity (id/path)
            if options.id or options.path then
                local attemptedId = options.path or options.id
                if Logger then
                    Logger:getShared():warn(
                        "[ExposedObjects] Identity change ignored for '%s'. " ..
                        "id/path is locked after first expose. Use :rename() to change display name.",
                        existingPath
                    )
                end
            end
            -- the type is locked too: check the filter against it
            checkOptions(existing.type, options)
            -- Update only non-identity options (name, readonly, mqtt, homekit, etc.)
            for k, v in pairs(options) do
                if k ~= "id" and k ~= "path" then
                    existing.options[k] = v
                end
            end
            StateChange.setFilter(obj, existing.options.filter)
            changed()
            if Logger then
                Logger:getShared():debug("[ExposedObjects] Re-expose (idempotent): %s", existingPath)
            end
            return existing
        end
    end

    -- Generate path for new object
    local pluginId = self._pluginContext
    local path = generatePath(obj, objType, options, pluginId)

    -- Check for path collision (different object, same path)
    if self._objects[path] then
        local existing = self._objects[path]
        if existing.object ~= obj then
            if Logger then
                Logger:getShared():error(
                    "[ExposedObjects] Path collision: '%s' already used by another object",
                    path
                )
            end
            error("ExposedObjects:expose() - path '" .. path .. "' already in use by another object")
        end
        -- Same object, same path - idempotent
        return existing
    end

    -- Create handle
    local handle = ExposedHandle:new(path, obj, objType, options)

    -- Apply defaults
    if options.mqtt == nil then handle.options.mqtt = true end
    if options.homekit == nil then handle.options.homekit = true end

    -- Store in ExposedObjects registry
    self._objects[path] = handle
    self._objectToPath[obj] = path
    changed()

    -- Ensure object is also in _registry (single source of truth for all objects).
    -- Objects from om.lua are already there; scenes/plugins added via expose() are not.
    if _registry and not _registry[path] then
        _registry[path] = obj
        if StateChange then StateChange.attachHistory(obj, nil) end
        handle._addedToRegistry = true  -- track so we can clean up on unexpose
    elseif _registry and type(_registry[path]) == "function" then
        -- An object at "get" or "value" would hide _:get or _:value from every module.
        if Logger then
            Logger:getShared():warn("[ExposedObjects] path '%s' is a name of a registry method, so _:get "
                .. "and _:value will not find this object; use a path such as vclu:%s", path, path)
        end
    end

    StateChange.setFilter(obj, options.filter)

    -- Register onChange callback to propagate state changes
    -- Store unsubscribe function for cleanup on unexpose()
    if type(obj.onChange) == "function" then
        local unsubscribe = obj:onChange(function(value, old)
            -- Plugin sensors emit OnChange without old; fall back to last seen
            if old == nil then old = handle._lastSeen end
            -- Remote*/Virtual* filter in updateState, before their own callbacks
            if not obj._selfFilter then
                local report
                report, old = StateChange.filter(obj, old, value)
                if not report then return end
            end
            handle._lastSeen = value
            if handle._exposed then
                handle._lastChanged = os.time()
                StateChange.publish(handle.path, obj, old, value, { type = handle.type })
            end
        end)
        -- Store unsubscribe if returned (required by Best Practices)
        if type(unsubscribe) == "function" then
            handle._unsubscribe = unsubscribe
        elseif not self._noUnsubscribeWarned[obj] then
            self._noUnsubscribeWarned[obj] = true
            if Logger then
                Logger:getShared():warn("[ExposedObjects] %s: onChange() returned no unsubscribe function; " ..
                    "its callback stays registered after unexpose()", path)
            end
        end
    end

    if Logger then
        Logger:getShared():info("[ExposedObjects] Exposed: %s (type=%s, name=%s)",
            path, objType, options.name or "unnamed")
    end

    -- Emit event for MQTT/HomeKit to pick up
    if StateBus then
        StateBus:getShared():emit("object_exposed", {
            path = path,
            type = objType,
            name = options.name,
            options = options,
        })
    end

    return handle
end

--- Remove an exposed object by path
-- @param path string Path to remove
function ExposedObjects:_remove(path)
    local handle = self._objects[path]
    if handle then
        -- Unsubscribe from object's onChange (cleanup callback)
        if handle._unsubscribe and type(handle._unsubscribe) == "function" then
            local ok, err = pcall(handle._unsubscribe)
            if not ok and Logger then
                Logger:getShared():warn("[ExposedObjects] Unsubscribe failed for %s: %s", path, tostring(err))
            end
            handle._unsubscribe = nil
        end

        StateChange.setFilter(handle.object, nil)

        -- Remove from _registry if we added it (don't remove om.lua objects)
        if handle._addedToRegistry and _registry then
            _registry[path] = nil
        end

        self._objects[path] = nil
        -- Note: _objectToPath weak table will clean up automatically
        changed()

        if Logger then
            Logger:getShared():info("[ExposedObjects] Unexposed: %s", path)
        end

        if StateBus then
            StateBus:getShared():emit("object_unexposed", { path = path })
        end
    end
end

--- Get an exposed handle by path (fields as in list())
-- @api public
-- @param path string Exposed path
-- @return ExposedHandle or nil when nothing is exposed at that path
function ExposedObjects:get(path)
    return self._objects[path]
end

--- All exposed objects: the live map, read it and never change it
-- @api public
-- @return table path -> handle; stable fields: type, options.name, options.area, options.tags, getValue()
function ExposedObjects:list()
    return self._objects
end

local function setOf(list)
    local s = {}
    for _, v in ipairs(list or {}) do s[v] = true end
    return s
end

-- Lights, switches, dimmers and locks first, then covers, then the rest
local SCREEN_ORDER = { light = 1, switch = 1, dimmer = 1, lock = 1, cover = 2 }

-- Polish letters sort right after their base letter (c < ć < d), case-insensitive
local POLISH = { ["ą"] = "a\2", ["ć"] = "c\2", ["ę"] = "e\2", ["ł"] = "l\2", ["ń"] = "n\2",
    ["ó"] = "o\2", ["ś"] = "s\2", ["ź"] = "z\2", ["ż"] = "z\3",
    ["Ą"] = "a\2", ["Ć"] = "c\2", ["Ę"] = "e\2", ["Ł"] = "l\2", ["Ń"] = "n\2",
    ["Ó"] = "o\2", ["Ś"] = "s\2", ["Ź"] = "z\2", ["Ż"] = "z\3" }

-- Sort key of a name: each character as its base letter and a rank
local function sortKey(name)
    return (tostring(name):lower():gsub("[%z\1-\127\194-\244][\128-\191]*", function(c)
        return POLISH[c] or (c .. "\1")
    end))
end

-- What a screen (lib/panel, a dashboard section) shows of the given areas: objects of a kind
-- in opts.kinds or with options[opts.flag] = true, not flag = false, not in opts.skip, on
-- opts.floor when given; sorted as SCREEN_ORDER, then by opts.key(name) (default: Polish
-- order, then by name), then by path.
-- @return table { { path, handle }, ... }
function ExposedObjects:_inArea(areas, opts)
    local inArea, kinds, skip = setOf(areas), setOf(opts.kinds), setOf(opts.skip)
    -- a key of the caller's own (lib/panel) breaks ties by path, as it always did
    local byName = opts.key == nil
    local key = opts.key or sortKey
    local found = {}
    for path, h in pairs(self._objects) do
        local o = h.options or {}
        local flag = o[opts.flag]
        if inArea[o.area] and (opts.floor == nil or o.floor == opts.floor)
            and (flag == true or (flag ~= false and kinds[h.type])) and not skip[path] then
            found[#found + 1] = { path = path, handle = h, name = tostring(o.name or path), key = key(o.name or path) }
        end
    end
    table.sort(found, function(a, b)
        local oa, ob = SCREEN_ORDER[a.handle.type] or 3, SCREEN_ORDER[b.handle.type] or 3
        if oa ~= ob then return oa < ob end
        if a.key ~= b.key then return a.key < b.key end
        if byName and a.name ~= b.name then return a.name < b.name end
        return a.path < b.path
    end)
    return found
end

-- The areas on a floor: those in order first, in that order, then the rest by key(area)
-- (default: Polish order), then by name
function ExposedObjects:_areasOn(floor, order, key)
    key = key or sortKey
    local seen, list = {}, {}
    for _, h in pairs(self._objects) do
        local o = h.options or {}
        if o.floor == floor and o.area and not seen[o.area] then
            seen[o.area] = true
            list[#list + 1] = o.area
        end
    end
    local rank, keys = {}, {}
    for i, a in ipairs(order or {}) do rank[a] = i end
    for _, a in ipairs(list) do keys[a] = key(a) end
    table.sort(list, function(a, b)
        local ra, rb = rank[a] or math.huge, rank[b] or math.huge
        if ra ~= rb then return ra < rb end
        if keys[a] ~= keys[b] then return keys[a] < keys[b] end
        return a < b
    end)
    return list
end

--- Get count of exposed objects
-- @return number Count
function ExposedObjects:count()
    local n = 0
    for _ in pairs(self._objects) do n = n + 1 end
    return n
end

--- Get capabilities for a type
-- @param objType string Type name
-- @return table Capabilities or nil
function ExposedObjects:getCapabilities(objType)
    return capabilities[objType]
end

--- True when the handle has a brightness: a dimmer, or a light whose object can dim
-- @param handle ExposedHandle
-- @return boolean
function ExposedObjects.hasBrightness(handle)
    if handle.type == "dimmer" then return true end
    local o = handle.object
    return handle.type == "light" and type(o) == "table" and type(o.getBrightness) == "function"
end

--- Capabilities of one exposed object (the type's, with brightness from the object)
-- @param handle ExposedHandle
-- @return table Capabilities (a copy)
function ExposedObjects:getHandleCapabilities(handle)
    local caps = {}
    for k, v in pairs(capabilities[handle.type] or {}) do caps[k] = v end
    if ExposedObjects.hasBrightness(handle) then
        caps.brightness = true
    end
    return caps
end

--- Convert exposed objects to JSON for HomeKit/MQTT
-- @param filter table Optional filter: { mqtt = true/false, homekit = true/false }
-- @return string JSON array
function ExposedObjects:toJSON(filter)
    filter = filter or {}
    local results = {}

    for path, handle in pairs(self._objects) do
        local include = true

        -- Apply filters
        if filter.mqtt ~= nil and handle.options.mqtt ~= filter.mqtt then
            include = false
        end
        if filter.homekit ~= nil and handle.options.homekit ~= filter.homekit then
            include = false
        end

        if include then
            local value = handle:getValue()
            if type(value) ~= "string" and type(value) ~= "boolean" and type(value) ~= "number" then
                value = nil
            end

            local caps = self:getHandleCapabilities(handle)
            -- caps stays a JSON object even when empty (Go decodes it into a struct)
            local capsList = {}
            for _, k in ipairs({ "binary", "brightness", "position", "execute", "stateless" }) do
                if caps[k] then table.insert(capsList, JSON._encodeString(k) .. ":true") end
            end

            -- JSON.encode escapes strings and turns NaN/inf into null, so one
            -- odd name or reading cannot break the whole list
            local fields = {
                '"path":' .. JSON._encodeString(path),
                '"type":' .. JSON._encodeString(tostring(handle.type)),
                '"name":' .. JSON._encodeString(tostring(handle.options.name or path)),
                '"value":' .. JSON.encode(value),
                '"readonly":' .. (handle.options.readonly and "true" or "false"),
                '"caps":{' .. table.concat(capsList, ",") .. '}',
            }
            if handle.object and handle.object._satel then
                table.insert(fields, '"satel":true')
            end
            -- HomeKit: brightness only for objects that have it (a DOUT
            -- exposed as "light" is on/off); covers report their position
            local o = handle.object
            if type(o) == "table" and type(o.getBrightness) == "function" then
                table.insert(fields, '"dimmable":true')
            end
            if handle.type == "cover" and type(o) == "table" and type(o.getPosition) == "function" then
                -- a Grenton roller without a position (class 11) has its movement as the value
                table.insert(fields, '"hasPosition":true')
                local ok, pos = pcall(o.getPosition, o)
                if ok and type(pos) == "number" then
                    table.insert(fields, '"position":' .. JSON.encode(pos))
                end
            end
            for _, k in ipairs({ "area", "group", "description" }) do
                if handle.options[k] then
                    table.insert(fields, JSON._encodeString(k) .. ":" .. JSON._encodeString(tostring(handle.options[k])))
                end
            end
            if handle.options.tags and #handle.options.tags > 0 then
                local tagParts = {}
                for _, t in ipairs(handle.options.tags) do
                    table.insert(tagParts, JSON._encodeString(tostring(t)))
                end
                table.insert(fields, '"tags":[' .. table.concat(tagParts, ",") .. ']')
            end
            table.insert(results, "{" .. table.concat(fields, ",") .. "}")
        end
    end

    return "[" .. table.concat(results, ",") .. "]"
end

--- Lightweight list for the access control panel.
--- Merges _registry and ExposedObjects without calling getValue() (avoids
--- slow network I/O on RemoteObjects).  Returns a JSON array string.
--- Source: "registry" for _registry objects, "module" for user modules,
--- "plugin" for plugin-exposed.
-- @return string JSON array of {path, type, name, cluId, source}
function ExposedObjects:accessList()
    local r = {}
    local seen = {}

    -- 1. _registry objects (skip plugin instances — not controllable devices)
    if _registry then
        for path, obj in pairs(_registry) do
            if type(path) == "string" and type(obj) == "table"
               and not obj._isPlugin then
                seen[path] = true
                local cid, oid = path:match("^([^.]+)%.(.+)$")
                r[#r+1] = '{"path":"' .. path
                    .. '","type":"' .. tostring(obj._type or "unknown")
                    .. '","name":"' .. tostring(obj._name or oid or ""):gsub('"', '\\"')
                    .. '","cluId":"' .. (cid or "")
                    .. '","source":"registry"}'
            end
        end
    end

    -- 2. ExposedObjects not already in _registry
    for path, handle in pairs(self._objects) do
        if not seen[path] then
            local src = "module"
            if path:find("^[%w%-]+:") then
                local prefix = path:match("^([%w%-]+):")
                if prefix ~= "scene" and prefix ~= "vclu" then
                    src = "plugin"
                end
            end
            r[#r+1] = '{"path":"' .. path
                .. '","type":"' .. (handle.type or "scene")
                .. '","name":"' .. ((handle.options and handle.options.name) or path):gsub('"', '\\"')
                .. '","cluId":""'
                .. ',"source":"' .. src .. '"}'
        end
    end

    table.sort(r)
    return "[" .. table.concat(r, ",") .. "]"
end

--- Set plugin context (called by PluginSandbox)
-- @param pluginId string or nil
function ExposedObjects:setPluginContext(pluginId)
    self._pluginContext = pluginId
end

--- Unexpose all objects from a specific plugin
-- @param pluginId string Plugin ID prefix
function ExposedObjects:unexposeByPlugin(pluginId)
    local prefix = pluginId .. ":"
    local toRemove = {}

    for path in pairs(self._objects) do
        if path:sub(1, #prefix) == prefix then
            table.insert(toRemove, path)
        end
    end

    for _, path in ipairs(toRemove) do
        self:_remove(path)
    end

    if Logger and #toRemove > 0 then
        Logger:getShared():info("[ExposedObjects] Unexposed %d objects from plugin %s", #toRemove, pluginId)
    end
end

-- ==========================================================================
-- Auto-expose registry objects
-- ==========================================================================

local TypeMap = require("TypeMap") -- central type mapping

--- Auto-expose all objects from _registry, respecting AccessControl.
-- Objects already exposed via manual expose() are skipped (user overrides win).
-- AccessControl determines per-integration visibility for remaining objects:
--   "hidden" from both mqtt+homekit → skipped entirely
--   "hidden" from one → that integration disabled (mqtt=false or homekit=false)
--   "readonly" → exposed as readonly
-- @param options table Optional default options for all objects
-- @return number Count of newly exposed objects
function ExposedObjects:exposeRegistry(options)
    if not _registry then
        if Logger then
            Logger:getShared():warn("[ExposedObjects] _registry not available")
        end
        return 0
    end

    options = options or {}
    local count = 0
    local skipped = 0

    for path, obj in pairs(_registry) do
        -- Skip plugins (they manage their own exposure)
        if obj._isPlugin then
            -- noop
        elseif type(obj._getExposeType) == "function" and obj:_getExposeType() == nil then
            -- nothing to expose it as (a thermostat): no switch fallback
        elseif not self._objects[path] then
            -- Check AccessControl for per-integration visibility
            local mqttHidden = AccessControl and AccessControl.isHidden("mqtt", path)
            local homekitHidden = AccessControl and AccessControl.isHidden("homekit", path)

            if mqttHidden and homekitHidden then
                -- Hidden from both integrations — skip entirely
                skipped = skipped + 1
            else
                -- NOTE: readonly is deliberately NOT snapshotted into objOptions here.
                --
                -- This used to read:
                --     if mqttReadonly and homekitReadonly then objOptions.readonly = true end
                -- which was wrong three times over:
                --   * AND, not per-integration — an object set readonly for MQTT only
                --     (the common case) never got the flag at all;
                --   * it ran only for objects not already exposed, so anything exposed
                --     explicitly from user code was skipped entirely;
                --   * it was a snapshot taken at expose time, so changing the level in
                --     the UI afterwards changed nothing until a reload.
                --
                -- Access is now resolved LIVE, per integration, at each point where a
                -- write can happen (HA command handler, HomeKit, MCP). A snapshot here
                -- could only go stale and give a false sense of protection.
                local objOptions = {}
                for k, v in pairs(options) do objOptions[k] = v end
                objOptions.path = path
                objOptions.name = objOptions.name or obj._name or path
                if mqttHidden then objOptions.mqtt = false end
                if homekitHidden then objOptions.homekit = false end

                -- If object has :expose() method, use it (RemoteObject)
                if type(obj.expose) == "function" then
                    local ok, err = pcall(function()
                        obj:expose(objOptions)
                    end)
                    if ok then
                        count = count + 1
                    elseif Logger then
                        Logger:getShared():warn("[ExposedObjects] Failed to expose %s: %s", path, tostring(err))
                    end
                else
                    -- Plain table — determine type and expose
                    local objType = obj._type or "switch"
                    local exposeType = TypeMap.getExposeType(objType, "switch")

                    local ok, err = pcall(function()
                        self:expose(obj, exposeType, objOptions)
                    end)
                    if ok then
                        count = count + 1
                    elseif Logger then
                        Logger:getShared():warn("[ExposedObjects] Failed to expose %s: %s", path, tostring(err))
                    end
                end
            end
        end
    end

    if Logger then
        local msg = string.format("[ExposedObjects] Auto-exposed %d objects from registry", count)
        if skipped > 0 then
            msg = msg .. string.format(" (%d hidden by AccessControl)", skipped)
        end
        Logger:getShared():info(msg)
    end

    return count
end

-- ==========================================================================
-- State history (kept by the Go history store)
-- ==========================================================================

--- Get recent state changes, newest first. Compatible wrapper over the Go
-- history store; entries keep the old `new` and `time` (seconds) aliases.
-- @param path string|nil Filter by device path (nil = all)
-- @param limit number Max entries to return (default 50, max 500)
-- @return string JSON array
function ExposedObjects.getHistory(path, limit)
    limit = math.min(limit or 50, 500)
    if not __go_history_query then return "[]" end
    if path == "" then path = nil end
    local entries = __go_history_query(path, limit)
    for _, e in ipairs(entries) do
        e.new = e.value
        e.time = math.floor(e.atMs / 1000)
    end
    if #entries == 0 then return "[]" end
    return JSON.encode(entries)
end

-- ==========================================================================
-- Global functions
-- ==========================================================================

--- Global function to expose an object.
-- A custom object's onChange(cb) must return a function that removes cb: unexpose() calls it,
-- otherwise the callback stays registered and expose() logs a warning.
-- @api public
-- @param obj table Object to expose
-- @param objType string Type (switch, light, sensor, etc.)
-- @param options table Optional settings
-- @return ExposedHandle
function expose(obj, objType, options)
    return ExposedObjects:expose(obj, objType, options)
end

--- Global function to auto-expose all registry objects
-- Call this after om.lua is loaded to expose all objects to MQTT/HomeKit.
-- Objects can then be modified via their handles.
-- @api public
-- @param options table Optional default options
-- @return number Count of exposed objects
function exposeRegistry(options)
    return ExposedObjects:exposeRegistry(options)
end

return ExposedObjects
