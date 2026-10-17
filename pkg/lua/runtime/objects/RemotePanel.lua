--- Remote Smart Panel (class 29, PANEL) and its screen.
-- Indices from Object Manager device-interfaces, SMART_PANEL_FM_4 fv05/fv07 (execute methods
-- of PANEL; see pkg/lua/AGENTS.md). The screen is drawn with frames: lists of calls sent
-- in short messages, spaced, the last one with a reply; a lost frame is sent again whole.
-- A CLU runs one expression per message, so every message is wrapped in a function call.
-- The CLU forwards the panel's events with click(name, input), where name is the panel or
-- one of its buttons (Object Manager fills in the button's id).
-- @module objects.RemotePanel
-- @inherits RemoteObject

RemotePanel = setmetatable({}, {__index = RemoteObject})
RemotePanel.__index = RemotePanel

-- PANEL execute methods
RemotePanel.SWITCH_ON_DISPLAY = 0
RemotePanel.CLEAR = 2
RemotePanel.PRINT_TEXT = 3
RemotePanel.DRAW_LINE = 4
RemotePanel.DRAW_POINT = 5
RemotePanel.DRAW_ICON = 6
RemotePanel.DISPLAY = 7
RemotePanel.PRINT_FLOAT = 20
RemotePanel.BEEP = 21
RemotePanel.SET_PAGE = 22
RemotePanel.CLEANING = 31
-- DrawBox (execute 30) is listed from fv05 on but drew nothing on a fv05 panel: not used
-- PANEL features
RemotePanel.FEATURE_BUZZER = 13
RemotePanel.FEATURE_PAGE_CONTROL = 20

-- Frame sending: the CLU drops long messages and bursts (pkg/lua/AGENTS.md); a WiFi panel
-- took 337 characters in one message, so 300 keeps a margin
RemotePanel.MAX_MESSAGE = 300
RemotePanel.SPACING_MS = 60
RemotePanel.RETRIES = 2
-- One expression per message: the statements of a part go inside this call
RemotePanel.WRAP_OPEN = "(function() "
RemotePanel.WRAP_CLOSE = " end)()"

-- Timer used for spacing; tests replace it
RemotePanel.timer = {
    after = function(ms, fn) return EventBus:getShared():setTimeout(fn, ms) end,
}

-- Panels by object id and by name, for click()
RemotePanel._byName = {}

--- Creates a new RemotePanel instance.
-- @param clu CLU_REMOTE The CLU the panel is on
-- @param objId string Object id (e.g. "PAN9297")
-- @param name string Optional name from Object Manager
-- @return RemotePanel
function RemotePanel:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "PANEL"
    obj._buttons = {}
    obj._gen = 0
    obj._queue = nil
    obj._busy = false
    obj._broken = true -- nothing known on screen yet: the first frame is full
    obj._tries = 0
    obj._latestFull = nil
    obj._allowedMethods = {"beep", "displayOn", "setPage", "getButton"}
    RemotePanel._byName[objId] = obj
    if name then RemotePanel._byName[name] = obj end
    return obj
end

--- Attaches a PANEL_BUTTON of the same module (the OM import does this).
-- @param index number Button index on the module (0-15; 0-3 are page 1)
-- @param button RemotePanelButton
-- @api public
function RemotePanel:addButton(index, button)
    self._buttons[index] = button
    -- click() from Object Manager often carries the button's id, not the panel's
    RemotePanel._byName[button._objId] = self
    if button._name then RemotePanel._byName[button._name] = self end
    return self
end

--- Gets an attached button.
-- @param index number Button index on the module
-- @return RemotePanelButton|nil
-- @api public
function RemotePanel:getButton(index)
    return self._buttons[index]
end

--- One call on the panel as a script statement, for a frame.
-- @param index number PANEL execute method
-- @return string The statement
function RemotePanel:op(index, ...)
    local args = {index}
    for _, v in ipairs({...}) do args[#args + 1] = RemoteObject.literal(v) end
    return self._objId .. ":execute(" .. table.concat(args, ", ") .. ")"
end

--- Switches the display on.
-- @api public
function RemotePanel:displayOn() self:execute(RemotePanel.SWITCH_ON_DISPLAY) end

--- Beeps.
-- @param freq number Frequency 30-5700 Hz (default 2000)
-- @param ms number Duration 1-2000 ms (default 100)
-- @param volume number Volume 0-16 (default 8)
-- @api public
function RemotePanel:beep(freq, ms, volume)
    self:execute(RemotePanel.BEEP, freq or 2000, ms or 100, volume or 8, 0)
end

--- Shows one of Grenton's built-in pages (1-4).
-- @api public
function RemotePanel:setPage(nr) self:execute(RemotePanel.SET_PAGE, nr) end

-- Packs statements into messages of at most MAX_MESSAGE characters, each one expression
local function pack(ops)
    local open, close = RemotePanel.WRAP_OPEN, RemotePanel.WRAP_CLOSE
    local budget = RemotePanel.MAX_MESSAGE - #open - #close
    local parts, cur = {}, ""
    for _, op in ipairs(ops) do
        if cur ~= "" and #cur + 1 + #op > budget then
            parts[#parts + 1] = open .. cur .. close
            cur = op
        else
            cur = (cur == "" and op) or (cur .. " " .. op)
        end
    end
    if cur ~= "" then parts[#parts + 1] = open .. cur .. close end
    return parts
end
RemotePanel._pack = pack

--- Sends a frame. A newer frame replaces what is left of an older one; then, and after
-- a lost frame, the next one is sent in full.
-- @param ops table Statements (from op()) for the change
-- @param full table|nil Statements that redraw everything (default: ops)
function RemotePanel:send(ops, full)
    full = full or ops
    if self._queue then self._broken = true end -- the rest of the older frame is dropped
    self._gen = self._gen + 1
    self._tries = 0
    self._latestFull = full
    self._queue = { gen = self._gen, parts = pack(self._broken and full or ops), full = self._broken }
    if not self._busy then self:_step() end
end

--- True while frames are waiting or going out.
function RemotePanel:isSending() return self._busy end

function RemotePanel:_step()
    local q = self._queue
    if not q then self._busy = false return end
    self._busy = true
    local part = table.remove(q.parts, 1)
    if #q.parts == 0 then
        self._queue = nil
        local gen = q.gen
        self._clu:executeAsync(part, function(_, err)
            if err then return self:_lost(gen, err) end
            if gen == self._gen and q.full then self._broken = false end
        end)
    else
        self._clu:executeNoReply(0, part)
    end
    RemotePanel.timer.after(RemotePanel.SPACING_MS, function() self:_step() end)
end

-- No answer to a frame's last message: send the newest frame again, whole
function RemotePanel:_lost(gen, err)
    self._broken = true
    if gen ~= self._gen and self._queue then return end -- a full frame is on its way
    if self._tries >= RemotePanel.RETRIES then
        Logger:getShared():warn("[Panel] %s: frame lost after %d tries: %s", self._name, self._tries + 1, tostring(err))
        return
    end
    self._tries = self._tries + 1
    self._queue = { gen = self._gen, parts = pack(self._latestFull), full = true }
    if not self._busy then self:_step() end
end

--- Sets the function that gets the panel's inputs (click(name, input) from the CLU).
-- @param fn function Called with (input)
function RemotePanel:onInput(fn)
    self._onInput = fn
    return self
end

local warned = {}

--- Entry point for the CLU: every panel event in Object Manager calls
-- click("<panel name>", "<input>"). Never raises into the CLU's request.
-- @param name string Panel object name or id
-- @param input string Input (btn1..btn4, btn1:long, swipe:up, proximity, wake, ...)
-- @api public
function RemotePanel.click(name, input)
    local panel = RemotePanel._byName[tostring(name)]
    if not panel or not panel._onInput then
        local key = tostring(name)
        if not warned[key] then
            warned[key] = true
            Logger:getShared():warn("[Panel] click from unknown panel %s (input %s)", key, tostring(input))
        end
        return false
    end
    local ok, err = pcall(panel._onInput, tostring(input))
    if not ok then
        Logger:getShared():error("[Panel] %s: input %s failed: %s", panel._name, tostring(input), tostring(err))
    end
    return ok
end

-- click() is the name to call from Object Manager; a script "click" defined on vCLU in
-- Object Manager replaces it on upload, so its body should call RemotePanel.click
click = RemotePanel.click
