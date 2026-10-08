--- StateBus - Global event bus for state synchronization
--
-- Used for broadcasting state changes to external systems (MQTT, WebSocket, etc.)
-- Separate from EventBus which handles internal module events.
--
-- Usage:
--   StateBus:on("state_changed", function(data) ... end)
--
-- Events:
--   "state_changed" - one per real value change, emitted only by StateChange.publish
--     data: {id, path, old, value, atMs, type, origin, actor, correlationId, obj}
--
-- @module StateBus

StateBus = {}
StateBus.listeners = {}

local shared = nil

--- Create a new StateBus instance
-- @return table New StateBus instance with empty listeners table
function StateBus:new()
    local o = {
        listeners = {}
    }
    setmetatable(o, self)
    self.__index = self
    return o
end

--- Get the shared StateBus singleton instance
-- @return table The shared StateBus instance
function StateBus:getShared()
    return shared
end

--- Register event listener
-- @param event string Event name (e.g., "state_changed")
-- @param callback function Function(data) called when event is emitted
function StateBus:on(event, callback)
    if not self.listeners[event] then
        self.listeners[event] = {}
    end
    table.insert(self.listeners[event], callback)
end

--- Remove all listeners for event
-- @param event string Event name
function StateBus:off(event)
    self.listeners[event] = nil
end

--- Emit event to all listeners
-- @param event string Event name
-- @param data any Data passed to listeners
function StateBus:emit(event, data)
    if not self.listeners[event] then return end

    for _, callback in ipairs(self.listeners[event]) do
        local success, err = pcall(callback, data)
        if not success then
            Logger:getShared():error("StateBus:emit error for '" .. event .. "': " .. tostring(err))
        end
    end
end

--- Get number of listeners for an event
-- @param event string Event name
-- @return number Number of registered listeners for the event
function StateBus:listenerCount(event)
    if not self.listeners[event] then return 0 end
    return #self.listeners[event]
end

--- Debug: list all registered events
-- Logs all registered events and their listener counts to the debug log
function StateBus:debug()
    for event, listeners in pairs(self.listeners) do
        Logger:getShared():debug("[StateBus] " .. event .. ": " .. #listeners)
    end
end

-- Initialize shared instance
shared = StateBus:new()

-- Watchers of StateBus:watch, by path; one shared state_changed listener
-- dispatches to them, so StateBus:off cannot drop a single watcher.
local watchers = {}
local watchOptions = { from = true, to = true, origin = true, actor = true, runAs = true }

local function dispatch(event)
    local lists = { watchers[event.path] }
    local reg = event.obj and event.obj._registryPath
    if reg and reg ~= event.path then lists[#lists + 1] = watchers[reg] end
    -- watch by name of an object created after the watch call
    local name = event.obj and event.obj._name
    if type(name) == "string" and name ~= event.path and name ~= reg then lists[#lists + 1] = watchers[name] end
    lists[#lists + 1] = watchers["*"]
    for _, list in pairs(lists) do
        -- copy: a callback may unsubscribe itself or others
        local copy = {}
        for i, w in ipairs(list) do copy[i] = w end
        for _, w in ipairs(copy) do
            local o = w.opts
            if w.active
                and (o.from == nil or event.old == o.from)
                and (o.to == nil or event.value == o.to)
                and (o.origin == nil or event.origin == o.origin)
                and (o.actor == nil or event.actor == o.actor) then
                local ok, err
                if o.runAs then
                    -- the callback acts as the automation; the cause keeps its origin
                    ok, err = pcall(StateContext.with, {
                        origin = event.origin, actor = o.runAs, correlationId = event.correlationId,
                    }, w.fn, event)
                else
                    ok, err = pcall(w.fn, event)
                end
                if not ok then
                    Logger:getShared():error("[StateBus] watch %s callback error: %s", w.path, tostring(err))
                end
            end
        end
    end
end

--- Calls fn(event) for each state change of path that passes the filters.
-- Works for objects created later; the event is the full state_changed record.
-- @param path string Object path or name ("*" for every object)
-- Callbacks should not raise: errors are isolated and logged, but gopher-lua
-- then detaches locals of still-running functions from their closures, so
-- keep shared counters in tables.
-- @param fn function Callback receiving the event
-- @param opts table|nil Filters {from, to, origin, actor}, compared by equality;
--   runAs = "automation:<id>" makes changes done by fn carry that actor
-- @return function Unsubscribe
-- @usage StateBus:watch("satel.zone.2", function(e) ... end, { to = 1 })
function StateBus:watch(path, fn, opts)
    if type(path) ~= "string" or path == "" then error("StateBus:watch: path must be a string", 2) end
    if type(fn) ~= "function" then error("StateBus:watch: callback must be a function", 2) end
    opts = opts or {}
    for k in pairs(opts) do
        if not watchOptions[k] then error("StateBus:watch: unknown option '" .. tostring(k) .. "'", 2) end
    end
    if opts.runAs ~= nil and type(opts.runAs) ~= "string" then
        error("StateBus:watch: runAs must be a string like \"automation:<id>\"", 2)
    end
    if path ~= "*" and _registry and rawget(_registry, path) == nil and _nameIndex and _nameIndex[path] then
        path = _nameIndex[path]
    end

    -- (re)attach the dispatcher; StateBus:off("state_changed") drops it
    local attached = false
    for _, cb in ipairs(shared.listeners["state_changed"] or {}) do
        if cb == dispatch then attached = true break end
    end
    if not attached then shared:on("state_changed", dispatch) end
    local w = { path = path, fn = fn, opts = opts, active = true }
    watchers[path] = watchers[path] or {}
    table.insert(watchers[path], w)

    return function()
        if not w.active then return end
        w.active = false
        local list = watchers[path]
        for i, x in ipairs(list or {}) do
            if x == w then table.remove(list, i) break end
        end
        if list and #list == 0 then watchers[path] = nil end
    end
end

return StateBus
