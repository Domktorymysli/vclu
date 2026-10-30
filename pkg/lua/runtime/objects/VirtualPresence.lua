--- Virtual presence: one presence per room from motion detectors, radars, doors and
-- "something is in use" signals. Same interface as VirtualSensor (_type = "DIN", getValue,
-- isActive, onSwitchOn, onSwitchOff), so expose(), StateBus and dashboards take it as one.
-- @module objects.VirtualPresence
-- @inherits VirtualSensor
--
-- Usage:
--   local p = VirtualPresence:new("lazienka", {
--       trigger = { "satel.zone.3" }, keep = { "zigbee.lazienka.radar" },
--       door = "satel.zone.7", hold = 120 })
--   p:onSwitchOn(function() light:switchOn() end)
--   p:onSwitchOff(function() light:switchOff() end)
--   expose(p, "occupancy", { name = "Łazienka", area = "Łazienka" })

VirtualPresence = setmetatable({}, {__index = VirtualSensor})
VirtualPresence.__index = VirtualPresence

-- Internals stay local: the class holds only the public API
local TICK_MS = 10000 -- picks up a first report and sources without onChange
local OPTIONS = { trigger = true, keep = true, door = true, hold = true, doorGrace = true }

-- Milliseconds on the runtime clock (virtual in tests and the sandbox)
local function now() return __go_time_ms() end

local function log() return Logger:getShared() end

local function finite(v)
    v = tonumber(v)
    if v and v - v == 0 then return v end
    return nil
end

local function active(v) return v == 1 or v == true end

-- A source's value, nil when it raises
local function read(src)
    local ok, v = pcall(src.obj.getValue, src.obj)
    if ok then return v end
    return nil
end

-- The name in getReason(): the path it was given or is registered at
local function nameOf(src)
    return src.path or src.obj._registryPath or tostring(src.obj._name or "?")
end

-- An object or its registry path; level is error()'s, from here
local function resolve(v, what, level)
    local path
    if type(v) == "string" then
        path = v
        v = _registry and _registry:get(path)
        if v == nil then error("VirtualPresence: " .. what .. " '" .. path .. "' not found", level) end
    end
    if type(v) ~= "table" or type(v.getValue) ~= "function" then
        error("VirtualPresence: " .. what .. " must be an object with getValue() or its path", level)
    end
    return { obj = v, path = path }
end

-- One source or a list of them
local function resolveList(v, what)
    if v == nil then return {} end
    if type(v) == "string" or (type(v) == "table" and type(v.getValue) == "function") then v = { v } end
    if type(v) ~= "table" then error("VirtualPresence: " .. what .. " must be a list of objects or paths", 3) end
    local list = {}
    for i, s in ipairs(v) do list[i] = resolve(s, what, 4) end
    return list
end

local function checkHold(sec, where)
    sec = finite(sec)
    if not sec or sec < 0 then error("VirtualPresence: " .. where .. " must be seconds >= 0", 3) end
    return sec
end

-- Events go through VirtualSensor; a listener's error must not stop the presence
local function setState(self, v)
    local ok, err = pcall(VirtualSensor.updateState, self, v)
    if not ok then log():warn("[Presence] %s: a listener failed: %s", tostring(self._name), tostring(err)) end
end

local function present(self)
    if self._state == 1 then return end
    self._since = now()
    setState(self, 1)
end

local function absent(self)
    if self._state ~= 1 then return end
    self._since = nil
    self._locked = false
    setState(self, 0)
end

local function doorClosed(self)
    if not self._door then return false end
    local v = read(self._door)
    return v == 0 or v == false
end

-- What holds presence now: triggers always, keep and the door only once it is on.
-- The door holds only when locked: someone was seen inside after it closed.
local function holder(self)
    local closed = doorClosed(self)
    if not closed then self._locked = false end
    for i, s in ipairs(self._triggers) do
        if s.armed and active(read(s)) then return "trigger: " .. nameOf(s) end
    end
    if self._state ~= 1 then return nil end
    for i, s in ipairs(self._keeps) do
        if active(read(s)) then return "keep: " .. nameOf(s) end
    end
    if closed and self._locked then return "door" end
    return nil
