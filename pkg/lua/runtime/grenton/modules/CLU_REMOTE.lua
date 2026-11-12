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

-- The CLU puts return before the script itself; "return x" is a syntax error there and gets no answer
local function warnReturn(id, script)
    if type(script) == "string" and (script:match("^%s*return$") or script:match("^%s*return[^%w_]")) then
        Logger:getShared():warn("CLU_REMOTE %s: the CLU adds return itself, send the expression without it: %s",
            tostring(id), script)
    end
end

function CLU_REMOTE:execute(method, script)
    local targetIP = numericIPToString(self.address)
    Logger:getShared():info("CLU_REMOTE:execute -> " .. targetIP .. ": " .. tostring(script))
    warnReturn(self.id, script)

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
    warnReturn(self.id, script)
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

-- A user feature name goes into the script as is, so only a Lua identifier is accepted
local function featureName(name)
    if type(name) ~= "string" or not name:match("^[%a_][%w_]*$") then
        error("CLU_REMOTE: feature name must be a Lua identifier, got " .. tostring(name), 3)
    end
    return name
end

-- A value as a Lua literal for the CLU; quotes, backslashes and control characters are escaped
local function literal(value)
    local t = type(value)
    if t == "nil" or t == "boolean" then return tostring(value) end
    if t == "number" then
        if value - value ~= 0 then -- NaN or infinity
            error("CLU_REMOTE: cannot send " .. tostring(value), 3)
        end
        return tostring(value)
    end
    if t == "string" then
        return '"' .. value:gsub('[%c"\\]', function(c)
            return string.format("\\%03d", c:byte())
        end) .. '"'
    end
    error("CLU_REMOTE: cannot send a " .. t .. " value", 3)
end

-- Sends a script and waits for the answer, like execute but logged at debug
function CLU_REMOTE:_ask(script)
    if not __go_send_command then return nil, "command sender not configured" end
    Logger:getShared():debug("CLU_REMOTE %s <- %s", tostring(self.id), script)
    local result, err = __go_send_command(numericIPToString(self.address), script)
    if err then return nil, err end
    if result == nil then return nil, "no answer" end
    return result
end

--- Reads a user feature (a variable defined in Object Manager) from the CLU.
-- Blocks until the CLU answers, like execute.
-- @api public
-- @param name string Feature name, e.g. "temp_zadana"
-- @return any Number, string or boolean; nil when not set, or nil and an error when the CLU did not answer
function CLU_REMOTE:get(name)
    featureName(name)
    -- The CLU answers with plain text, so the type is sent along with the value
    local result, err = self:_ask('(function() local v = getVar("' .. name ..
        '") return type(v) .. ":" .. tostring(v) end)()')
    if err then
        Logger:getShared():warn("CLU_REMOTE %s: get(%s) failed: %s", tostring(self.id), name, tostring(err))
        return nil, err
    end
    local t, text = result:match("^(%a+):(.*)$")
    if t == "nil" then return nil end
    if t == "string" then return text end
    if t == "boolean" then return text == "true" end
    if t == "number" and tonumber(text) then return tonumber(text) end
    return nil, "unexpected answer: " .. result
end

--- Sets a user feature on the CLU. Blocks until the CLU answers, like execute.
-- @api public
-- @param name string Feature name, e.g. "temp_zadana"
-- @param value any Number, string, boolean or nil
-- @return boolean true when the CLU answered, or nil and an error
function CLU_REMOTE:set(name, value)
    featureName(name)
    local _, err = self:_ask('setVar("' .. name .. '", ' .. literal(value) .. ')')
    if err then
        Logger:getShared():warn("CLU_REMOTE %s: set(%s) failed: %s", tostring(self.id), name, tostring(err))
        return nil, err
    end
    return true
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
-- else, a value like 21.5 included, forgets it. nil means the CLU did not answer.
function CLU_REMOTE:_setFirmware(result)
    self._firmwareAnswered = result ~= nil
    local s = type(result) == "string" and result:match('^%s*"?([^"]-)"?%s*$') or nil
    if s and s:match("^%d+%.%d+%.%d+%-?%w*$") then self._firmware = s else self._firmware = nil end
    return self._firmware
end

-- Forgets the firmware (the CLU stopped answering); the next subscribe or poll asks again
function CLU_REMOTE:_forgetFirmware()
    self._firmware = nil
    self._firmwareAnswered = false
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

-- Asks without blocking; done(firmware, answered) gets nil firmware also when the answer
-- is not a version, answered false when the CLU did not answer
function CLU_REMOTE:_checkFirmwareAsync(done)
    self:executeAsync(self:_firmwareScript(), function(result, err)
        local fw = self:_setFirmware(not err and result or nil)
        if done then done(fw, self._firmwareAnswered) end
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