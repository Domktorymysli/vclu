--- Dashboard - Lua-defined dashboards for tablet/wall-mounted control panels
-- Builder pattern API for defining dashboards entirely in Lua.
-- Each dashboard is a public page at /d/:id with per-dashboard auth.
-- @module Dashboard

-- Global registry (cleared on module reload, rebuilt by build() calls)
_dashboards = _dashboards or {}
_dashboardOrder = _dashboardOrder or {}

-- ==========================================================================
-- SectionHandle - proxy for adding widgets to a specific section
-- ==========================================================================

local SectionHandle = {}
SectionHandle.__index = SectionHandle

local CALENDAR_WIDGET_KEYS = { view = true, tags = true, schedules = true, holidays = true, namedays = true, sun = true, span = true }

-- "a", "b": the sorted keys of a set, or "none".
local function quoted(set)
    local out = {}
    for k in pairs(set) do out[#out + 1] = '"' .. tostring(k) .. '"' end
    table.sort(out)
    return #out > 0 and table.concat(out, ', ') or 'none'
end

-- :width() of a dashboard; level points the error at the module's line
local function setWidth(b, w, level)
    if w ~= 'full' and w ~= 'normal' then
        error(":width(): expected 'full' or 'normal', got " .. (type(w) == 'string' and "'" .. w .. "'" or type(w)), level)
    end
    b._width = w
end

-- A calendar widget keeps the calendar's id, so a dashboard may be built before the calendar.
local function calendarWidget(id, label, cal, opts)
    local calId = type(cal) == 'table' and cal.id or cal
    if type(calId) ~= 'string' then
        error('calendar widget "' .. tostring(id) .. '": expected a calendar or its id', 3)
    end
    opts = opts or {}
    local where = 'calendar widget "' .. tostring(id) .. '"'
    for k in pairs(opts) do
        if not CALENDAR_WIDGET_KEYS[k] then
            error(where .. ': unknown option "' .. tostring(k) .. '" (view, tags, schedules, holidays, namedays, sun, span)', 3)
        end
    end
    if opts.view ~= nil and opts.view ~= 'month' and opts.view ~= 'day' then
        error(where .. ': view must be "month" or "day"', 3)
    end
    for _, k in ipairs({ 'holidays', 'namedays', 'sun' }) do
        if opts[k] ~= nil and type(opts[k]) ~= 'boolean' then error(where .. ': ' .. k .. ' must be true or false', 3) end
    end
    if opts.schedules ~= nil and opts.schedules ~= true and type(opts.schedules) ~= 'table' then
        error(where .. ': schedules expects a list of schedule ids, or true for all', 3)
    end
    -- a calendar built already (the usual order: its module, then the dashboard) is checked now
    local cal = Calendar and Calendar.get(calId)
    if cal then
        local info = cal:info()
        local known = {}
        for _, sc in ipairs(info.schedules) do known[sc.id] = true end
        if type(opts.schedules) == 'table' then
            for _, sid in ipairs(opts.schedules) do
                if not known[sid] then
                    error(where .. ': calendar "' .. calId .. '" has no schedule "' .. tostring(sid) .. '" (schedules: ' .. quoted(known) .. ')', 3)
                end
            end
        end
        local tags = {}
        for _, ev in ipairs(info.events) do for _, t in ipairs(ev.tags or {}) do tags[t] = true end end
        for _, t in ipairs(type(opts.tags) == 'table' and opts.tags or { opts.tags }) do
            if not tags[t] then
                error(where .. ': no event of calendar "' .. calId .. '" has the tag "' .. tostring(t) .. '" (tags: ' .. quoted(tags) .. ')', 3)
            end
        end
    end
    return { id = id, type = 'calendar', label = label, calendar = calId, opts = opts }
end

-- A colour widget given a light object (RemoteRGBLight) instead of fns. Reads come from what
-- the light keeps (a get() would ask the CLU on every poll); brightness and white are 0..100.
local function lightColorFns(light)
    local function pct(v) v = tonumber(v); return v and math.floor(v * 100 + 0.5) end
    local fns = {
        getColor = function()
            local r, g, b = light:getColor()
            if r then return { r = r, g = g, b = b } end
        end,
        setColor = function(r, g, b) light:setColor(r, g, b) end,
        get = function() return pct(light:getValue()) end,
        set = function(p) light:setValue(p / 100) end,
        switchOn = function() light:switchOn() end,
        switchOff = function() light:switchOff() end,
    }
    if light._hasWhite then
        fns.getWhite = function() return light._white and pct(light._white / 255) end
        fns.setWhite = function(p) light:setWhite(p * 255 / 100) end
    end
    return fns
end

local function colorFns(fns)
    if type(fns) == 'table' and fns._objId and type(fns.setColor) == 'function' then
        return lightColorFns(fns)
    end
    return fns
end

-- ==========================================================================
-- Cover groups: one tile for several covers
-- ==========================================================================

local COVERS_KEYS = { icon = true, disabled = true, span = true, size = true, color = true }

-- Spacing between commands of one series: the CLU drops bursts (pkg/lua/AGENTS.md)
local COVERS_SPACING_MS = 50

-- A cover object: a table with the three moves (RemoteCover, VirtualCover)
local function isCover(v)
    return type(v) == 'table' and getmetatable(v) ~= RemoteGroup
        and type(v.open) == 'function' and type(v.close) == 'function' and type(v.stop) == 'function'
end

local function coversWidget(id, label, list, opts)
    local where = 'section:covers("' .. tostring(id) .. '")'
    if type(id) ~= 'string' or id == '' then error('section:covers: id must be a non-empty string', 3) end
    if type(label) ~= 'string' then error(where .. ': label must be a string', 3) end
    if opts ~= nil and type(opts) ~= 'table' then error(where .. ': opts must be a table', 3) end
    opts = opts or {}
    for k in pairs(opts) do
        if not COVERS_KEYS[k] then error(where .. ': unknown option "' .. tostring(k) .. '" (icon, disabled, span, size, color)', 3) end
    end
    if opts.icon ~= nil and (type(opts.icon) ~= 'string' or not opts.icon:match('^[%w%-]+$')) then
        error(where .. ': opts.icon must be a Bootstrap icon name (letters, digits and -)', 3)
    end
    if opts.disabled ~= nil and type(opts.disabled) ~= 'function' then error(where .. ': opts.disabled must be a function', 3) end
    if type(list) == 'table' and getmetatable(list) ~= RemoteGroup then
        if isCover(list) then error(where .. ': list must be a list of covers, put the cover in { }', 3) end
        for i, item in ipairs(list) do
            if type(item) ~= 'string' and not isCover(item) then
                error(where .. ': item ' .. i .. ' must be a cover (open, close, stop) or its path, got ' .. type(item), 3)
            end
        end
    elseif type(list) ~= 'function' and type(list) ~= 'table' then
        error(where .. ': list must be a list of covers or paths, or a function returning one, got ' .. type(list), 3)
    end
    return { id = id, type = 'covers', label = label, icon = opts.icon or 'window', list = list, opts = opts }
end

-- Problems with a cover list already logged, by "dashboard/widget/what"
local _coversLogged = {}
local function coversWarnOnce(key, fmt, ...)
    if _coversLogged[key] then return end
    _coversLogged[key] = true
    Logger:getShared():warn(fmt, ...)
end

-- The covers of a group as they are now: a function list is called, paths looked up; missing ones skipped
local function coversResolve(dashId, w)
    local key = tostring(dashId) .. '/' .. tostring(w.id)
    local src = w.list
    if type(src) == 'function' then
        local ok, res = pcall(src)
        if not ok then
            coversWarnOnce(key .. '/fn', "[DASHBOARD] Cover list of '%s' failed: %s", key, tostring(res))
            return {}
        end
        src = res
    end
    if type(src) == 'table' and getmetatable(src) == RemoteGroup then src = rawget(src, '_objects') end
    local out, seen = {}, {}
    if type(src) ~= 'table' then
        if src ~= nil then coversWarnOnce(key .. '/type', "[DASHBOARD] Cover list of '%s' is a %s, not a list", key, type(src)) end
        return out
    end
    for i, item in ipairs(src) do
        local c = item
        if type(item) == 'string' then
            c = _registry:get(item)
            if c == nil then coversWarnOnce(key .. '/' .. item, "[DASHBOARD] '%s': no cover at '%s', skipped", key, item) end
        end
        if c ~= nil and not isCover(c) then
            coversWarnOnce(key .. '/#' .. i, "[DASHBOARD] '%s': item %d is not a cover, skipped", key, i)
            c = nil
        end
        if c ~= nil and not seen[c] then
            seen[c] = true
            out[#out + 1] = c
        end
    end
    return out
end

-- opts.decimals of a status: whole places 0..6; the page rounds a number to them
local function checkDecimals(what, id, opts)
    local d = type(opts) == 'table' and opts.decimals or nil
    if d ~= nil and (type(d) ~= 'number' or d % 1 ~= 0 or d < 0 or d > 6) then
        error(what .. '("' .. tostring(id) .. '"): opts.decimals must be a whole number 0-6', 3)
    end
end

-- ==========================================================================
-- Thermostat: current temperature, target, +/- for the target
-- ==========================================================================

local THERMOSTAT_FNS = { getCurrent = true, getTarget = true, setTarget = true, getState = true, getMode = true }
local THERMOSTAT_KEYS = { min = true, max = true, step = true, unit = true, readonly = true,
    disabled = true, span = true, size = true, color = true }
-- the range of an exposed climate (ExposedObjects capabilities)
local THERMOSTAT_MIN, THERMOSTAT_MAX = 5, 35

-- A thermostat object, as Home Assistant's climate takes one (RemoteThermostat)
local function isThermostat(v)
    return type(v) == 'table' and type(v.getValue) == 'function'
        and type(v.getTarget) == 'function' and type(v.setSetpoint) == 'function'
end

-- The object's methods as the widget's functions; as lib/panel's menu:thermostat, the set
-- point is written by hand, so only in manual mode (getMode 0)
local function thermostatFns(o, readonly)
    local function m(name)
        if type(o[name]) == 'function' then return function() return o[name](o) end end
    end
    return { getCurrent = m('getValue'), getTarget = m('getTarget'), getState = m('getState'), getMode = m('getMode'),
        setTarget = not readonly and function(v) o:setSetpoint(v) end or nil }
end

local function thermostatWidget(id, label, icon, fns, opts)
    local where = 'section:thermostat("' .. tostring(id) .. '")'
    if type(id) ~= 'string' or id == '' then error('section:thermostat: id must be a non-empty string', 3) end
    if type(label) ~= 'string' then error(where .. ': label must be a string', 3) end
    if icon ~= nil and (type(icon) ~= 'string' or not icon:match('^[%w%-]+$')) then
        error(where .. ': icon must be a Bootstrap icon name (letters, digits and -)', 3)
    end
    if opts ~= nil and type(opts) ~= 'table' then error(where .. ': opts must be a table', 3) end
    opts = opts or {}
    for k in pairs(opts) do
        if not THERMOSTAT_KEYS[k] then
            error(where .. ': unknown option "' .. tostring(k) .. '" (min, max, step, unit, readonly, disabled, span, size, color)', 3)
        end
    end
    for _, k in ipairs({ 'min', 'max', 'step' }) do
        local v = opts[k]
        if v ~= nil and (type(v) ~= 'number' or v - v ~= 0) then error(where .. ': opts.' .. k .. ' must be a number', 3) end
    end
    local min, max = opts.min or THERMOSTAT_MIN, opts.max or THERMOSTAT_MAX
    if min >= max then error(where .. ': opts.min must be below opts.max', 3) end
    if opts.step ~= nil and opts.step <= 0 then error(where .. ': opts.step must be above 0', 3) end
    if opts.unit ~= nil and (type(opts.unit) ~= 'string' or #opts.unit > 8) then
        error(where .. ': opts.unit must be a short string such as "°C"', 3)
    end
    if opts.readonly ~= nil and type(opts.readonly) ~= 'boolean' and type(opts.readonly) ~= 'function' then
        error(where .. ': opts.readonly must be true, false or a function', 3)
    end
    if opts.disabled ~= nil and type(opts.disabled) ~= 'function' then error(where .. ': opts.disabled must be a function', 3) end
    local f
    if isThermostat(fns) then
        f = thermostatFns(fns, opts.readonly == true)
    elseif type(fns) == 'table' and getmetatable(fns) ~= nil then
        -- an object (it has methods), but not a thermostat: say which and what it lacks
        local missing = {}
        for _, m in ipairs({ 'getValue', 'getTarget', 'setSetpoint' }) do
            if type(fns[m]) ~= 'function' then missing[#missing + 1] = m end
        end
        local what = tostring(fns._objId or fns._name or 'the object')
        if fns._type then what = what .. ' (' .. tostring(fns._type) .. ')' end
        error(where .. ': ' .. what .. ' is not a thermostat: it has no ' .. table.concat(missing, ', ')
            .. '; pass a thermostat or a table {getCurrent, getTarget, setTarget?, ...}', 3)
    elseif type(fns) == 'table' then
        f = {}
        for k, v in pairs(fns) do
            if not THERMOSTAT_FNS[k] then
                error(where .. ': unknown key "' .. tostring(k) .. '" in fns (getCurrent, getTarget, setTarget, getState, getMode)', 3)
            end
            if type(v) ~= 'function' then error(where .. ': fns.' .. k .. ' must be a function', 3) end
            f[k] = v
        end
        if not f.getCurrent or not f.getTarget then error(where .. ': fns needs getCurrent and getTarget', 3) end
        if opts.readonly == true then f.setTarget = nil end
    else
        if fns == nil then
            error(where .. ': no thermostat (nil): check the path in _:get(...), the object may not be imported', 3)
        end
        error(where .. ': fns must be {getCurrent, getTarget, setTarget?, getState?, getMode?} or a thermostat (RemoteThermostat), got ' .. type(fns), 3)
    end
    -- a readonly function locks and unlocks the buttons while the dashboard runs
    return { id = id, type = 'thermostat', label = label, icon = icon or 'thermometer-half', fns = f,
        readonly = type(opts.readonly) == 'function' and opts.readonly or nil,
        min = min, max = max, step = opts.step or 0.5, unit = opts.unit or '°C', opts = opts }
end

-- A finite number from fn, nil before the first report; err when fn fails
local function thermostatRead(fn)
    if type(fn) ~= 'function' then return nil end
    local ok, v = pcall(fn)
    if not ok then return nil, tostring(v) end
    if type(v) == 'boolean' then return v and 1 or 0 end
    v = tonumber(v)
    if v ~= nil and v - v == 0 then return v end
end

-- The target may be written: there is setTarget, opts.readonly (a function) says no lock and the mode (if
-- known) is manual; a failing getMode or readonly says no
local function thermostatCanSet(fns, readonly)
    if type(fns.setTarget) ~= 'function' then return false end
    if readonly then
        local ok, ro = pcall(readonly)
        if not ok or ro then return false end
    end
    if type(fns.getMode) ~= 'function' then return true end
    local mode, err = thermostatRead(fns.getMode)
    return err == nil and (mode == nil or mode == 0)
end

-- What the tile shows: {current, target, state, mode, set}, error when getCurrent or getTarget fails
local function thermostatState(w)
    local f = w.fns
    local cur, err1 = thermostatRead(f.getCurrent)
    local target, err2 = thermostatRead(f.getTarget)
    local e = { current = cur and math.floor(cur * 10 + 0.5) / 10, target = target,
        state = (thermostatRead(f.getState)), mode = (thermostatRead(f.getMode)), set = thermostatCanSet(f, w.readonly) }
    if w.readonly then
        local ok, ro = pcall(w.readonly)
        -- false, not nil: the page merges values, so a missing field would keep the last true
        e.locked = false
        if not ok or ro then e.locked = true end
    end
    e.error = err1 or err2
    return e
end

-- ==========================================================================
-- Rooms from the house model (section:area, dash:floor)
-- ==========================================================================

-- Kinds that have a widget; a thermostat (climate) only through kinds, as switch and lock
local AREA_KINDS = { 'light', 'switch', 'dimmer', 'cover', 'temperature', 'humidity', 'lock', 'climate' }
local AREA_DEFAULT_KINDS = { 'light', 'dimmer', 'cover', 'temperature', 'humidity' }
local AREA_KEYS = { 'kinds', 'skip' }
local FLOOR_KEYS = { 'kinds', 'order', 'rooms', 'skip', 'titles' }
local AREA_ID = 'area:'

local function isList(t, kind)
    if type(t) ~= 'table' then return false end
    for k, v in pairs(t) do
        if type(k) ~= 'number' or type(v) ~= kind then return false end
    end
    return true
end

local function has(list, v)
    for _, x in ipairs(list) do if x == v then return true end end
    return false
end

-- Checked options of section:area / dash:floor; errors name the module's line (level)
local function areaSpec(where, opts, keys, level)
    opts = opts or {}
    if type(opts) ~= 'table' then error(where .. ': options must be a table', level) end
    for k in pairs(opts) do
        if not has(keys, k) then
            error(where .. ': unknown option "' .. tostring(k) .. '" (' .. table.concat(keys, ', ') .. ')', level)
        end
    end
    if opts.kinds ~= nil then
        if not isList(opts.kinds, 'string') then error(where .. ': kinds expects a list of kinds', level) end
        for _, k in ipairs(opts.kinds) do
            if not has(AREA_KINDS, k) then
                error(where .. ': kind "' .. k .. '" has no widget (' .. table.concat(AREA_KINDS, ', ') .. ')', level)
            end
        end
    end
    if opts.skip ~= nil and not isList(opts.skip, 'string') then error(where .. ': skip expects a list of paths', level) end
    if opts.order ~= nil and not isList(opts.order, 'string') then error(where .. ': order expects a list of areas', level) end
    if opts.titles ~= nil and type(opts.titles) ~= 'table' then error(where .. ': titles expects { [area] = title }', level) end
    if opts.rooms ~= nil and type(opts.rooms) ~= 'boolean' then error(where .. ': rooms must be true or false', level) end
    return { kinds = opts.kinds or AREA_DEFAULT_KINDS, skip = opts.skip, order = opts.order, titles = opts.titles or {},
        rooms = opts.rooms == true }
end

local function isOn(v)
    local n = tonumber(v)
    return v == true or (n ~= nil and n > 0)
end

-- Paths already warned about: dashboard = true on a kind without a widget
local _noWidgetWarned = {}

-- A widget for an exposed object, or nil for a kind without one; off() also disables it (a room tile's)
local function areaWidget(h, id, off)
    local o, kind = h.object, h.type
    if type(o) ~= 'table' then return nil end
    local function m(name)
        if type(o[name]) == 'function' then return function(...) return o[name](o, ...) end end
    end
    local w = { id = id, label = h.options.name or h.path, _h = h,
        opts = { disabled = function() return h.options.readonly == true or (off ~= nil and off()) end } }
    local rgb = o._objId and type(o.setColor) == 'function' and type(o.getColor) == 'function'
    local dims = type(o.getBrightness) == 'function' and type(o.setBrightness) == 'function'
    if (kind == 'light' or kind == 'dimmer') and rgb then
        w.type, w.icon, w.fns = 'color', 'palette', lightColorFns(o)
    elseif kind == 'dimmer' or (kind == 'light' and dims) then
        w.type, w.icon = 'dimmer', 'lightbulb'
        w.fns = {
            get = function()
                if dims then return o:getBrightness() end
                local v = tonumber(h:getValue())
                return v and math.floor(v * 100 + 0.5)
            end,
            set = function(p) if dims then o:setBrightness(p) else o:setValue(p / 100) end end,
            switchOn = m('switchOn'), switchOff = m('switchOff'),
        }
    elseif kind == 'light' or kind == 'switch' or kind == 'lock' then
        w.type = 'toggle'
        w.icon = (kind == 'lock' and 'lock') or (kind == 'switch' and 'power') or 'lightbulb'
        w.getValue = function() return isOn(h:getValue()) end
        w.onToggle = function(v)
            if v and o.switchOn then o:switchOn()
            elseif not v and o.switchOff then o:switchOff()
            elseif o.setValue then o:setValue(v and 1 or 0)
            elseif not h:setValue(v and 1 or 0) then error('cannot switch ' .. h.path) end
        end
    elseif kind == 'cover' then
        w.type, w.icon = 'cover', 'window'
        -- a Grenton roller keeps its movement aside; without a position the state is the movement
        w.fns = { getPosition = m('getPosition'), getState = o._hasPosition and m('getState') or nil,
            open = m('open'), close = m('close'), stop = m('stop'), set = m('setPosition') }
    elseif kind == 'temperature' or kind == 'humidity' then
        w.type = 'status'
        w.icon = kind == 'temperature' and 'thermometer-half' or 'droplet'
        w.unit = kind == 'temperature' and '°C' or '%'
        w.getValue = function() return h:getValue() end
    elseif kind == 'climate' and isThermostat(o) then
        -- the expose options' range, as Home Assistant clamps a target; readonly disables the widget
        local min, max = tonumber(h.options.min) or THERMOSTAT_MIN, tonumber(h.options.max) or THERMOSTAT_MAX
        if min >= max then min, max = THERMOSTAT_MIN, THERMOSTAT_MAX end
        local step = tonumber(h.options.step)
        w.type, w.icon, w.fns = 'thermostat', 'thermometer-half', thermostatFns(o)
        w.min, w.max, w.step, w.unit = min, max, (step and step > 0) and step or 0.5, '°C'
    else
        if h.options.dashboard == true and not _noWidgetWarned[h.path] then
            _noWidgetWarned[h.path] = true
            Logger:getShared():warn("[DASHBOARD] %s: dashboard = true, but a %s has no widget (%s)",
                h.path, tostring(kind), table.concat(AREA_KINDS, ', '))
        end
        return nil
    end
    return w
end

local function escapeId(s)
    return (s:gsub('[^%w%._:%-]', function(c) return string.format('%%%02X', c:byte()) end))
end

-- A path in an id: other bytes as %XX, so two paths never give one id
local function idOf(path)
    return AREA_ID .. escapeId(path)
end

-- base, or base#2, #3... when taken
local function claimId(used, base)
    local id, n = base, 1
    while used[id] do n = n + 1; id = base .. '#' .. n end
    used[id] = true
    return id
end

-- Widgets of the devices in areas (on floor, when given); ids from the path, the same on every
-- read; "#n" only for the same device shown again
local function areaWidgets(spec, areas, used, floor, off)
    local E = ExposedObjects
    if not (E and E._inArea) then return {} end
    local out = {}
    for _, f in ipairs(E:_inArea(areas, { kinds = spec.kinds, skip = spec.skip, floor = floor, flag = 'dashboard' })) do
        local base = idOf(f.path)
        local id, n = base, 1
        while used[id] do n = n + 1; id = base .. '#' .. n end
        local w = areaWidget(f.handle, id, off)
        if w then
            used[id] = true
            out[#out + 1] = w
        end
    end
    return out
end

-- Temperature and humidity devices of a room (dashboard = false and skip leave them out)
local function roomSensors(spec, areas, floor)
    local E = ExposedObjects
    local out = { temperature = {}, humidity = {} }
    if not (E and E._inArea) then return out end
    for _, f in ipairs(E:_inArea(areas, { kinds = { 'temperature', 'humidity' }, skip = spec.skip, floor = floor, flag = 'dashboard' })) do
        local list = out[f.handle.type]
        if list then list[#list + 1] = f.handle end
    end
    return out
end

local function coverCall(c, name)
    if type(c[name]) ~= 'function' then return nil end
    local ok, v = pcall(c[name], c)
    if ok then return tonumber(v) end
end

-- Tilt only where the object has both methods and reports an angle
local function coverTilt(c)
    if type(c.getTilt) ~= 'function' or type(c.setTilt) ~= 'function' then return nil end
    return coverCall(c, 'getTilt')
end

-- A cover with a position (a Grenton roller without one has _hasPosition = false)
local function coverCanSet(c)
    return type(c.setPosition) == 'function' and c._hasPosition ~= false
end

-- Counts of covers by state; not moving and no position is unknown
local function coverCounts(readings)
    local s = { open = 0, moving = 0, closed = 0, unknown = 0 }
    for _, r in ipairs(readings) do
        if r.state == 1 or r.state == 2 then s.moving = s.moving + 1
        elseif r.position == nil then s.unknown = s.unknown + 1
        elseif r.position > 0 then s.open = s.open + 1
        else s.closed = s.closed + 1 end
    end
    return s
end

local function coverReading(c)
    return { position = coverCall(c, 'getPosition'), state = coverCall(c, 'getState') }
end

-- What a group tile shows: counts, the average position and tilt, whether a slider can set them
local function coversEntry(covers)
    local readings, posSum, posN, tiltSum, tiltN, canSet = {}, 0, 0, 0, 0, false
    for i, c in ipairs(covers) do
        local r = coverReading(c)
        readings[i] = r
        if r.position ~= nil then posSum = posSum + r.position; posN = posN + 1 end
        local t = coverTilt(c)
        if t ~= nil then tiltSum = tiltSum + t; tiltN = tiltN + 1 end
        if coverCanSet(c) then canSet = true end
    end
    local e = coverCounts(readings)
    if posN > 0 then e.position = math.floor(posSum / posN + 0.5) end
    if tiltN > 0 then e.tilt = math.floor(tiltSum / tiltN + 0.5) end
    e.set = canSet
    return e, readings
end

-- Series of commands by "dashboard/widget/move" and ".../tilt": the first now, the next ones
-- COVERS_SPACING_MS apart on timers; a new one of the same kind drops what is left of the old
local _coversRuns = {}
local function coversCancel(key)
    local prev = _coversRuns[key]
    if prev and prev.timer then TIMERS:clearTimer(prev.timer) end
    _coversRuns[key] = nil
end

-- stopIf() true before a later step (the widget got disabled) drops the rest
local function coversSeries(key, covers, fn, stopIf)
    coversCancel(key)
    local run = { timer = nil }
    _coversRuns[key] = run
    local function step(i)
        if _coversRuns[key] ~= run then return end
        run.timer = nil
        if i > 1 and stopIf and stopIf() then
            Logger:getShared():info("[DASHBOARD] '%s': disabled, %d covers not moved", key, #covers - i + 1)
            _coversRuns[key] = nil
            return
        end
        local ok, err = pcall(fn, covers[i])
        if not ok then Logger:getShared():warn("[DASHBOARD] '%s': cover %d: %s", key, i, tostring(err)) end
        if i >= #covers then
            _coversRuns[key] = nil
            return
        end
        local id = TIMERS:setTimeout(COVERS_SPACING_MS, function() step(i + 1) end)
        if not id or id < 0 then
            Logger:getShared():warn("[DASHBOARD] '%s': no timer, %d covers not moved", key, #covers - i)
            _coversRuns[key] = nil
            return
        end
        run.timer = id
    end
    if #covers > 0 then step(1) else _coversRuns[key] = nil end
end

-- ==========================================================================
-- Media player: artwork, title, artist, transport and volume
-- ==========================================================================

local PLAYER_KEYS = { icon = true, disabled = true, span = true, size = true, color = true }
local PLAYER_FNS = { 'getState', 'play', 'pause', 'next', 'previous', 'setVolume', 'setMute', 'on', 'off' }
local PLAYER_FN_SET = {}
for i = 1, #PLAYER_FNS do PLAYER_FN_SET[PLAYER_FNS[i]] = true end

-- A MusicCast device (@vclu/musiccast) by its methods, not the plugin's internals
local function isMusicCast(v)
    return type(v.getState) == 'function' and type(v.getTrack) == 'function'
        and type(v.setVolume) == 'function' and type(v.play) == 'function'
end

-- The device's methods as the player's functions; its getState mapped to the tile's
local function musicCastFns(dev)
    local fns = {
        getState = function()
            local s = dev:getState()
            if type(s) ~= 'table' then return s end
            local t = type(s.track) == 'table' and s.track or {}
            return { playing = s.playback == 'play', title = t.title, artist = t.artist, album = t.album,
                artwork = t.artwork, volume = s.volume, muted = s.mute, power = s.power, online = s.online }
        end,
    }
    for i = 2, #PLAYER_FNS do
        local name = PLAYER_FNS[i]
        if type(dev[name]) == 'function' then
            -- the device's methods take an optional callback after their arguments
            if name == 'setVolume' or name == 'setMute' then
                fns[name] = function(v) dev[name](dev, v) end
            else
                fns[name] = function() dev[name](dev) end
            end
        end
    end
    return fns
end

local function playerWidget(id, label, src, opts)
    local where = 'section:player("' .. tostring(id) .. '")'
    if type(id) ~= 'string' or id == '' then error('section:player: id must be a non-empty string', 3) end
    if type(label) ~= 'string' then error(where .. ': label must be a string', 3) end
    if opts ~= nil and type(opts) ~= 'table' then error(where .. ': opts must be a table', 3) end
    opts = opts or {}
    for k in pairs(opts) do
        if not PLAYER_KEYS[k] then error(where .. ': unknown option "' .. tostring(k) .. '" (icon, disabled, span, size, color)', 3) end
    end
    if opts.icon ~= nil and (type(opts.icon) ~= 'string' or not opts.icon:match('^[%w%-]+$')) then
        error(where .. ': opts.icon must be a Bootstrap icon name (letters, digits and -)', 3)
    end
    if opts.disabled ~= nil and type(opts.disabled) ~= 'function' then error(where .. ': opts.disabled must be a function', 3) end
    if type(src) ~= 'table' then
        error(where .. ': src must be a MusicCast device or a table of functions {getState, play, pause, ...}, got ' .. type(src), 3)
    end
    local fns
    if isMusicCast(src) then
        fns = musicCastFns(src)
    else
        if type(src.getState) ~= 'function' then error(where .. ': src.getState must be a function returning the state', 3) end
        fns = {}
        for k, v in pairs(src) do
            if not PLAYER_FN_SET[k] then
                error(where .. ': unknown key "' .. tostring(k) .. '" in src (' .. table.concat(PLAYER_FNS, ', ') .. ')', 3)
            end
            if type(v) ~= 'function' then error(where .. ': src.' .. k .. ' must be a function', 3) end
            fns[k] = v
        end
    end
    return { id = id, type = 'player', label = label, icon = opts.icon or 'music-note-beamed', fns = fns, opts = opts }
end

-- The controls the functions allow; a missing one hides its button
local function playerCapabilities(fns)
    local function has(k) return type(fns[k]) == 'function' end
    return { play = has('play'), pause = has('pause'), next = has('next'), previous = has('previous'),
        volume = has('setVolume'), mute = has('setMute'), power = has('on') and has('off') }
end

-- Text for the page: a string or a number, cut to a sane length (not mid-character)
local function playerText(v)
    if type(v) == 'number' then v = tostring(v) end
    if type(v) ~= 'string' or v == '' then return nil end
    if #v <= 300 then return v end
    v = v:sub(1, 300)
    -- drop a last UTF-8 character the cut left incomplete
    local i = #v
    while i > 1 and v:byte(i) >= 128 and v:byte(i) < 192 do i = i - 1 end
    local b = v:byte(i)
    local need = b >= 240 and 4 or b >= 224 and 3 or b >= 192 and 2 or 1
    if #v - i + 1 < need then v = v:sub(1, i - 1) end
    return v
end

-- The page sets it as an <img> src: only an absolute http(s) URL
local function playerArtwork(v)
    if type(v) ~= 'string' or #v > 2048 or not v:lower():match('^https?://[^/%s]') or v:find('[%c%s"<>\\]') then
        return nil
    end
    return v
end

-- What the tile shows: {playing, title, artist, album, artwork, volume, muted, power, online}, or {error}
local function playerState(w)
    local ok, s = pcall(w.fns.getState)
    if not ok then return { error = tostring(s) } end
    if type(s) ~= 'table' then
        return { error = 'section:player("' .. tostring(w.id) .. '"): getState must return a table, got ' .. type(s) }
    end
    local function bool(v) if type(v) == 'boolean' then return v end end
    local vol = tonumber(s.volume)
    if vol and vol - vol == 0 then vol = math.floor(math.max(0, math.min(100, vol)) + 0.5) else vol = nil end
    return { playing = s.playing == true, title = playerText(s.title), artist = playerText(s.artist),
        album = playerText(s.album), artwork = playerArtwork(s.artwork), volume = vol,
        muted = bool(s.muted), power = bool(s.power), online = bool(s.online) }
end

-- ==========================================================================
-- Weather forecast: the next days and a chart of the next hours
-- ==========================================================================

local FORECAST_KEYS = { days = true, hours = true, unit = true, icon = true, disabled = true, span = true, size = true, color = true }

local function forecastWidget(id, label, src, opts)
    local where = 'section:forecast("' .. tostring(id) .. '")'
    if type(id) ~= 'string' or id == '' then error('section:forecast: id must be a non-empty string', 3) end
    if type(label) ~= 'string' then error(where .. ': label must be a string', 3) end
    if opts ~= nil and type(opts) ~= 'table' then error(where .. ': opts must be a table', 3) end
    opts = opts or {}
    for k in pairs(opts) do
        if not FORECAST_KEYS[k] then
            error(where .. ': unknown option "' .. tostring(k) .. '" (days, hours, unit, icon, disabled, span, size, color)', 3)
        end
    end
    local function count(k, lo, hi)
        local v = opts[k]
        if v ~= nil and (type(v) ~= 'number' or v ~= math.floor(v) or v < lo or v > hi) then
            error(where .. ': opts.' .. k .. ' must be a whole number ' .. lo .. '-' .. hi, 4)
        end
    end
    count('days', 1, 7)
    count('hours', 0, 48)
    if opts.unit ~= nil and (type(opts.unit) ~= 'string' or #opts.unit > 8) then
        error(where .. ': opts.unit must be a short string such as "°C"', 3)
    end
    if opts.icon ~= nil and (type(opts.icon) ~= 'string' or not opts.icon:match('^[%w%-]+$')) then
        error(where .. ': opts.icon must be a Bootstrap icon name (letters, digits and -)', 3)
    end
    if opts.disabled ~= nil and type(opts.disabled) ~= 'function' then error(where .. ': opts.disabled must be a function', 3) end
    local fn
    if type(src) == 'function' then
        fn = src
    elseif type(src) == 'table' and type(src.getForecast) == 'function' then
        -- the Weather plugin (@vclu/weather): its getForecast gives the same table
        fn = function() return src:getForecast() end
    else
        error(where .. ': src must be a function returning { days, hours } or the Weather plugin (Plugin.getPlugin("@vclu/weather")), got ' .. type(src), 3)
    end
    return { id = id, type = 'forecast', label = label, icon = opts.icon or 'cloud-sun', fn = fn, opts = opts }
end

-- A finite number rounded to one decimal, or nil
local function round1(v)
    v = tonumber(v)
    if not v or v - v ~= 0 then return nil end
    return math.floor(v * 10 + 0.5) / 10
end

-- A Bootstrap icon name from the data; anything else is a cloud
local function forecastIcon(v)
    if type(v) == 'string' and #v <= 40 and v:match('^[%w%-]+$') then return v end
    return 'cloud'
end

-- What the tile shows: {days, hours, updated}, {none = true} before the first fetch, or {error}
local function forecastState(w)
    local ok, f = pcall(w.fn)
    if not ok then return { error = tostring(f) } end
    if f == nil then return { none = true } end
    if type(f) ~= 'table' then
        return { error = 'section:forecast("' .. tostring(w.id) .. '"): src must return a table or nil, got ' .. type(f) }
    end
    local today = datetime.now().date
    local days = {}
    for i, d in ipairs(type(f.days) == 'table' and f.days or {}) do
        if type(d) == 'table' and type(d.date) == 'string' and d.date:match('^%d%d%d%d%-%d%d%-%d%d$') and d.date >= today then
            local pop = tonumber(d.pop)
            -- 0..1 is a fraction; above 1 it is a percent already
            if pop and pop - pop == 0 then
                pop = math.floor(math.max(0, math.min(100, pop > 1 and pop or pop * 100)) + 0.5)
            else
                pop = nil
            end
            local rain = round1(d.rain)
            days[#days + 1] = { date = d.date, icon = forecastIcon(d.iconName or d.icon), min = round1(d.min),
                max = round1(d.max), rain = rain and math.max(0, rain), pop = pop }
        end
    end
    table.sort(days, function(a, b) return a.date < b.date end)
    for i = #days, (w.opts.days or 5) + 1, -1 do days[i] = nil end

    -- the current hour and limit-1 after it: exactly limit points of hourly data
    local hours = {}
    local limit = w.opts.hours or 24
    local now = math.floor(__go_time_ms() / 1000)
    for i, h in ipairs(limit > 0 and type(f.hours) == 'table' and f.hours or {}) do
        local at = type(h) == 'table' and tonumber(h.at)
        if at and at - at == 0 and at > now - 3600 and at <= now + (limit - 1) * 3600 then
            local rain = round1(h.rain)
            hours[#hours + 1] = { at = math.floor(at), temp = round1(h.temp), rain = rain and math.max(0, rain) or 0 }
        end
    end
    table.sort(hours, function(a, b) return a.at < b.at end)

    local updated = tonumber(f.updated)
    return { days = days, hours = hours, updated = updated and updated - updated == 0 and math.floor(updated) or nil }
end

-- The dashboard as the API reads it: area sections filled from what is exposed, a floor one
-- section per area (or one of room tiles); reused until something is exposed, unexposed or its
-- options change (widgets read the objects live). A room tile's overlay is a hidden section
-- after the visible ones, so its widgets go through values and actions like any other.
local function expandAreas(dash)
    if not dash or not dash._hasAreas then return dash end
    local E = ExposedObjects
    local gen = E and E._generation or 0
    if dash._areaView and dash._areaGen == gen then return dash._areaView end
    local used, sections, overlays = {}, {}, {}
    local function add(sec, widgets)
        local c = {}
        for k, v in pairs(sec) do c[k] = v end
        c.widgets = widgets
        sections[#sections + 1] = c
    end
    -- the tile with what it counts; ids of its overlay are given after the visible sections'
    local function room(w, spec, areas, floor)
        local c = {}
        for k, v in pairs(w) do c[k] = v end
        local off = function() return type(w.opts.disabled) == 'function' and w.opts.disabled() == true end
        c._items = areaWidgets(spec, areas, {}, floor, off)
        c._sensors = roomSensors(spec, areas, floor)
        if not c.link then overlays[#overlays + 1] = c end
        return c
    end
    for _, sec in ipairs(dash._sections) do
        local spec = sec._floor
        if spec and spec.rooms then
            local tiles = {}
            for _, a in ipairs(E and E._areasOn and E:_areasOn(spec.floor, spec.order) or {}) do
                local t = room({ id = AREA_ID .. '@' .. escapeId(a), type = 'room', label = spec.titles[a] or a,
                    icon = 'door-open', opts = {} }, spec, { a }, spec.floor)
                local s = t._sensors
                if #t._items > 0 or #s.temperature > 0 or #s.humidity > 0 then
                    t.id = claimId(used, t.id)
                    tiles[#tiles + 1] = t
                else
                    overlays[#overlays] = nil
                end
            end
            if #tiles > 0 then add({ name = sec.name, icon = sec.icon }, tiles) end
        elseif spec then
            for _, a in ipairs(E and E._areasOn and E:_areasOn(spec.floor, spec.order) or {}) do
                local widgets = areaWidgets(spec, { a }, used, spec.floor)
                if #widgets > 0 then add({ name = spec.titles[a] or a, icon = sec.icon }, widgets) end
            end
        else
            local widgets = sec._area and areaWidgets(sec._area, sec._area.areas, used) or {}
            local rooms = false
            for _, w in ipairs(sec.widgets) do
                if w.type == 'room' then
                    rooms = true
                    w = room(w, w._spec, w._spec.areas)
                end
                widgets[#widgets + 1] = w
            end
            if sec._area or rooms then add(sec, widgets) else sections[#sections + 1] = sec end
        end
    end
    for _, t in ipairs(overlays) do
        for _, w in ipairs(t._items) do w.id = claimId(used, idOf(w._h.path)) end
        sections[#sections + 1] = { name = t.label, icon = t.icon, widgets = t._items, _room = t.id }
    end
    local view = setmetatable({ _sections = sections }, { __index = dash })
    dash._areaView, dash._areaGen = view, gen
    return view
end

function SectionHandle:new(builder, sectionIndex)
    local h = { _builder = builder, _sectionIndex = sectionIndex }
    setmetatable(h, self)
    return h
end

--- Add a button widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "power", "door-open")
-- @param onTap function Callback fired on button press: function()
-- @param opts table Optional: {disabled=function()->bool, confirm=string}
-- @api public
function SectionHandle:button(id, label, icon, onTap, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'button', label = label, icon = icon,
        onTap = onTap, opts = opts or {}
    })
    return self
end

--- Add a toggle widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "lightbulb", "lamp")
-- @param getValue function Returns current state: function() -> boolean|number (truthy=on)
-- @param onToggle function Called with desired state: function(bool) — use switchOn/switchOff, NOT toggle()
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:toggle(id, label, icon, getValue, onToggle, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'toggle', label = label, icon = icon,
        getValue = getValue, onToggle = onToggle, opts = opts or {}
    })
    return self
end

--- Add a slider widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param min number Minimum slider value
-- @param max number Maximum slider value
-- @param getValue function Returns current value: function() -> number
-- @param onSet function Called with new value: function(number)
-- @param opts table Optional: {step=number, unit=string, icon=string, disabled=function()->bool}
-- @api public
function SectionHandle:slider(id, label, min, max, getValue, onSet, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'slider', label = label,
        min = min, max = max,
        getValue = getValue, onSet = onSet, opts = opts or {}
    })
    return self
end

--- Add a status widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "thermometer-half", "droplet")
-- @param getValue function Returns current value: function() -> number|string
-- @param unit string Unit suffix displayed after value (e.g., "°C", "%", "lx")
-- @param opts table Optional: {decimals=0-6 (a number is shown rounded to that many places), disabled=function()->bool, color=string|function()->string|nil}
-- @api public
function SectionHandle:status(id, label, icon, getValue, unit, opts)
    checkDecimals('section:status', id, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'status', label = label, icon = icon,
        getValue = getValue, unit = unit or '', opts = opts or {}
    })
    return self
end

--- Add a link widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param url string|function URL string or function() -> string
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:link(id, label, icon, url, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'link', label = label, icon = icon,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a chart widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label (e.g., "Temperatura 24h")
-- @param metricPath string Metric path registered via metric() (e.g., "temp:salon", "salda:outsideAir")
-- @param opts table Optional: {range=string (any duration such as "30m", "2h", "24h", "7d"; default "2h"; 30m/2h/24h/7d have tuned refresh, others refresh every 60 s), unit=string, min=n, max=n (Y axis), fill=bool (default true), refresh=seconds, span=n, color=string}
-- @api public
function SectionHandle:chart(id, label, metricPath, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'chart', label = label,
        metricPath = metricPath, opts = opts or {}
    })
    return self
end

--- Add a calendar widget to this section: a month grid and the day of a tapped date.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param cal table Calendar from Calendar.new(...):build(), or its id
-- @param opts table Optional: {view="month"|"day", tags={...}, schedules={ids drawn as 24 h bars in the day view} or true for all of the calendar's,
--   holidays=true (holiday names, on by default), namedays=false (name days in the day view),
--   sun=true (sunrise and sunset in the day view, with :location()), span=n}; what is off is not sent to the browser
-- @api public
function SectionHandle:calendar(id, label, cal, opts)
    self._builder:_addWidget(self._sectionIndex, calendarWidget(id, label, cal, opts))
    return self
end

--- Add a select widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param options table Array of {value=any, label=string} or function() -> same
-- @param getValue function Returns current selected value: function() -> any
-- @param onSelect function Called with selected value: function(any)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:select(id, label, icon, options, getValue, onSelect, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'select', label = label, icon = icon,
        options = options, getValue = getValue, onSelect = onSelect, opts = opts or {}
    })
    return self
end

--- Add an image widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param url string|function Image URL string or function() -> string
-- @param opts table Optional: {refreshInterval=number (seconds)}
-- @api public
function SectionHandle:image(id, label, url, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'image', label = label,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a group label widget to this section.
-- @param label string Group heading text
-- @param icon string Bootstrap icon name (optional)
-- @api public
function SectionHandle:group(label, icon)
    self._builder:_addWidget(self._sectionIndex, {
        id = '_group_' .. tostring(#self._builder._sections[self._sectionIndex].widgets),
        type = 'group', label = label, icon = icon, opts = {}
    })
    return self
end

--- Add a cover widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "arrows-expand")
-- @param fns table {getPosition=function()->number|nil, getState=function()->number|nil, open=function(), close=function(), stop=function(), set=function(position)}
--   Buttons up/stop/down call open/stop/close. With set, a 0-100% slider is shown under them.
--   Position 0 = closed, 100 = open, as in Grenton; nil (not reported yet) is shown as --.
--   getState lights the button of the movement: 0 = stopped, 1 = up, 2 = down.
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:cover(id, label, icon, fns, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'cover', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a cover group: one tile moving several covers, with their counts and average position.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param list table Covers (RemoteCover, VirtualCover) or their paths, _:byTag(...), or a function returning one (read each time; missing paths skipped)
--   Buttons up/stop/down call open/stop/close on each cover, the slider setPosition on those with a position,
--   the slat slider (shown when a cover reports getTilt) setTilt on those with a tilt; commands go out 50 ms apart
--   and a new movement (or slat) command drops what is left of the previous one; stop ends both.
-- @param opts table Optional: {icon=string (default "window"), disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function SectionHandle:covers(id, label, list, opts)
    if self._builder._sections[self._sectionIndex]._isHeader then
        error('section:covers("' .. tostring(id) .. '"): a cover group goes in a section, not the header', 2)
    end
    self._builder:_addWidget(self._sectionIndex, coversWidget(id, label, list, opts))
    return self
end

--- Add a media player: artwork, title and artist, previous/play-pause/next, volume, mute and power.
-- @param id string Unique widget identifier
-- @param label string Display label (e.g. the room)
-- @param src table A MusicCast device (Plugin.getPlugin("@vclu/musiccast"):create{...}) or a table of functions
--   {getState=function()->{playing, title, artist, album, artwork (http(s) URL), volume (0-100), muted, power, online},
--   play?, pause?, next?, previous?, setVolume?(percent), setMute?(bool), on?, off?}; a missing function hides its button
-- @param opts table Optional: {icon=string (default "music-note-beamed"), disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function SectionHandle:player(id, label, src, opts)
    if self._builder._sections[self._sectionIndex]._isHeader then
        error('section:player("' .. tostring(id) .. '"): a player goes in a section, not the header', 2)
    end
    self._builder:_addWidget(self._sectionIndex, playerWidget(id, label, src, opts))
    return self
end

--- Add a weather forecast: the next days (icon, max/min, rain) and a chart of the next hours.
-- src gives {days={{date='YYYY-MM-DD', iconName (or icon), min, max, rain (mm), pop (0-1, or a percent when above 1)}},
-- hours={{at (unix s), temp, rain}}, updated (optional unix s, shown as "odświeżono HH:MM")},
-- or nil while there is no forecast yet; days before today in the house's time zone are dropped;
-- opts.hours counts hours: the current one and hours-1 after it (hourly data gives exactly that many points).
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param src function A function returning the forecast, or the Weather plugin (Plugin.getPlugin("@vclu/weather"))
-- @param opts table Optional: {days=1-7 (default 5), hours=0-48 (default 24, 0 hides the chart), unit=string (default "°C"), icon=string (default "cloud-sun"), disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function SectionHandle:forecast(id, label, src, opts)
    if self._builder._sections[self._sectionIndex]._isHeader then
        error('section:forecast("' .. tostring(id) .. '"): a forecast goes in a section, not the header', 2)
    end
    self._builder:_addWidget(self._sectionIndex, forecastWidget(id, label, src, opts))
    return self
end

--- Add a dimmer widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "brightness-high")
-- @param fns table {get=function()->number(0-100), set=function(number 0-100), switchOn=function()?, switchOff=function()?, isOn=function()->bool? (default: get() > 0)}
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:dimmer(id, label, icon, fns, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'dimmer', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a color picker widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "palette")
-- @param fns table {getColor=function()->{r,g,b}, setColor=function(r,g,b), get?/set? brightness 0-100,
--   switchOn?, switchOff?, isOn?, getWhite?/setWhite? white 0-100}, or a RemoteRGBLight (white when it has one)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:color(id, label, icon, fns, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'color', label = label, icon = icon,
        fns = colorFns(fns), opts = opts or {}
    })
    return self
end

--- Add a thermostat: current temperature, target and +/- for the target.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string|nil Bootstrap icon name (default "thermometer-half")
-- @param fns table {getCurrent=function()->number|nil, getTarget=function()->number|nil, setTarget=function(number)?,
--   getState=function()->number|bool? (1/true active, 0/false off), getMode=function()->number? (0 manual, 1 holiday,
--   2 schedule, 3 heat-up)}, or a thermostat object (RemoteThermostat: getValue, getTarget, setSetpoint, getState, getMode)
--   Shows the current temperature and the target ("--" until reported); + and - change the target by step and
--   setTarget is called once, 1 s after the last tap; the chosen value shows until the thermostat reports a
--   new target, at most 90 s. Without setTarget (or opts.readonly) there are no buttons; with getMode the
--   target is set only in manual mode (0), as lib/panel does. The action endpoint refuses a set outside min..max.
-- @param opts table Optional: {min=5, max=35, step=0.5, unit="°C", readonly=bool or function()->bool (true: no buttons; a function locks them while it returns true), disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function SectionHandle:thermostat(id, label, icon, fns, opts)
    if self._builder._sections[self._sectionIndex]._isHeader then
        error('section:thermostat("' .. tostring(id) .. '"): a thermostat goes in a section, not the header', 2)
    end
    self._builder:_addWidget(self._sectionIndex, thermostatWidget(id, label, icon, fns, opts))
    return self
end

local CHIP_KEYS = { color = true, link = true, disabled = true }

-- opts.link: an http(s) URL or a path as is, anything else is a dashboard id; level as error()'s, default 3
local function chipLink(where, link, level)
    level = level or 3
    if type(link) ~= 'string' or link == '' then
        error(where .. ': opts.link must be a URL or a dashboard id', level)
    end
    if link:match('^/[/\\]') then
        error(where .. ': opts.link "' .. link .. '" leaves the site; give an http(s):// URL', level)
    end
    if link:lower():match('^https?://') or link:sub(1, 1) == '/' then return link end
    if link:find('[:%s/?#]') then
        error(where .. ': opts.link must be http(s)://..., a path starting with / or a dashboard id, got "' .. link .. '"', level)
    end
    return '/d/' .. link
end

local ROOM_KEYS = { 'color', 'disabled', 'humidity', 'icon', 'kinds', 'link', 'size', 'skip', 'span', 'temperature', 'title' }

-- A room tile: the area's name, what is on and its climate; a tap opens its widgets (or opts.link).
-- Errors name the module's line: level 3 is the caller of section:room / dash:room.
local function roomWidget(id, area, opts)
    local where = 'section:room("' .. tostring(id) .. '")'
    if type(id) ~= 'string' or id == '' then error('section:room: id must be a non-empty string', 3) end
    local areas = type(area) == 'table' and area or { area }
    if (type(area) ~= 'string' and type(area) ~= 'table') or #areas == 0 or not isList(areas, 'string') then
        error(where .. ': expected an area name or a list of them, got ' .. type(area), 3)
    end
    local spec = areaSpec(where, opts, ROOM_KEYS, 4)
    opts = opts or {}
    spec.areas = areas
    for _, k in ipairs({ 'temperature', 'humidity' }) do
        local v = opts[k]
        if v ~= nil and type(v) ~= 'function' and (type(v) ~= 'string' or v == '') then
            error(where .. ': opts.' .. k .. ' must be a path or a function returning a number', 3)
        end
    end
    if opts.title ~= nil and type(opts.title) ~= 'string' then error(where .. ': opts.title must be a string', 3) end
    if opts.icon ~= nil and (type(opts.icon) ~= 'string' or not opts.icon:match('^[%w%-]+$')) then
        error(where .. ': opts.icon must be a Bootstrap icon name (letters, digits and -)', 3)
    end
    if opts.disabled ~= nil and type(opts.disabled) ~= 'function' then error(where .. ': opts.disabled must be a function', 3) end
    local link = opts.link ~= nil and chipLink(where, opts.link, 4) or nil
    return { id = id, type = 'room', label = opts.title or areas[1], icon = opts.icon or 'door-open', link = link,
        opts = opts, _spec = spec, _temperature = opts.temperature, _humidity = opts.humidity }
end

--- Add a chip to the header: an icon and a short text from fn, hidden while fn returns nil.
-- @param id string Unique widget identifier
-- @param icon string Bootstrap icon name (e.g., "alarm", "water", "shield")
-- @param fn function function() -> string|nil|{text=string, icon=string, color=string}; nil hides the chip
-- @param opts table Optional: {color=string|function()->string|nil, link=URL or dashboard id, disabled=function()->bool}
-- @api public
function SectionHandle:chip(id, icon, fn, opts)
    local where = 'header:chip("' .. tostring(id) .. '")'
    if not self._builder._sections[self._sectionIndex]._isHeader then
        error(where .. ': chips go in the header only, add them after dash:header()', 2)
    end
    if type(id) ~= 'string' or id == '' then error('header:chip: id must be a non-empty string', 2) end
    if type(icon) ~= 'string' or not icon:match('^[%w%-]+$') then
        error(where .. ': icon must be a Bootstrap icon name (letters, digits and -)', 2)
    end
    if type(fn) ~= 'function' then error(where .. ': fn must be a function returning the text or nil', 2) end
    if opts ~= nil and type(opts) ~= 'table' then error(where .. ': opts must be a table', 2) end
    opts = opts or {}
    for k in pairs(opts) do
        if not CHIP_KEYS[k] then error(where .. ': unknown option "' .. tostring(k) .. '" (color, link, disabled)', 2) end
    end
    if opts.color ~= nil and type(opts.color) ~= 'string' and type(opts.color) ~= 'function' then
        error(where .. ': opts.color must be a colour or a function', 2)
    end
    if opts.disabled ~= nil and type(opts.disabled) ~= 'function' then
        error(where .. ': opts.disabled must be a function', 2)
    end
    local link = opts.link ~= nil and chipLink(where, opts.link) or nil
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'chip', icon = icon, chipFn = fn, link = link, opts = opts
    })
    return self
end

--- Fill this section with a widget for every device expose() put in an area, picked by kind:
-- a light, switch or lock a toggle, a dimmer a dimmer, an RGB light the colour widget, a cover
-- a cover, temperature and humidity a status; lights first, then covers, then the rest by name.
-- Filled when something is exposed or unexposed, so devices a plugin exposes later show up;
-- widgets added by hand come after them; a read-only device's widget is disabled. Like the
-- panel, expose(obj, type, { dashboard = false }) keeps an object off dashboards and
-- dashboard = true puts one of those kinds on them even when kinds leaves it out (other kinds
-- have no widget and log a warning). Widget ids are "area:" .. path, other bytes as %XX.
-- Anyone who can open the dashboard controls what is exposed in the area later: use
-- :auth('session') or 'pin'; access profiles of MQTT, HomeKit and MCP do not apply here.
-- @param area string Area name, or a list of names (one room under two names)
-- @param opts table Optional: {kinds={"light","dimmer","cover","temperature","humidity"} (also "switch", "lock"), skip={path, ...}}
-- @api public
function SectionHandle:area(area, opts)
    local sec = self._builder._sections[self._sectionIndex]
    local areas = type(area) == 'table' and area or { area }
    if (type(area) ~= 'string' and type(area) ~= 'table') or #areas == 0 or not isList(areas, 'string') then
        error('section:area: expected an area name or a list of them, got ' .. type(area), 2)
    end
    if sec._area then error('section:area: this section already shows an area; pass a list for several', 2) end
    sec._area = areaSpec('section:area', opts, AREA_KEYS, 3)
    sec._area.areas = areas
    self._builder._hasAreas = true
    self._builder._areaView = nil
    return self
end

--- Add one section per area on a floor (delegates to DashboardBuilder:floor).
-- @api public
function SectionHandle:floor(floor, opts)
    local b = self._builder:_floor(floor, opts) -- no tail call: errors count the levels
    return b
end

--- Add a room tile: the area's name, how many lights and switches are on and covers open, its
-- temperature and humidity ("Sypialnia · 2 włączone · 21,5 °C · 60 %"); a tap opens the room's
-- widgets, chosen as section:area does (same kinds, skip and ids), over the page. Lights,
-- dimmers and switches count as on above 0, covers as open above 0 or while moving; only what the
-- room shows is counted. Temperature and humidity default to the area's devices of that kind
-- (the average of several, left out with none).
-- @param id string Unique widget identifier
-- @param area string Area name, or a list of names (one room under two names)
-- @param opts table Optional: {title=string, temperature=path|function()->number, humidity=path|function()->number, kinds, skip (as section:area), icon=string, link=dashboard id|/path|http(s) URL (opens it instead of the room), disabled=function()->bool (also disables the room's widgets), span=n, size=string, color=string}
-- @api public
function SectionHandle:room(id, area, opts)
    local sec = self._builder._sections[self._sectionIndex]
    if sec._isHeader then error('section:room("' .. tostring(id) .. '"): a room tile goes in a section, not the header', 2) end
    self._builder:_addWidget(self._sectionIndex, roomWidget(id, area, opts))
    self._builder._hasAreas = true
    return self
end

--- Conditional block: only execute fn if current time matches any of the given periods.
-- @param periods table Array of period names ("morning","afternoon","evening","night")
-- @param fn function Receives self (SectionHandle) as argument
-- @api public
function SectionHandle:when(periods, fn)
    self._builder:when(periods, fn)
    return self
end

--- Start a new section (delegates to DashboardBuilder:section).
-- @api public
function SectionHandle:section(name, icon, opts)
    return self._builder:section(name, icon, opts)
end

--- Start a header section (delegates to DashboardBuilder:header).
-- @api public
function SectionHandle:header()
    return self._builder:header()
end

--- Finalize and register the dashboard (delegates to DashboardBuilder:build).
-- @api public
function SectionHandle:build()
    return self._builder:build()
end

--- Set dashboard name (delegates to DashboardBuilder:name).
-- @api public
function SectionHandle:name(s)  self._builder:name(s);  return self end
--- Set dashboard theme (delegates to DashboardBuilder:theme).
-- @api public
function SectionHandle:theme(t) self._builder:theme(t); return self end
--- Set dashboard poll interval (delegates to DashboardBuilder:poll).
-- @api public
function SectionHandle:poll(n)  self._builder:poll(n);  return self end
--- Add dashboard to a tab group (delegates to DashboardBuilder:tabGroup).
-- @api public
function SectionHandle:tabGroup(g) self._builder:tabGroup(g); return self end

--- Set the page width (delegates to DashboardBuilder:width).
-- @api public
function SectionHandle:width(w) setWidth(self._builder, w, 3); return self end

-- ==========================================================================
-- DashboardBuilder
-- ==========================================================================

local DashboardBuilder = {}
DashboardBuilder.__index = DashboardBuilder

function DashboardBuilder:new(id)
    if type(id) ~= 'string' or id == '' then
        error('Dashboard.new() requires a non-empty string ID')
    end
    local b = {
        _id = id,
        _name = id,
        _icon = 'grid',
        _columns = 3,
        _auth = 'public',
        _pin = nil,
        _poll = 2,
        _theme = nil,
        _sections = {},    -- { {name, icon, widgets={}} }
        _currentSection = nil, -- index into _sections
        _groups = {},
        _groupSet = {},    -- dedup lookup
    }
    setmetatable(b, self)
    return b
end

--- Set the display name for this dashboard.
-- @param s string Display name shown in navigation
-- @api public
function DashboardBuilder:name(s)
    self._name = s
    return self
end

--- Set the icon for this dashboard.
-- @param s string Bootstrap icon name (e.g., "house", "thermometer", "camera-video")
-- @api public
function DashboardBuilder:icon(s)
    self._icon = s
    return self
end

--- Set the number of grid columns.
-- @param n number Number of columns (default 3)
-- @api public
function DashboardBuilder:columns(n)
    self._columns = n
    return self
end

--- Set the page width: 'full' fills the screen edge to edge (wall tablets), 'normal' (default) keeps wide screens centred.
-- @param w string "full" or "normal"
-- @api public
function DashboardBuilder:width(w)
    setWidth(self, w, 3)
    return self
end

--- Set the authentication mode ("public", "pin").
-- @param s string Auth mode: "public" (no auth) or "pin" (requires PIN)
-- @api public
function DashboardBuilder:auth(s)
    self._auth = s
    return self
end

--- Set the PIN code for pin-authenticated dashboards.
-- @param s string PIN code (used when auth="pin")
-- @api public
function DashboardBuilder:pin(s)
    self._pin = s
    return self
end

--- Set the poll interval in seconds.
-- @param n number Poll interval in seconds (default 2)
-- @api public
function DashboardBuilder:poll(n)
    self._poll = n
    return self
end

--- Set the dashboard theme.
-- @param t string Theme name (e.g., "dark", "light")
-- @api public
function DashboardBuilder:theme(t)
    self._theme = t
    return self
end

--- Add this dashboard to a named tab group for navigation.
-- @param name string Tab group name (dashboards with same group show as tabs)
-- @api public
function DashboardBuilder:tabGroup(name)
    if not self._groupSet[name] then
        self._groupSet[name] = true
        table.insert(self._groups, name)
    end
    return self
end

--- Start a header section (must be called before any section).
-- @api public
function DashboardBuilder:header()
    if #self._sections > 0 then
        error('header() must be called before any section()')
    end
    local sec = { name = '', icon = '', widgets = {}, _isHeader = true }
    table.insert(self._sections, sec)
    self._currentSection = #self._sections
    return SectionHandle:new(self, self._currentSection)
end

--- Start a new named section for grouping widgets.
-- @param name string Section title
-- @param icon string Bootstrap icon name
-- @param opts table Optional: {columns=number, covers=true (the header counts the section's covers open, moving and closed)}
-- @return SectionHandle Handle for adding widgets to this section
-- @api public
function DashboardBuilder:section(name, icon, opts)
    opts = opts or {}
    local sec = { name = name, icon = icon or '', widgets = {}, _columns = opts.columns, _covers = opts.covers == true or nil }
    table.insert(self._sections, sec)
    self._areaView = nil
    self._currentSection = #self._sections
    return SectionHandle:new(self, self._currentSection)
end

--- Add one section per area on a floor (expose(..., { area = "Salon", floor = "Parter" })),
-- each filled as section:area; areas with nothing to show are left out. Read each time the
-- dashboard is, so new areas show up. Start a section() to add widgets by hand after it.
-- With rooms = true: one section named after the floor with a room tile per area (section:room).
-- @param floor string Floor name
-- @param opts table Optional: {order={area, ...} (first, in this order; the rest by name), titles={[area]=title}, kinds, skip, rooms=true}
-- @api public
function DashboardBuilder:floor(floor, opts)
    local b = self:_floor(floor, opts) -- no tail call: errors count the levels
    return b
end

--- Add a room tile to the current section (see SectionHandle:room).
-- @param id string Unique widget identifier
-- @param area string Area name, or a list of names
-- @param opts table Optional: as SectionHandle:room
-- @api public
function DashboardBuilder:room(id, area, opts)
    if not self._currentSection then
        error('section:room: call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    if self._sections[self._currentSection]._isHeader then
        error('section:room("' .. tostring(id) .. '"): a room tile goes in a section, not the header', 2)
    end
    self:_addWidget(self._currentSection, roomWidget(id, area, opts))
    self._hasAreas = true
    return self
end

-- Errors name the module that called floor() on the builder or a section
function DashboardBuilder:_floor(floor, opts)
    if type(floor) ~= 'string' or floor == '' then
        error('dash:floor: expected a floor name, got ' .. type(floor), 3)
    end
    local spec = areaSpec('dash:floor', opts, FLOOR_KEYS, 4)
    spec.floor = floor
    table.insert(self._sections, { name = floor, icon = '', widgets = {}, _floor = spec })
    self._areaView = nil
    self._currentSection = nil
    self._hasAreas = true
    return self
end

-- Internal: add widget to a section
function DashboardBuilder:_addWidget(sectionIndex, widget)
    local sec = self._sections[sectionIndex]
    if not sec then
        error('No section at index ' .. tostring(sectionIndex))
    end
    -- _group_N and _section_N are the page's own keys, area: is section:area's; level 3: the module calling a widget method
    if widget.type ~= 'group' and type(widget.id) == 'string'
        and (widget.id:sub(1, 7) == '_group_' or widget.id:sub(1, 9) == '_section_') then
        error('widget id "' .. widget.id .. '": ids starting with _group_ or _section_ are reserved', 3)
    end
    if type(widget.id) == 'string' and widget.id:sub(1, #AREA_ID) == AREA_ID then
        error('widget id "' .. widget.id .. '": ids starting with "' .. AREA_ID .. '" are kept for section:area', 3)
    end
    table.insert(sec.widgets, widget)
    self._areaView = nil
end

--- Add a button widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "power", "door-open")
-- @param onTap function Callback fired on button press: function()
-- @param opts table Optional: {disabled=function()->bool, confirm=string}
-- @api public
function DashboardBuilder:button(id, label, icon, onTap, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'button', label = label, icon = icon,
        onTap = onTap, opts = opts or {}
    })
    return self
end

--- Add a toggle widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "lightbulb", "lamp")
-- @param getValue function Returns current state: function() -> boolean|number (truthy=on)
-- @param onToggle function Called with desired state: function(bool) — use switchOn/switchOff, NOT toggle()
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:toggle(id, label, icon, getValue, onToggle, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'toggle', label = label, icon = icon,
        getValue = getValue, onToggle = onToggle, opts = opts or {}
    })
    return self
end

--- Add a slider widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param min number Minimum slider value
-- @param max number Maximum slider value
-- @param getValue function Returns current value: function() -> number
-- @param onSet function Called with new value: function(number)
-- @param opts table Optional: {step=number, unit=string, icon=string, disabled=function()->bool}
-- @api public
function DashboardBuilder:slider(id, label, min, max, getValue, onSet, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'slider', label = label,
        min = min, max = max,
        getValue = getValue, onSet = onSet, opts = opts or {}
    })
    return self
end

--- Add a status widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "thermometer-half", "droplet")
-- @param getValue function Returns current value: function() -> number|string
-- @param unit string Unit suffix displayed after value (e.g., "°C", "%", "lx")
-- @param opts table Optional: {decimals=0-6 (a number is shown rounded to that many places), disabled=function()->bool, color=string|function()->string|nil}
-- @api public
function DashboardBuilder:status(id, label, icon, getValue, unit, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    checkDecimals('dash:status', id, opts)
    self:_addWidget(self._currentSection, {
        id = id, type = 'status', label = label, icon = icon,
        getValue = getValue, unit = unit or '', opts = opts or {}
    })
    return self
end

--- Add a link widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param url string|function URL string or function() -> string
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:link(id, label, icon, url, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'link', label = label, icon = icon,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a chart widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label (e.g., "Temperatura 24h")
-- @param metricPath string Metric path registered via metric() (e.g., "temp:salon", "salda:outsideAir")
-- @param opts table Optional: {range=string (any duration such as "30m", "2h", "24h", "7d"; default "2h"; 30m/2h/24h/7d have tuned refresh, others refresh every 60 s), unit=string, min=n, max=n (Y axis), fill=bool (default true), refresh=seconds, span=n, color=string}
-- @api public
function DashboardBuilder:chart(id, label, metricPath, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'chart', label = label,
        metricPath = metricPath, opts = opts or {}
    })
    return self
end

--- Add a calendar widget to the current section: a month grid and the day of a tapped date.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param cal table Calendar from Calendar.new(...):build(), or its id
-- @param opts table Optional: {view="month"|"day", tags={...}, schedules={ids drawn as 24 h bars in the day view} or true for all of the calendar's,
--   holidays=true (holiday names, on by default), namedays=false (name days in the day view),
--   sun=true (sunrise and sunset in the day view, with :location()), span=n}; what is off is not sent to the browser
-- @api public
function DashboardBuilder:calendar(id, label, cal, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, calendarWidget(id, label, cal, opts))
    return self
end

--- Add a select widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param options table Array of {value=any, label=string} or function() -> same
-- @param getValue function Returns current selected value: function() -> any
-- @param onSelect function Called with selected value: function(any)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:select(id, label, icon, options, getValue, onSelect, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'select', label = label, icon = icon,
        options = options, getValue = getValue, onSelect = onSelect, opts = opts or {}
    })
    return self
end

--- Add an image widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param url string|function Image URL string or function() -> string
-- @param opts table Optional: {refreshInterval=number (seconds)}
-- @api public
function DashboardBuilder:image(id, label, url, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'image', label = label,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a group label widget to the current section.
-- @param label string Group heading text
-- @param icon string Bootstrap icon name (optional)
-- @api public
function DashboardBuilder:group(label, icon)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = '_group_' .. tostring(#self._sections[self._currentSection].widgets),
        type = 'group', label = label, icon = icon, opts = {}
    })
    return self
end

--- Add a cover widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "arrows-expand")
-- @param fns table {getPosition=function()->number|nil, getState=function()->number|nil, open=function(), close=function(), stop=function(), set=function(position)}
--   Buttons up/stop/down call open/stop/close. With set, a 0-100% slider is shown under them.
--   Position 0 = closed, 100 = open, as in Grenton; nil (not reported yet) is shown as --.
--   getState lights the button of the movement: 0 = stopped, 1 = up, 2 = down.
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:cover(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'cover', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a cover group to the current section: one tile moving several covers, with their counts and average position.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param list table Covers (RemoteCover, VirtualCover) or their paths, _:byTag(...), or a function returning one (read each time; missing paths skipped)
--   Buttons up/stop/down call open/stop/close on each cover, the slider setPosition on those with a position,
--   the slat slider (shown when a cover reports getTilt) setTilt on those with a tilt; commands go out 50 ms apart
--   and a new movement (or slat) command drops what is left of the previous one; stop ends both.
-- @param opts table Optional: {icon=string (default "window"), disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function DashboardBuilder:covers(id, label, list, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets', 2)
    end
    if self._sections[self._currentSection]._isHeader then
        error('section:covers("' .. tostring(id) .. '"): a cover group goes in a section, not the header', 2)
    end
    self:_addWidget(self._currentSection, coversWidget(id, label, list, opts))
    return self
end

--- Add a media player to the current section: artwork, title and artist, previous/play-pause/next, volume, mute and power.
-- @param id string Unique widget identifier
-- @param label string Display label (e.g. the room)
-- @param src table A MusicCast device or a table of functions {getState, play?, pause?, next?, previous?, setVolume?, setMute?, on?, off?}
-- @param opts table Optional: {icon=string, disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function DashboardBuilder:player(id, label, src, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets', 2)
    end
    if self._sections[self._currentSection]._isHeader then
        error('section:player("' .. tostring(id) .. '"): a player goes in a section, not the header', 2)
    end
    self:_addWidget(self._currentSection, playerWidget(id, label, src, opts))
    return self
end

--- Add a weather forecast to the current section: the next days and a chart of the next hours.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param src function A function returning {days={...}, hours={...}, updated?} or nil, or the Weather plugin
-- @param opts table Optional: {days=1-7, hours=0-48, unit=string, icon=string, disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function DashboardBuilder:forecast(id, label, src, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets', 2)
    end
    if self._sections[self._currentSection]._isHeader then
        error('section:forecast("' .. tostring(id) .. '"): a forecast goes in a section, not the header', 2)
    end
    self:_addWidget(self._currentSection, forecastWidget(id, label, src, opts))
    return self
end

--- Add a dimmer widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "brightness-high")
-- @param fns table {get=function()->number(0-100), set=function(number 0-100), switchOn=function()?, switchOff=function()?, isOn=function()->bool? (default: get() > 0)}
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:dimmer(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'dimmer', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a color picker widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "palette")
-- @param fns table {getColor=function()->{r,g,b}, setColor=function(r,g,b), get?/set? brightness 0-100,
--   switchOn?, switchOff?, isOn?, getWhite?/setWhite? white 0-100}, or a RemoteRGBLight (white when it has one)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:color(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'color', label = label, icon = icon,
        fns = colorFns(fns), opts = opts or {}
    })
    return self
end

--- Add a thermostat to the current section: current temperature, target and +/- for the target.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string|nil Bootstrap icon name (default "thermometer-half")
-- @param fns table {getCurrent=function()->number|nil, getTarget=function()->number|nil, setTarget=function(number)?,
--   getState=function()->number|bool? (1/true active, 0/false off), getMode=function()->number? (0 manual, 1 holiday,
--   2 schedule, 3 heat-up)}, or a thermostat object (RemoteThermostat: getValue, getTarget, setSetpoint, getState, getMode)
--   Shows the current temperature and the target ("--" until reported); + and - change the target by step and
--   setTarget is called once, 1 s after the last tap; the chosen value shows until the thermostat reports a
--   new target, at most 90 s. Without setTarget (or opts.readonly) there are no buttons; with getMode the
--   target is set only in manual mode (0), as lib/panel does. The action endpoint refuses a set outside min..max.
-- @param opts table Optional: {min=5, max=35, step=0.5, unit="°C", readonly=bool or function()->bool (true: no buttons; a function locks them while it returns true), disabled=function()->bool, span=n, size=string, color=string}
-- @api public
function DashboardBuilder:thermostat(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    if self._sections[self._currentSection]._isHeader then
        error('section:thermostat("' .. tostring(id) .. '"): a thermostat goes in a section, not the header', 2)
    end
    self:_addWidget(self._currentSection, thermostatWidget(id, label, icon, fns, opts))
    return self
end

--- Conditional block: only execute fn if current time matches any of the given periods.
-- @param periods table|string Period name(s): "morning","afternoon","evening","night"
-- @param fn function Receives self (DashboardBuilder) as argument
-- @api public
function DashboardBuilder:when(periods, fn)
    if type(periods) == 'string' then periods = {periods} end
    local current = Dashboard.timeOfDay()
    for _, p in ipairs(periods) do
        if p == current then
            fn(self)
            return self
        end
    end
    return self
end

--- Finalize and register the dashboard in the global registry.
-- @api public
function DashboardBuilder:build()
    -- Validate unique widget IDs (skip auto-generated group labels)
    local seen = {}
    for _, sec in ipairs(self._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.type ~= 'group' then
                if seen[w.id] then
                    error('Duplicate widget ID "' .. w.id .. '" in dashboard "' .. self._id .. '"')
                end
                seen[w.id] = true
            end
        end
    end

    -- Store in global registry (idempotent — replaces previous)
    _dashboards[self._id] = self

    -- Track insertion order (avoid duplicates on rebuild)
    local found = false
    for _, oid in ipairs(_dashboardOrder) do
        if oid == self._id then found = true; break end
    end
    if not found then
        table.insert(_dashboardOrder, self._id)
    end

    return self
end

-- ==========================================================================
-- DashboardAPI — called from Go via LuaEngine.Execute()
-- ==========================================================================

local DashboardAPI = {}

-- Safe value getter: wraps getValue in pcall
local function safeGetValue(fn)
    if type(fn) ~= 'function' then return nil end
    local ok, val = pcall(fn)
    if ok then
        -- Remote CLU calls return strings via __go_send_command;
        -- convert numeric strings to numbers so JSON encodes them unquoted
        if type(val) == 'string' then
            local num = tonumber(val)
            if num then return num, nil end
        end
        return val, nil
    else
        return nil, tostring(val)
    end
end

-- A group's covers and what its tile shows, read once per request
local function coversState(dashId, w, cache)
    local st = cache[w]
    if st then return st end
    local covers = coversResolve(dashId, w)
    local entry, readings = coversEntry(covers)
    st = { covers = covers, entry = entry, readings = readings }
    cache[w] = st
    return st
end

-- Header counts of a section with covers = true: each cover once within groups; a cover tile
-- counts on its own (its fns cannot be matched to an object)
local function sectionCoverCounts(dashId, sec, cache)
    local seen, readings = {}, {}
    for _, w in ipairs(sec.widgets) do
        if w.type == 'cover' and type(w.fns) == 'table' and not seen[w.fns] then
            seen[w.fns] = true
            readings[#readings + 1] = { position = tonumber((safeGetValue(w.fns.getPosition))),
                state = tonumber((safeGetValue(w.fns.getState))) }
        elseif w.type == 'covers' then
            local st = coversState(dashId, w, cache)
            for i, c in ipairs(st.covers) do
                if not seen[c] then
                    seen[c] = true
                    readings[#readings + 1] = st.readings[i]
                end
            end
        end
    end
    return coverCounts(readings)
end

-- Colour errors already logged, by "dashboard/widget"
local _colorErrorLogged = {}

-- Safe colour: a string as is, a status tile's function's current string result, nil on error
local function safeColor(dashId, w)
    local c = w.opts.color
    if type(c) ~= 'function' then return c end
    if w.type ~= 'status' and w.type ~= 'chip' then return nil end
    local ok, val = pcall(c)
    if ok then
        if type(val) == 'string' then return val end
        return nil
    end
    local key = tostring(dashId) .. '/' .. tostring(w.id)
    if not _colorErrorLogged[key] then
        _colorErrorLogged[key] = true
        Logger:getShared():warn("[DASHBOARD] Color error for '%s': %s", key, tostring(val))
    end
    return nil
end

-- Safe disabled check
local function safeDisabled(opts)
    if not opts or type(opts.disabled) ~= 'function' then return false end
    local ok, val = pcall(opts.disabled)
    if ok then return val == true end
    return false
end

-- What fn gives goes into a class and a CSS value: an icon name, a colour name, #hex or rgb()/hsl()
local function chipIcon(v)
    if type(v) == 'string' and v:match('^[%w%-]+$') then return v end
end
local function chipColor(v)
    if type(v) ~= 'string' then return nil end
    if v:match('^%a+$') or v:match('^#%x+$') or v:match('^rgba?%([%d%.%%,%s]+%)$')
        or v:match('^hsla?%([%d%.%%,%s]+%)$') then
        return v
    end
end

-- A chip's state: {text, icon, color} as the page needs it, {error} when fn fails, nil hides it
local function chipState(dashId, w)
    local ok, val = pcall(w.chipFn)
    if not ok then return { error = tostring(val) } end
    if val == nil then return nil end
    local s = {}
    if type(val) == 'table' then
        -- no text: an icon-only chip (and a JSON object, never [])
        s.text = val.text ~= nil and tostring(val.text) or ''
        s.icon = chipIcon(val.icon)
        s.color = chipColor(val.color)
    elseif type(val) == 'string' or type(val) == 'number' then
        s.text = val
    else
        return { error = 'header:chip("' .. tostring(w.id) .. '"): fn must return a string, a table or nil, got ' .. type(val) }
    end
    if s.color == nil then s.color = chipColor(safeColor(dashId, w)) end
    return s
end

-- Problems with a room's temperature or humidity already logged, by "dashboard/widget/kind"
local _roomLogged = {}

-- A finite number to one decimal, or nil
local function round1n(v)
    v = tonumber(v)
    if v == nil or v - v ~= 0 then return nil end
    return math.floor(v * 10 + 0.5) / 10
end

-- A room's temperature or humidity: opts as a path (exposed, else in the registry) or a
-- function, else the average of the area's devices of that kind; nil when there is none
local function roomClimate(dashId, w, kind)
    local src = w['_' .. kind]
    local function fail(msg)
        local key = tostring(dashId) .. '/' .. tostring(w.id) .. '/' .. kind
        if not _roomLogged[key] then
            _roomLogged[key] = true
            Logger:getShared():warn("[DASHBOARD] Room '%s/%s': %s %s", tostring(dashId), tostring(w.id), kind, msg)
        end
    end
    if type(src) == 'function' then
        local ok, v = pcall(src)
        if not ok then fail('failed: ' .. tostring(v)); return nil end
        return round1n(v)
    elseif type(src) == 'string' then
        local h = ExposedObjects and ExposedObjects:get(src)
        local o = h or (_registry and _registry:get(src))
        if type(o) ~= 'table' or type(o.getValue) ~= 'function' then fail("path '" .. src .. "' not found"); return nil end
        local ok, v = pcall(o.getValue, o)
        return ok and round1n(v) or nil
    end
    local sum, n = 0, 0
    for _, h in ipairs(w._sensors[kind]) do
        local v = round1n(h:getValue())
        if v then sum = sum + v; n = n + 1 end
    end
    if n > 0 then return round1n(sum / n) end
end

-- What a room tile shows: lights, dimmers and switches on, covers open (moving counts), its climate
local function roomState(dashId, w)
    local on, readings = 0, {}
    for _, x in ipairs(w._items) do
        local h = x._h
        if h.type == 'cover' then
            readings[#readings + 1] = coverReading(h.object)
        elseif h.type == 'light' or h.type == 'dimmer' or h.type == 'switch' then
            local ok, v = pcall(h.getValue, h)
            if ok and isOn(v) then on = on + 1 end
        end
    end
    local c = coverCounts(readings)
    return { on = on, open = c.open + c.moving, moving = c.moving,
        temperature = roomClimate(dashId, w, 'temperature'), humidity = roomClimate(dashId, w, 'humidity') }
end

--- Internal: resolve a dashboard entry. For dynamic dashboards, rebuilds fresh.
-- bare: without filling area sections (auth and PIN need no widgets)
local function resolveDashboard(id, bare)
    local entry = _dashboards[id]
    if not entry then return nil end
    if not entry._isDynamic then return bare and entry or expandAreas(entry) end

    -- Dynamic: create fresh builder, call user function, auto-build
    local builder = DashboardBuilder:new(id)
    local ok, err = pcall(entry._builderFn, builder)
    if not ok then
        Logger:getShared():warn("[DASHBOARD] Dynamic build error for '" .. id .. "': " .. tostring(err))
        return nil
    end
    -- Auto-build if the function didn't call build()
    if not _dashboards[id] or _dashboards[id]._isDynamic then
        builder:build()
    end
    local result = _dashboards[id]
    -- Restore the dynamic sentinel so next call rebuilds again
    _dashboards[id] = entry
    return bare and result or expandAreas(result)
end

--- Get auth type for a dashboard (lightweight, no getValue calls)
-- @param id string Dashboard ID
-- @return string JSON with auth type
function DashboardAPI.getAuth(id)
    local dash = resolveDashboard(id, true)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end
    return JSON.encode({ auth = dash._auth })
end

--- What the home-screen app of a dashboard needs: no widgets or values, any auth may read it
-- @param id string Dashboard ID
-- @return string JSON {name, auth, width, background} or {error}
function DashboardAPI.getAppInfo(id)
    local dash = resolveDashboard(id, true)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end
    local theme = type(dash._theme) == 'table' and dash._theme or {}
    return JSON.encode({
        name = tostring(dash._name),
        auth = dash._auth,
        width = dash._width,
        background = type(theme.background) == 'string' and theme.background or nil,
    })
end

--- Whether the dashboard draws this metric (chart or status sparkline)
-- @return string JSON {used=bool, ranges=[...]} or {error=...}
function DashboardAPI.usesMetric(id, path)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end
    local ranges = {}
    for _, sec in ipairs(dash._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.type == 'chart' and w.metricPath == path then
                table.insert(ranges, w.opts.range or '2h')
            elseif w.type == 'status' and w.opts and w.opts.spark == path then
                table.insert(ranges, w.opts.sparkRange or '2h')
            end
        end
    end
    if #ranges == 0 then return JSON.encode({ used = false }) end
    return JSON.encode({ used = true, ranges = ranges })
end

local CALENDAR_CHIPS = 3      -- a month cell shows 3 events and "+n"
local CALENDAR_DAY_MAX = 300  -- events in one day view

--- Occurrences of a calendar widget's calendar for the days from..to ('YYYY-MM-DD', at most 42)
-- @return string JSON {today, now, ready, days=[{date, weekday, workday, holiday}], items=[...]},
--   or {error, status} with the HTTP status of a bad request
function DashboardAPI.calendarRange(id, widgetId, from, to)
    local function fail(status, msg) return JSON.encode({ error = msg, status = status }) end
    local dash = resolveDashboard(id)
    if not dash then return fail(404, 'Dashboard not found') end
    local w
    for _, sec in ipairs(dash._sections) do
        for _, x in ipairs(sec.widgets) do
            if x.id == widgetId and x.type == 'calendar' then w = x end
        end
    end
    if not w then return fail(403, 'calendar widget not on this dashboard') end
    local cal = Calendar.get(w.calendar)
    if not cal then return fail(404, 'calendar "' .. w.calendar .. '" not built') end
    local okA, a = pcall(cal.day, cal, from)
    local okB, b = pcall(cal.day, cal, to)
    if not okA or not okB or b.starts < a.starts then return fail(400, 'from and to expected as YYYY-MM-DD') end
    if a.date < '2000-01-01' or b.date > '2100-12-31' then return fail(400, 'dates from 2000 to 2100') end
    local today = cal:day()

    -- what the author switched off is not sent at all (a public page would show the house's location)
    local showHol, showNames, showSun = w.opts.holidays ~= false, w.opts.namedays == true, w.opts.sun ~= false
    local function minute(ts) return ts and math.floor(ts / 60 + 0.5) * 60 end
    local days = {}
    local d = a
    while d.starts <= b.starts do
        if #days == 42 then return fail(400, 'at most 42 days') end
        local one = a.date == b.date
        local sun = one and showSun and cal:sun(d.date) or nil
        days[#days + 1] = { date = d.date, weekday = d.weekday, workday = d.workday,
            holiday = showHol and d.holiday or false, holidayName = showHol and d.holidayName or nil,
            namedays = one and showNames and d.namedays or nil,
            sunrise = sun and minute(sun.sunrise), sunset = sun and minute(sun.sunset), polar = sun and sun.polar }
        d = cal:day(d.ends)
    end
    local items = {}
    local ok, list = pcall(cal.between, cal, a.date, b.date, { tags = w.opts.tags })
    if not ok then
        Logger:getShared():error('[DASHBOARD] %s calendar %s: %s', tostring(id), tostring(widgetId), tostring(list))
        return fail(500, 'calendar unavailable')
    end
    -- a range sends what its cells draw and each day's count: a busy calendar stays small
    -- (and under gopher-lua's limit on the values table.concat can join)
    local byDate, shown = {}, {}
    for _, day in ipairs(days) do byDate[day.date] = day; day.count = 0; shown[day.date] = 0 end
    local cap = a.date == b.date and CALENDAR_DAY_MAX or CALENDAR_CHIPS
    for _, e in ipairs(list) do
        local keep = false
        for _, day in ipairs(days) do
            if day.date >= e.date and day.date <= (e.last or e.date) then
                day.count = day.count + 1
                if shown[day.date] < cap then shown[day.date] = shown[day.date] + 1; keep = true end
            end
        end
        if keep then
            items[#items + 1] = { id = e.id, title = e.title, icon = e.icon, at = e.at, ends = e.ends,
                allDay = e.allDay, date = e.date, last = e.last, paused = e.paused, source = e.source }
        end
    end
    -- schedules as 24 h bars: only for a single day (the day view)
    local schedules
    if w.opts.schedules and a.date == b.date and cal:isReady() then
        local titles, ids = {}, w.opts.schedules
        if ids == true then ids = {} end
        for _, sc in ipairs(cal:info().schedules) do
            titles[sc.id] = sc.title
            if w.opts.schedules == true then ids[#ids + 1] = sc.id end
        end
        schedules = {}
        for _, sid in ipairs(ids) do
            local okS, segs = pcall(cal.segments, cal, sid, a.date)
            if okS then
                for _, sg in ipairs(segs) do sg.value = tostring(sg.value) end
                schedules[#schedules + 1] = { id = sid, title = titles[sid], segments = segs }
            end
        end
    end
    return JSON.encode({ today = today.date, now = math.floor(__go_time_ms() / 1000), zone = datetime.timezone(),
        ready = cal:isReady(), days = days, items = items, schedules = schedules })
end

--- List all dashboards with widget details
-- @return string JSON array of dashboard summaries including widgets array
function DashboardAPI.list()
    local result = {}
    -- Collect IDs first to avoid iterator issues with dynamic resolution
    local ids = {}
    for id in pairs(_dashboards) do
        table.insert(ids, id)
    end
    table.sort(ids)

    for _, id in ipairs(ids) do
        local dash = resolveDashboard(id)
        if dash then
            local widgetCount = 0
            local widgets = {}
            -- visible sections only: a room tile's widgets are not on the page until it is tapped
            for _, sec in ipairs(dash._sections) do
                if not sec._room then
                    widgetCount = widgetCount + #sec.widgets
                    for _, w in ipairs(sec.widgets) do
                        table.insert(widgets, {
                            id = w.id,
                            type = w.type,
                            label = w.label,
                            icon = w.icon,
                        })
                    end
                end
            end
            table.insert(result, {
                id = id,
                name = dash._name,
                icon = dash._icon,
                auth = dash._auth,
                widgetCount = widgetCount,
                widgets = widgets,
            })
        end
    end
    return JSON.encode(result)
end

--- Get full dashboard definition with current values
-- @param id string Dashboard ID
-- @return string JSON definition or error
function DashboardAPI.getDefinition(id)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end

    local sections = {}
    local widgets = {}
    local covers = {}

    for si, sec in ipairs(dash._sections) do
        local secDef = { name = sec.name, icon = sec.icon }
        if sec._isHeader then secDef.isHeader = true end
        if sec._columns then secDef.columns = sec._columns end
        if sec._covers then secDef.covers = sectionCoverCounts(id, sec, covers) end
        -- a room tile's overlay: drawn when the tile is tapped
        if sec._room then secDef.room = sec._room end
        table.insert(sections, secDef)

        for _, w in ipairs(sec.widgets) do
            local widget = {
                id = w.id,
                type = w.type,
                label = w.label,
                icon = w.icon,
                section = si - 1, -- 0-indexed for JS
                span = w.opts.span,
                size = w.opts.size,
                color = w.type ~= 'chip' and safeColor(id, w) or nil,
            }

            -- Get current value
            if w.type == 'toggle' or w.type == 'slider' or w.type == 'status' or w.type == 'select' then
                local val, err = safeGetValue(w.getValue)
                if err then
                    widget.value = JSON.null
                    widget.error = err
                else
                    widget.value = val
                end
            end

            -- Slider-specific fields
            if w.type == 'slider' then
                widget.min = w.min
                widget.max = w.max
            end

            -- Status unit + sparkline
            if w.type == 'status' then
                widget.unit = w.unit
                widget.decimals = w.opts.decimals
                if w.opts.spark then
                    widget.spark = w.opts.spark
                    widget.sparkRange = w.opts.sparkRange or '2h'
                end
            end

            -- Link URL
            if w.type == 'link' then
                widget.url = w.url
            end

            -- Chip: the link, and its state as a poll gives it; none while fn gives nil
            if w.type == 'chip' then
                widget.link = w.link
                widget.chip = chipState(id, w)
            end

            -- Chart fields (no getValue — frontend fetches directly)
            if w.type == 'chart' then
                widget.metricPath = w.metricPath
                widget.range = w.opts.range or '2h'
                widget.refresh = w.opts.refresh
                widget.unit = w.opts.unit or ''
                widget.min = w.opts.min
                widget.max = w.opts.max
                widget.fill = w.opts.fill
            end

            if w.type == 'calendar' then
                widget.calendar = w.calendar
                widget.view = w.opts.view or 'month'
                -- defaults; a viewer can switch them on the widget
                widget.holidays = w.opts.holidays ~= false
                widget.namedays = w.opts.namedays == true
                widget.sun = w.opts.sun ~= false
            end

            -- Select widget: options list
            if w.type == 'select' then
                -- Normalize options to {value, label} format
                local opts_list = {}
                for _, opt in ipairs(w.options or {}) do
                    if type(opt) == 'string' then
                        table.insert(opts_list, { value = opt, label = opt })
                    elseif type(opt) == 'table' then
                        table.insert(opts_list, { value = opt.value, label = opt.label or opt.value })
                    end
                end
                widget.options = opts_list
            end

            -- Image widget: URL (static string or dynamic function)
            if w.type == 'image' then
                if type(w.url) == 'function' then
                    local ok, u = pcall(w.url)
                    widget.url = ok and u or ''
                else
                    widget.url = w.url or ''
                end
                widget.refresh = w.opts.refresh
                widget.height = w.opts.height
                widget.align = w.opts.align
            end

            -- Group label: spans full width, no interaction
            if w.type == 'group' then
                -- label and icon already set above
            end

            -- Cover widget
            if w.type == 'cover' then
                local fns = w.fns or {}
                widget.position = safeGetValue(fns.getPosition)
                local state = safeGetValue(fns.getState)
                widget.state = (state ~= nil) and state or 0
                widget.capabilities = {
                    set = type(fns.set) == 'function'
                }
            end

            -- Room tile: what is on, its climate, where a tap goes when not to the room
            if w.type == 'room' then
                for k, v in pairs(roomState(id, w)) do widget[k] = v end
                widget.link = w.link
            end

            -- Cover group: counts, average position and tilt
            if w.type == 'covers' then
                for k, v in pairs(coversState(id, w, covers).entry) do widget[k] = v end
            end

            -- Media player: its state and the controls it has
            if w.type == 'player' then
                for k, v in pairs(playerState(w)) do widget[k] = v end
                widget.capabilities = playerCapabilities(w.fns)
            end

            -- Forecast: its days and hours, and how many hours the chart shows
            if w.type == 'forecast' then
                for k, v in pairs(forecastState(w)) do widget[k] = v end
                widget.unit = w.opts.unit or '°C'
                widget.chartHours = w.opts.hours or 24
                widget.zone = datetime.timezone()
            end

            -- Thermostat: current, target, state, mode; its range and whether it can be set at all
            if w.type == 'thermostat' then
                for k, v in pairs(thermostatState(w)) do widget[k] = v end
                widget.min, widget.max, widget.step, widget.unit = w.min, w.max, w.step, w.unit
                widget.capabilities = { set = type(w.fns.setTarget) == 'function' }
            end

            -- Dimmer widget
            if w.type == 'dimmer' then
                local fns = w.fns or {}
                widget.value = safeGetValue(fns.get)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    widget.on = (isOn == true)
                elseif widget.value ~= nil then
                    widget.on = (widget.value > 0)
                end
                widget.capabilities = {
                    toggle = type(fns.switchOn) == 'function' and type(fns.switchOff) == 'function'
                }
            end

            -- Color picker widget
            if w.type == 'color' then
                local fns = w.fns or {}
                local clr = safeGetValue(fns.getColor)
                if type(clr) == 'table' then
                    widget.rgb = {
                        r = clr.r or clr[1] or 0,
                        g = clr.g or clr[2] or 0,
                        b = clr.b or clr[3] or 0
                    }
                end
                widget.brightness = safeGetValue(fns.get)
                widget.white = safeGetValue(fns.getWhite)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    widget.on = (isOn == true)
                elseif widget.brightness ~= nil then
                    widget.on = (widget.brightness > 0)
                end
                local presets = (w.opts and w.opts.presets) or {
                    {label='Warm White', r=255, g=180, b=100},
                    {label='Cool White', r=224, g=232, b=255},
                    {label='Red', r=255, g=68, b=68},
                    {label='Green', r=68, g=255, b=68},
                    {label='Blue', r=68, g=68, b=255},
                    {label='Purple', r=170, g=68, b=255},
                    {label='Orange', r=255, g=140, b=0},
                    {label='Cyan', r=0, g=206, b=209},
                }
                widget.presets = presets
                widget.capabilities = {
                    brightness = type(fns.get) == 'function' and type(fns.set) == 'function',
                    white = type(fns.getWhite) == 'function' and type(fns.setWhite) == 'function',
                    toggle = type(fns.switchOn) == 'function' and type(fns.switchOff) == 'function'
                }
            end

            -- Confirm dialog text (for buttons)
            if w.opts.confirm then
                widget.confirm = w.opts.confirm
            end

            -- Disabled state
            widget.disabled = safeDisabled(w.opts)

            table.insert(widgets, widget)
        end
    end

    local def = {
        id = dash._id,
        name = dash._name,
        icon = dash._icon,
        columns = dash._columns,
        auth = dash._auth,
        poll = dash._poll,
        theme = dash._theme,
        width = dash._width,
        period = Dashboard.timeOfDay(),
        sections = sections,
        widgets = widgets,
    }

    -- Compute group tabs for navigation
    if dash._groups and #dash._groups > 0 then
        local groupTabs = {}
        for _, grp in ipairs(dash._groups) do
            local tabs = {}
            for _, otherId in ipairs(_dashboardOrder) do
                local otherDb = _dashboards[otherId]
                if otherDb then
                    local otherGroups = otherDb._groups or {}
                    -- For dynamic dashboards, resolve to read _groups
                    if otherDb._isDynamic then
                        local resolved = resolveDashboard(otherId, true)
                        if resolved then
                            otherGroups = resolved._groups or {}
                            -- Also read name/icon from resolved
                            for _, otherGrp in ipairs(otherGroups) do
                                if otherGrp == grp then
                                    tabs[#tabs+1] = {
                                        id = otherId,
                                        name = resolved._name or otherId,
                                        icon = resolved._icon or 'grid',
                                        active = (otherId == id)
                                    }
                                    break
                                end
                            end
                        end
                    else
                        for _, otherGrp in ipairs(otherGroups) do
                            if otherGrp == grp then
                                tabs[#tabs+1] = {
                                    id = otherId,
                                    name = otherDb._name or otherId,
                                    icon = otherDb._icon or 'grid',
                                    active = (otherId == id)
                                }
                                break
                            end
                        end
                    end
                end
            end
            if #tabs >= 2 then
                groupTabs[#groupTabs+1] = { name = grp, tabs = tabs }
            end
        end
        if #groupTabs > 0 then
            def.groupTabs = groupTabs
        end
    end

    return JSON.encode(def)
end

--- Get widget values for polling
-- @param id string Dashboard ID
-- @return string JSON map of widgetId -> {value, disabled}
function DashboardAPI.getValues(id)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end

    local result = {}
    local covers = {}
    for si, sec in ipairs(dash._sections) do
        -- a section's cover counts, by its 0-based index as in the definition
        if sec._covers then result['_section_' .. (si - 1)] = sectionCoverCounts(id, sec, covers) end
        for _, w in ipairs(sec.widgets) do
            if w.type == 'covers' then
                local entry = coversState(id, w, covers).entry
                entry.disabled = safeDisabled(w.opts)
                result[w.id] = entry
            elseif w.type == 'room' then
                local entry = roomState(id, w)
                entry.disabled = safeDisabled(w.opts)
                result[w.id] = entry
            elseif w.type == 'player' or w.type == 'forecast' then
                local entry = w.type == 'player' and playerState(w) or forecastState(w)
                entry.disabled = safeDisabled(w.opts)
                result[w.id] = entry
            elseif w.type == 'thermostat' then
                local entry = thermostatState(w)
                entry.disabled = safeDisabled(w.opts)
                result[w.id] = entry
            elseif w.type == 'chip' then
                -- a hidden chip is left out; disabled only when it can change
                local entry = chipState(id, w)
                if entry then
                    if type(w.opts.disabled) == 'function' then entry.disabled = safeDisabled(w.opts) end
                    result[w.id] = entry
                end
            elseif w.getValue then
                local entry = {}
                local val, err = safeGetValue(w.getValue)
                if err then
                    entry.value = JSON.null
                    entry.error = err
                else
                    entry.value = val
                end
                entry.disabled = safeDisabled(w.opts)
                -- false: the function gave no colour, the page drops it
                if w.type == 'status' and type(w.opts.color) == 'function' then
                    entry.color = safeColor(id, w) or false
                end
                result[w.id] = entry
            elseif w.type == 'cover' then
                local fns = w.fns or {}
                local entry = { disabled = safeDisabled(w.opts) }
                entry.position = safeGetValue(fns.getPosition)
                local state = safeGetValue(fns.getState)
                entry.state = (state ~= nil) and state or 0
                result[w.id] = entry
            elseif w.type == 'dimmer' then
                local fns = w.fns or {}
                local entry = { disabled = safeDisabled(w.opts) }
                entry.value = safeGetValue(fns.get)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    entry.on = (isOn == true)
                elseif entry.value ~= nil then
                    entry.on = (entry.value > 0)
                end
                result[w.id] = entry
            elseif w.type == 'color' then
                local fns = w.fns or {}
                local entry = { disabled = safeDisabled(w.opts) }
                local clr = safeGetValue(fns.getColor)
                if type(clr) == 'table' then
                    entry.rgb = {
                        r = clr.r or clr[1] or 0,
                        g = clr.g or clr[2] or 0,
                        b = clr.b or clr[3] or 0
                    }
                end
                entry.brightness = safeGetValue(fns.get)
                entry.white = safeGetValue(fns.getWhite)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    entry.on = (isOn == true)
                elseif entry.brightness ~= nil then
                    entry.on = (entry.brightness > 0)
                end
                result[w.id] = entry
            elseif w.opts and type(w.opts.disabled) == 'function' then
                -- Button with disabled callback
                result[w.id] = { disabled = safeDisabled(w.opts) }
            end
        end
    end

    return JSON.encode(result)
end

--- Execute a widget action
-- @param id string Dashboard ID
-- @param widgetId string Widget ID
-- @param action string Action type (tap, toggle, set)
-- @param value any Action value (for toggle: bool, for set: number)
-- @return string JSON result
function DashboardAPI.executeAction(id, widgetId, action, value)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end

    -- Find widget by ID
    local widget = nil
    for _, sec in ipairs(dash._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.id == widgetId then
                widget = w
                break
            end
        end
        if widget then break end
    end

    if not widget then
        return JSON.encode({ error = 'Widget not found' })
    end

    -- Check disabled
    if safeDisabled(widget.opts) then
        return JSON.encode({ error = 'Widget is disabled' })
    end

    -- Validate action matches widget type
    if widget.type == 'button' then
        if action ~= 'tap' then
            return JSON.encode({ error = 'Invalid action for button' })
        end
        local ok, ret = pcall(widget.onTap)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        -- onTap can return a string message for toast notification
        local result = { success = true }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'toggle' then
        if action ~= 'toggle' then
            return JSON.encode({ error = 'Invalid action for toggle' })
        end
        local boolVal = value == true or value == 'true'
        local ok, ret = pcall(widget.onToggle, boolVal)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        local result = { success = true, value = boolVal }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'slider' then
        if action ~= 'set' then
            return JSON.encode({ error = 'Invalid action for slider' })
        end
        local numVal = tonumber(value)
        if not numVal then
            return JSON.encode({ error = 'Value must be a number' })
        end
        if numVal < widget.min or numVal > widget.max then
            return JSON.encode({ error = 'Value out of range [' .. widget.min .. ', ' .. widget.max .. ']' })
        end
        local ok, ret = pcall(widget.onSet, numVal)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        local result = { success = true, value = numVal }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'select' then
        if action ~= 'select' then
            return JSON.encode({ error = 'Invalid action for select' })
        end
        local ok, ret = pcall(widget.onSelect, value)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        local result = { success = true, value = value }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'status' then
        return JSON.encode({ error = 'Read-only widget' })

    elseif widget.type == 'link' then
        return JSON.encode({ error = 'Link widget has no server action' })

    elseif widget.type == 'image' then
        return JSON.encode({ error = 'Image widget has no server action' })

    elseif widget.type == 'group' then
        return JSON.encode({ error = 'Group label has no server action' })

    elseif widget.type == 'chip' then
        return JSON.encode({ error = 'Chip has no server action' })

    elseif widget.type == 'room' then
        return JSON.encode({ error = 'Room tile has no server action' })

    elseif widget.type == 'cover' then
        local fns = widget.fns or {}
        if action == 'open' then
            if type(fns.open) ~= 'function' then return JSON.encode({ error = 'No open function' }) end
            local ok, ret = pcall(fns.open)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'close' then
            if type(fns.close) ~= 'function' then return JSON.encode({ error = 'No close function' }) end
            local ok, ret = pcall(fns.close)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'stop' then
            if type(fns.stop) ~= 'function' then return JSON.encode({ error = 'No stop function' }) end
            local ok, ret = pcall(fns.stop)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'set' then
            if type(fns.set) ~= 'function' then return JSON.encode({ error = 'No set function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.set, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        else
            return JSON.encode({ error = 'Invalid action for cover' })
        end

    elseif widget.type == 'covers' then
        local covers = coversResolve(id, widget)
        local fn, numVal
        if action == 'open' or action == 'close' or action == 'stop' then
            fn = function(c) c[action](c) end
        elseif action == 'set' or action == 'tilt' then
            numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            local max = action == 'set' and 100 or 90
            if numVal < 0 or numVal > max then return JSON.encode({ error = 'Value out of range [0, ' .. max .. ']' }) end
            local only = {}
            for _, c in ipairs(covers) do
                if action == 'set' and coverCanSet(c) or action == 'tilt' and coverTilt(c) ~= nil then only[#only + 1] = c end
            end
            if #only == 0 then
                return JSON.encode({ error = action == 'set' and 'No cover with a position' or 'No cover with a tilt' })
            end
            covers = only
            if action == 'set' then
                fn = function(c) c:setPosition(numVal) end
            else
                fn = function(c) c:setTilt(numVal) end
            end
        else
            return JSON.encode({ error = 'Invalid action for covers' })
        end
        -- movement and slats are separate series; stop ends both
        local key = tostring(id) .. '/' .. tostring(widgetId)
        if action == 'stop' then coversCancel(key .. '/tilt') end
        coversSeries(key .. (action == 'tilt' and '/tilt' or '/move'), covers, fn,
            function() return safeDisabled(widget.opts) end)
        if action == 'set' then return JSON.encode({ success = true, value = numVal }) end
        return JSON.encode({ success = true })

    elseif widget.type == 'thermostat' then
        if action ~= 'set' then return JSON.encode({ error = 'Invalid action for thermostat' }) end
        local fns = widget.fns
        if type(fns.setTarget) ~= 'function' then return JSON.encode({ error = 'Read-only thermostat' }) end
        if widget.readonly then
            local ok, ro = pcall(widget.readonly)
            if not ok or ro then return JSON.encode({ error = 'Read-only thermostat' }) end
        end
        if type(value) ~= 'number' or value - value ~= 0 then return JSON.encode({ error = 'Value must be a number' }) end
        if value < widget.min or value > widget.max then
            return JSON.encode({ error = 'Value out of range [' .. widget.min .. ', ' .. widget.max .. ']' })
        end
        if not thermostatCanSet(fns, widget.readonly) then return JSON.encode({ error = 'Thermostat is not in manual mode' }) end
        local ok, ret = pcall(fns.setTarget, value)
        if not ok then return JSON.encode({ error = tostring(ret) }) end
        return JSON.encode({ success = true, value = value })

    elseif widget.type == 'player' then
        local name, arg
        if action == 'play' or action == 'pause' or action == 'next' or action == 'previous' then
            name = action
        elseif action == 'volume' then
            arg = tonumber(value)
            if not arg or arg ~= arg then return JSON.encode({ error = 'Value must be a number' }) end
            if arg < 0 or arg > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            name = 'setVolume'
        elseif action == 'mute' or action == 'power' then
            if value == true or value == 'true' then arg = true
            elseif value == false or value == 'false' then arg = false
            else return JSON.encode({ error = 'Value must be true or false' }) end
            name = action == 'mute' and 'setMute' or (arg and 'on' or 'off')
        else
            return JSON.encode({ error = 'Invalid action for player' })
        end
        local fn = widget.fns[name]
        if type(fn) ~= 'function' then return JSON.encode({ error = 'No ' .. name .. ' function' }) end
        local ok, ret = pcall(fn, arg)
        if not ok then return JSON.encode({ error = tostring(ret) }) end
        if action == 'volume' then return JSON.encode({ success = true, value = arg }) end
        return JSON.encode({ success = true })

    elseif widget.type == 'dimmer' then
        local fns = widget.fns or {}
        if action == 'set' then
            if type(fns.set) ~= 'function' then return JSON.encode({ error = 'No set function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.set, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        elseif action == 'switchOn' then
            if type(fns.switchOn) ~= 'function' then return JSON.encode({ error = 'No switchOn function' }) end
            local ok, ret = pcall(fns.switchOn)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'switchOff' then
            if type(fns.switchOff) ~= 'function' then return JSON.encode({ error = 'No switchOff function' }) end
            local ok, ret = pcall(fns.switchOff)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        else
            return JSON.encode({ error = 'Invalid action for dimmer' })
        end

    elseif widget.type == 'color' then
        local fns = widget.fns or {}
        if action == 'setColor' then
            if type(fns.setColor) ~= 'function' then return JSON.encode({ error = 'No setColor function' }) end
            if type(value) ~= 'table' then return JSON.encode({ error = 'Value must be {r, g, b}' }) end
            local function channel(v) return math.floor(math.max(0, math.min(255, tonumber(v) or 0)) + 0.5) end
            local ok, ret = pcall(fns.setColor, channel(value.r), channel(value.g), channel(value.b))
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'set' then
            if type(fns.set) ~= 'function' then return JSON.encode({ error = 'No set function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.set, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        elseif action == 'setWhite' then
            if type(fns.setWhite) ~= 'function' then return JSON.encode({ error = 'No setWhite function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.setWhite, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        elseif action == 'switchOn' then
            if type(fns.switchOn) ~= 'function' then return JSON.encode({ error = 'No switchOn function' }) end
            local ok, ret = pcall(fns.switchOn)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'switchOff' then
            if type(fns.switchOff) ~= 'function' then return JSON.encode({ error = 'No switchOff function' }) end
            local ok, ret = pcall(fns.switchOff)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        else
            return JSON.encode({ error = 'Invalid action for color' })
        end
    end

    return JSON.encode({ error = 'Unknown widget type' })
end

--- Verify PIN for a dashboard
-- @param id string Dashboard ID
-- @param pin string PIN to verify
-- @return string JSON result
function DashboardAPI.verifyPin(id, pin)
    local dash = resolveDashboard(id, true)
    if not dash then
        return JSON.encode({ valid = false })
    end
    if dash._auth ~= 'pin' or not dash._pin then
        return JSON.encode({ valid = false })
    end
    -- :pin(1234) is a number, the typed PIN a string
    return JSON.encode({ valid = (tostring(pin) == tostring(dash._pin)) })
end

--- PIN of a 'pin' dashboard, for checking cookies server-side (never sent to pages)
-- @return string JSON {pin=...} or {}
function DashboardAPI.getPin(id)
    local dash = resolveDashboard(id, true)
    if not dash or dash._auth ~= 'pin' or not dash._pin then
        return JSON.encode({})
    end
    return JSON.encode({ pin = tostring(dash._pin) })
end

-- ==========================================================================
-- Module interface
-- ==========================================================================

local Dashboard = {}

--- Create a new DashboardBuilder for the given ID.
-- @api public
function Dashboard.new(id)
    return DashboardBuilder:new(id)
end

--- Register a dynamic dashboard that rebuilds on every definition request.
-- The builder function receives a fresh DashboardBuilder and should configure it.
-- build() is called automatically if the function doesn't call it.
-- @param id string Dashboard ID
-- @param builderFn function(DashboardBuilder) Builder function
-- @api public
function Dashboard.dynamic(id, builderFn)
    if type(id) ~= 'string' or id == '' then
        error('Dashboard.dynamic() requires a non-empty string ID')
    end
    if type(builderFn) ~= 'function' then
        error('Dashboard.dynamic() requires a builder function')
    end
    -- Store a sentinel with the builder function
    _dashboards[id] = { _isDynamic = true, _builderFn = builderFn, _id = id }

    -- Track insertion order
    local found = false
    for _, oid in ipairs(_dashboardOrder) do
        if oid == id then found = true; break end
    end
    if not found then
        table.insert(_dashboardOrder, id)
    end
end

--- Return current time-of-day period name.
-- @return string "morning"|"afternoon"|"evening"|"night"
-- @api public
function Dashboard.timeOfDay()
    local h = tonumber(os.date("%H"))
    if h >= 6 and h < 12 then return "morning"
    elseif h >= 12 and h < 17 then return "afternoon"
    elseif h >= 17 and h < 22 then return "evening"
    else return "night" end
end

--- Return human-friendly label for a period.
-- @api public
function Dashboard.periodLabel(period)
    local labels = {
        morning = "Rano", afternoon = "Popołudnie",
        evening = "Wieczór", night = "Noc",
    }
    return labels[period] or period
end

-- Attach API for Go calls
Dashboard.API = DashboardAPI

return Dashboard
