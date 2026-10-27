--- Virtual thermostat: keeps a room at its target with any temperature sensor and any
-- on/off output. Same interface as RemoteThermostat (getValue, getTarget, getSetpoint,
-- setSetpoint, getState, getMode, CHANGE_EVENTS), so expose(), lib/panel and dashboards
-- take it as a Grenton thermostat.
-- @module objects.VirtualThermostat
-- @inherits VirtualObject
--
-- Usage:
--   local th = VirtualThermostat:new("salon", {
--       source = _:get("z2m.salon_temp"), output = _:get("CLU1.DOU0123"),
--       hysteresis = 0.5, setpoint = 21, holiday = 16, persist = true })
--   expose(th, "climate", { name = "Salon", area = "Salon" })

VirtualThermostat = setmetatable({}, {__index = VirtualObject})
VirtualThermostat.__index = VirtualThermostat

-- Events besides OnChange that change what the object shows (a panel redraws on them)
VirtualThermostat.CHANGE_EVENTS = {"OnSettingsChange"}

-- Internals stay local: the class holds only the public API
local MODE_SETPOINT, MODE_HOLIDAY = 0, 1
local TICK_MS = 10000 -- staleness and maxOn checked, a first reading picked up
local PERSIST_NS = "virtual-objects" -- shared with VirtualObject
local live = setmetatable({}, {__mode = "k"}) -- thermostats of this Lua state
local OPTIONS = { source = true, output = true, hysteresis = true, direction = true, setpoint = true,
    holiday = true, min = true, max = true, staleAfter = true, maxOn = true, persist = true }
-- Field names in OnSettingsChange as RemoteThermostat's (_state there is the state)
local EVENT_FIELD = { _st = "_state" }

-- Seconds on the runtime clock (virtual in tests and the sandbox)
local function now() return __go_time_ms() / 1000 end

local function finite(v)
    v = tonumber(v)
    if v and v - v == 0 then return v end
    return nil
end

local function log() return Logger:getShared() end

-- A listener's error is logged: it must not keep the output from switching
local function notify(self, fn, ...)
    local ok, err = pcall(fn, self, ...)
    if not ok then log():warn("[Thermostat] %s: a listener failed: %s", tostring(self._name), tostring(err)) end
end

-- An object or its registry path
local function resolve(v, what, need)
    if type(v) == "string" then
        local path = v
        v = _registry and _registry:get(path)
        if v == nil then error("VirtualThermostat: " .. what .. " '" .. path .. "' not found", 3) end
    end
    if type(v) ~= "table" then error("VirtualThermostat: " .. what .. " must be an object or its path", 3) end
    for _, m in ipairs(need) do
        if type(v[m]) ~= "function" then error("VirtualThermostat: " .. what .. " has no " .. m .. "()", 3) end
    end
    return v
end

local function clamp(self, v)
    if v < self._min then return self._min end
    if v > self._max then return self._max end
    return v
end

local function targetOf(self)
    if self._mode == MODE_HOLIDAY then return self._holiday end
    return self._point
end

local function restore(self, key)
    if key == true then
        if self._name == "Virtual" then error("VirtualThermostat: persist = true needs a name (or give persist a key)", 3) end
        key = self._name
    end
    if type(key) ~= "string" or key == "" or key == "get" or key == "set" or key == "save" or key == "toTable" then
        error("VirtualThermostat: persist must be true or a string key (not get/set/save/toTable)", 3)
    end
    -- its own key: a virtual object of the same name keeps its value apart
    key = "thermostat:" .. key
    self._persistAs = key
    local saved = persist(PERSIST_NS):get(key)
    if type(saved) ~= "table" then return end
    self._point = finite(saved.setpoint) or self._point
    if saved.mode == 0 or saved.mode == 1 then self._mode = saved.mode end
    if saved.state == 0 or saved.state == 1 then self._st = saved.state end
end

local function save(self)
    if not self._persistAs then return end
    persist(PERSIST_NS):set(self._persistAs, { setpoint = self._point, mode = self._mode, state = self._st })
end

-- A setting changed: emit as RemoteThermostat does, and the target with it
local function setAside(self, field, value)
    local old = self[field]
    if value == old then return false end
    self[field] = value
    notify(self, self.emit, "OnSettingsChange", EVENT_FIELD[field] or field, value, old)
    local target = targetOf(self)
    if field ~= "_target" and target ~= self._target then setAside(self, "_target", target) end
    return true
end

-- ==========================================================================
-- Control
-- ==========================================================================

-- The source's value, nil when it has none or raises (then it goes stale)
local function read(self)
    local ok, v = pcall(self._source.getValue, self._source)
    if ok then return finite(v) end
    return nil
