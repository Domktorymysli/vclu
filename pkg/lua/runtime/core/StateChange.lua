--- StateChange - the single producer of StateBus "state_changed" events.
--
-- One real value change = exactly one event; old == value emits nothing.
-- Event: {id, path, old, value, atMs, type, origin, actor, correlationId, obj}
-- (oldValue = old for older scripts)
--
-- Every change is recorded in the Go history store (__go_history_record)
-- before watchers run, so a callback already sees it in lastChange().
--
-- Input adapters (_:sync, ExposedHandle:setValue, subscription reports) take
-- mark(obj) before updateState and call publishedSince(obj, mark) after it:
-- if an onChange callback already published, they must not emit again.
--
-- @module core.StateChange

StateChange = {}

-- Fallback when Go has no id source (sandbox); Go ids survive Lua reloads
local seq = 0
local idPrefix = string.format("lua%x-%s-", os.time(), tostring({}):match("(%x+)$") or "0")
local published = setmetatable({}, { __mode = "k" })
-- quiet[obj]: a first reading within delta of the last value in history; it
-- goes to the bus (HA needs a state) but is no change for the history
local quiet = setmetatable({}, { __mode = "k" })
-- source[obj]: context of the adapter feeding obj right now (a CLU report,
-- a Satel event); it applies to obj only, not to changes its handlers make
local source = setmetatable({}, { __mode = "k" })

-- Lua code with no context: an automation, identity unknown
local DEFAULT_CONTEXT = { origin = "lua", actor = "automation:unknown" }

local function nowMs()
    if __go_time_ms then return __go_time_ms() end
    return os.time() * 1000
end