end

local function cancel(self)
    if self._timer then TIMERS:clearTimer(self._timer) end
    self._timer = nil
end

local evaluate

local function expire(self)
    self._timer = nil
    evaluate(self)
    if self._override ~= nil or self._by or not self._until then return end
    if now() >= self._until then
        self._until = nil
        absent(self)
    else
        self._timer = TIMERS:setTimeout(math.ceil(self._until - now()), function() expire(self) end)
    end
end

-- Hold until at least sec from now; the timer runs only while no source holds it
local function extend(self, sec)
    local t = now() + sec * 1000
    if not self._until or t > self._until then self._until = t end
    if self._by then return end
    cancel(self)
    if self._until <= now() then
        expire(self)
    else
        self._timer = TIMERS:setTimeout(math.ceil(self._until - now()), function() expire(self) end)
    end
end

-- Recomputes presence from the sources; a listener that changes a source runs it again
evaluate = function(self)
    if self._override ~= nil then return end
    if self._busy then self._again = true return end
    self._busy = true
    repeat
        self._again = false
        local prev = self._by
        local by = holder(self)
        self._by = by
        if by then
            if not prev then
                cancel(self)
                self._until = nil
            end
            present(self)
        elseif self._state == 1 and (prev or not self._until) then
            extend(self, self._hold)
        end
    until not self._again
    self._busy = false
end

local function anyTrigger(self)
    for i, s in ipairs(self._triggers) do
        if s.armed and active(read(s)) then return true end
    end
    return false
end

-- A trigger still on doorGrace s after the door closed: someone inside, not leaving
local function graceEnd(self)
    self._graceTimer = nil
    if doorClosed(self) and anyTrigger(self) then self._locked = true end
    evaluate(self)
end

-- The door opened or closed (from its onChange or the tick); runs also under an override
local function doorChanged(self)
    local closed = doorClosed(self)
    if closed == self._doorClosed then return end
    self._doorClosed = closed
    if self._graceTimer then TIMERS:clearTimer(self._graceTimer) end
    self._graceTimer = nil
    if not closed then
        self._locked = false
    elseif self._doorGrace == 0 then
        graceEnd(self)
    else
        self._graceTimer = TIMERS:setTimeout(math.ceil(self._doorGrace * 1000), function() graceEnd(self) end)
    end
end

-- A trigger's value seen; going on with the door closed means someone is inside
local function triggerSeen(self, src, v)
    if active(v) and not active(src.last) and src.armed and doorClosed(self) then self._locked = true end
    src.last = v
end

local function tick(self)
    if self._door then doorChanged(self) end
    for i, s in ipairs(self._triggers) do
        local v = read(s)
        if not s.armed and not active(v) then s.armed = true end
        triggerSeen(self, s, v)
    end
    evaluate(self)
end

local function watch(self, src, kind)
    if type(src.obj.onChange) ~= "function" then return end
    src.obj:onChange(function(v)
        if kind == "trigger" then
            src.armed = true
            triggerSeen(self, src, v)
        elseif kind == "door" then
            doorChanged(self)
        end
        evaluate(self)
    end)
end