end

-- What the output should be: true on, false off; thresholds are target ± hysteresis
local function wanted(self)
    local t = self._state
    if self._st ~= 1 or self._fault or not self._hasReading or type(t) ~= "number" then return false end
    local target, h = targetOf(self), self._hysteresis
    if self._cool then
        if t >= target + h then return true end
        if t <= target - h then return false end
    else
        if t <= target - h then return true end
        if t >= target + h then return false end
    end
    return self._on == true -- between the thresholds: as it is
end

-- Switches the output only when what it should be changes (force: whatever it shows)
local function apply(self, on, force)
    if not force and on == self._on then return end
    self._on = on
    self._onSince = on and now() or nil
    local out = self._output
    local ok, err = pcall(function()
        if on then out:switchOn() else out:switchOff() end
    end)
    if not ok then
        self._on = nil -- unknown: the next decision switches again
        log():warn("[Thermostat] %s: output not switched: %s", tostring(self._name), tostring(err))
    end
end

local function reading(self, v, at)
    self._readAt = at
    self._hasReading = true
    if self._fault == "stale" then
        log():info("[Thermostat] %s: the source reports again", tostring(self._name))
        setAside(self, "_fault", nil)
    end
    notify(self, self.updateState, v)
end

-- A change of the source's value
local function onReading(self, v)
    v = finite(v)
    if v == nil then return end
    reading(self, v, now())
    apply(self, wanted(self))
end

local function tick(self)
    local t = now()
    if not self._hasReading then
        local v = read(self)
        -- a Grenton object's first report fires no OnChange
        if v and v ~= self._before then
            reading(self, v, t)
            apply(self, wanted(self))
            return
        end
    end
    -- one fault at a time: a maxOn stop must not end with the next reading
    if not self._fault and t - self._readAt >= self._staleAfter then
        log():warn("[Thermostat] %s: no reading from its source for %d s, output off", tostring(self._name),
            math.floor(t - self._readAt))
        setAside(self, "_fault", "stale")
        apply(self, false)
        return
    end
    if self._maxOn and self._on and t - self._onSince >= self._maxOn then
        log():warn("[Thermostat] %s: output on for %d s (maxOn), off until a setting changes", tostring(self._name),
            math.floor(t - self._onSince))
        setAside(self, "_fault", "maxOn")
        apply(self, false)
    end
end

-- A person's change ends a maxOn stop and is applied at once
local function changed(self)
    if self._fault == "maxOn" then setAside(self, "_fault", nil) end
    save(self)
    apply(self, wanted(self))
end

-- Before a Lua state goes away (reload, shutdown) Go calls this: every output that is or
-- may be on is switched off; one failing output does not stop the others.
function __vclu_release_thermostats()
    for th in pairs(live) do
        if th._on ~= false then
            th._on = false
            local ok, err = pcall(th._output.switchOff, th._output)
            if not ok then log():warn("[Thermostat] %s: output not switched off: %s", tostring(th._name), tostring(err)) end
        end
    end
end

-- ==========================================================================
-- Thermostat interface (as RemoteThermostat)
-- ==========================================================================

