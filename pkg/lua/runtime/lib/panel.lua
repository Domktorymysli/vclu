--- Panel: vCLU runs a Grenton Smart Panel.
-- The CLU only forwards the panel's events (click(name, input)); this library keeps the
-- state (open pages, cursor, edits, idle timer) and draws the screen with frames sent by
-- RemotePanel. The panel is a square with four buttons in the corners around a small
-- display: a page gives the corners meanings (roles up, down, ok); one corner is always
-- back. Hints for the corners are drawn next to them, the buttons' LEDs show which work.
--
--   local Panel = require("lib/panel")
--   Panel.new(dom.P1_PANEL_SALON)
--     :home(Panel.menu("Dom")
--       :toggle("Swiatlo", dom.LAMPA)
--       :number("Zadana", getFn, setFn, { step = 0.5, min = 19, max = 22 }))
--     :idle(Panel.idle{ { "Salon", dom.TEMP } })
--     :sleepAfter(30)
--
-- A page is a table: draw(screen, panel), keys (table or function(panel) -> {role = {label, fn}}),
-- input(name, panel), back(panel) -> true when it handled back itself, watch (objects or
-- function), every (seconds), modal (gets every input, no hints), open/close(panel).
-- Panel.register(name, factory) adds a page type as Panel.<name>.
-- @module lib.panel

local Panel = {}
Panel.__index = Panel

-- Screen layout (128 x 64). Measured on a Smart Panel WiFi (module fv05): y is the top of
-- the text; font 1 is 10 px high (capitals; g, y go lower), font 2 14 px, font 3 28 px.
-- PrintText paints a black cell behind every letter, so text never inverts and nothing
-- fills a box (DrawBox draws nothing): every frame redraws the whole screen.
-- The fonts are proportional: a row holds 16 digits in font 1 and 13 in font 2; a space is
-- narrower (about 6 px), so a column is set by x, never by leading spaces.
Panel.layout = {
    w = 128, h = 64,
    cw = { 8, 9.6, 16 },     -- width of a digit or capital per font, for aligning text
    -- narrower characters in font 1 (measured from a photo); other fonts scale them by cw
    narrow = { ["1"] = 7, F = 7, ["%"] = 9, ["."] = 4, [","] = 4, ["["] = 4, ["]"] = 4, [" "] = 6 },
    fh = { 10, 14, 28 },     -- font height per font
    top = 0, bottom = 54,    -- y of the hint rows
    bottomText = 48,         -- y of a longer bottom hint: lower case runs below 64 at 54
    rows = { 14, 27, 40 },   -- y of the content rows
    rowH = 12,
    indent = 8,              -- x of a menu label, after the cursor
}
Panel.MAX_TEXT = 15
-- Texts the library draws itself; a module sets them in its own language
Panel.strings = {
    noChanges = "no changes",  -- an empty history
    cleaning = "Cleaning",
    thermostat = "Thermostat",
    now = "Now",               -- thermostat: current temperature
    set = "Set",               -- thermostat: set point
    on = "ON", off = "OFF",    -- a switch, a dimmer at 0, a binary sensor
    open = "OPEN", closed = "closed",       -- a gate in a menu
    openShort = "open", closedShort = "shut", -- a gate in its history (about 5 characters fit)
    coverOpen = "Open", coverClose = "Close", coverStop = "Stop", -- the corners of a cover page
}

-- Texts per language for Panel.lang; letters the panel lacks are mapped when drawn
Panel.languages = {
    pl = {
        noChanges = "brak zmian", cleaning = "Czyszczenie", thermostat = "Termostat",
        now = "Teraz", set = "Zadana", on = "ON", off = "OFF",
        open = "OTWARTA", closed = "zamk.", openShort = "otw.", closedShort = "zamk.",
        coverOpen = "Otworz", coverClose = "Zamknij", coverStop = "Stop",
    },
}

--- Sets Panel.strings to a language ("pl"); English is the default.
-- @param code string Language code from Panel.languages
-- @api public
function Panel.lang(code)
    local t = Panel.languages[code]
    assert(t, "Panel.lang: no language " .. tostring(code))
    for k, v in pairs(t) do Panel.strings[k] = v end
end
Panel.REDRAW_MS = 20         -- changes within this time make one frame
Panel.pageControlOff = true  -- stop gestures from switching Grenton's own pages

-- Letters the panel's fonts may not have, replaced in one place
Panel.charMap = {
    ["ą"] = "a", ["ć"] = "c", ["ę"] = "e", ["ł"] = "l", ["ń"] = "n", ["ó"] = "o",
    ["ś"] = "s", ["ź"] = "z", ["ż"] = "z", ["Ą"] = "A", ["Ć"] = "C", ["Ę"] = "E",
    ["Ł"] = "L", ["Ń"] = "N", ["Ó"] = "O", ["Ś"] = "S", ["Ź"] = "Z", ["Ż"] = "Z",
}

Panel.timer = {
    after = function(ms, fn) return EventBus:getShared():setTimeout(fn, ms) end,
    cancel = function(id) if id then EventBus:getShared():clearInterval(id) end end,
    now = function() return (__go_time_ms and __go_time_ms()) or os.time() * 1000 end,
    -- "HH:MM  DD.MM" in the installation's time zone (datetime.setTimezone), not the server's
    clock = function()
        if datetime and datetime.now and datetime.format then
            return datetime.format(datetime.now(), "15:04  02.01")
        end
        return os.date("%H:%M  %d.%m")
    end,
    -- "HH:MM DD.MM" of a time in ms, in the installation's time zone
    at = function(ms)
        if datetime and datetime.fromTimestamp and datetime.format then
            return datetime.format(datetime.fromTimestamp(ms), "15:04 02.01")
        end
        return os.date("%H:%M %d.%m", math.floor(ms / 1000))
    end,
}

local CORNERS = { "tl", "tr", "bl", "br" }
-- For each back corner: the other column is up (top) / down (bottom), the rest is ok.
-- With back top right: up and down on the left, ok bottom right.
local ROLES = {
    tr = { up = "tl", down = "bl", ok = "br" },
    br = { up = "tl", down = "bl", ok = "tr" },
    tl = { up = "tr", down = "br", ok = "bl" },
    bl = { up = "tr", down = "br", ok = "tl" },
}

local function log() return Logger:getShared() end