--- Creates a presence that is on while a `trigger` source is active, or a `keep` source is
-- active after a start, or the `door` is closed with someone inside, and for `hold` seconds after
-- the last of these ends. It starts empty (also after a restart or a reload): a trigger active at
-- creation counts once it has changed.
-- Options: trigger (list of objects or registry paths; 1 or true starts and keeps presence),
-- keep (list; keeps presence that is on, never starts it), door (one object or path, 1 = open:
-- it keeps presence until it opens once someone is seen inside after it closed: a trigger going
-- on, or one on at closing still on doorGrace s later; closing it on leaving lets the hold run
-- out), doorGrace (10 s: a detector that stays on longer by itself belongs in keep, or needs
-- more), hold (300 s).
-- Do not make a light switched by this presence its own keep source.
-- @api public
-- @param name string Name
-- @param opts table Options, see above
-- @return VirtualPresence The new presence
function VirtualPresence:new(name, opts)
    if opts == nil then opts = {} end
    if type(opts) ~= "table" then error("VirtualPresence:new(name, opts): opts must be a table", 2) end
    for k in pairs(opts) do
        if not OPTIONS[k] then error("VirtualPresence: unknown option '" .. tostring(k) .. "'", 2) end
    end
    local obj = VirtualSensor.new(self, name)
    obj._allowedMethods = {"getValue", "isActive", "getReason", "getRemaining", "getDuration", "getHold",
        "setHold", "trigger", "setOverride"}
    obj._triggers = resolveList(opts.trigger, "trigger")
    obj._keeps = resolveList(opts.keep, "keep")
    if opts.door ~= nil then obj._door = resolve(opts.door, "door", 3) end
    obj._hold = opts.hold == nil and 300 or checkHold(opts.hold, "hold")
    obj._doorGrace = opts.doorGrace == nil and 10 or checkHold(opts.doorGrace, "doorGrace")
    obj._doorClosed = doorClosed(obj)

    for i, s in ipairs(obj._triggers) do
        -- a value from before (kept over a reload) does not start presence
        s.last = read(s)
        s.armed = not active(s.last)
        watch(obj, s, "trigger")
    end
    for i, s in ipairs(obj._keeps) do watch(obj, s, "keep") end
    if obj._door then watch(obj, obj._door, "door") end
    TIMERS:setInterval(TICK_MS, function() tick(obj) end)
    return obj
end

--- Presence is set by its sources only: nothing is settable by index.
function VirtualPresence:set(index, value)
end

--- What holds presence now: "trigger: <path>", "keep: <path>", "door", "hold 42 s",
-- "override", nil when empty.
-- @api public
-- @return string|nil Reason
function VirtualPresence:getReason()
    if self._state ~= 1 then return nil end
    if self._override == 1 then return "override" end
    if self._by then return self._by end
    return "hold " .. self:getRemaining() .. " s"
end

--- Seconds of hold left: 0 while a source or an override holds it, or when empty.
-- @api public
-- @return number Seconds
function VirtualPresence:getRemaining()
    if self._state ~= 1 or self._override ~= nil or self._by or not self._until then return 0 end
    return math.max(0, math.ceil((self._until - now()) / 1000))
end

--- Seconds since presence started, 0 when empty.
-- @api public
-- @return number Seconds
function VirtualPresence:getDuration()
    if self._state ~= 1 or not self._since then return 0 end
    return math.floor((now() - self._since) / 1000)
end

--- A signal from code ("I'm here"): starts presence or extends it by sec (default hold).
-- Ignored while an override is set.
-- @api public
-- @param sec number Seconds (nil: the hold)
function VirtualPresence:trigger(sec)
    sec = sec == nil and self._hold or checkHold(sec, "trigger(sec)")
    if self._override ~= nil then return end
    -- the end first: a listener of OnSwitchOn must not see a shorter hold
    local t = now() + sec * 1000
    if not self._until or t > self._until then self._until = t end
    present(self)
    extend(self, sec)
end

--- Forces presence: 1 present (guests), 0 empty (alarm armed), nil back to the sensors
-- (a presence that was on then holds for hold seconds unless a source keeps it).
-- @api public
-- @param v number 1, 0 or nil
function VirtualPresence:setOverride(v)
    if v ~= nil and v ~= 0 and v ~= 1 then error("VirtualPresence:setOverride: 1, 0 or nil", 2) end
    if v == self._override then return end
    cancel(self)
    self._until = nil
    self._by = nil
    self._override = v
    if v == 1 then
        present(self)
    elseif v == 0 then
        absent(self)
    else
        evaluate(self)
    end
end

--- Sets the hold time; a hold already running keeps its end.
-- @api public
-- @param sec number Seconds >= 0
function VirtualPresence:setHold(sec)
    self._hold = checkHold(sec, "setHold(sec)")
end

--- Gets the hold time.
-- @api public
-- @return number Seconds
function VirtualPresence:getHold()
    return self._hold
end

return VirtualPresence
