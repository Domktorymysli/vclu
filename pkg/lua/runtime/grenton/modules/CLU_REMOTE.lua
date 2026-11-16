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
    if type(script) ~= "string" then return end
    local code = script
    repeat
        local rest = code:match("^%s*%-%-[^\n]*\n(.*)$")
        code = rest or code
    until not rest
    if code:match("^%s*return$") or code:match("^%s*return[^%w_]") then
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
        local hint = type(name) == "number" and " (a feature by index: execute(0, \"CLU...:get(n)\"))" or ""
        error("CLU_REMOTE: feature name must be a Lua identifier, got " .. tostring(name) .. hint, 3)
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

-- Sends a script and waits for the answer, like execute but logged at debug. No result
-- and no error is the sandbox, which records the script and does not answer
function CLU_REMOTE:_ask(what, script)
    if not __go_send_command then return nil, "command sender not configured" end
    Logger:getShared():debug("CLU_REMOTE %s <- %s", tostring(self.id), script)
    local result, err = __go_send_command(numericIPToString(self.address), script)
    if err then
        Logger:getShared():warn("CLU_REMOTE %s: %s failed: %s", tostring(self.id), what, tostring(err))
        return nil, err
    end
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
    local result, err = self:_ask("get(" .. name .. ")", '(function() local v = getVar("' .. name ..
        '") return type(v) .. ":" .. tostring(v) end)()')
    if err then return nil, err end
    -- A CLU answers at most 2047 characters; with a longer value it answers nothing
    if result == "" then return nil, "empty answer (a value longer than about 2000 characters?)" end
    local t, text = result:match("^(%a+):(.*)$")
    if t == "nil" then return nil end
    if t == "string" then return text end
    if t == "boolean" then return text == "true" end
    -- gopher-lua reads "1e+15" (Lua 5.1 prints big and small numbers so) only with a point
    local n = t == "number" and (tonumber(text) or tonumber((text:gsub("^([+-]?%d+)([eE])", "%1.0%2"))))
    if n then return n end
    return nil, "unexpected answer: " .. result
end

--- Sets a user feature on the CLU. Blocks until the CLU answers, like execute.
-- @api public
-- @param name string Feature name, e.g. "temp_zadana"
-- @param value any Number, string, boolean or nil
-- @return boolean true when the CLU answered, or nil and an error
function CLU_REMOTE:set(name, value)
    featureName(name)
    local _, err = self:_ask("set(" .. name .. ")", 'setVar("' .. name .. '", ' .. literal(value) .. ')')
    if err then return nil, err end
    return true
end

-- Bindings of objects to user features (bind); the CLU is asked for its uptime (feature 0)
-- every BIND_CHECK_SECONDS, and an uptime that went back is a restart
CLU_REMOTE.FEATURE_UPTIME = 0
CLU_REMOTE.BIND_CHECK_SECONDS = 30
-- A boot time later by more than this (s) is a restart, not clock jitter
CLU_REMOTE.BIND_RESTART_SLACK = 10
-- One write script stays under the CLU's packet (about 1440 characters)
CLU_REMOTE.BIND_SCRIPT_MAX = 1400
CLU_REMOTE._bound = {}
CLU_REMOTE._bindTimer = nil

-- The object's value now: getValue (Remote*, Virtual*) or get (a plugin sensor or control)
local function currentValue(obj)
    local fn = obj.getValue or (obj._getter and obj.get)
    if type(fn) ~= "function" then return nil end
    local ok, v = pcall(fn, obj)
    if ok then return v end
    return nil
end

-- The setVar for a bound value, or nil when the value has no literal (a table, NaN)
local function setVarCall(name, value)
    local ok, lit = pcall(literal, value)
    if not ok then return nil end
    return 'setVar("' .. name .. '", ' .. lit .. ')'
end

