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

VirtualThermostat.MODE_SETPOINT = 0
VirtualThermostat.MODE_HOLIDAY = 1

-- Events besides OnChange that change what the object shows (a panel redraws on them)
VirtualThermostat.CHANGE_EVENTS = {"OnSettingsChange"}

-- How often staleness and maxOn are checked, and a first reading picked up
VirtualThermostat.TICK_MS = 10000

-- Time and the tick; tests replace them with a virtual clock
VirtualThermostat.timer = {
    now = function() return __go_time_ms() / 1000 end,
    every = function(ms, fn) return TIMERS:setInterval(ms, fn) end,
}

local PERSIST_NS = "virtual-objects" -- shared with VirtualObject
local OPTIONS = { source = true, output = true, hysteresis = true, direction = true, setpoint = true,
    holiday = true, min = true, max = true, staleAfter = true, maxOn = true, persist = true }

local function finite(v)
    v = tonumber(v)
    if v and v - v == 0 then return v end
    return nil
end

local function log() return Logger:getShared() end

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
        "setMode", "start", "stop", "getFault"}

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
    obj._holiday = obj:_clamp(obj._holiday)

    obj._point = finite(opts.setpoint == nil and 21 or opts.setpoint)
    if not obj._point then error("VirtualThermostat: setpoint must be a number", 2) end
    obj._mode = VirtualThermostat.MODE_SETPOINT
    obj._st = 1
    if opts.persist then obj:_restore(opts.persist) end
    obj._point = obj:_clamp(obj._point)
    obj._target = obj:getTarget()

    local now = VirtualThermostat.timer.now()
    obj._readAt = now -- no reading for staleAfter from here is stale too
    -- a value from before (a default 0, one kept over a reload) is not a reading
    obj._before = finite(obj._source:getValue())
    obj:_apply(false, true)

    if type(obj._source.onChange) == "function" then
        obj._source:onChange(function(v) obj:_onReading(v) end)
    end
    VirtualThermostat.timer.every(VirtualThermostat.TICK_MS, function() obj:_tick() end)
    return obj
end

function VirtualThermostat:_clamp(v)
    if v < self._min then return self._min end
    if v > self._max then return self._max end
    return v
end

function VirtualThermostat:_restore(key)
    if key == true then
        if self._name == "Virtual" then error("VirtualThermostat: persist = true needs a name (or give persist a key)", 3) end
        key = self._name
    end
    if type(key) ~= "string" or key == "" or key == "get" or key == "set" or key == "save" or key == "toTable" then
        error("VirtualThermostat: persist must be true or a string key (not get/set/save/toTable)", 3)
    end
    self._persistAs = key
    local saved = persist(PERSIST_NS):get(key)
    if type(saved) ~= "table" then return end
    self._point = finite(saved.setpoint) or self._point
    if saved.mode == 0 or saved.mode == 1 then self._mode = saved.mode end
    if saved.state == 0 or saved.state == 1 then self._st = saved.state end
end

function VirtualThermostat:_save()
    if not self._persistAs then return end
    persist(PERSIST_NS):set(self._persistAs, { setpoint = self._point, mode = self._mode, state = self._st })
end

-- Field names in OnSettingsChange as RemoteThermostat's (_state there is the state)
local EVENT_FIELD = { _st = "_state" }

-- A setting changed: emit as RemoteThermostat does, and the target with it
function VirtualThermostat:_setAside(field, value)
    local old = self[field]
    if value == old then return false end
    self[field] = value
    self:emit("OnSettingsChange", EVENT_FIELD[field] or field, value, old)
    local target = self:getTarget()
    if field ~= "_target" and target ~= self._target then self:_setAside("_target", target) end
    return true
end

-- ==========================================================================
-- Control
-- ==========================================================================

-- A change of the source's value
function VirtualThermostat:_onReading(v)
    v = finite(v)
    if v == nil then return end
    self:_reading(v, VirtualThermostat.timer.now())
    self:_apply(self:_wanted())
end

function VirtualThermostat:_reading(v, now)
    self._readAt = now
    self._hasReading = true
    if self._fault == "stale" then
        log():info("[Thermostat] %s: the source reports again", tostring(self._name))
        self:_setAside("_fault", nil)
    end
    self:updateState(v)
end

-- What the output should be: true on, false off; thresholds are target ± hysteresis
function VirtualThermostat:_wanted()
    local t = self._state
    if self._st ~= 1 or self._fault or not self._hasReading or type(t) ~= "number" then return false end
    local target, h = self:getTarget(), self._hysteresis
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
function VirtualThermostat:_apply(on, force)
    if not force and on == self._on then return end
    self._on = on
    self._onSince = on and VirtualThermostat.timer.now() or nil
    local out = self._output
    local ok, err = pcall(function()
        if on then out:switchOn() else out:switchOff() end
    end)
    if not ok then log():warn("[Thermostat] %s: output not switched: %s", tostring(self._name), tostring(err)) end
end

function VirtualThermostat:_tick()
    local now = VirtualThermostat.timer.now()
    if not self._hasReading then
        local v = finite(self._source:getValue())
        -- a Grenton object's first report fires no OnChange
        if v and v ~= self._before then
            self:_reading(v, now)
            self:_apply(self:_wanted())
            return
        end
    end
    -- one fault at a time: a maxOn stop must not end with the next reading
    if not self._fault and now - self._readAt >= self._staleAfter then
        log():warn("[Thermostat] %s: no reading from its source for %d s, output off", tostring(self._name),
            math.floor(now - self._readAt))
        self:_setAside("_fault", "stale")
        self:_apply(false)
        return
    end
    if self._maxOn and self._on and now - self._onSince >= self._maxOn then
        log():warn("[Thermostat] %s: output on for %d s (maxOn), off until a setting changes", tostring(self._name),
            math.floor(now - self._onSince))
        self:_setAside("_fault", "maxOn")
        self:_apply(false)
    end
end

-- A person's change ends a maxOn stop and is applied at once
function VirtualThermostat:_changed()
    if self._fault == "maxOn" then self:_setAside("_fault", nil) end
    self:_save()
    self:_apply(self:_wanted())
end

-- ==========================================================================
-- Thermostat interface (as RemoteThermostat)
-- ==========================================================================

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
    if self._mode == VirtualThermostat.MODE_HOLIDAY then return self._holiday end
    return self._point
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
    self:_setAside("_point", self:_clamp(value))
    self:_changed()
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
    if mode ~= 0 and mode ~= 1 then error("VirtualThermostat:setMode: mode must be 0 (set point) or 1 (holiday)", 2) end
    self:_setAside("_mode", mode)
    self:_changed()
end

--- Starts regulating (state 1).
-- @api public
function VirtualThermostat:start()
    self:_setAside("_st", 1)
    self:_changed()
end

--- Stops regulating and switches the output off (state 0).
-- @api public
function VirtualThermostat:stop()
    self:_setAside("_st", 0)
    self:_changed()
end

--- Why the output is held off besides the state: "stale" (no reading for staleAfter),
-- "maxOn" (on too long), nil when regulating normally.
-- @api public
-- @return string|nil Fault
function VirtualThermostat:getFault()
    return self._fault
end

return VirtualThermostat
