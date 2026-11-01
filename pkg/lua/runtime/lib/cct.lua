--- Tunable white from two dimmers: a warm and a cold white channel as one colour temperature.
--
--   local cct = require("lib/cct")
--   local biuro = cct.mix(_:get("CLU1.DIM1"), _:get("CLU1.DIM2"), { warm = 2700, cold = 6500 })
--   dash:section("Biuro"):colorTemp("biuro", "Biuro", biuro, { min = 2700, max = 6500 })
--
-- A channel is anything with getValue/setValue on 0..1 (RemoteLight, RemoteLedChannel,
-- VirtualDimmer). Brightness is the sum of both channels (at most 100%), the colour temperature
-- their ratio, linear in kelvin between warm and cold. Changing one keeps the other. A colour
-- temperature set while both are off is kept for the next switchOn, and so are the level and
-- colour temperature a light had before it was switched off (also by a scene or a wall switch, when
-- a poll saw them). While the channels show what the mix sent (within one 0..255 step) it keeps
-- its own kelvin: a dim light on stepped channels does not drift. Commands go out at least 50 ms
-- apart, only the latest values wait: the CLU drops bursts (pkg/lua/AGENTS.md).
-- @module lib.cct

local M = {}

M.SPACING_MS = 50
-- one step of a 0..255 channel: closer values are the same
local STEP = 1 / 255

local function clamp(v, lo, hi) return math.max(lo, math.min(hi, v)) end

local function channel(v, what)
    if type(v) ~= "table" or type(v.getValue) ~= "function" or type(v.setValue) ~= "function" then
        error("cct.mix: the " .. what .. " channel must be a dimmer (getValue/setValue on 0..1), got " .. type(v), 3)
    end
end

--- A warm and a cold dimmer as the functions of section:colorTemp.
-- @api public
-- @param warm table The warm white channel (getValue/setValue 0..1)
-- @param cold table The cold white channel (getValue/setValue 0..1)
-- @param opts table Optional: {warm=2700, cold=6500} the kelvin of each channel alone
-- @return table {getKelvin, setKelvin, getBrightness, setBrightness (0-100), isOn, switchOn, switchOff}
function M.mix(warm, cold, opts)
    channel(warm, "warm")
    channel(cold, "cold")
    if opts ~= nil and type(opts) ~= "table" then error("cct.mix: opts must be a table", 2) end
    opts = opts or {}
    for k in pairs(opts) do
        if k ~= "warm" and k ~= "cold" then error('cct.mix: unknown option "' .. tostring(k) .. '" (warm, cold)', 2) end
    end
    local kw, kc = opts.warm or 2700, opts.cold or 6500
    if type(kw) ~= "number" or type(kc) ~= "number" or kw - kw ~= 0 or kc - kc ~= 0 then
        error("cct.mix: warm and cold must be numbers (kelvin)", 2)
    end
    if kw <= 0 or kw >= kc then error("cct.mix: warm must be above 0 and below cold", 2) end

    -- kelvin and level of the light when last lit; sent is what the mix last asked for,
    -- target what is still being sent
    local s = { kelvin = nil, level = 1, sent = nil, target = nil, timer = nil }
    local mid = (kw + kc) / 2

    local function close(a, b) return a ~= nil and math.abs(a - b) <= STEP end

    -- the level (nil before a report) and kelvin now; an outside change becomes the kept state
    local function state()
        local w, c
        if s.target then
            w, c = s.target.w, s.target.c
        else
            w, c = tonumber(warm:getValue()), tonumber(cold:getValue())
        end
        if w == nil and c == nil then return nil, s.kelvin end
        w = w or 0
        c = c or 0
        if s.sent and close(w, s.sent.w) and close(c, s.sent.c) then
            return math.min(1, s.sent.w + s.sent.c), s.kelvin
        end
        local sum = w + c
        s.sent = { w = w, c = c }
        if sum <= 0 then return 0, s.kelvin end
        s.kelvin = kw + c / sum * (kc - kw)
        s.level = math.min(1, sum)
        return s.level, s.kelvin
    end

    -- one command per call, the next one SPACING_MS later; a channel at most once per target
    local function pump()
        if s.timer then return end
        local tg = s.target
        if not tg then return end
        local ch, v
        if not tg.doneW and not close(tonumber(warm:getValue()), tg.w) then
            tg.doneW = true
            ch, v = warm, tg.w
        elseif not tg.doneC and not close(tonumber(cold:getValue()), tg.c) then
            tg.doneC = true
            ch, v = cold, tg.c
        else
            s.target = nil
            return
        end
        ch:setValue(v)
        local id = TIMERS and TIMERS:setTimeout(M.SPACING_MS, function()
            s.timer = nil
            pump()
        end)
        if id and id >= 0 then
            s.timer = id
        else
            pump()
        end
    end

    local function apply(level, k)
        if k then s.kelvin = clamp(k, kw, kc) end
        if level > 0 then s.level = level end
        local t = (clamp(s.kelvin or mid, kw, kc) - kw) / (kc - kw)
        s.target = { w = level * (1 - t), c = level * t }
        s.sent = { w = s.target.w, c = s.target.c }
        pump()
    end

    local fns = {}

    function fns.getKelvin()
        local _, k = state()
        return k and math.floor(k + 0.5)
    end

    function fns.setKelvin(k)
        k = tonumber(k)
        if not k then error("cct: kelvin must be a number", 2) end
        local l = state()
        if l and l > 0 then
            apply(l, k)
        else
            s.kelvin = clamp(k, kw, kc)
        end
    end

    -- a lit light shows at least 1%
    function fns.getBrightness()
        local l = state()
        if l == nil then return nil end
        local p = math.floor(l * 100 + 0.5)
        if l > 0 and p == 0 then p = 1 end
        return p
    end

    -- 0 switches off and keeps the level and kelvin, as switchOff
    function fns.setBrightness(p)
        p = tonumber(p)
        if not p then error("cct: brightness must be a number", 2) end
        state()
        apply(clamp(p / 100, 0, 1))
    end

    function fns.isOn()
        local l = state()
        if l == nil then return nil end
        return l > 0
    end

    function fns.switchOn()
        state()
        apply(s.level)
    end

    function fns.switchOff()
        state()
        apply(0)
    end

    return fns
end

return M
