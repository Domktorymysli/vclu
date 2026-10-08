--- EventBus for Go runtime
-- This module provides an event bus implementation that integrates with the Go backend.
-- It handles event registration and firing, timer management (setTimeout/setInterval),
-- and HTTP request coordination between Lua and Go.
--
-- The EventBus uses Go callbacks for timer functionality via __go_setTimeout, __go_setInterval,
-- and __go_clearTimer functions provided by the Go runtime.
-- @module EventBus

EventBus = {}
EventBus.callbacks = {}
EventBus.timers = {}
EventBus.nextTimerId = 1

local shared = nil

--- Create a new EventBus instance
-- @return EventBus A new EventBus object with empty callbacks and timers
function EventBus:new()
    local o = {
        id = "EventBus",
        callbacks = {},
        timers = {},
        pendingTimers = {}, -- timers to be processed by Go
        pendingHttpRequests = {}, -- HTTP requests to be processed by Go
    }
    setmetatable(o, self)
    self.__index = self
    return o
end

--- Get the shared singleton EventBus instance
-- @return EventBus The shared EventBus instance
function EventBus:getShared()
    return shared
end

--- Generate an internal event key from module ID and event name
-- @param id string|nil The module ID (defaults to empty string if nil)
-- @param event string|nil The event name (defaults to empty string if nil)
-- @return string The combined event key in format "id-event"
function EventBus:getKey(id, event)
    if (id == nil) then
        id = ''
    end
    if (event == nil) then
        event = ''
    end
    return id .. '-' .. event
end

--- Register an event callback
-- Registers a callback function to be executed when the specified event is fired.
-- Multiple callbacks can be registered for the same event.
-- Supports two signatures:
--   EventBus:on(module, name, callback) - module-based (original Grenton style)
--   EventBus:on(eventName, callback) - simple string-based (for sandbox/plugins)
-- @param moduleOrEventName table|string Either a module object or an event name string
-- @param nameOrCallback string|function Either the event name (if module) or the callback (if event name)
-- @param callback function|nil The callback function (only if first arg is module)
function EventBus:on(moduleOrEventName, nameOrCallback, callback)
    local eventName, callbackToAdd

    -- Support both signatures
    if type(moduleOrEventName) == "table" and moduleOrEventName.id then
        -- Module-based: on(module, name, callback)
        eventName = EventBus:getKey(moduleOrEventName.id, nameOrCallback)
        callbackToAdd = callback
    else
        -- String-based: on(eventName, callback)
        eventName = tostring(moduleOrEventName)
        callbackToAdd = nameOrCallback
    end

    if (self.callbacks[eventName] == nil) then
        self.callbacks[eventName] = {}
    end
    table.insert(self.callbacks[eventName], callbackToAdd)
end

--- Fire an event and execute all registered callbacks
-- Executes all callbacks registered for the specified module and event name.
-- Callbacks are executed with error handling via pcall. Errors are logged but don't stop execution.
-- Supports two signatures:
--   EventBus:fire(module, name) - module-based (original Grenton style)
--   EventBus:emit(eventName, ...) - simple string-based with args (for sandbox/plugins)
-- @param module table The module object containing id and description fields
-- @param name string The name of the event to fire
function EventBus:fire(module, name)
    local eventName = EventBus:getKey(module.id, name)

    if (self.callbacks[eventName] == nil) then
        return
    end

    for i, callback in pairs(self.callbacks[eventName]) do
        local t0 = os.clock()
        local success, err = pcall(callback)
        local elapsed = os.clock() - t0

        if not success then
            Logger:getShared():error("EventBus:fire callback error: " .. tostring(err))
        end
        if elapsed > 0.5 then
            local info = debug and debug.getinfo and debug.getinfo(callback, "Sl")
            local src = info and ((info.short_src or "?") .. ":" .. (info.linedefined or "?")) or "unknown"
            if elapsed > 2.0 then
                Logger:getShared():error("[EVENT] Slow callback for '%s' took %.1fs at %s", eventName, elapsed, src)
            else
                Logger:getShared():warn("[EVENT] Slow callback for '%s' took %.1fs at %s", eventName, elapsed, src)
            end
        end
    end
end

--- Emit an event with optional arguments (simple string-based API for plugins)
-- @param eventName string The name of the event to emit
-- @param ... any Optional arguments to pass to callbacks
function EventBus:emit(eventName, ...)
    if (self.callbacks[eventName] == nil) then
        return
    end

    local args = {...}
    for i, callback in pairs(self.callbacks[eventName]) do
        local t0 = os.clock()
        local success, err = pcall(function()
            callback(unpack(args))
        end)
        local elapsed = os.clock() - t0

        if not success then
            Logger:getShared():error("EventBus:emit callback error: " .. tostring(err))
        end
        if elapsed > 0.5 then
            local info = debug and debug.getinfo and debug.getinfo(callback, "Sl")
            local src = info and ((info.short_src or "?") .. ":" .. (info.linedefined or "?")) or "unknown"
            if elapsed > 2.0 then
                Logger:getShared():error("[EVENT] Slow callback for '%s' took %.1fs at %s", eventName, elapsed, src)
            else
                Logger:getShared():warn("[EVENT] Slow callback for '%s' took %.1fs at %s", eventName, elapsed, src)
            end
        end
    end
