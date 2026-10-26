--- CLU_REMOTE - a CLU on another device in the network (a global CLU<serial> from the OM import).
-- @module grenton.CLU_REMOTE

CLU_REMOTE = {}

-- FirmwareVersion of the CLU object (Object Manager clu_*.xml), e.g. "04.08.05"
CLU_REMOTE.FEATURE_FIRMWARE = 17

function CLU_REMOTE:new(id, address, eventBus)
    if (id == nil) then
        error("CLU_REMOTE: missing id")
    end

    if (address == nil) then
        error("CLU_REMOTE: missing address")
    end

    if (eventBus == nil) then
        error("CLU_REMOTE: missing eventBus")
    end

    local clu_remote = {
        id = id,
        description = "CLU_REMOTE",
        address = address,
        eventBus = eventBus,
    }

    setmetatable(clu_remote, self)
    self.__index = self

    return clu_remote
end

function CLU_REMOTE:set(key, value)
    Logger:getShared():debug("CLU_REMOTE:set(" .. key .. ", " .. tostring(value) .. ")")
end

function CLU_REMOTE:get(key)
    Logger:getShared():debug("CLU_REMOTE:get(" .. key .. ")")
    return nil
end

-- Convert numeric IP (uint32) to dotted string format
local function numericIPToString(numIP)
    if type(numIP) == "string" then
        return numIP  -- Already a string
    end
    local b1 = math.floor(numIP / 16777216) % 256
    local b2 = math.floor(numIP / 65536) % 256
    local b3 = math.floor(numIP / 256) % 256
    local b4 = numIP % 256
    return string.format("%d.%d.%d.%d", b1, b2, b3, b4)
end

function CLU_REMOTE:execute(method, script)
    local targetIP = numericIPToString(self.address)
    Logger:getShared():info("CLU_REMOTE:execute -> " .. targetIP .. ": " .. tostring(script))

    if not __go_send_command then
        Logger:getShared():warn("CLU_REMOTE:execute: __go_send_command not available")
        return nil, "command sender not configured"
    end

    local result, err = __go_send_command(targetIP, script)
    if err then
        Logger:getShared():error("CLU_REMOTE:execute failed: " .. tostring(err))
        return nil, err
    end
    Logger:getShared():debug("CLU_REMOTE:execute result: " .. tostring(result))
    return result
end

-- Fire-and-forget: sends command without waiting for response.
-- Used by set/execute on Remote* objects (switchOn, toggle, etc.)
-- where the response is not needed — state comes back via subscriptions.
function CLU_REMOTE:executeNoReply(method, script)
    local targetIP = numericIPToString(self.address)
    Logger:getShared():debug("CLU_REMOTE:executeNoReply -> " .. targetIP .. ": " .. tostring(script))

    if __go_send_command_noreply then
        __go_send_command_noreply(targetIP, script)
    end
end

-- Answers to executeAsync, by request id
CLU_REMOTE._pending = {}
CLU_REMOTE._nextId = 0
-- Per Lua state: an answer to a request sent before a reload must not match a new request
CLU_REMOTE._idPrefix = string.format("%d.%d.", os.time(), math.floor(os.clock() * 1000000))

-- Sends a command without blocking the run loop; done(result, err) is called when the
-- answer comes (err set on a timeout). Without the async sender it falls back to
-- fire-and-forget and calls done("", nil) at once.
function CLU_REMOTE:executeAsync(script, done)
    local targetIP = numericIPToString(self.address)
    Logger:getShared():debug("CLU_REMOTE:executeAsync -> " .. targetIP .. ": " .. tostring(script))
    if not __go_send_command_async then
        self:executeNoReply(0, script)
        if done then done("", nil) end
        return
    end
    CLU_REMOTE._nextId = CLU_REMOTE._nextId + 1
    local id = CLU_REMOTE._idPrefix .. tostring(CLU_REMOTE._nextId)
    CLU_REMOTE._pending[id] = done or false
    __go_send_command_async(targetIP, script, id)
end

function __vclu_command_reply(id, result, err)
    local done = CLU_REMOTE._pending[id]
    CLU_REMOTE._pending[id] = nil
    if done then done(result, err) end
end

--- The firmware the CLU reported for get(17), e.g. "04.08.05" or "05.15.12-2607".
-- Asked when its objects are subscribed; nil until the CLU answered.
-- @api public
-- @return string|nil Firmware version
function CLU_REMOTE:firmware()
    return self._firmware
end

-- The firmware in om.lua at import ("4/198"; FwType/FwVersion without leading zeros), or nil
function CLU_REMOTE:_omFirmwareKey()
    local om = self._omFirmware
    if not om or not om.type or not om.version then return nil end
    local t = tostring(om.type):lower():gsub("^0+", "")
    local v = tostring(om.version):lower():gsub("^0+", "")
    return t .. "/" .. v
end

-- Major version of the reported firmware ("04.08.05" -> 4), or nil
function CLU_REMOTE:_firmwareMajor()
    return self._firmware and tonumber(self._firmware:match("^(%d+)%.")) or nil
end

function CLU_REMOTE:_firmwareScript()
    local name = tostring(self.id)
    if not name:match("^CLU") then name = "CLU" .. name end
    return name .. ":get(" .. CLU_REMOTE.FEATURE_FIRMWARE .. ")"
end

-- Keeps an answer to get(17) shaped like a version (04.08.05, 05.15.12-2607); anything
-- else, a value like 21.5 included, forgets it
function CLU_REMOTE:_setFirmware(result)
    local s = type(result) == "string" and result:match('^%s*"?([^"]-)"?%s*$') or nil
    if s and s:match("^%d+%.%d+%.%d+%-?%w*$") then self._firmware = s else self._firmware = nil end
    return self._firmware
end

-- Forgets the firmware (the CLU stopped answering); the next subscribe or poll asks again
function CLU_REMOTE:_forgetFirmware()
    self._firmware = nil
end

-- Asks the CLU for its firmware, blocking; delayMs spaces the next message to this CLU
function CLU_REMOTE:_checkFirmware(delayMs)
    if not __go_send_command then return nil end
    local result, err = __go_send_command(numericIPToString(self.address), self:_firmwareScript(), delayMs or 0)
    if err then
        Logger:getShared():warn("CLU_REMOTE %s: no answer to get(17): %s", tostring(self.id), tostring(err))
        return self:_setFirmware(nil)
    end
    return self:_setFirmware(result)
end

-- Asks without blocking; done(firmware) gets nil when the CLU did not answer
function CLU_REMOTE:_checkFirmwareAsync(done)
    self:executeAsync(self:_firmwareScript(), function(result, err)
        local fw = self:_setFirmware(not err and result or nil)
        if done then done(fw) end
    end)
end

function CLU_REMOTE:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

function CLU_REMOTE:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'CLU_REMOTE',
    }
end

return CLU_REMOTE