--- Creates a thermostat that switches `output` by the temperature of `source`.
-- The output is switched off at once (also after a restart or a reload) and goes on only on a
-- reading that arrives after; the source's value at creation does not count.
-- Options: source (object with getValue, or its path), output (object with switchOn and
-- switchOff, or its path), hysteresis (0.5 °C), direction ("heat" or "cool"), setpoint (21),
-- holiday (16 heating, 28 cooling), min (5), max (35), staleAfter (900 s without a change of
-- the source's value: output off), maxOn (s on in one go, then off until a setting changes;
-- default no limit), persist (true or a key: set point, mode and state survive restarts).
-- @api public
-- @param name string Name (also the persist key with persist = true)
-- @param opts table Options, see above
-- @return VirtualThermostat The new thermostat
function VirtualThermostat:new(name, opts)
    if type(opts) ~= "table" then error("VirtualThermostat:new(name, opts): opts must be a table", 2) end
    for k in pairs(opts) do
        if not OPTIONS[k] then error("VirtualThermostat: unknown option '" .. tostring(k) .. "'", 2) end
    end
    local obj = VirtualObject.new(self, name)
    obj._type = "THERMOSTAT"
    obj._allowedMethods = {"getValue", "getTarget", "getSetpoint", "setSetpoint", "getState", "getMode",
        "setMode", "start", "stop", "getFault", "getDirection"}

    obj._source = resolve(opts.source, "source", {"getValue"})
    obj._output = resolve(opts.output, "output", {"switchOn", "switchOff"})
    obj._cool = opts.direction == "cool"
    if opts.direction ~= nil and opts.direction ~= "heat" and opts.direction ~= "cool" then
        error("VirtualThermostat: direction must be \"heat\" or \"cool\"", 2)
    end
    obj._hysteresis = finite(opts.hysteresis == nil and 0.5 or opts.hysteresis)
    if not obj._hysteresis or obj._hysteresis < 0 then error("VirtualThermostat: hysteresis must be a number >= 0", 2) end
    obj._min = finite(opts.min == nil and 5 or opts.min)
    obj._max = finite(opts.max == nil and 35 or opts.max)
    if not obj._min or not obj._max or obj._min >= obj._max then error("VirtualThermostat: min must be below max", 2) end
    obj._staleAfter = finite(opts.staleAfter == nil and 900 or opts.staleAfter)
    if not obj._staleAfter or obj._staleAfter <= 0 then error("VirtualThermostat: staleAfter must be seconds > 0", 2) end
    if opts.maxOn ~= nil then
        obj._maxOn = finite(opts.maxOn)
        if not obj._maxOn or obj._maxOn <= 0 then error("VirtualThermostat: maxOn must be seconds > 0", 2) end
    end
    local holiday = opts.holiday == nil and (obj._cool and 28 or 16) or opts.holiday
    obj._holiday = finite(holiday)
    if not obj._holiday then error("VirtualThermostat: holiday must be a number", 2) end
    obj._holiday = clamp(obj, obj._holiday)

    obj._point = finite(opts.setpoint == nil and 21 or opts.setpoint)
    if not obj._point then error("VirtualThermostat: setpoint must be a number", 2) end
    obj._mode = MODE_SETPOINT
    obj._st = 1
    if opts.persist then restore(obj, opts.persist) end
    obj._point = clamp(obj, obj._point)
    obj._target = targetOf(obj)

    obj._readAt = now() -- no reading for staleAfter from here is stale too
    -- a value from before (a default 0, one kept over a reload) is not a reading
    obj._before = read(obj)
    apply(obj, false, true)
    live[obj] = true

    if type(obj._source.onChange) == "function" then
        obj._source:onChange(function(v) onReading(obj, v) end)
    end
    TIMERS:setInterval(TICK_MS, function() tick(obj) end)
    return obj
end

--- Index 0 is the temperature, which only the source sets: nothing is settable by index.
function VirtualThermostat:set(index, value)
end

--- Gets the temperature from the source.
-- @api public
-- @return number|nil Current temperature in °C, nil before the first reading
function VirtualThermostat:getValue()
    return self._state
end

--- Gets the target in effect: the set point, or the holiday value in holiday mode.
-- @api public
-- @return number Target temperature in °C
function VirtualThermostat:getTarget()
    return targetOf(self)
end

--- Gets the set point (the temperature set by hand).
-- @api public
-- @return number Set point in °C
function VirtualThermostat:getSetpoint()
    return self._point
end

--- Sets the set point, clamped to min/max; the target follows in set point mode.
-- @api public
-- @param value number Set point in °C (not a number: ignored)
function VirtualThermostat:setSetpoint(value)
    value = finite(value)
    if not value then return end
    setAside(self, "_point", clamp(self, value))
    changed(self)
end

--- Gets the state: 1 active, 0 off (output off).
-- @api public
-- @return number State
function VirtualThermostat:getState()
    return self._st
end

--- Gets the mode: 0 set point, 1 holiday.
-- @api public
-- @return number Mode
function VirtualThermostat:getMode()
    return self._mode
end

--- Sets the mode: 0 set point, 1 holiday (the target is the holiday value).
-- @api public
-- @param mode number 0 or 1
function VirtualThermostat:setMode(mode)
    if mode ~= MODE_SETPOINT and mode ~= MODE_HOLIDAY then
        error("VirtualThermostat:setMode: mode must be 0 (set point) or 1 (holiday)", 2)
    end
    setAside(self, "_mode", mode)
    changed(self)
end

--- Starts regulating (state 1).
-- @api public
function VirtualThermostat:start()
    setAside(self, "_st", 1)
    changed(self)
end

--- Stops regulating and switches the output off (state 0).
-- @api public
function VirtualThermostat:stop()
    setAside(self, "_st", 0)
    changed(self)
end

--- Why the output is held off besides the state: "stale" (no reading for staleAfter),
-- "maxOn" (on too long), nil when regulating normally.
-- @api public
-- @return string|nil Fault
function VirtualThermostat:getFault()
    return self._fault
end

--- What the output does: "heat" or "cool" (Home Assistant shows it as the mode).
-- @api public
-- @return string Direction
function VirtualThermostat:getDirection()
    return self._cool and "cool" or "heat"
end

return VirtualThermostat
