--- Remote cover class for ROLLER_SHUTTER and ROLLER_SH objects.
-- Provides control for blinds, shutters, and covers.
-- @module objects.RemoteCover
-- @inherits RemoteObject

RemoteCover = setmetatable({}, {__index = RemoteObject})
RemoteCover.__index = RemoteCover

--- Creates a new RemoteCover instance.
-- @param clu CLU_REMOTE The CLU instance this cover belongs to
-- @param objId string The unique identifier of the cover
-- @param name string Optional name for the cover
-- @return RemoteCover The new remote cover instance
function RemoteCover:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "ROLLER"
    obj._hasPosition = true
    obj._lastPosition = nil
    obj._lastTilt = nil
    obj._allowedMethods = {"getState", "getPosition", "setPosition", "getTilt", "setTilt", "open", "close", "stop", "up", "down"}
    return obj
end

--- Gets the current state of the cover (from local cache, updated by subscriptions).
-- @return number Current state (0 = stopped, 1 = up, 2 = down)
function RemoteCover:getState()
    return self._lastState
end

--- Gets the current position of the cover (from local cache).
-- @return number Current position (0 = open, 100 = closed)
function RemoteCover:getPosition()
    return self._lastPosition
end

--- Sets the cover to a specific position.
-- @param position number Target position (0 = open, 100 = closed)
-- @param lamel number Optional lamel angle (defaults to 91)
function RemoteCover:setPosition(position, lamel)
    self:execute(10, position, lamel or 91)
end

--- Gets the current tilt angle of the cover slats (from local cache).
-- @return number Current tilt angle
function RemoteCover:getTilt()
    return self._lastTilt
end

--- Sets the tilt angle of the cover slats.
-- @param angle number Target tilt angle
function RemoteCover:setTilt(angle)
    self:execute(9, angle)
end

--- Opens the cover.
-- @param time number Optional duration in milliseconds
function RemoteCover:open(time)
    self:execute(0, time or 0)
    self:updateState(1)  -- 1 = moving up
end

--- Closes the cover.
-- @param time number Optional duration in milliseconds
function RemoteCover:close(time)
    self:execute(1, time or 0)
    self:updateState(2)  -- 2 = moving down
end

--- Stops the cover movement.
function RemoteCover:stop()
    self:execute(3)
    self:updateState(0)  -- 0 = stopped
end

--- Moves the cover up (alias for open).
-- @param time number Optional duration in milliseconds
function RemoteCover:up(time)
    self:open(time)
end

--- Moves the cover down (alias for close).
-- @param time number Optional duration in milliseconds
function RemoteCover:down(time)
    self:close(time)
end

--- Registers a callback for when the cover starts moving up.
-- @param callback function Function called when cover moves up
-- @return RemoteCover Self for method chaining
function RemoteCover:onUp(callback)
    return self:onEvent("OnUp", callback)
end

--- Registers a callback for when the cover starts moving down.
-- @param callback function Function called when cover moves down
-- @return RemoteCover Self for method chaining
function RemoteCover:onDown(callback)
    return self:onEvent("OnDown", callback)
end

--- Registers a callback for when the cover stops moving.
-- @param callback function Function called when cover stops
-- @return RemoteCover Self for method chaining
function RemoteCover:onStop(callback)
    return self:onEvent("OnStop", callback)
end

--- Registers a callback for when the cover position changes.
-- @param callback function Function called when position changes
-- @return RemoteCover Self for method chaining
function RemoteCover:onPositionChange(callback)
    return self:onEvent("OnPositionChange", callback)
end

return RemoteCover