-- Panels, for events that are not a value change (a cover's movement)
Panel._panels = setmetatable({}, { __mode = "k" })
local hooked = setmetatable({}, { __mode = "k" })

--- Text with the letters the panel lacks replaced.
-- @api public
function Panel.map(s)
    s = tostring(s == nil and "" or s)
    for from, to in pairs(Panel.charMap) do s = s:gsub(from, to) end
    return s
end

--- Text as the panel can print it: letters mapped, cut to MAX_TEXT.
-- @api public
function Panel.text(s)
    s = Panel.map(s)
    if #s > Panel.MAX_TEXT then s = s:sub(1, Panel.MAX_TEXT) end
    return s
end

-- An object, or one from the registry by path ("CLU221000290.DOU4157"); nil if missing
local function object(o, label)
    if type(o) ~= "string" then
        if o == nil and label then
            Logger:getShared():warn("[Panel] %s: no object, row left out", tostring(label))
        end
        return o
    end
    local found = _registry and _registry:get(o)
    if not found then Logger:getShared():warn("[Panel] no object %s, row left out", o) end
    return found
end
Panel.object = object

-- Value of an object (getValue/get) or a function
local function read(src)
    if type(src) == "function" then return src() end
    if type(src) == "table" then
        if src.getValue then return src:getValue() end
        if src.get then return src:get() end
    end
    return src
end
Panel.read = read

-- Switches an on/off object
local function flip(o)
    if o.toggle then o:toggle()
    elseif o.setValue then o:setValue((read(o) == 1) and 0 or 1) end
end

local function fmt(f, v)
    if v == nil then return "--" end
    if type(f) == "function" then return f(v) end
    if type(f) == "table" then -- { [0] = "closed", [1] = "open" }
        if v == true then v = 1 elseif v == false then v = 0 end
        local t = f[v]
        if t == nil then t = f[tonumber(v)] end
        if t ~= nil then return tostring(t) end
        return tostring(v)
    end
    if f then
        local ok, s = pcall(string.format, f, v)
        if ok then return s end
    end
    return tostring(v)
end
Panel.format = fmt

-- A value just set shows until the device reports it, at most HOLD_S: a device behind
-- HTTP or a slow bus reads back the old value for a while
Panel.HOLD_S = 90

local function same(a, b)
    if a == b then return true end
    local x, y = tonumber(a), tonumber(b)
    return x ~= nil and y ~= nil and math.abs(x - y) < 1e-6
end

-- before: what the device reported when the value was set; any other report ends the hold
-- (the value, or the device clamped it, or someone changed it there)
local function hold(t, v, before, panel)
    t._held = { value = v, before = before, untilMs = Panel.timer.now() + Panel.HOLD_S * 1000 }
    Panel.timer.after(Panel.HOLD_S * 1000, function() panel:redraw() end)
end

local function shown(t, current)
    local h = t._held
    if not h then return current end
    if not same(current, h.before) or Panel.timer.now() >= h.untilMs then
        t._held = nil
        return current
    end
    return h.value
end

-- ============================================================
-- Screen: collects the draw calls of one frame
-- ============================================================

local Screen = {}
Screen.__index = Screen

local function newScreen(panelObj)
    return setmetatable({ _obj = panelObj, _ops = {} }, Screen)
end

--- Names the area drawn next (kept for readable page code; a frame always redraws all).
function Screen:region(name)
    self._region = name
    return self
end

function Screen:_add(op)
    self._ops[#self._ops + 1] = op
    return self
end

-- Width of text in a font, from Panel.layout.cw and .narrow
local function textW(s, font)
    local L = Panel.layout
    local cw = L.cw[font or 1] or L.cw[1]
    local w = 0
    for c in s:gmatch(".") do
        local n = L.narrow[c]
        w = w + (n and n * cw / L.cw[1] or cw)
    end
    return w
end

-- Text cut so it is at most w pixels wide
local function fit(s, w, font)
    s = Panel.text(s)
    if textW(s, font) <= w then return s end
    while #s > 0 and textW(s, font) > w do s = s:sub(1, -2) end
    s = s:gsub("[\192-\255][\128-\191]*$", "") -- no half of a UTF-8 character (°)
    return (s:gsub("%s+$", ""))
end

--- Text at x, y (y is the top), font 1-3; cut to 15 characters.
-- @api public
function Screen:text(x, y, s, font)
    return self:_add(self._obj:op(RemotePanel.PRINT_TEXT, math.floor(x), math.floor(y), Panel.text(s), font or 1))
end

--- Text aligned to the right edge (or to x as its right end).
-- @api public
function Screen:textRight(y, s, font, right)
    s = Panel.text(s)
    local x = (right or Panel.layout.w) - textW(s, font)
    return self:text(math.max(0, x), y, s, font)
end

--- Text centred between x0 and x1 (default: the whole width).
-- @api public
function Screen:textCenter(y, s, font, x0, x1)
    s = Panel.text(s)
    x0, x1 = x0 or 0, x1 or Panel.layout.w
    local x = x0 + ((x1 - x0) - textW(s, font)) / 2
    return self:text(math.max(0, x), y, s, font)
end

--- A number printed by the panel itself, with precision 0-2.
-- @api public
function Screen:float(x, y, n, precision, font)
    return self:_add(self._obj:op(RemotePanel.PRINT_FLOAT, x, y, n, precision or 1, font or 1))
end

--- A line from x, y to xe, ye (color 1 white, 0 black).
-- @api public
function Screen:line(x, y, xe, ye, color)
    return self:_add(self._obj:op(RemotePanel.DRAW_LINE, x, y, xe, ye, color or 1))
end

--- A horizontal rule across the screen.
-- @api public
function Screen:rule(y)
    return self:line(0, y, Panel.layout.w - 1, y, 1)
end

--- One pixel.
-- @api public
function Screen:point(x, y, color)
    return self:_add(self._obj:op(RemotePanel.DRAW_POINT, x, y, color or 1))
end

--- An icon from the panel's card: file name without .bmp (most are 64 x 32, arrows 16 x 16).
-- @api public
function Screen:icon(x, y, file)
    return self:_add(self._obj:op(RemotePanel.DRAW_ICON, x, y, tostring(file)))
end

Panel.Screen = Screen

-- ============================================================
-- Panel instance
-- ============================================================

--- Takes over a panel.
-- @param obj RemotePanel The panel from the OM import
-- @return Panel
-- @api public
function Panel.new(obj)
    if type(obj) == "string" then
        local id = obj
        obj = Panel.find(id)
        assert(obj, "Panel.new: no panel " .. id .. " (OM import of its CLU?)")
    end
    assert(obj and obj.op and obj.send, "Panel.new: a RemotePanel is required")
    -- the click() Object Manager uploads in user.lua is empty and hides the runtime's
    click = RemotePanel.click
    local self = setmetatable({
        obj = obj,
        _input = { btn1 = "tl", btn2 = "tr", btn3 = "bl", btn4 = "br" },
        _back = "tr",
        _home = nil,
        _idle = nil,
        _sleep = nil,
        _gestures = {},
        _onProximity = nil,
        _stack = {},
        _subs = {},
        _timers = {},
        _deb = {},
        _leds = nil,       -- LEDs as the last frame left them
        _pending = false,
        _started = false,
    }, Panel)
    obj:onInput(function(input) self:input(input) end)
    Panel._panels[self] = true
    Panel.timer.after(0, function() self:start() end)
    return self
end

--- A panel from the OM import by its object id or name ("PAN1085"), or nil.
-- @api public
function Panel.find(id)
    return RemotePanel and RemotePanel._byName[tostring(id)] or nil
end

--- Which physical button is in which corner.
-- @param map table { tl = "btn1", tr = "btn2", bl = "btn3", br = "btn4" }
-- @api public
function Panel:buttons(map)
    self._input = {}
    for corner, btn in pairs(map) do self._input[btn] = corner end
    return self
end

--- The corner that is back on every screen (default "tr"; ok is below or above it).
-- @api public
function Panel:back(corner)
    if corner == nil then return self:goBack() end
    assert(ROLES[corner], "Panel:back: corner must be tl, tr, bl or br")
    self._back = corner
    return self
end

--- The home page (a menu); without an argument, goes home.
-- @api public
function Panel:home(page)
    if page == nil then return self:goHome() end
    self._home = page
    return self
end

local function setIdle(self, page)
    self._idle = page
    if page then page._isIdle = true end
    return self
end

--- After this many seconds without input: the idle screen, or home.
-- @api public
function Panel:sleepAfter(seconds)
    self._sleep = seconds
    return self
end

--- A gesture handler: name "swipe:up" / "swipe:down" / "swipe:left" / "swipe:right".
-- @api public
function Panel:onGesture(name, fn)
    if not name:find(":") then name = "swipe:" .. name end
    self._gestures[name] = fn
    return self
end

--- Called when someone comes close to the panel.
-- @api public
function Panel:onProximity(fn)
    self._onProximity = fn
    return self
end

--- Draws the home page; runs by itself once the definition is complete.
function Panel:start()
    if self._started then return self end
    self._started = true
    if Panel.pageControlOff then self.obj:set(RemotePanel.FEATURE_PAGE_CONTROL, 0) end
    self:goHome()
    return self
end

-- ---------- pages ----------

function Panel:top() return self._stack[#self._stack] end

local function keysOf(page, panel)
    local k = page and page.keys
    if type(k) == "function" then k = k(panel) end
    return k or {}
end

function Panel:_unwatch()
    for _, unsub in ipairs(self._subs) do pcall(unsub) end
    self._subs = {}
    self._watching = {}
    for _, id in ipairs(self._timers) do Panel.timer.cancel(id) end
    self._timers = {}
end

function Panel:_watch(page)
    self:_unwatch()
    if not page then return end
    local objs = page.watch
    if type(objs) == "function" then objs = objs(self) end
    for _, o in ipairs(objs or {}) do
        if type(o) == "table" and o.onChange then
            local unsub = o:onChange(function() self:redraw() end)
            if type(unsub) == "function" then self._subs[#self._subs + 1] = unsub end
        end
        -- what an object keeps aside from its value (a roller's movement, a thermostat's
        -- target) comes with CHANGE_EVENTS; onEvent has no unsubscribe, so one handler per
        -- object redraws the panels watching it
        if type(o) == "table" and o.CHANGE_EVENTS and o.onEvent then
            self._watching[o] = true
            if not hooked[o] then
                hooked[o] = true
                for _, ev in ipairs(o.CHANGE_EVENTS) do
                    o:onEvent(ev, function()
                        for p in pairs(Panel._panels) do
                            if p._watching and p._watching[o] then p:redraw() end
                        end
                    end)
                end
            end
        end
    end
    if page.every then
        local function tick()
            if self:top() ~= page then return end
            self:redraw()
            local ms = page.every * 1000
            if page.align then ms = ms - Panel.timer.now() % ms end
            self._timers[#self._timers + 1] = Panel.timer.after(ms, tick)
        end
        local ms = page.every * 1000
        if page.align then ms = ms - Panel.timer.now() % ms end
        self._timers[#self._timers + 1] = Panel.timer.after(ms, tick)
    end
end

function Panel:_show()
    local page = self:top()
    self:_watch(page)
    self:redraw()
end

local function call(page, name, ...)
    local fn = page and page[name]
    if type(fn) ~= "function" then return nil end
    local ok, r = pcall(fn, ...)
    if not ok then log():error("[Panel] page %s failed: %s", name, tostring(r)) end
    return r
end

--- Opens a page on top of the current one.
-- @api public
function Panel:open(page)
    if type(page) == "string" then page = self._named and self._named[page] end
    if not page then return self end
    if getmetatable(page) == Panel.Menu then page:_prepare() end
    self._stack[#self._stack + 1] = page
    call(page, "open", self)
    self:_show()
    return self
end

--- Closes the current page; on home, goes to the idle screen.
-- @api public
function Panel:goBack()
    local page = self:top()
    if #self._stack <= 1 then
        if page and not page._isIdle and self._idle then self:goIdle() end
        return self
    end
    table.remove(self._stack)
    call(page, "close", self)
    self:_show()
    return self
end

--- Closes a given page wherever it is (notify, cleaning end).
function Panel:close(page)
    for i = #self._stack, 1, -1 do
        if self._stack[i] == page then
            table.remove(self._stack, i)
            call(page, "close", self)
            if i == #self._stack + 1 then self:_show() end
            return self
        end
    end
    return self
end

function Panel:_reset(page)
    for i = #self._stack, 1, -1 do call(self._stack[i], "close", self) end
    self._stack = {}
    if page then
        if getmetatable(page) == Panel.Menu then page:_prepare() end
        self._stack[1] = page
        call(page, "open", self)
    end
    self:_show()
end

--- Closes every page and shows home.
-- @api public
function Panel:goHome()
    self:_reset(self._home)
    return self
end

--- Closes every page and shows the idle screen (or home).
-- @api public
function Panel:goIdle()
    self:_reset(self._idle or self._home)
    return self
end

--- Names a page so panel:open("name") and gestures can open it.
-- @api public
function Panel:name(name, page)
    self._named = self._named or {}
    self._named[name] = page
    return self
end

-- ---------- input ----------

function Panel:_touch()
    if self._sleepTimer then Panel.timer.cancel(self._sleepTimer) end
    self._sleepTimer = nil
    if not self._sleep then return end
    self._sleepTimer = Panel.timer.after(self._sleep * 1000, function()
        self._sleepTimer = nil
        local top = self:top()
        if top and (top.modal or top._isIdle) then return self:_touch() end
        self:goIdle()
    end)
end

--- Handles one input from the CLU.
-- @param raw string btn1..btn4 (":long", ":hold"), swipe:<dir>, proximity, wake
function Panel:input(raw)
    raw = tostring(raw)
    if raw == "wake" then
        self:redraw(true)
        return
    end
    if raw == "proximity" then
        self:_touch()
        if self._onProximity then
            local ok, err = pcall(self._onProximity, self)
            if not ok then log():error("[Panel] onProximity failed: %s", tostring(err)) end
        end
        return
    end
    local base, mod = raw:match("^([^:]+):?(.*)$")
    if base == "swipe" then
        self:_touch()
        local fn = self._gestures[raw]
        if fn then
            local ok, err = pcall(fn, self)
            if not ok then log():error("[Panel] gesture %s failed: %s", raw, tostring(err)) end
        elseif self:top() and self:top().gestures then
            call(self:top(), "input", raw, self)
            self:redraw()
        end
        return
    end
    local corner = self._input[base]
    if not corner then
        log():warn("[Panel] %s: unknown input %s", tostring(self.obj._name), raw)
        return
    end
    self:_touch()
    local page = self:top()
    if not page then return end
    local role = corner == self._back and "back" or nil
    if not role then
        for r, c in pairs(ROLES[self._back]) do if c == corner then role = r end end
    end
    local name = mod ~= "" and (role .. ":" .. mod) or role

    if page.modal then
        call(page, "input", name, self)
        self:redraw()
        return
    end
    if page._isIdle and self._home and page ~= self._home then
        if mod == "" then self:goHome() end
        return
    end
    if page.corners then
        local c = page.corners[corner]
        local action = mod == "" and c and c[2]
        if type(action) == "function" then
            local ok, err = pcall(action, self)
            if not ok then log():error("[Panel] corner %s failed: %s", corner, tostring(err)) end
        elseif type(action) == "table" then
            Panel.press(action)
        end
        self:redraw()
        return
    end
    if name == "back" then
        if call(page, "back", self) ~= true then self:goBack() end
        self:redraw()
        return
    end
    if page.input then
        call(page, "input", name, self)
    else
        local key = keysOf(page, self)[name]
        if key and type(key[2]) == "function" then
            local ok, err = pcall(key[2], self)
            if not ok then log():error("[Panel] key %s failed: %s", name, tostring(err)) end
        end
    end
    self:redraw()
end

-- ---------- helpers for pages ----------

--- Runs fn once, seconds after the last call with this key.
-- @api public
function Panel:debounce(key, seconds, fn)
    if self._deb[key] then Panel.timer.cancel(self._deb[key]) end
    self._deb[key] = Panel.timer.after((seconds or 1) * 1000, function()
        self._deb[key] = nil
        local ok, err = pcall(fn)
        if not ok then log():error("[Panel] debounce %s failed: %s", tostring(key), tostring(err)) end
    end)
end

-- Splits text into lines of at most MAX_TEXT characters, by words
local function wrap(text, max)
    local lines, cur = {}, ""
    for word in tostring(text):gmatch("%S+") do
        if cur == "" then cur = word
        elseif #cur + 1 + #word <= max then cur = cur .. " " .. word
        else lines[#lines + 1] = cur; cur = word end
    end
    if cur ~= "" then lines[#lines + 1] = cur end
    return lines
end
Panel.wrap = wrap

--- Shows a message over the current page; OK, back or the timeout closes it.
-- @param text string
-- @param opts table|nil { beep = true, seconds = 30, title = "..." }
-- @api public
function Panel:notify(text, opts)
    opts = opts or {}
    local page
    page = {
        keys = { ok = { ">", function(p) p:close(page) end } },
        draw = function(s)
            local L = Panel.layout
            if opts.title then s:region("title"):textCenter(L.top, opts.title, 1, 30, 98) end
            s:region("msg")
            for i, line in ipairs(wrap(Panel.map(text), Panel.MAX_TEXT)) do
                if L.rows[i] then s:textCenter(L.rows[i], line) end
            end
        end,
        back = function(p) p:close(page) return true end,
    }
    self:open(page)
    if opts.beep then self.obj:beep() end
    if opts.seconds then
        Panel.timer.after(opts.seconds * 1000, function() self:close(page) end)
    end
    return page
end

-- ---------- drawing ----------

--- Asks for a redraw; changes within REDRAW_MS make one frame.
-- @param full boolean Kept for callers; every frame redraws the whole screen
-- @api public
function Panel:redraw(full)
    if self._pending then return end
    self._pending = true
    Panel.timer.after(Panel.REDRAW_MS, function()
        self._pending = false
        self:_frame()
    end)
end

local HINT_W = 30

function Panel:_frame()
    local page = self:top()
    if not page then return end
    local L = Panel.layout
    local s = newScreen(self.obj)
    local keys = keysOf(page, self)
    local roles = ROLES[self._back]
    local lit = {}

    -- hints in the corners
    for _, corner in ipairs(CORNERS) do
        local role
        if corner == self._back then role = "back" end
        for r, c in pairs(roles) do if c == corner then role = r end end
        local label
        if page.corners then
            local c = page.corners[corner]
            label = c and c[1]
            if c and c[2] then lit[corner] = true end
        elseif not page.modal and not page._isIdle then
            if role == "back" then
                label = keys.back and keys.back[1] or "<"
            elseif keys[role] then
                label = keys[role][1]
            end
        end
        local y = L.top
        if corner == "bl" or corner == "br" then
            y = (label and #label > 1) and L.bottomText or L.bottom
        end
        s:region("hint." .. corner)
        if label and not page.corners then lit[corner] = true end -- "" lights the LED with no hint
        if label and label ~= "" then
            if corner == "tl" or corner == "bl" then s:text(0, y, label)
            else s:textRight(y, label) end
        end
    end
    if page._isIdle then for _, c in ipairs(CORNERS) do lit[c] = true end end

    local ok, err = pcall(page.draw, s, self)
    if not ok then log():error("[Panel] draw failed: %s", tostring(err)) end

    -- LEDs of the buttons; only the ones that change go out
    local leds, ledState = {}, {}
    for btn, corner in pairs(self._input) do
        local idx = tonumber(btn:match("^btn(%d+)$"))
        local b = idx and self.obj:getButton(idx - 1)
        if b then leds[#leds + 1] = { key = btn, on = lit[corner] == true, op = b:ledOp(lit[corner] == true) } end
    end
    table.sort(leds, function(a, b) return a.key < b.key end)

    local ops = { self.obj:op(RemotePanel.CLEAR) }
    for _, op in ipairs(s._ops) do ops[#ops + 1] = op end
    ops[#ops + 1] = self.obj:op(RemotePanel.DISPLAY)
    local full = {}
    for i, op in ipairs(ops) do full[i] = op end
    local last = self._leds or {}
    for _, l in ipairs(leds) do
        full[#full + 1] = l.op
        if last[l.key] ~= l.on then ops[#ops + 1] = l.op end
        ledState[l.key] = l.on
    end
    self._leds = ledState

    self.obj:send(ops, full)
end

-- ============================================================
-- Page types
-- ============================================================

--- Adds a page type: Panel.<name>(...) calls factory(...) and returns the page.
-- @api public
function Panel.register(name, factory)
    assert(type(factory) == "function", "Panel.register: factory must be a function")
    Panel[name] = factory
end

local function title(s, text)
    local L = Panel.layout
    s:region("title", HINT_W, L.top, L.w - 2 * HINT_W, 10):textCenter(L.top, text, 1, HINT_W, L.w - HINT_W)
end
Panel.drawTitle = title

-- ---------- menu ----------

local Menu = {}
Menu.__index = Menu

--- A list of rows; up/down move, OK opens or edits, back leaves (or cancels an edit).
-- @param name string Title
-- @param submenus table|nil Menus to add as submenus (empty ones are left out)
-- @api public
function Panel.menu(name, submenus)
    local m = setmetatable({ name = name, rows = {}, cursor = 1, scroll = 0, edit = nil }, Menu)
    -- the panel calls page functions without self
    m.keys = function() return Menu.keys(m) end
    m.watch = function() return Menu.watch(m) end
    m.draw = function(s, p) return Menu.draw(m, s, p) end
    m.input = function(n, p) return Menu.input(m, n, p) end
    m.back = function() return Menu.back(m) end
    m.close = function() return Menu.close(m) end
    submenus = submenus or {}
    for i = 1, table.maxn(submenus) do
        if submenus[i] then m:submenu(submenus[i]) end
    end
    return m
end

-- Rows a module added are kept apart from the rows shown, which _prepare rebuilds
function Menu:_add(row)
    local t = self._filling or self._own or self.rows
    t[#t + 1] = row
    return self
end

--- A row that opens a page or a submenu (also Menu:submenu).
-- @api public
function Menu:item(label, page) return self:_add({ label = label, page = page }) end

--- A row that opens a menu; a menu with no rows adds nothing. Without a label: the menu's name.
-- @api public
function Menu:submenu(label, menu)
    if menu == nil and getmetatable(label) == Menu then
        menu = label
        label = menu.name
    end
    if getmetatable(menu) == Menu and #(menu._own or menu.rows) == 0 and not menu._fill then return self end
    return self:_add({ label = label, page = menu, sub = true })
end

-- Each time a menu opens: refills a menu built from expose (Panel.area: its devices,
-- then the rows added by hand), so devices a plugin exposes later show up, and leaves out
-- submenus that are empty now
function Menu:_prepare(seen)
    seen = seen or {}
    if seen[self] then return end
    seen[self] = true
    self._own = self._own or self.rows
    local all = {}
    if self._fill then
        self._filling = {}
        local ok, err = pcall(self._fill, self)
        if not ok then log():error("[Panel] %s: filling failed: %s", tostring(self.name), tostring(err)) end
        for _, row in ipairs(self._filling) do all[#all + 1] = row end
        self._filling = nil
    end
    for _, row in ipairs(self._own) do all[#all + 1] = row end
    local rows = {}
    for _, row in ipairs(all) do
        local sub = row.sub and getmetatable(row.page) == Menu and row.page or nil
        if sub then sub:_prepare(seen) end
        if not (sub and #sub.rows == 0) then rows[#rows + 1] = row end
    end
    self.rows = rows
    if self.cursor > #rows then self.cursor = math.max(1, #rows) end
end

--- A row with an on/off object (or its registry path); OK switches it. No object: no row.
-- @api public
function Menu:toggle(label, obj)
    obj = object(obj, label)
    if obj == nil then return self end
    return self:_add({ label = label, obj = obj, kind = "toggle" })
end

--- A read-only value; with a page (e.g. Panel.chart), OK opens it.
-- @api public
function Menu:value(label, src, format, page)
    return self:_add({ label = label, src = src, format = format, kind = "value",
        obj = type(src) == "table" and src or nil, page = page })
end

--- A number edited in place: OK, then +/-, OK sets it once, back cancels.
-- @param label string Row label
-- @param get function Returns the current value
-- @param set function Gets the new value once, on OK
-- @param opts table { step = 1, min, max, format = "%.1f", watch = obj }
-- @api public
function Menu:number(label, get, set, opts)
    opts = opts or {}
    return self:_add({ label = label, get = get, set = set, opts = opts, kind = "number", obj = opts.watch })
end

--- A dimmer's brightness edited in place, 0-100 by 10; 0 shows OFF. No object: no row.
-- @param obj table|string Object with getBrightness/setBrightness (percent), or its path;
--   without them the row switches it on and off
-- @param opts table|nil { step = 10, format }
-- @api public
function Menu:dimmer(label, obj, opts)
    obj = object(obj, label)
    if obj == nil then return self end
    if not (obj.getBrightness and obj.setBrightness) then return self:toggle(label, obj) end
    opts = opts or {}
    local function get() return obj:getBrightness() or 0 end
    local function set(v) obj:setBrightness(v) end
    return self:number(label, get, set, { step = opts.step or 10, min = 0, max = 100, watch = obj,
        format = opts.format or function(v)
            v = tonumber(v) or 0
            if v <= 0 then return Panel.strings.off end
            return string.format("%d%%", v)
        end })
end

--- One of a list of options edited in place.
-- @param label string Row label
-- @param get function Returns the current option
-- @param set function Gets the chosen option once, on OK
-- @param opts table { options = { "A", "B" }, watch = obj }
-- @api public
function Menu:choice(label, get, set, opts)
    opts = opts or {}
    return self:_add({ label = label, get = get, set = set, opts = opts, kind = "choice", obj = opts.watch })
end

--- A row that runs fn(panel) (or opens a page); opts.confirm asks first.
-- @api public
function Menu:action(label, fn, opts)
    opts = opts or {}
    return self:_add({ label = label, fn = fn, opts = opts, kind = "action" })
end

local function rowValue(row)
    if row.kind == "toggle" then
        local v = read(row.obj)
        return (v == 1 or v == true) and Panel.strings.on or Panel.strings.off
    elseif row.kind == "value" then
        return fmt(row.format, read(row.src))
    elseif row.kind == "number" or row.kind == "choice" then
        return fmt(row.opts.format, shown(row, read(row.get)))
    elseif row.kind == "cover" then
        return Panel.coverText(row.obj)
    elseif row.page then
        return ">"
    end
    return ""
end

function Menu:keys()
    if self.edit then
        return { up = { "+" }, down = { "-" }, ok = { ">" }, back = { "x" } }
    end
    local k = {}
    -- up and down need no hint: the lit LEDs show them, and ^ v got clipped at the edges
    if #self.rows > 1 then k.up = { "" }; k.down = { "" } end
    local row = self.rows[self.cursor]
    if row and (row.kind ~= "value" or row.page) then k.ok = { ">" } end
    return k
end

function Menu:watch()
    local objs = {}
    for _, row in ipairs(self.rows) do
        if type(row.obj) == "table" then objs[#objs + 1] = row.obj end
    end
    return objs
end

function Menu:draw(s, panel)
    local L = Panel.layout
    title(s, self.name or "")
    local n = #L.rows
    if self.cursor > self.scroll + n then self.scroll = self.cursor - n end
    if self.cursor <= self.scroll then self.scroll = self.cursor - 1 end
    for i = 1, n do
        local row = self.rows[self.scroll + i]
        local y = L.rows[i]
        s:region("row" .. i, 0, y - 1, L.w, L.rowH)
        if row then
            local sel = (self.scroll + i) == self.cursor
            local value
            if sel and self.edit then
                -- no brackets: the + and - hints show the row is being edited
                value = row.kind == "choice" and tostring(self.edit.value)
                    or fmt(row.opts.format, self.edit.value)
            else
                value = rowValue(row)
            end
            if sel then s:text(0, y, ">") end
            -- a long label (e.g. a name from expose) is cut before the value
            local room = L.w - L.indent - (value ~= "" and textW(Panel.text(value)) + 4 or 0)
            s:text(L.indent, y, fit(row.label, room))
            if value ~= "" then s:textRight(y, value) end
        end
    end
end

local function clamp(v, opts)
    if opts.min and v < opts.min then v = opts.min end
    if opts.max and v > opts.max then v = opts.max end
    return v
end

function Menu:input(name, panel)
    local row = self.rows[self.cursor]
    if self.edit then
        local e = self.edit
        if name == "up" or name == "down" or name == "up:hold" or name == "down:hold" then
            local dir = name:sub(1, 2) == "up" and 1 or -1
            if row.kind == "number" then
                local step = row.opts.step or 1
                e.value = clamp((tonumber(e.value) or row.opts.min or 0) + dir * step, row.opts)
                e.value = math.floor(e.value / step + 0.5) * step
            else
                local opts = row.opts.options or {}
                local i = 1
                for k, o in ipairs(opts) do if o == e.value then i = k end end
                i = (i - 1 + dir) % #opts + 1
                e.value = opts[i]
            end
        elseif name == "ok" then
            self.edit = nil
            local before = read(row.get)
            local ok, err = pcall(row.set, e.value)
            if ok then hold(row, e.value, before, panel)
            else log():error("[Panel] set %s failed: %s", row.label, tostring(err)) end
        end
        return
    end
    if #self.rows == 0 then return end
    if name == "up" then
        self.cursor = (self.cursor - 2) % #self.rows + 1
    elseif name == "down" then
        self.cursor = self.cursor % #self.rows + 1
    elseif name == "ok" and row then
        if row.page then
            panel:open(row.page)
        elseif row.kind == "toggle" then
            flip(row.obj)
        elseif row.kind == "number" or row.kind == "choice" then
            local v = read(row.get)
            if row.kind == "number" then v = clamp(tonumber(v) or row.opts.min or 0, row.opts) end
            if row.kind == "choice" and v == nil then v = (row.opts.options or {})[1] end
            self.edit = { value = v }
        elseif row.kind == "action" then
            local run = function(p)
                if type(row.fn) == "table" then return p:open(row.fn) end
                local ok, err = pcall(row.fn, p)
                if not ok then log():error("[Panel] action %s failed: %s", row.label, tostring(err)) end
            end
            if row.opts.confirm then
                panel:open(Panel.confirm(row.opts.confirm, run, row.label))
            else
                run(panel)
            end
        end
    end
end

function Menu:back()
    if self.edit then self.edit = nil return true end
end

function Menu:close() self.edit = nil end

Panel.Menu = Menu

-- ---------- devices ----------

-- The handle expose() made for an object (type, options.name, options.area), or nil
local function exposed(obj)
    local E = ExposedObjects
    local path = E and E._objectToPath and E._objectToPath[obj]
    local h = path and E:get(path)
    -- the path can outlive an unexpose and be taken by another object
    if h and h.object == obj then return h end
    return nil
end

--- What device an object is: its expose() type, else the one of its Grenton type (TypeMap).
-- @return string|nil "switch", "light", "dimmer", "cover", "temperature", "sensor", ...
-- @api public
function Panel.kind(obj)
    if type(obj) == "table" and obj._type == "THERMOSTAT" then return "thermostat" end
    local h = exposed(obj)
    if h then return h.type end
    local ok, TypeMap = pcall(require, "TypeMap")
    if ok and type(obj) == "table" then return TypeMap.grentonToExpose[obj._type or ""] end
end

--- One press on a device: a cover opens (closes when open), anything else switches.
-- @api public
function Panel.press(obj)
    if Panel.kind(obj) == "cover" and obj.open and obj.close then
        local pos = obj.getPosition and obj:getPosition()
        if pos and pos > 0 then obj:close() else obj:open() end
        return
    end
    flip(obj)
end

local FORMATS = { temperature = "%.1f", humidity = "%.0f%%", illuminance = "%.0f", number = "%g" }
local BINARY = { sensor = true, binary_sensor = true, motion = true }

-- 1/0 as Panel.strings on/off, read when drawn (a module may set the texts after its menus)
local function onOff(v)
    if v == true or tonumber(v) == 1 then return Panel.strings.on end
    if v == false or tonumber(v) == 0 then return Panel.strings.off end
    return tostring(v)
end

--- A row for a device, by what it is (Panel.kind): a switch or light switches, a dimmer
-- edits its brightness, a cover opens its page, a sensor's value opens its chart (a
-- binary sensor's its history). No label: the name from expose().
-- @param label string|nil Row label
-- @param obj table|string Object or registry path; missing: no row
-- @param opts table|nil { kind, format, chart = false, history = false, readonly = true }
--   readonly: the state only, nothing switches (expose(..., { readonly = true }) sets it)
-- @api public
function Menu:add(label, obj, opts)
    if obj == nil then
        obj = label
        label = nil
    end
    obj = object(obj)
    if obj == nil then return self end
    opts = opts or {}
    local h = exposed(obj)
    label = label or (h and h.options and h.options.name) or obj._name or "?"
    local kind = opts.kind or Panel.kind(obj)
    local isObj = type(obj) == "table"
    local readonly = opts.readonly or (h and h.options and h.options.readonly)
    local dims = isObj and obj.getBrightness and obj.setBrightness
    if kind == "dimmer" or (kind == "light" and dims) then
        if not readonly then return self:dimmer(label, obj, opts) end
        return self:value(label, function() return obj:getBrightness() end, function(v)
            v = tonumber(v) or 0
            if v <= 0 then return Panel.strings.off end
            return string.format("%d%%", v)
        end)
    end
    if kind == "switch" or kind == "light" or kind == "lock" then
        if readonly then return self:value(label, obj, onOff) end
        return self:toggle(label, obj)
    end
    if kind == "cover" then
        if readonly then return self:value(label, function() return Panel.coverText(obj) end) end
        return self:cover(label, obj)
    end
    if kind == "thermostat" then
        return self:thermostat(label, obj, { readonly = readonly, format = opts.format })
    end
    if BINARY[kind] then
        local f = opts.format or onOff
        local page = isObj and opts.history ~= false and Panel.history(label, obj, { format = f }) or nil
        return self:value(label, obj, f, page)
    end
    -- a number gets its chart; text, scenes and the like only their value
    local numeric = kind == nil or FORMATS[kind] ~= nil
    local f = opts.format or FORMATS[kind] or (numeric and "%g") or "%s"
    local page = isObj and numeric and opts.chart ~= false and Panel.chart(label, obj, { format = f }) or nil
    return self:value(label, obj, f, page)
end

--- A gate (or a door, a window): open or closed, OK shows when it changed.
-- @param obj table|string A binary input or its path; missing: no row
-- @param opts table|nil { open = 1 } the value that means open (a Grenton input wired to
--   "closed" gives 0 when open)
-- @api public
function Menu:gate(label, obj, opts)
    obj = object(obj, label)
    if obj == nil then return self end
    local open = (opts and opts.open) or 1
    -- texts read when drawn: a module may set Panel.strings after its menus
    local function state(v, short)
        if v == true then v = 1 elseif v == false then v = 0 end
        local S = Panel.strings
        if tonumber(v) == open then return short and S.openShort or S.open end
        if tonumber(v) == 1 - open then return short and S.closedShort or S.closed end
        return tostring(v)
    end
    return self:value(label, obj, function(v) return state(v) end,
        Panel.history(label, obj, { format = function(v) return state(v, true) end }))
end

--- A Grenton thermostat: the temperature at its sensor; OK opens its page with the target.
-- + and - write the set point by hand, so only in manual mode (in holiday or auto mode the
-- thermostat follows its own value or the schedule, and the page only shows).
-- @param label string Row label and page title
-- @param obj table|string A RemoteThermostat or its path; missing: no row
-- @param opts table|nil { readonly = true (the page shows, nothing changes), step = 0.5,
--   min, max (what + and - can reach), format = "%.1f" }
-- @api public
function Menu:thermostat(label, obj, opts)
    obj = object(obj, label)
    if obj == nil then return self end
    opts = opts or {}
    local f = opts.format or "%.1f"
    return self:value(label, obj, f, Panel.thermostat{
        name = label,
        current = obj,
        get = function() return obj:getTarget() end,
        set = not opts.readonly and function(v) obj:setSetpoint(v) end or nil,
        canSet = function() local m = obj.getMode and obj:getMode() return m == nil or m == 0 end,
        step = opts.step, min = opts.min, max = opts.max, format = f,
    })
end

--- A cover: its position (^ or v while it moves); OK opens its page.
-- @api public
function Menu:cover(label, obj)
    obj = object(obj, label)
    if obj == nil then return self end
    return self:_add({ label = label, obj = obj, kind = "cover", page = Panel.cover(label, obj) })
end

--- A cover's position as text: "70%", "^ 70%" while it opens, "v" while it closes
function Panel.coverText(obj)
    local pos = obj.getPosition and obj:getPosition()
    -- a Grenton roller keeps its movement aside (getState); one without a position has
    -- the movement as its state
    local mv
    if obj._hasPosition or pos == nil then mv = obj.getState and obj:getState() end
    local arrow = (mv == 1 and "^ ") or (mv == 2 and "v ") or ""
    if pos == nil then return arrow ~= "" and arrow:sub(1, 1) or "--" end
    return arrow .. string.format("%d%%", tonumber(pos) or 0)
end

--- A cover on its own page: up opens, down closes, OK stops; position and movement follow.
-- @param name string Title
-- @param obj table|string Cover (open, close, stop, getPosition) or its path
-- @api public
function Panel.cover(name, obj)
    obj = object(obj)
    if obj == nil then return nil end
    local S = Panel.strings
    local page = { watch = { obj } }
    page.keys = function()
        return { up = { S.coverOpen }, down = { S.coverClose }, ok = { S.coverStop } }
    end
    page.input = function(n)
        if n == "up" then obj:open()
        elseif n == "down" then obj:close()
        elseif n == "ok" then obj:stop() end
    end
    page.draw = function(s)
        -- the long corner hints take the top and bottom rows: the name goes below them
        s:region("name"):textCenter(12, name)
        s:region("pos"):textCenter(26, Panel.coverText(obj), 2)
    end
    return page
end

--- A menu of the devices expose() put in an area, each with its row (Menu:add), named as
-- in Home Assistant and HomeKit; filled each time it opens, so devices a plugin exposes
-- later show up. Rows added by hand come after the devices.
-- Lights, dimmers and covers first, then temperature and humidity; switches (pumps,
-- gates, door openers) and sensors only when asked for in kinds. Like homekit and mqtt,
-- expose(obj, type, { panel = false }) keeps an object off the panels and panel = true
-- puts it on whatever its kind (e.g. the intercom).
-- @param area string|table Area name, or several (one room under two names)
-- @param opts table|nil { title, kinds = { "light", "dimmer", "cover", "temperature", "humidity" }, skip = { path } }
-- @api public
function Panel.area(area, opts)
    opts = opts or {}
    local areas = type(area) == "table" and area or { area }
    local m = Panel.menu(opts.title or areas[1])
    m._fill = function(self)
        local E = ExposedObjects
        if not (E and E._inArea) then return end
        local found = E:_inArea(areas, { kinds = opts.kinds or { "light", "dimmer", "cover", "temperature", "humidity" },
            skip = opts.skip, flag = "panel", key = Panel.map })
        for _, f in ipairs(found) do self:add(f.handle.options.name or f.path, f.handle.object) end
    end
    return m
end

--- A menu of the areas on a floor (expose(..., { area = "Salon", floor = "Parter" })),
-- each a Panel.area; areas with nothing for the panel are left out. Filled each time it opens.
-- @param floor string Floor name
-- @param opts table|nil { title, order = { area, ... } (first, in this order; the rest by
--   name), titles = { [area] = title }, kinds, skip }
-- @api public
function Panel.floor(floor, opts)
    opts = opts or {}
    local m = Panel.menu(opts.title or floor)
    m._fill = function(self)
        local E = ExposedObjects
        if not (E and E._areasOn) then return end
        local list = E:_areasOn(floor, opts.order, Panel.map)
        local titles = opts.titles or {}
        for _, a in ipairs(list) do
            self:submenu(titles[a] or a, Panel.area(a, { title = titles[a], kinds = opts.kinds, skip = opts.skip }))
        end
    end
    return m
end

-- ---------- confirm ----------

--- Asks before running fn(panel): OK runs it and closes, back closes.
-- @api public
function Panel.confirm(text, fn, heading)
    local page
    page = {
        keys = { ok = { ">" } },
        draw = function(s)
            local L = Panel.layout
            title(s, (heading or "") .. "?")
            s:region("msg")
            for i, line in ipairs(wrap(Panel.map(text), Panel.MAX_TEXT)) do
                if L.rows[i] then s:text(0, L.rows[i], line) end
            end
        end,
        input = function(name, p)
            if name == "ok" then
                p:close(page)
                fn(p)
            end
        end,
    }
    return page
end

-- ---------- toggle, value, thermostat ----------

--- One on/off object on its own page; OK switches.
-- @api public
function Panel.toggle(obj, name)
    return {
        watch = { obj },
        keys = { ok = { ">" } },
        draw = function(s)
            local L = Panel.layout
            title(s, name or obj._name or "")
            local v = read(obj)
            s:region("value", 0, L.rows[2] - 1, L.w, 16):textCenter(L.rows[2], (v == 1 or v == true) and "ON" or "OFF", 2)
        end,
        input = function(n)
            if n ~= "ok" then return end
            if obj.toggle then obj:toggle()
            elseif obj.setValue then obj:setValue((read(obj) == 1) and 0 or 1) end
        end,
    }
end

--- One value on its own page.
-- @api public
function Panel.value(name, src, format)
    return {
        watch = { type(src) == "table" and src or nil },
        every = type(src) == "function" and 30 or nil,
        draw = function(s)
            local L = Panel.layout
            title(s, name)
            s:region("value", 0, L.rows[2] - 1, L.w, 16):textCenter(L.rows[2], fmt(format, read(src)), 2)
        end,
    }
end

-- ---------- chart ----------

local HOUR_MS = 3600000

-- Time-weighted mean per hour from changes (newest first): the last `hours` full hours
-- ending with the current one; nil where nothing is known
local function hourly(entries, hours, now)
    local cur = now - now % HOUR_MS
    local first = cur - (hours - 1) * HOUR_MS
    local sum, len = {}, {}
    for i = #entries, 1, -1 do
        local e, nxt = entries[i], entries[i - 1]
        local v, from = tonumber(e.value), tonumber(e.atMs)
        local to = nxt and tonumber(nxt.atMs) or now
        if v and from and to > first then
            from = math.max(from, first)
            while from < to do
                local b = math.floor((from - first) / HOUR_MS) + 1
                local edge = math.min(to, first + b * HOUR_MS)
                sum[b] = (sum[b] or 0) + v * (edge - from)
                len[b] = (len[b] or 0) + (edge - from)
                from = edge
            end
        end
    end
    local out = {}
    for b = 1, hours do
        if len[b] and len[b] > 0 then out[b] = sum[b] / len[b] end
    end
    return out
end
Panel._hourly = hourly

--- Hourly bars of a value's history: up and down pick an hour, its time and value show
-- on the right. Each bar is the time-weighted mean of that hour; the last is the
-- current one.
-- @param name string Title
-- @param src table|function An object with history (obj:history(n)) or a function
--   returning changes newest first ({ value, atMs })
-- @param opts table { hours = 12, format = "%.1f" }
-- @return table|nil The page; nil when src has no history (so a menu row gets no OK)
-- @api public
function Panel.chart(name, src, opts)
    if not (type(src) == "function" or (type(src) == "table" and src.history)) then return nil end
    opts = opts or {}
    local hours = opts.hours or 12
    local page = { every = 60, sel = hours }
    page.watch = { type(src) == "table" and src or nil }
    page.keys = { up = { "" }, down = { "" } }
    page.open = function() page.sel = hours end
    page.input = function(n)
        if n == "up" then page.sel = math.max(1, page.sel - 1) -- an hour back
        elseif n == "down" then page.sel = math.min(hours, page.sel + 1) end
    end
    page.draw = function(s)
        local L = Panel.layout
        title(s, name)
        local entries
        if type(src) == "function" then entries = src()
        elseif type(src) == "table" and src.history then entries = src:history(2000) end
        local bars = hourly(entries or {}, hours, Panel.timer.now())
        local lo, hi
        for b = 1, hours do
            local v = bars[b]
            if v then lo, hi = math.min(lo or v, v), math.max(hi or v, v) end
        end
        -- bars from the base up; the lowest hour still gets a stub
        local base, top, pitch = 52, 13, 5
        if lo then
            if hi - lo < 1 then lo, hi = (lo + hi) / 2 - 0.5, (lo + hi) / 2 + 0.5 end
            for b = 1, hours do
                local v = bars[b]
                if v then
                    local h = 2 + math.floor((v - lo) / (hi - lo) * (base - top - 2) + 0.5)
                    local x = 3 + (b - 1) * pitch -- the ^ of the first bar starts at x = 1
                    local x0, x1 = x, x + 1
                    if b == page.sel then x0, x1 = x - 1, x + 2 end
                    for xx = x0, x1 do s:line(xx, base - h, xx, base) end
                    if b == page.sel then -- a ^ under the chosen bar
                        s:line(x - 2, base + 5, x, base + 3):line(x + 1, base + 3, x + 3, base + 5)
                    end
                end
            end
        end
        -- the chosen hour and its value
        local h0 = tonumber(Panel.timer.clock():match("^(%d+)")) or 0
        local hour = (h0 - (hours - page.sel)) % 24
        local right = L.w
        local value = fmt(opts.format or "%.1f", bars[page.sel])
        s:textRight(24, value, 2, right - 8)
        s:textRight(52, string.format("%02d:00", hour), 1, right - 3)
    end
    return page
end

--- Current and set temperature; +/- change the set point, written 1 s after the last press.
-- Without set (or while canSet() is false) the page only shows them.
-- @param o table { name, current = obj|fn, get = fn, set = fn|nil, canSet = fn|nil, step = 0.5, min, max, format = "%.1f" }
-- @api public
function Panel.thermostat(o)
    local page = { pending = nil }
    page.watch = function()
        local w = {}
        if type(o.current) == "table" then w[#w + 1] = o.current end
        if type(o.watch) == "table" then w[#w + 1] = o.watch end
        return w
    end
    local function editable() return o.set ~= nil and (o.canSet == nil or o.canSet()) end
    page.keys = function() return editable() and { up = { "+" }, down = { "-" } } or {} end
    page.draw = function(s)
        local L = Panel.layout
        title(s, o.name or Panel.strings.thermostat)
        s:region("now", 0, L.rows[1] - 1, L.w, L.rowH)
        s:text(0, L.rows[1], Panel.strings.now):textRight(L.rows[1], fmt(o.format or "%.1f", read(o.current)))
        local set = page.pending or shown(page, read(o.get))
        s:region("set", 0, L.rows[2] - 1, L.w, L.rowH)
        s:text(0, L.rows[2], Panel.strings.set):textRight(L.rows[2], fmt(o.format or "%.1f", set))
        if editable() and (o.min or o.max) then
            s:region("range", 0, L.rows[3] - 1, L.w, L.rowH)
            s:textCenter(L.rows[3], fmt("%g", o.min or 0) .. "-" .. fmt("%g", o.max or 0))
        end
    end
    page.input = function(n, panel)
        local base = n:match("^(%a+)")
        if not editable() or (base ~= "up" and base ~= "down") then return end
        local step = o.step or 0.5
        local v = tonumber(page.pending or read(o.get)) or o.min or 0
        v = clamp(v + (base == "up" and step or -step), o)
        v = math.floor(v / step + 0.5) * step
        page.pending = v
        panel:debounce(page, 1, function()
            page.pending = nil
            local before = read(o.get)
            o.set(v)
            hold(page, v, before, panel)
            panel:redraw()
        end)
    end
    return page
end

-- ---------- history ----------

--- The last changes of an object, newest first, as a list: when and what.
-- @param name string Title
-- @param src table|function Object with :history(n), or a function returning { {value, atMs}, ... }
-- @param opts table|nil { count = 10, format = "%s" | fn(value) | { [value] = text } }
-- @return table|nil The page; nil when src has no history
-- @api public
function Panel.history(name, src, opts)
    if not (type(src) == "function" or (type(src) == "table" and src.history)) then return nil end
    opts = opts or {}
    local count = opts.count or 10
    local page = { cursor = 1, scroll = 0 }
    page.watch = { type(src) == "table" and src or nil }
    page.keys = { up = { "" }, down = { "" } }
    page.open = function() page.cursor, page.scroll = 1, 0 end
    local function entries()
        local e
        if type(src) == "function" then e = src()
        elseif type(src) == "table" and src.history then e = src:history(count) end
        e = e or {}
        while #e > count do table.remove(e) end
        return e
    end
    page.input = function(n)
        local total = #entries()
        if n == "up" then page.cursor = math.max(1, page.cursor - 1)
        elseif n == "down" then page.cursor = math.max(1, math.min(total, page.cursor + 1)) end
    end
    page.draw = function(s)
        local L = Panel.layout
        title(s, name)
        local list = entries()
        if #list == 0 then
            s:textCenter(L.rows[2], Panel.strings.noChanges)
            return
        end
        page.cursor = math.min(page.cursor, #list)
        local n = #L.rows
        if page.cursor > page.scroll + n then page.scroll = page.cursor - n end
        if page.cursor <= page.scroll then page.scroll = page.cursor - 1 end
        local at = Panel.timer.at or function(ms) return os.date("%H:%M %d.%m", math.floor(ms / 1000)) end
        for i = 1, n do
            local e = list[page.scroll + i]
            local y = L.rows[i]
            if e then
                if page.scroll + i == page.cursor then s:text(0, y, ">") end
                s:text(L.indent, y, at(e.atMs))
                s:textRight(y, fmt(opts.format or "%s", e.value))
            end
        end
    end
    return page
end

-- ---------- corners ----------

--- A page where each corner is one button, e.g. home with the room's lights; no back.
-- @param opts table { tl = { "Swiatlo", obj|path|fn }, tr, bl, br, clock = true,
--   draw = fn(screen, panel), every, watch }
-- A corner with a device does what its row would (a light or dimmer switches, a cover
-- opens or closes), with a function calls fn(panel); a corner with only a label shows it
-- and leaves its LED off; a path not in the registry keeps the label only. clock draws
-- the time in the middle (refreshed every minute), draw anything else there.
-- @api public
function Panel.corners(opts)
    local every = opts.every or (opts.clock and 60) or nil
    local page = { corners = {}, every = every, align = every ~= nil }
    local objs = {}
    for _, c in ipairs(CORNERS) do
        local spec = opts[c]
        if spec then
            local action = spec[2]
            if type(action) == "string" then action = object(action, spec[1]) end
            page.corners[c] = { spec[1], action }
            if type(action) == "table" then objs[#objs + 1] = action end
        end
    end
    page.watch = function(panel)
        local w = {}
        for _, o in ipairs(objs) do w[#w + 1] = o end
        local extra = opts.watch
        if type(extra) == "function" then extra = extra(panel) end
        for _, o in ipairs(extra or {}) do w[#w + 1] = o end
        return w
    end
    page.draw = function(s, panel)
        if opts.clock then
            s:textCenter(24, Panel.timer.clock():match("^(%d+:%d+)") or "", 2)
        end
        if opts.draw then opts.draw(s, panel) end
    end
    return page
end

-- ---------- idle ----------

--- The idle screen: time, date and a few values; any button opens home.
-- @param rows table { { "Salon", obj|fn, "%.1f" }, ... }
-- @api public
function Panel.idle(rows, page)
    -- panel:idle(page) sets the idle screen; Panel.idle(rows) makes one
    if getmetatable(rows) == Panel then return setIdle(rows, page) end
    rows = rows or {}
    local page = { every = 60, align = true }
    page.watch = function()
        local w = {}
        for _, r in ipairs(rows) do if type(r[2]) == "table" then w[#w + 1] = r[2] end end
        return w
    end
    page.draw = function(s)
        local L = Panel.layout
        s:region("time"):textCenter(L.top, Panel.timer.clock())
        for i, r in ipairs(rows) do
            local y = L.rows[i]
            if not y then break end
            s:region("v" .. i, 0, y - 1, L.w, L.rowH)
            s:text(0, y, r[1]):textRight(y, fmt(r[3] or "%.1f", read(r[2])))
        end
    end
    return page
end

-- ---------- cleaning ----------

--- Locks the panel for cleaning: a countdown, inputs ignored, buzzer and LEDs off.
-- @api public
function Panel.cleaning(seconds)
    seconds = seconds or 30
    local page = { modal = true }
    page.open = function(panel)
        page.left = seconds
        panel.obj:set(RemotePanel.FEATURE_BUZZER, 0)
        local function tick()
            if panel:top() ~= page then return end
            page.left = page.left - 1
            if page.left <= 0 then
                panel.obj:set(RemotePanel.FEATURE_BUZZER, 1)
                panel:close(page)
                return
            end
            panel:redraw()
            page.timer = Panel.timer.after(1000, tick)
        end
        page.timer = Panel.timer.after(1000, tick)
    end
    page.close = function(panel)
        Panel.timer.cancel(page.timer)
        if page.left and page.left > 0 then panel.obj:set(RemotePanel.FEATURE_BUZZER, 1) end
    end
    page.draw = function(s)
        local L = Panel.layout
        s:region("title", 0, L.top, L.w, 10):textCenter(L.top, Panel.strings.cleaning)
        s:region("count", 0, L.rows[1] + 2, L.w, 20):textCenter(L.rows[1] + 4, tostring(page.left or seconds), 3)
    end
    page.input = function() end
    return page
end

return Panel