-- Sends the dirty bindings of a CLU in one script, one write at a time per CLU; what does
-- not fit goes after the answer. A failure waits for the CLU to come back (_checkBound)
function CLU_REMOTE:_flushBound()
    local st = self._bindState
    if not st or st.inflight or st.down then return end
    local names = {}
    for name, b in pairs(CLU_REMOTE._bound[self] or {}) do
        if b.dirty then names[#names + 1] = name end
    end
    if #names == 0 then return end
    table.sort(names)
    local parts, scripts, seen, sent = {}, {}, {}, {}
    local size = #"(function()  end)()"
    for _, name in ipairs(names) do
        local b = CLU_REMOTE._bound[self][name]
        local part = setVarCall(name, b.value)
        local call = b.runScript and b.script and not seen[b.script] and (b.script .. "()") or nil
        local add = (part and #part + 1 or 0) + (call and #call + 1 or 0)
        if not part or add + #"(function()  end)()" > CLU_REMOTE.BIND_SCRIPT_MAX then
            Logger:getShared():warn("CLU_REMOTE %s: bind(%s): cannot send %s", tostring(self.id), name,
                tostring(b.value):sub(1, 60))
            b.dirty = false
            b.runScript = false
            b.value = nil
        elseif size + add <= CLU_REMOTE.BIND_SCRIPT_MAX then
            size = size + add
            parts[#parts + 1] = part
            if call then
                seen[b.script] = true
                scripts[#scripts + 1] = call
            end
            sent[#sent + 1] = b
            b.dirty = false
        end
    end
    if #sent == 0 then return end
    local wasScript = {}
    for i, b in ipairs(sent) do
        wasScript[i] = b.runScript
        b.runScript = false
    end
    for _, call in ipairs(scripts) do parts[#parts + 1] = call end
    st.inflight = true
    self:executeAsync("(function() " .. table.concat(parts, " ") .. " end)()", function(result, err)
        st.inflight = false
        if CLU_REMOTE._bound[self] == nil then return end
        if err then
            for i, b in ipairs(sent) do
                -- a value written meanwhile is already dirty; keep its script too
                b.dirty = true
                b.runScript = b.runScript or wasScript[i]
            end
            if not st.down then
                st.down = true
                Logger:getShared():warn("CLU_REMOTE %s: bound features not written: %s", tostring(self.id),
                    tostring(err))
            end
            return
        end
        self:_flushBound()
    end)
end

-- Marks every bound feature of the CLU to be written again, without its script
function CLU_REMOTE:_rewriteBound()
    for _, b in pairs(CLU_REMOTE._bound[self] or {}) do
        if b.value ~= nil then b.dirty = true end
    end
    self:_flushBound()
end

-- Asks each CLU with bindings for its uptime; a restart or an answer after none writes
-- all its bound features again
function CLU_REMOTE._checkBound()
    -- without answers (the sandbox) an uptime cannot be read
    if not __go_send_command_async then return end
    for clu in pairs(CLU_REMOTE._bound) do
        local st = clu._bindState
        if st and not st.inflight and not st.checking then
            st.checking = true
            local name = tostring(clu.id)
            if not name:match("^CLU") then name = "CLU" .. name end
            clu:executeAsync(name .. ":get(" .. CLU_REMOTE.FEATURE_UPTIME .. ")", function(result, err)
                st.checking = false
                if CLU_REMOTE._bound[clu] == nil then return end
                local uptime = not err and tonumber(result) or nil
                if not uptime then
                    if not st.down then
                        st.down = true
                        Logger:getShared():warn("CLU_REMOTE %s: no uptime for bound features: %s",
                            tostring(clu.id), tostring(err or result))
                    end
                    return
                end
                local bootAt = os.time() - uptime
                local restarted = st.bootAt and bootAt > st.bootAt + CLU_REMOTE.BIND_RESTART_SLACK
                local back = st.down
                st.bootAt = bootAt
                st.down = false
                if restarted or back then
                    Logger:getShared():info("CLU_REMOTE %s: %s, writing bound features again", tostring(clu.id),
                        restarted and "restarted" or "answers again")
                    clu:_rewriteBound()
                else
                    clu:_flushBound()
                end
            end)
        end
    end
end

--- Keeps a user feature equal to an object's value. Writes it when the value changes
-- (without waiting for the CLU), and all bound features again when the CLU comes back
-- (a restart, a configuration from OM). nil is not written.
-- @api public
-- @param name string Feature name, e.g. "ha_temp_balkon"
-- @param obj table Object with onChange (Remote*, Virtual*, a plugin sensor)
-- @param opts table Optional: {script = "HaZmiana"} an OM script run on the CLU after a write caused by a change
-- @return function Call it to remove the binding
function CLU_REMOTE:bind(name, obj, opts)
    featureName(name)
    if type(obj) ~= "table" or type(obj.onChange) ~= "function" then
        error("CLU_REMOTE: bind needs an object with onChange, got " .. tostring(obj), 2)
    end
    if opts ~= nil and type(opts) ~= "table" then
        error("CLU_REMOTE: bind options must be a table, got " .. tostring(opts), 2)
    end
    local script = opts and opts.script
    if script ~= nil and (type(script) ~= "string" or not script:match("^[%a_][%w_]*$")) then
        error("CLU_REMOTE: script name must be a Lua identifier, got " .. tostring(script), 2)
    end

    local old = CLU_REMOTE._bound[self] and CLU_REMOTE._bound[self][name]
    if old then old.unbind() end
    local list = CLU_REMOTE._bound[self]
    if not list then
        list = {}
        CLU_REMOTE._bound[self] = list
    end
    self._bindState = self._bindState or {}

    local b = { obj = obj, script = script, dirty = false, runScript = false }
    local function changed(value, fromChange)
        if value == nil or value == b.value then return end
        b.value = value
        b.dirty = true
        if fromChange and b.script then b.runScript = true end
        self:_flushBound()
    end
    b.unsubscribe = obj:onChange(function(value) changed(value, true) end)
    b.unbind = function()
        if type(b.unsubscribe) == "function" then b.unsubscribe() end
        b.unsubscribe = nil
        local cur = CLU_REMOTE._bound[self]
        if not cur or cur[name] ~= b then return end
        cur[name] = nil
        if next(cur) == nil then CLU_REMOTE._bound[self] = nil end
        if next(CLU_REMOTE._bound) == nil and CLU_REMOTE._bindTimer then
            EventBus:getShared():clearInterval(CLU_REMOTE._bindTimer)
            CLU_REMOTE._bindTimer = nil
        end
    end
    list[name] = b

    if not CLU_REMOTE._bindTimer then
        CLU_REMOTE._bindTimer = EventBus:getShared():setInterval(CLU_REMOTE._checkBound,
            CLU_REMOTE.BIND_CHECK_SECONDS * 1000)
    end
    changed(currentValue(obj), false)
    return b.unbind
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