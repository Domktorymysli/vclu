--- Virtual cover class for blinds, shutters, and covers.
-- State is position 0-100 (0 = open, 100 = closed).
-- _type = "ROLLER" — must stay in sync with RemoteCover.
-- Public API must match RemoteCover (see objects/RemoteCover.lua).
-- @module objects.VirtualCover
-- @inherits VirtualObject

VirtualCover = setmetatable({}, {__index = VirtualObject})
VirtualCover.__index = VirtualCover

--- Creates a new VirtualCover instance.
-- @api public
-- @param name string Optional name for the cover
-- @param opts table Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)
-- @return VirtualCover The new virtual cover instance
function VirtualCover:new(name, opts)
    local obj = VirtualObject.new(self, name)
    obj._type = "ROLLER"
    obj:_setup(opts, 0)
    obj._allowedMethods = {"getPosition", "setPosition", "open", "close", "stop", "up", "down"}
    return obj
end

--- Gets the current position.
-- @api public
-- @return number Position (0 = open, 100 = closed)
function VirtualCover:getPosition()
    return self._state or 0
end

--- Sets the cover to a specific position.
-- @api public
-- @param position number Target position (0 = open, 100 = closed)
function VirtualCover:setPosition(position)
    self:updateState(position)
end

--- Opens the cover (position = 0).
-- @api public
function VirtualCover:open()
    self:updateState(0)
end

--- Closes the cover (position = 100).
-- @api public
function VirtualCover:close()
    self:updateState(100)
end

--- Stops the cover movement. Override for actual stop command.
-- @api public
function VirtualCover:stop()
    -- No-op by default. Users override for real stop commands.
end

--- Moves the cover up (alias for open).
-- @api public
function VirtualCover:up()
    self:open()
end

--- Moves the cover down (alias for close).
-- @api public
function VirtualCover:down()
    self:close()
end

--- Updates the cover state and triggers appropriate events.
-- @api public
-- @param newValue number The new position value
function VirtualCover:updateState(newValue)
    local old = self._state
    VirtualObject.updateState(self, newValue)
    if old ~= newValue then
        self:emit("OnPositionChange", newValue, old)
    end
end

--- Registers a callback for when the cover position changes.
-- @api public
-- @param callback function Function called with (newPos, oldPos)
-- @return VirtualCover Self for method chaining
function VirtualCover:onPositionChange(callback)
    return self:onEvent("OnPositionChange", callback)
end

return VirtualCover
