--
-- CLU_REMOTE - Remote CLU reference
-- Represents a CLU on another device in the network
--

CLU_REMOTE = {}

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
    local id = tostring(CLU_REMOTE._nextId)
    CLU_REMOTE._pending[id] = done or false
    __go_send_command_async(targetIP, script, id)
end

function __vclu_command_reply(id, result, err)
    local done = CLU_REMOTE._pending[id]
    CLU_REMOTE._pending[id] = nil
    if done then done(result, err) end
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