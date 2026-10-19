--- Remote cover class for ROLLER_SHUTTER and ROLLER_SH objects.
-- Provides control for blinds, shutters, and covers.
-- The value is the position, 0 = closed .. 100 = open, as in Grenton (ROLLER_SHUTTER,
-- class 24: feature 7 Position and method 10 SetPosition; Object Manager device-interfaces),
-- Home Assistant and HomeKit. The OM import sets _hasPosition = true for
-- class 24; without it (ROLLER_SH, class 11, or a proxy generated before) the value
-- stays feature 0. First firmware of the 2.0 roller modules (fv01) has no Position either.
-- @module objects.RemoteCover
-- @inherits RemoteObject

RemoteCover = setmetatable({}, {__index = RemoteObject})
RemoteCover.__index = RemoteCover

-- Feature indexes of ROLLER_SHUTTER (class 24)
RemoteCover.FEATURE_STATE = 0
RemoteCover.FEATURE_POSITION = 7

-- Events besides OnChange that change what the object shows (a panel redraws on them)
RemoteCover.CHANGE_EVENTS = {"OnMovementChange"}

--- Creates a new RemoteCover instance.
-- @param clu CLU_REMOTE The CLU instance this cover belongs to
-- @param objId string The unique identifier of the cover
-- @param name string Optional name for the cover
-- @return RemoteCover The new remote cover instance
function RemoteCover:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "ROLLER"
    obj._hasPosition = false
    obj._movement = nil
    obj._lastTilt = nil
    obj._allowedMethods = {"getState", "getPosition", "setPosition", "getTilt", "setTilt", "open", "close", "stop", "up", "down"}
    return obj
end

--- Gets the movement of the cover (from local cache, updated by subscriptions).
-- @return number Movement (0 = stopped, 1 = up, 2 = down, 3 = blocked, 4 = calibration)
function RemoteCover:getState()
    if self._hasPosition then return self._movement end
    return self._lastState
end

--- Gets the current position of the cover (from local cache).
-- @return number|nil Current position (0 = closed, 100 = open), nil until reported
function RemoteCover:getPosition()
    if self._hasPosition then return self._lastState end
    return nil
end

--- Sets the cover to a specific position.
-- @param position number Target position (0 = closed, 100 = open)
-- @param lamel number Optional lamel angle (defaults to 91)
function RemoteCover:setPosition(position, lamel)
    position = math.floor(math.max(0, math.min(100, position)) + 0.5)
    self:execute(10, position, lamel or 91)
end

--- Features to subscribe to, in report order: the movement first, so a report that
-- carries both publishes the new position with the movement already stored.
-- @return table Feature indexes
function RemoteCover:subscribeFeatures()
    if self._hasPosition then
        return {RemoteCover.FEATURE_STATE, RemoteCover.FEATURE_POSITION}
    end
    return {RemoteCover.FEATURE_STATE}
end

--- Turns a reported feature value into the object's value.
-- @param index number Feature index
-- @param value any Reported value
-- @return any The value to store, or nil when the feature is kept aside (movement)
function RemoteCover:fromFeature(index, value)
    if not self._hasPosition then return value end
    if index == RemoteCover.FEATURE_POSITION then
        return tonumber(value)
    end
    self:_setMovement(value)
    return nil
end

-- Movement aside from the value; integrations (Home Assistant) listen for its changes
function RemoteCover:_setMovement(movement)
    local old = self._movement
    self._movement = movement
    if movement ~= old then self:emit("OnMovementChange", movement, old) end
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

--- Opens the cover. With a position and no time: SetPosition 100 %, which opens also while
-- the cover moves; otherwise MoveUp, which stops instead when the cover is moving.
-- @param time number Optional duration in milliseconds (0 = full travel)
function RemoteCover:open(time)
    if self._hasPosition and (time or 0) == 0 then
        self:setPosition(100)
    else
        self:execute(0, time or 0)
    end
    self:_moved(1)
end

--- Closes the cover. With a position and no time: SetPosition 0 %, which closes also while
-- the cover moves; otherwise MoveDown, which stops instead when the cover is moving.
-- @param time number Optional duration in milliseconds (0 = full travel)
function RemoteCover:close(time)
    if self._hasPosition and (time or 0) == 0 then
        self:setPosition(0)
    else
        self:execute(1, time or 0)
    end
    self:_moved(2)
end

--- Stops the cover movement.
function RemoteCover:stop()
    self:execute(3)
    self:_moved(0)
end

-- Optimistic movement until the CLU reports. Without a position the movement is the value.
function RemoteCover:_moved(movement)
    if self._hasPosition then
        self:_setMovement(movement)
    else
        self:updateState(movement)
    end
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