end

--- Unregister an event callback
-- Removes a specific callback from the event listeners.
-- @param moduleOrEventName table|string Either a module object or an event name string
-- @param nameOrCallback string|function Either the event name (if module) or the callback (if event name)
-- @param callback function|nil The callback to remove (only if first arg is module)
-- @return boolean True if callback was found and removed
function EventBus:off(moduleOrEventName, nameOrCallback, callback)
    local eventName, callbackToRemove

    -- Support both signatures:
    -- EventBus:off(module, name, callback) - original module-based
    -- EventBus:off(eventName, callback) - simple string-based for sandbox
    if type(moduleOrEventName) == "table" and moduleOrEventName.id then
        -- Module-based: off(module, name, callback)
        eventName = EventBus:getKey(moduleOrEventName.id, nameOrCallback)
        callbackToRemove = callback
    else
        -- String-based: off(eventName, callback)
        eventName = tostring(moduleOrEventName)
        callbackToRemove = nameOrCallback
    end

    if self.callbacks[eventName] == nil then
        return false
    end

    -- Find and remove the callback
    for i, cb in ipairs(self.callbacks[eventName]) do
        if cb == callbackToRemove then
            table.remove(self.callbacks[eventName], i)
            return true
        end
    end

    return false
end

--- Debug helper to display all registered events
-- Logs all registered events and their callback counts to the debug log.
-- Useful for troubleshooting event registration issues.
function EventBus:debug()
    for eventName, callbacks in pairs(self.callbacks) do
        local count = 0
        for _ in pairs(callbacks) do count = count + 1 end
        Logger:getShared():debug("[EventBus] " .. eventName .. ": " .. count)
    end
end

--- Execute a callback function after a delay
-- Schedules a one-time callback execution after the specified period.
-- Uses the Go runtime's __go_setTimeout function for actual timer management.
-- @param callback function The function to execute after the delay
-- @param period number The delay in milliseconds before executing the callback
-- @return number|nil The timer ID that can be used with clearInterval, or nil if callback is nil
function EventBus:setTimeout(callback, period)
    if callback == nil then
        Logger:getShared():error("EventBus:setTimeout - callback is nil")
        return nil
    end

    local p = tonumber(period) or 0
    local timerId = __go_setTimeout(callback, p)
    return timerId
end

--- Execute a callback function repeatedly at fixed intervals
-- Schedules a callback to execute repeatedly with the specified period between executions.
-- Uses the Go runtime's __go_setInterval function for actual timer management.
-- @param callback function The function to execute at each interval
-- @param period number The interval in milliseconds between executions
-- @return number|nil The timer ID that can be used with clearInterval to stop the timer, or nil if callback is nil
function EventBus:setInterval(callback, period)
    if callback == nil then
        Logger:getShared():error("EventBus:setInterval - callback is nil")
        return nil
    end

    local p = tonumber(period) or 0
    local timerId = __go_setInterval(callback, p)
    return timerId
end

--- Stop a running timer
-- Clears/stops a timer created by either setTimeout or setInterval.
-- Uses the Go runtime's __go_clearTimer function to cancel the timer.
-- @param timerId number|nil The timer ID returned by setTimeout or setInterval (nil values are ignored)
function EventBus:clearInterval(timerId)
    if timerId == nil then return end

    __go_clearTimer(timerId)
end

--- Queue an HTTP request to be processed by the Go backend
-- This is a stub implementation that queues HTTP requests for later processing.
-- The actual HTTP request is handled by the Go runtime.
-- @param module table The module object making the request
-- @param request table The HTTP request object containing host and other request parameters
function EventBus:sendHttpRequest(module, request)
    table.insert(self.pendingHttpRequests, {
        moduleId = module.id,
        request = request
    })
end

--- Retrieve and clear pending HTTP requests
-- Internal method called by the Go runtime to fetch queued HTTP requests.
-- Returns all pending requests and clears the queue.
-- @return table Array of pending HTTP request objects with moduleId and request fields
function EventBus:__getPendingHttpRequests()
    local pending = self.pendingHttpRequests
    self.pendingHttpRequests = {}
    return pending
end

-- Initialize shared instance
shared = EventBus:new()

return EventBus