--- Current primary state of an object (Remote* cache, Virtual* state,
-- local CLU modules' value).
-- @param obj table
-- @return any
function StateChange.current(obj)
    if obj._lastState ~= nil then return obj._lastState end
    if obj._state ~= nil then return obj._state end
    return obj.value
end

--- State of obj including getter-only objects, and whether it has a state.
-- Never calls get(0) on CLU objects (blocking UDP); they keep _lastState.
-- @param obj table
-- @return any, boolean Value, false for write-only controls
function StateChange.read(obj)
    local v = StateChange.current(obj)
    if v ~= nil or obj._clu then return v, true end
    local ok
    if type(obj.getValue) == "function" then
        ok, v = pcall(obj.getValue, obj)
    elseif type(obj.get) == "function" then
        ok, v = pcall(obj.get, obj, 0)
    else
        return nil, false
    end
    if not ok then v = nil end
    return v, true
end

--- Publish counter of obj, to compare with publishedSince().
-- @param obj table
-- @return number
function StateChange.mark(obj)
    return published[obj] or 0
end

--- True if an event for obj was published after mark was taken.
-- @param obj table
-- @param mark number Value returned by mark()
-- @return boolean
function StateChange.publishedSince(obj, mark)
    return (published[obj] or 0) ~= mark
end

--- Path under which obj's changes are published: its expose path while
-- exposed (exposed objects keep the expose path in events), else fallback.
-- @param obj table|nil
-- @param fallback string Registry path
-- @return string
function StateChange.pathOf(obj, fallback)
    if obj and ExposedObjects and ExposedObjects._objects then
        local ep = ExposedObjects._objectToPath[obj]
        local h = ep and ExposedObjects._objects[ep]
        if h and h.object == obj then return ep end
    end
    return fallback
end

-- History methods for registry objects: obj:lastChange(), obj:since(),
-- obj:history(n). Existing methods of the same name are left alone.
local historyMethods = {
    -- Last recorded change {id, value, old, atMs, origin, actor, correlationId}, or nil.
    lastChange = function(self)
        local path = StateChange.pathOf(self, self._registryPath)
        if not __go_history_last or not path then return nil end
        return __go_history_last(path)
    end,
    -- Seconds in the current state, or nil when no change was seen yet.
    since = function(self)
        local last = self:lastChange()
        if not last then return nil end
        return (nowMs() - last.atMs) / 1000
    end,
    -- Recent changes, newest first (default 10).
    history = function(self, n)
        local path = StateChange.pathOf(self, self._registryPath)
        if not __go_history_query or not path then return {} end
        return __go_history_query(path, n or 10)
    end,
}

--- Gives obj the history methods; called by registerObject and expose.
-- @param obj table
-- @param path string|nil Registry path (the first one is kept); nil from
--   expose, where the expose path applies only while the object is exposed
function StateChange.attachHistory(obj, path)
    if type(obj) ~= "table" then return end
    obj._registryPath = obj._registryPath or path
    for name, fn in pairs(historyMethods) do
        if obj[name] == nil then obj[name] = fn end
    end
end

--- Runs fn(...) as an update of obj coming from ctx (e.g. a CLU report).
-- Changes that obj's handlers make to other objects keep their own context.
-- @param obj table
-- @param ctx table {origin, actor, correlationId}
-- @param fn function Usually a method of obj, e.g. obj.updateState
function StateChange.applyFrom(obj, ctx, fn, ...)
    local prev = source[obj]
    source[obj] = ctx
    local ok, err = pcall(fn, ...)
    source[obj] = prev
    if not ok then error(err, 0) end
end

-- A device report from 2 s before to 15 s after the expected time counts as
-- the expected one: reports are late rather than early (a renewed CLU
-- session pushes its state only when re-created)
local EXPECT_EARLY_MS, EXPECT_LATE_MS = 2000, 15000

--- Registers that obj will change to value by itself after delayMs, as a
-- result of a command issued now (switchOn(60000) -> 0 in a minute). The
-- report of that change gets the command's context, not "grenton".
-- @param obj table
-- @param value any Expected value
-- @param delayMs number
function StateChange.expectLater(obj, value, delayMs)
    local path = StateChange.pathOf(obj, obj._registryPath)
    if not path or not __go_expect then return end
    local ctx = (StateContext and StateContext.current()) or DEFAULT_CONTEXT
    __go_expect({
        path = path, value = value, dueAtMs = nowMs() + delayMs,
        earlyMs = EXPECT_EARLY_MS, lateMs = EXPECT_LATE_MS,
        origin = ctx.origin, actor = ctx.actor, correlationId = ctx.correlationId,
    })
end

--- Drops a pending expectation of obj (a new command overrides the old one).
function StateChange.forgetExpected(obj)
    local path = StateChange.pathOf(obj, obj._registryPath)
    if path and __go_expect_forget then __go_expect_forget(path) end
end

--- Context for a device report of obj: the command's, when the report is
-- the change that command scheduled; otherwise fallback.
-- @param obj table
-- @param value any Reported value
-- @param fallback table Context of an ordinary report
-- @return table
function StateChange.reportContext(obj, value, fallback)
    local path = StateChange.pathOf(obj, obj._registryPath)
    if path and __go_expect_match then
        local ctx = __go_expect_match(path, value)
        if ctx then return ctx end
    end
    return fallback
end

--- Context of a change of obj: explicit > adapter source > StateContext > Lua default.
local function resolveContext(obj, context)
    if context and context.origin then return context end
    local ctx = (obj and source[obj]) or (StateContext and StateContext.current()) or DEFAULT_CONTEXT
    if context and context.type then
        return { type = context.type, origin = ctx.origin, actor = ctx.actor, correlationId = ctx.correlationId }
    end
    return ctx
end

--- Publish one state change on StateBus.
-- @param path string Object path (an exposed obj is published under its expose path)
-- @param obj table|nil The object (nil for raw sources like GPIO pins)
-- @param old any Value before the change
-- @param value any Value after the change
-- @param context table|nil {type, origin, actor, correlationId}
-- @return table|nil The event, nil when nothing changed
function StateChange.publish(path, obj, old, value, context)
    if old == value then return nil end
    local skipHistory = false
    if obj and quiet[obj] ~= nil then
        skipHistory = old == nil and quiet[obj] == value
        quiet[obj] = nil
    end
    context = resolveContext(obj, context)
    if obj then published[obj] = (published[obj] or 0) + 1 end
    path = StateChange.pathOf(obj, path)
    local handle = ExposedObjects and ExposedObjects._objects and ExposedObjects._objects[path]
    -- expose(..., { history = false }): events still flow, history stays out
    if handle and handle.options.history == false then skipHistory = true end

    local origin, actor = context.origin or "unknown", context.actor or "unknown"
    local id, atMs
    if __go_history_record and not skipHistory then
        local ok, rid, rat = pcall(__go_history_record, {
            path = path, value = value, old = old,
            origin = origin, actor = actor, correlationId = context.correlationId,
        })
        if ok then id, atMs = rid, rat end
    end
    if not id then
        if __go_state_change_id then
            id = __go_state_change_id()
        else
            seq = seq + 1
            id = idPrefix .. seq
        end
        atMs = nowMs()
    end

    local event = {
        id = id,
        path = path,
        old = old,
        oldValue = old, -- documented pre-C0 name
        value = value,
        atMs = atMs,
        type = context.type or (handle and handle.type) or (obj and obj._type),
        origin = origin,
        actor = actor,
        correlationId = context.correlationId,
        obj = obj,
    }
    local bus = StateBus and StateBus:getShared()
    if bus then bus:emit("state_changed", event) end
    return event
end

-- filters[obj]: reporting threshold of a noisy numeric sensor (expose option
-- filter); getValue() keeps the raw value, reports carry the filtered one
local filters = setmetatable({}, { __mode = "k" })

--- Raises an error unless f is a valid filter option.
-- @param f table {delta = number > 0, heartbeat = seconds > 0 (optional)}
function StateChange.checkFilter(f)
    if type(f) ~= "table" or type(f.delta) ~= "number" or f.delta <= 0 then
        error("filter: delta must be a number > 0, e.g. { delta = 0.2 }", 2)
    end
    if f.heartbeat ~= nil and (type(f.heartbeat) ~= "number" or f.heartbeat <= 0) then
        error("filter: heartbeat must be seconds > 0", 2)
    end
    for k in pairs(f) do
        if k ~= "delta" and k ~= "heartbeat" then
            error("filter: unknown option '" .. tostring(k) .. "' (delta, heartbeat)", 2)
        end
    end
end

--- Sets the reporting threshold of obj; nil removes it. A change is reported
-- when it differs by at least delta from the last reported value, or when
-- heartbeat seconds have passed since that report.
-- @param obj table
-- @param f table|nil|false See checkFilter; nil or false removes it
function StateChange.setFilter(obj, f)
    if not f then
        filters[obj] = nil
        return
    end
    StateChange.checkFilter(f)
    local prev = filters[obj]
    local cur = StateChange.read(obj)
    filters[obj] = {
        delta = f.delta,
        heartbeatMs = f.heartbeat and f.heartbeat * 1000,
        reported = prev and prev.reported or cur,
        atMs = prev and prev.atMs or nowMs(),
    }
end

--- Whether a change of obj from old to value is reported. Returns the old
-- value to report: the last reported one when obj has a filter.
-- Non-numeric values always pass.
-- @return boolean, any
function StateChange.filter(obj, old, value)
    local f = filters[obj]
    if not f then return true, old end
    -- first reading after a restart or reload: measure it against the last
    -- value in history, not against nothing
    if f.reported == nil and not f.seeded and type(value) == "number" then
        f.seeded = true
        local path = StateChange.pathOf(obj, obj._registryPath)
        local h = path and __go_history_last and __go_history_last(path)
        if h and type(h.value) == "number" then
            local due = f.heartbeatMs and nowMs() - h.atMs >= f.heartbeatMs
            if math.abs(value - h.value) < f.delta - 1e-9 and not due then
                f.reported, f.atMs = h.value, h.atMs
                quiet[obj] = value
                return true, old
            end
        end
    end
    local last = f.reported
    if type(value) == "number" and type(last) == "number" then
        local now = nowMs()
        -- 1e-9: 22.3 - 22.1 is 0.19999... in floating point
        local due = f.heartbeatMs and now - f.atMs >= f.heartbeatMs
        if value == last or (math.abs(value - last) < f.delta - 1e-9 and not due) then
            -- counts as handled: adapters (_:sync, reports) must not publish it
            published[obj] = (published[obj] or 0) + 1
            return false, last
        end
    end
    f.reported, f.atMs = value, nowMs()
    return true, last
end

return StateChange
