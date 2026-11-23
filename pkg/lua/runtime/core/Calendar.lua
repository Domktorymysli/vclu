--- Calendar - house calendar: events for people and schedules of values, defined in Lua.
--
--   local dom = Calendar.new('dom'):holidays('pl')
--       :event('bio', { title = 'Bio', every = 'tu', weeks = 2, from = '2026-10-27',
--           time = '06:30', remind = '-12h' })
--       :schedule('taryfa', { default = 'droga',
--           { every = 'workday', hours = '13:00-15:00', value = 'tania' } })
--       :build()
--   dom:on('start', 'bio', function(e) end)
--   dom:value('taryfa')
--
-- Rules are expanded in Go (pkg/calendar); this file keeps the calendars, fires their
-- events on timers and stores what users change at runtime (added events, skips, pauses)
-- in Persist. Nothing fires before the clock is trusted (NTP), and moments skipped by a
-- clock jump do not fire later.
-- @module core.Calendar

local Calendar = { CHECK_S = 60, JUMP_S = 90, WAIT_POLL_S = 10, MAX_WAIT_S = 600 }

local calendars = {}   -- id -> calendar
local order = {}

local RULE_KEYS = { every = true, weeks = true, months = true, years = true, day = true, on = true,
    date = true, dates = true, month = true, from = true, to = true, times = true, time = true, duration = true, hours = true,
    except = true, moves = true, sun = true }
local EVENT_KEYS = { title = true, icon = true, tags = true, remind = true, notify = true,
    catchup = true, jitter = true }
local USER_KEYS = { id = true, title = true, icon = true, tags = true, remind = true }
local KINDS = { start = true, ['end'] = true, reminder = true, change = true, ready = true, edit = true }
local SCHEDULE_KEYS = { default = true, title = true, colors = true, values = true, editable = true, roles = true }
local WEEKDAYS = { 'mo', 'tu', 'we', 'th', 'fr', 'sa', 'su' }
local WEEK_FORMAT, WEEK_MAX_PER_DAY = 1, 48
local KIND_ORDER = { ['end'] = 1, start = 2, reminder = 3 }

local function log() return Logger:getShared() end
local function now() return __go_time_ms() / 1000 end

-- "a", "b", "c": the sorted keys of the given sets (or "none"), for an error that says what is allowed.
local function quoted(...)
    local out = {}
    for _, set in ipairs({ ... }) do for k in pairs(set) do out[#out + 1] = '"' .. tostring(k) .. '"' end end
    table.sort(out)
    return #out > 0 and table.concat(out, ', ') or 'none'
end

local function copy(t)
    local out = {}
    for k, v in pairs(t or {}) do out[k] = v end
    return out
end

local function clean(err) return (tostring(err):gsub('^[^\n]-:%d+: ', '')) end

local function list(v)
    if v == nil then return {} end
    if type(v) == 'table' then return v end
    return { v }
end

local MAX_S = 366 * 86400

-- Seconds of "-12h" or a number, at most 366 days; nil and the error otherwise (does not raise).
local function parseSeconds(v, what)
    if type(v) == 'number' then
        if v - v ~= 0 or math.abs(v) > MAX_S then return nil, what .. ': at most 366 days' end
        return v
    end
    if type(v) ~= 'string' then return nil, what .. ': expected e.g. "-12h" or seconds' end
    local sign, rest = 1, v
    if rest:sub(1, 1) == '-' then sign, rest = -1, rest:sub(2) elseif rest:sub(1, 1) == '+' then rest = rest:sub(2) end
    local total, any = 0, false
    for n, unit in rest:gmatch('(%d+)([smhd])') do
        total = total + tonumber(n) * ({ s = 1, m = 60, h = 3600, d = 86400 })[unit]
        any = true
    end
    if not any or rest:gsub('%d+[smhd]', '') ~= '' then return nil, what .. ': bad duration "' .. v .. '"' end
    if total > MAX_S then return nil, what .. ': at most 366 days' end
    return sign * total
end

local function seconds(v, what)
    local n, err = parseSeconds(v, what)
    if not n then error(err, 3) end
    return n
end

-- A reminder says which side of the start it is on: '12h' alone reads as before, but is after.
local function remindSeconds(v, what)
    if type(v) == 'string' and not v:match('^[%+%-]') then
        error(what .. ': "' .. v .. '" needs a sign: "-' .. v .. '" before the start, "+' .. v .. '" after', 3)
    end
    local n, err = parseSeconds(v, what)
    if not n then error(err, 3) end
    return n
end

-- Fields of an event that are not its rule; raises with the option's name.
local function checkEventFields(where, o, level)
    for _, k in ipairs({ 'title', 'icon' }) do
        if o[k] ~= nil and type(o[k]) ~= 'string' then error(where .. ': ' .. k .. ' must be a string', level) end
    end
    if o.tags ~= nil and type(o.tags) ~= 'string' and type(o.tags) ~= 'table' then
        error(where .. ': tags expects a string or a list of strings', level)
    end
    for _, tag in ipairs(list(o.tags)) do
        if type(tag) ~= 'string' then error(where .. ': tags must be strings', level) end
    end
    if o.catchup ~= nil and type(o.catchup) ~= 'boolean' then error(where .. ': catchup must be true or false', level) end
end

-- 'workday' and 'holiday' follow the public holidays: without them they would silently
-- treat 11 November as a workday.
local function checkHolidaysNeeded(where, o, holidays)
    if holidays then return end
    for _, e in ipairs(list(o.every)) do
        if e == 'workday' or e == 'holiday' then
            error(where .. ": every = '" .. e .. "' needs the public holidays: :holidays('pl') before :build()", 3)
        end
    end
end

-- Checks the keys of an event or a window and compiles its rule: the rule, or nil and the
-- error (does not raise, so that a saved event can be skipped during a module's load).
local function tryCompile(where, opts, allowed, ctx)
    if type(opts) ~= 'table' then return nil, where .. ': options table expected' end
    local spec = {}
    for k, v in pairs(opts) do
        if type(k) == 'string' then
            if RULE_KEYS[k] then
                spec[k] = v
            elseif not allowed[k] then
                return nil, where .. ': unknown option "' .. k .. '" (' .. quoted(RULE_KEYS, allowed) .. ')'
            end
        end
    end
    if spec.hours then
        local a, b
        if type(spec.hours) == 'table' then
            a, b = spec.hours[1], spec.hours[2]   -- { 'sunset', 'sunrise+15m' }
        else
            a, b = tostring(spec.hours):match('^(%d+:%d+)%s*%-%s*(%d+:%d+)$')
        end
        if type(a) ~= 'string' or type(b) ~= 'string' then
            return nil, where .. ': hours expected as "13:00-15:00" or { "sunset", "sunrise" }'
        end
        if spec.time or spec.duration then return nil, where .. ': give hours or time/duration, not both' end
        spec.time, spec['until'], spec.hours = a, b, nil
    end
    ctx = ctx or {}
    local rule, err = __go_calendar_rule(spec, ctx.holidays, ctx.lat, ctx.lon)
    if not rule then return nil, where .. ': ' .. tostring(err) end
    return rule
end

local function compile(where, opts, allowed, ctx)
    local rule, err = tryCompile(where, opts, allowed, ctx)
    if not rule then error(err, 3) end
    return rule
end

-- ---------- the week of an editable schedule ----------

local DAY_INDEX = {}
for i, d in ipairs(WEEKDAYS) do DAY_INDEX[d] = i end

-- Minutes of 'HH:MM' (whole minutes); '24:00' only with allow24. nil when it is not one.
local function minutesOf(s, allow24)
    if type(s) ~= 'string' then return nil end
    local h, m = s:match('^(%d%d?):(%d%d)$')
    h, m = tonumber(h), tonumber(m)
    if not h or m > 59 then return nil end
    if h == 24 and m == 0 and allow24 then return 1440 end
    if h > 23 then return nil end
    return h * 60 + m
end

local function hhmm(m) return string.format('%02d:%02d', math.floor(m / 60), m % 60) end

-- The days (indexes 1..7) of a weekly window's every, or nil when it is not one: weekdays,
-- 'day' or 'weekend'. 'workday' gives false: it is weekly but follows the holidays.
local function weeklyDays(every)
    local days, workday = {}, false
    local list = type(every) == 'table' and every or { every }
    if #list == 0 then return nil end
    for _, e in ipairs(list) do
        if type(e) ~= 'string' then return nil end
        e = e:lower()
        if e == 'day' then for i = 1, 7 do days[i] = true end
        elseif e == 'weekend' then days[6], days[7] = true, true
        elseif e == 'workday' then workday = true
        elseif DAY_INDEX[e] then days[DAY_INDEX[e]] = true
        else return nil end
    end
    if workday then return false end
    return days
end

-- A window the editor can show as a week: only every, hours ('HH:MM-HH:MM') and value. Gives
-- its days and minutes (from, to; from >= to runs over midnight), false for a weekly-looking
-- window with 'workday', nil for a fixed one.
local function weeklyWindow(w)
    for k in pairs(w) do
        if k ~= 'every' and k ~= 'hours' and k ~= 'value' then return nil end
    end
    local days = weeklyDays(w.every)
    if days == nil then return nil end
    local from, to = 0, 0
    if w.hours ~= nil then
        if type(w.hours) ~= 'string' then return nil end
        local a, b = w.hours:match('^(%d+:%d+)%s*%-%s*(%d+:%d+)$')
        from, to = minutesOf(a), minutesOf(b)
        if not from or not to then return nil end
    end
    if days == false then return false end
    return { days = days, from = from, to = to, value = w.value }
end

-- The code's week: its weekly windows painted in order (later wins), split at midnight.
local function paintWeek(windows, default)
    local slots = {}
    for d = 1, 7 do slots[d] = {} end
    local function paint(d, a, b, v)
        local s = slots[d]
        for m = a, b - 1 do s[m] = v end
    end
    for _, w in ipairs(windows) do
        for d = 1, 7 do
            if w.days[d] then
                if w.from < w.to then
                    paint(d, w.from, w.to, w.value)
                else   -- over midnight, also from == to: a whole day from that time
                    paint(d, w.from, 1440, w.value)
                    paint(d % 7 + 1, 0, w.to, w.value)
                end
            end
        end
    end
    local week = {}
    for d, name in ipairs(WEEKDAYS) do
        local out, s = {}, slots[d]
        local m = 0
        while m < 1440 do
            local v = s[m]
            local a = m
            while m < 1440 and s[m] == v do m = m + 1 end
            if v ~= nil and v ~= default then out[#out + 1] = { from = hhmm(a), to = hhmm(m), value = v } end
        end
        week[name] = out
    end
    return week
end

-- A week checked against a schedule: a copy with every day, or nil and what is wrong.
local function checkWeek(sc, week)
    if type(week) ~= 'table' then return nil, 'week expected as { mo = { {from, to, value}, ... }, ... }' end
    for k in pairs(week) do
        if not DAY_INDEX[k] then return nil, 'unknown day "' .. tostring(k) .. '" (mo, tu, we, th, fr, sa, su)' end
    end
    local out = {}
    for _, day in ipairs(WEEKDAYS) do
        local list = week[day] or {}
        if type(list) ~= 'table' then return nil, day .. ': expected a list of intervals' end
        for k in pairs(list) do
            if type(k) ~= 'number' or k < 1 or k > #list or k ~= math.floor(k) then return nil, day .. ': expected a list of intervals' end
        end
        if #list > WEEK_MAX_PER_DAY then return nil, day .. ': at most ' .. WEEK_MAX_PER_DAY .. ' intervals' end
        local copied, prev = {}, 0
        for i, iv in ipairs(list) do
            local at = day .. ' interval ' .. i
            if type(iv) ~= 'table' then return nil, at .. ': expected { from, to, value }' end
            for k in pairs(iv) do
                if k ~= 'from' and k ~= 'to' and k ~= 'value' then return nil, at .. ': unknown key "' .. tostring(k) .. '" (from, to, value)' end
            end
            local a, b = minutesOf(iv.from), minutesOf(iv.to, true)
            if not a then return nil, at .. ': from expected as HH:MM' end
            if not b then return nil, at .. ': to expected as HH:MM (up to 24:00)' end
            if a >= b then return nil, at .. ': from must be before to' end
            if a < prev then return nil, at .. ': overlaps the one before or is out of order' end
            if not sc.known[iv.value] then return nil, at .. ': value ' .. tostring(iv.value) .. ' is not one of the schedule\'s values' end
            if iv.value == sc.default then return nil, at .. ': value ' .. tostring(iv.value) .. ' is the default, leave the time out instead' end
            copied[i] = { from = hhmm(a), to = hhmm(b), value = iv.value }
            prev = b
        end
        out[day] = copied
    end
    return out
end

-- The windows of a checked week, one per interval, days with the same hours and value joined.
local function weekWindows(week)
    local byKey, order = {}, {}
    for _, day in ipairs(WEEKDAYS) do
        for _, iv in ipairs(week[day]) do
            local key = iv.from .. '-' .. iv.to .. '=' .. type(iv.value) .. ':' .. tostring(iv.value)
            local w = byKey[key]
            if not w then
                -- '24:00' ends at the next midnight: until before time runs over it
                w = { every = {}, hours = iv.from .. '-' .. (iv.to == '24:00' and '00:00' or iv.to), value = iv.value }
                byKey[key] = w
                order[#order + 1] = w
            end
            w.every[#w.every + 1] = day
        end
    end
    return order
end

local function deepCopy(v)
    if type(v) ~= 'table' then return v end
    local out = {}
    for k, x in pairs(v) do out[k] = deepCopy(x) end
    return out
end

-- Same pseudo-random shift for the same occurrence, also after a restart.
local function jitterOf(key, span)
    local h = 5381
    for i = 1, #key do h = (h * 33 + key:byte(i)) % 2147483647 end
    return (h % (2 * span + 1)) - span
end

-- When an occurrence starts: its time, shifted by the event's jitter.
local function startOf(ev, o)
    if ev.jitter > 0 then return o.at + jitterOf(ev.id .. o.date, ev.jitter) end
    return o.at
end

-- ==========================================================================
-- Builder
-- ==========================================================================

local Builder = {}
Builder.__index = Builder

-- What :schedule() returns: :editable() and :roles() for that schedule, and every builder
-- method, which goes on with the calendar (an error names the module's line).
local ScheduleHandle = {}
ScheduleHandle.__index = function(h, k)
    if ScheduleHandle[k] then return ScheduleHandle[k] end
    local f = Builder[k]
    if type(f) ~= 'function' then return nil end
    return function(self, ...)
        local ok, res = pcall(f, rawget(self, '_b'), ...)
        if not ok then error(clean(res), 2) end
        return res
    end
end

local function checkRoles(where, roles, level)
    if type(roles) ~= 'table' or #roles == 0 then error(where .. ": roles expects names, e.g. roles('rodzice')", level) end
    local out = {}
    for _, r in ipairs(roles) do
        if type(r) ~= 'string' or r == '' then error(where .. ': roles must be non-empty strings', level) end
        out[#out + 1] = r
    end
    return out
end

--- Start defining a calendar.
-- @param id string Calendar id (letters, digits, _ - .)
-- @api public
function Calendar.new(id)
    if type(id) ~= 'string' or not id:match('^[%w][%w_%.%-]*$') or #id > 40 then
        error('Calendar.new: id must be letters, digits, _ - . (max 40)', 2)
    end
    return setmetatable({ _id = id, _name = id, _items = {}, _byId = {}, _schedules = {},
        _clock = { wait = true } }, Builder)
end

--- Display name. @api public
function Builder:name(s) self._name = tostring(s); return self end
--- Icon (Bootstrap icon name). @api public
function Builder:icon(s) self._icon = tostring(s); return self end

--- Public holidays that make 'workday' and 'holiday': 'pl'. @api public
function Builder:holidays(country)
    local ok, err = pcall(__go_calendar_day, '2026-01-01', country)
    if not ok then error('Calendar ' .. self._id .. ': ' .. clean(err), 2) end
    self._holidays = country
    return self
end

--- Where the house is, for sunrise and sunset in times ('sunset-30m'): degrees, east positive. @api public
function Builder:location(lat, lon)
    if type(lat) ~= 'number' or type(lon) ~= 'number' or lat < -90 or lat > 90 or lon < -180 or lon > 180 then
        error('Calendar ' .. self._id .. ': location expects latitude -90..90 and longitude -180..180', 2)
    end
    self._lat, self._lon = lat, lon
    return self
end

--- Default channel for reminders: function(e) or an object with :send(e). @api public
function Builder:notify(channel)
    if type(channel) ~= 'function' and not (type(channel) == 'table' and type(channel.send) == 'function') then
        error('Calendar ' .. self._id .. ': notify expects a function or an object with :send(e)', 2)
    end
    self._notify = channel
    return self
end

--- Clock guard: { wait = false } plans without waiting for NTP. @api public
function Builder:clock(opts)
    if type(opts) ~= 'table' then error('Calendar ' .. self._id .. ': clock expects a table', 2) end
    for k in pairs(opts) do
        if k ~= 'wait' then error('Calendar ' .. self._id .. ': clock: unknown option "' .. tostring(k) .. '"', 2) end
    end
    self._clock = { wait = opts.wait ~= false }
    return self
end

-- Bounds of the day for isDay(), compiled in Go; raises naming the calendar.
local function daylightOf(id, what, from, to, fixedOnly)
    local dl, err = __go_calendar_daylight(from, to, fixedOnly)
    if not dl then error('Calendar ' .. id .. ': ' .. what .. ': ' .. tostring(err), 3) end
    return dl
end

local DAYLIGHT_KEYS = { from = true, to = true, fallback = true }
local FALLBACK_KEYS = { from = true, to = true }

local function checkKeys(where, t, allowed)
    for k in pairs(t) do
        if not allowed[k] then error(where .. ': unknown option "' .. tostring(k) .. '" (' .. quoted(allowed) .. ')', 3) end
    end
end

--- When the day starts and ends for isDay(): { from = 'sunrise', to = 'sunset', fallback =
-- { from = '06:00', to = '20:00' } }. Times as in events ('sunrise+30m', 'dusk', '07:00');
-- the fallback hours are used without :location(). @api public
function Builder:daylight(opts)
    local where = 'Calendar ' .. self._id .. ': daylight'
    if type(opts) ~= 'table' then error(where .. ' expects a table', 2) end
    checkKeys(where, opts, DAYLIGHT_KEYS)
    local fb = opts.fallback or {}
    if type(fb) ~= 'table' then error(where .. ": fallback expects { from = '06:00', to = '20:00' }", 2) end
    checkKeys(where .. ' fallback', fb, FALLBACK_KEYS)
    self._daylight = {
        sun = daylightOf(self._id, 'daylight', opts.from or 'sunrise', opts.to or 'sunset', false),
        fixed = daylightOf(self._id, 'daylight fallback', fb.from or '06:00', fb.to or '20:00', true),
    }
    return self
end

local function checkId(b, id, what)
    if type(id) ~= 'string' or id == '' then error('Calendar ' .. b._id .. ': ' .. what .. ' needs a string id', 3) end
    if b._byId[id] then error('Calendar ' .. b._id .. ': duplicate id "' .. id .. '"', 3) end
end

--- Add an event (start, end and reminder events). @api public
function Builder:event(id, opts)
    checkId(self, id, 'event')
    local where = 'Calendar ' .. self._id .. ' event ' .. id
    if type(opts) == 'table' then
        checkEventFields(where, opts, 3)
        for _, r in ipairs(list(opts.remind)) do remindSeconds(r, where .. ' remind') end
    end
    local item = { id = id, kind = 'event', source = 'code', opts = opts,
        rule = compile(where, opts, EVENT_KEYS, { lat = self._lat or 0, lon = self._lon or 0 }) }   -- again in build()
    self._items[#self._items + 1] = item
    self._byId[id] = item
    return self
end

--- Add a schedule: windows { every, hours, value, ... }, a default value, values = { ... } (the
-- allowed values, required when they are text), and optionally a title and colors = { value = color }
-- for the dashboard. @api public
function Builder:schedule(id, opts)
    checkId(self, id, 'schedule')
    if type(opts) ~= 'table' or #opts == 0 then
        error('Calendar ' .. self._id .. ' schedule ' .. id .. ': expected { default = v, { every = ..., value = v }, ... }', 2)
    end
    local where = 'Calendar ' .. self._id .. ' schedule ' .. id
    for k in pairs(opts) do
        if type(k) ~= 'number' and not SCHEDULE_KEYS[k] then
            error(where .. ': unknown option "' .. tostring(k) .. '" (' .. quoted(SCHEDULE_KEYS) .. ', windows)', 2)
        end
    end
    if opts.editable ~= nil and type(opts.editable) ~= 'boolean' then
        error(where .. ': editable must be true or false', 2)
    end
    if opts.default == nil then
        error(where .. ': needs a default, the value outside its windows', 2)
    end
    if opts.colors ~= nil and type(opts.colors) ~= 'table' then
        error(where .. ': colors expects { value = color }', 2)
    end
    -- the values, when declared, are the only ones a window may give: a typo is an error here,
    -- not a value nobody compares with
    local known
    if opts.values ~= nil then
        if type(opts.values) ~= 'table' or #opts.values == 0 then
            error(where .. ": values expects a list, e.g. { 'off', 'on' }", 2)
        end
        known = {}
        for _, v in ipairs(opts.values) do known[v] = true end
    end
    local function name(v) return type(v) == 'string' and ('"' .. v .. '"') or tostring(v) end
    local function listed()
        local out = {}
        for _, v in ipairs(opts.values) do out[#out + 1] = name(v) end
        return table.concat(out, ', ')
    end
    if known and not known[opts.default] then
        error(where .. ': default ' .. name(opts.default) .. ' is not one of values (' .. listed() .. ')', 2)
    end
    local used, seen, text = { opts.default }, { [opts.default] = true }, type(opts.default) == 'string'
    for i, w in ipairs(opts) do
        if type(w) ~= 'table' or w.value == nil then
            error(where .. ' window ' .. i .. ': needs a value', 2)
        end
        if known and not known[w.value] then
            error(where .. ' window ' .. i .. ': value ' .. name(w.value) .. ' is not one of values (' .. listed() .. ')', 2)
        end
        if not seen[w.value] then used[#used + 1] = w.value; seen[w.value] = true end
        text = text or type(w.value) == 'string'
    end
    local function given()
        local out = {}
        for _, v in ipairs(used) do out[#out + 1] = name(v) end
        return table.concat(out, ', ')
    end
    -- words are where typos happen: a schedule of text lists them, numbers need not
    if text and not known then
        error(where .. ': text values need a list of them: values = { ' .. given() .. ' }', 2)
    end
    for v in pairs(opts.colors or {}) do
        if not (known or seen)[v] then
            error(where .. ': colors names ' .. name(v) .. ', which is not one of its values ('
                .. (known and listed() or given()) .. ')', 2)
        end
    end
    local item = { id = id, kind = 'schedule', opts = opts, editable = opts.editable == true }
    if opts.roles ~= nil then item.roles = checkRoles(where, opts.roles, 3) end
    self._schedules[#self._schedules + 1] = item
    self._byId[id] = item
    return setmetatable({ _b = self, _item = item }, ScheduleHandle)
end

--- The schedule just added can be edited from a dashboard: its weekly hours, as far as
-- :roles() allows (see Cal:week).
-- @api public
function ScheduleHandle:editable(on)
    if on ~= nil and type(on) ~= 'boolean' then
        error('Calendar ' .. self._b._id .. ' schedule ' .. self._item.id .. ': editable expects true, false or nothing', 2)
    end
    self._item.editable = on ~= false
    return self
end

--- Who may edit the schedule through a dashboard: people given one of these home roles in
-- vclu cloud (and its owner and admins). Without it everyone the dashboard lets act.
-- @api public
function ScheduleHandle:roles(...)
    local list = { ... }
    if #list == 1 and type(list[1]) == 'table' then list = list[1] end
    self._item.roles = checkRoles('Calendar ' .. self._b._id .. ' schedule ' .. self._item.id, list, 3)
    return self
end

-- The calendar's own editable() and roles() come before its events and schedules, so that a
-- schedule's :editable() is never taken for the calendar's.
local function checkCalendarLevel(b, what)
    if #b._items > 0 or #b._schedules > 0 then
        error('Calendar ' .. b._id .. ': ' .. what .. ' of the calendar goes right after Calendar.new(), before its events and schedules'
            .. ' (a schedule\'s own: :schedule(id, { ... }):editable())', 3)
    end
end

--- People may add their own events and skip a day of any event from a dashboard, as far as
-- :roles() allows. Right after Calendar.new().
-- @api public
function Builder:editable(on)
    checkCalendarLevel(self, 'editable()')
    if on ~= nil and type(on) ~= 'boolean' then error('Calendar ' .. self._id .. ': editable expects true, false or nothing', 2) end
    self._editable = on ~= false
    return self
end

--- Who may change the calendar's events through a dashboard: people given one of these home
-- roles in vclu cloud (and its owner and admins). After :editable().
-- @api public
function Builder:roles(...)
    checkCalendarLevel(self, 'roles()')
    if not self._editable then error('Calendar ' .. self._id .. ': roles() follows :editable()', 2) end
    local list = { ... }
    if #list == 1 and type(list[1]) == 'table' then list = list[1] end
    self._roles = checkRoles('Calendar ' .. self._id, list, 3)
    return self
end

-- ==========================================================================
-- Calendar instance
-- ==========================================================================

local Cal = {}
Cal.__index = Cal

-- An editable schedule: its weekly windows first, then the fixed ones; raises naming the
-- module's line that called build().
local function editableOf(where, sc, it)
    local opts = it.opts
    if opts.values == nil then
        error(where .. ": an editable schedule needs its values = { ... }, the editor offers them", 3)
    end
    local weekly, fixedFrom = {}, nil
    for i, w in ipairs(opts) do
        local ww = weeklyWindow(w)
        if ww == false then
            error(where .. ' window ' .. i .. ": 'workday' follows the holidays and a week does not: "
                .. "use every = { 'mo', 'tu', 'we', 'th', 'fr' } and a window { every = 'holiday', ... } after the weekly ones", 3)
        elseif ww and fixedFrom then
            error(where .. ' window ' .. i .. ': the weekly windows (every, hours, value) of an editable schedule come before the others (window '
                .. fixedFrom .. ')', 3)
        elseif ww then
            weekly[#weekly + 1] = ww
        elseif not fixedFrom then
            fixedFrom = i
        end
    end
    if #weekly == 0 then
        error(where .. ": an editable schedule needs a weekly window, e.g. { every = 'mo', hours = '06:00-08:00', value = ... }", 3)
    end
    sc.editable, sc.roles = true, it.roles
    sc.known = {}
    for _, v in ipairs(opts.values) do sc.known[v] = true end
    sc.valueList = copy(opts.values)
    sc.codeRules, sc.codeValues = sc.rules, sc.values
    sc.fixedRules, sc.fixedValues, sc.fixed = {}, {}, {}
    for i = #weekly + 1, #opts do
        sc.fixedRules[#sc.fixedRules + 1] = sc.rules[i]
        sc.fixedValues[#sc.fixedValues + 1] = sc.values[i]
        sc.fixed[#sc.fixed + 1] = deepCopy(opts[i])
    end
    sc.codeWeek = paintWeek(weekly, sc.default)
    sc.saved = nil
end

-- The rules and values of a checked week followed by the fixed windows; nil and the error
-- when one does not compile (does not raise).
function Cal:_compileWeek(sc, week)
    local rules, values = {}, {}
    for i, w in ipairs(weekWindows(week)) do
        local spec = { every = w.every, hours = w.hours }
        local rule, err = tryCompile('Calendar ' .. self.id .. ' schedule ' .. sc.id .. ' week window ' .. i, spec, {}, self._ctx)
        if not rule then return nil, nil, err end
        rules[#rules + 1], values[#values + 1] = rule, w.value
    end
    for i, r in ipairs(sc.fixedRules) do
        rules[#rules + 1], values[#values + 1] = r, sc.fixedValues[i]
    end
    return rules, values
end

-- Saved weeks back at build(); one the code no longer allows is dropped with a log line.
-- The version carries on, so a page holding the old week gets a conflict, and a dropped week
-- does not come back when the module makes the schedule editable again.
function Cal:_loadWeeks()
    local saved, dropped = self._st.schedules, nil
    for _, sc in ipairs(self._schedules) do
        local s = saved[sc.id]
        if type(s) ~= 'table' then s = nil end
        if sc.editable then
            sc.version = s and tonumber(s.version) or 0
            if s then sc.by, sc.at = s.by, s.at end
        end
        if s and s.week ~= nil then
            local week, err, rules, values
            if sc.editable then
                week, err = checkWeek(sc, s.week)
                if week then rules, values, err = self:_compileWeek(sc, week) end
            else
                err = 'the schedule is not editable'
            end
            if rules then
                sc.rules, sc.values, sc.saved = rules, values, week
            else
                log():error('[CALENDAR] %s: saved week of %s dropped, the module\'s hours apply: %s', self.id, sc.id, tostring(err))
                dropped = dropped or copy(saved)
                dropped[sc.id] = { version = tonumber(s.version) or 0, by = s.by, at = s.at }
            end
        end
    end
    if dropped then self._st.schedules = dropped end
end

--- Register the calendar and start it. Replaces a calendar with the same id. @api public
function Builder:build()
    local hol = self._holidays
    local ctx = { holidays = hol, lat = self._lat, lon = self._lon }
    local where = 'Calendar ' .. self._id
    local cal = setmetatable({ id = self._id, name = self._name, icon = self._icon,
        editable = self._editable == true, roles = self._roles,
        _holidays = hol, _ctx = ctx, _notify = self._notify, _clock = self._clock,
        _events = {}, _byId = {}, _schedules = {},
        _handlers = { start = {}, ['end'] = {}, reminder = {}, change = {}, ready = {}, edit = {} },
        _s = { ready = false, waitSince = now(), warned = false } }, Cal)

    for _, it in ipairs(self._items) do
        local o = it.opts
        local ev = { id = it.id, source = 'code', title = o.title or it.id, icon = o.icon,
            tags = list(o.tags), notify = o.notify, catchup = o.catchup == true,
            rule = compile(where .. ' event ' .. it.id, o, EVENT_KEYS, ctx) }
        if o.notify ~= nil and type(o.notify) ~= 'function'
            and not (type(o.notify) == 'table' and type(o.notify.send) == 'function') then
            error(where .. ' event ' .. it.id .. ': notify expects a function or an object with :send(e)', 2)
        end
        checkHolidaysNeeded(where .. ' event ' .. it.id, o, hol)
        ev.remind = {}
        for _, r in ipairs(list(o.remind)) do ev.remind[#ev.remind + 1] = { s = remindSeconds(r, where .. ' event ' .. it.id .. ' remind'), label = r } end
        ev.jitter = o.jitter and math.abs(seconds(o.jitter, where .. ' event ' .. it.id .. ' jitter')) or 0
        cal._events[#cal._events + 1] = ev
        cal._byId[it.id] = ev
    end
    for _, it in ipairs(self._schedules) do
        local rules, values = {}, {}
        for i, w in ipairs(it.opts) do
            checkHolidaysNeeded(where .. ' schedule ' .. it.id .. ' window ' .. i, w, hol)
            local spec = copy(w)
            spec.value = nil
            rules[i] = compile(where .. ' schedule ' .. it.id .. ' window ' .. i, spec, {}, ctx)
            values[i] = w.value
        end
        local sc = { id = it.id, rules = rules, values = values, default = it.opts.default,
            title = it.opts.title or it.id, colors = it.opts.colors or {} }
        if it.editable then
            editableOf(where .. ' schedule ' .. it.id, sc, it)
        elseif it.roles then
            error(where .. ' schedule ' .. it.id .. ': roles is for an editable schedule: add :editable()', 2)
        end
        cal._schedules[#cal._schedules + 1] = sc
        cal._byId[it.id] = sc
    end

    if not self._daylight then self:daylight({}) end
    cal._daylight = self._daylight

    cal._st = persist('calendar.' .. cal.id, { events = {}, skips = {}, pauses = {}, schedules = {}, seq = 0 })
    cal:_checkSaved()
    cal:_loadUserEvents()
    cal:_loadWeeks()

    local old = calendars[cal.id]
    if old then
        old:_stop()
        log():warn('[CALENDAR] calendar "%s" built again: the earlier one stops, its handlers no longer fire', cal.id)
    else
        order[#order + 1] = cal.id
    end
    calendars[cal.id] = cal
    -- first check on the next turn of the loop, after the module subscribed with :on()
    cal._s.timer = EventBus:getShared():setTimeout(function() cal:_tick() end, 1)
    return cal
end

--- A calendar built with Calendar.new(id):build(), or nil. @api public
function Calendar.get(id) return calendars[id] end

--- All calendars in the order they were built. @api public
function Calendar.list()
    local out = {}
    for _, id in ipairs(order) do out[#out + 1] = calendars[id] end
    return out
end

function Cal:_stop()
    if self._s.timer then EventBus:getShared():clearInterval(self._s.timer) end
    self._s.timer = nil
    self._s.stopped = true
end

-- Saved state of an unexpected shape (edited by hand, an older version) is dropped, not fatal.
function Cal:_checkSaved()
    local st = self._st
    for _, k in ipairs({ 'events', 'skips', 'pauses', 'schedules' }) do
        if type(st[k]) ~= 'table' then
            if st[k] ~= nil then log():error('[CALENDAR] %s: saved %s dropped (not a table)', self.id, k) end
            st[k] = {}
        end
    end
    local pauses = {}
    for _, p in ipairs(st.pauses) do
        if type(p) == 'table' and type(p.from) == 'string' and type(p.to) == 'string' then
            pauses[#pauses + 1] = p
        else
            log():error('[CALENDAR] %s: a saved pause dropped', self.id)
        end
    end
    if #pauses ~= #st.pauses then st.pauses = pauses end
end

-- No pcall here: build() runs while a module loads (see pkg/lua/AGENTS.md).
function Cal:_loadUserEvents()
    for id, o in pairs(self._st.events or {}) do
        if type(id) ~= 'string' or type(o) ~= 'table' then
            log():error('[CALENDAR] %s: saved event %s skipped: not an event', self.id, tostring(id))
        else
        local rule, err = tryCompile('Calendar ' .. self.id .. ' user event ' .. id, o, USER_KEYS, self._ctx)
        if rule and not self._byId[id] then
            local ev = { id = id, source = 'user', title = o.title or id, icon = o.icon, tags = list(o.tags),
                rule = rule, remind = {}, jitter = 0 }
            for _, r in ipairs(list(o.remind)) do
                local s = parseSeconds(r, 'remind')
                if s then ev.remind[#ev.remind + 1] = { s = s, label = r } end
            end
            self._events[#self._events + 1] = ev
            self._byId[id] = ev
        elseif not rule then
            log():error('[CALENDAR] %s: saved event %s skipped: %s', self.id, id, tostring(err))
        end
        end
    end
end

-- ---------- subscriptions ----------

--- Subscribe: on(kind, fn) or on(kind, id, fn); kinds start, end, reminder, change, ready, edit.
-- At one moment end comes before start; an end from skip, pause or remove has e.cancelled = true.
-- edit fires after a change made while running (a week saved or reset, an event added, removed,
-- skipped, a pause): e = { calendar, id, kind = 'schedule' | 'event', action, title, origin, actor }.
-- @return function unsubscribe
-- @api public
function Cal:on(kind, id, fn)
    if fn == nil then fn, id = id, nil end
    if not KINDS[kind] then error('Calendar ' .. self.id .. ':on: unknown kind "' .. tostring(kind) .. '" (start, end, reminder, change, ready, edit)', 2) end
    if type(fn) ~= 'function' then error('Calendar ' .. self.id .. ':on: function expected', 2) end
    if id ~= nil and not self._byId[id] then error('Calendar ' .. self.id .. ':on: unknown id "' .. tostring(id) .. '"', 2) end
    if id ~= nil then
        local isSchedule = self._byId[id].rules ~= nil
        if kind == 'change' and not isSchedule then
            error('Calendar ' .. self.id .. ":on: '" .. id .. "' is an event: it has start, end and reminder, change is for schedules", 2)
        elseif kind ~= 'change' and kind ~= 'ready' and kind ~= 'edit' and isSchedule then
            error('Calendar ' .. self.id .. ":on: '" .. id .. "' is a schedule: it has change and edit, " .. kind .. ' is for events', 2)
        elseif kind == 'ready' then
            error('Calendar ' .. self.id .. ':on: ready is for the whole calendar, without an id', 2)
        end
    end
    local h = { id = id, fn = fn }
    local hs = self._handlers[kind]
    hs[#hs + 1] = h
    return function()
        for i, x in ipairs(hs) do if x == h then table.remove(hs, i); return end end
    end
end

function Cal:_emit(kind, id, ...)
    for _, h in ipairs(copy(self._handlers[kind])) do
        if h.id == nil or h.id == id then
            local ok, err = pcall(h.fn, ...)
            if not ok then log():error('[CALENDAR] %s %s %s: %s', self.id, kind, tostring(id), tostring(err)) end
        end
    end
end

-- ---------- occurrences ----------

local function skipKey(id, date) return id .. '@' .. date end

-- Whether a pause covers an event's tags (a pause without tags covers all).
local function pausedBy(p, ev)
    if not p.tags or #p.tags == 0 then return true end
    for _, t in ipairs(p.tags) do
        for _, et in ipairs(ev.tags) do if t == et then return true end end
    end
    return false
end

function Cal:_pausedAt(ev, date)
    for _, p in ipairs(self._st.pauses or {}) do
        if date >= p.from and date <= p.to and pausedBy(p, ev) then return true end
    end
    return false
end

function Cal:_occ(ev, o)
    local last = o.date
    if o.ends and o.ends - 1 > o.at then last = __go_calendar_day(o.ends - 1).date end
    return { calendar = self.id, id = ev.id, title = ev.title, icon = ev.icon, tags = ev.tags,
        source = ev.source, at = o.at, ends = o.ends, allDay = o.allDay, date = o.date, last = last }
end

function Cal:_list(from, to, opts)
    opts = opts or {}
    local want = opts.tags and list(opts.tags)
    local out = {}
    local skips = self._st.skips or {}
    for _, ev in ipairs(self._events) do
        local tagged = not want
        if want then
            for _, t in ipairs(want) do
                for _, et in ipairs(ev.tags) do if t == et then tagged = true end end
            end
        end
        if tagged then
            for _, o in ipairs(__go_calendar_between(ev.rule, from, to, true)) do
                local skipped = skips[skipKey(ev.id, o.date)]
                if not skipped or opts.skipped then
                    local e = self:_occ(ev, o)
                    e.paused = self:_pausedAt(ev, o.date)
                    e.skipped = skipped or nil
                    out[#out + 1] = e
                end
            end
        end
    end
    table.sort(out, function(a, b) if a.at ~= b.at then return a.at < b.at end return a.id < b.id end)
    return out
end

-- A bound given as a timestamp or a date: a date starts at its midnight, or ends at the next one.
local function bound(cal, v, endOfDay, what)
    if type(v) == 'number' then return v end
    if type(v) == 'string' and v:match('^%d%d%d%d%-%d%d%-%d%d$') then
        local d = __go_calendar_day(v, cal._holidays)
        return endOfDay and d.ends or d.starts
    end
    error('Calendar ' .. cal.id .. ':' .. what .. ': expected a timestamp or YYYY-MM-DD', 3)
end

--- A day: { date, weekday ('mo'..'su'), workday, holiday, holidayName, namedays, starts, ends };
-- date as 'YYYY-MM-DD' or a timestamp, today when nil. Names follow :holidays(country). @api public
function Cal:day(date)
    return __go_calendar_day(date, self._holidays)
end

--- Occurrences between two moments (timestamps, or dates: from its start, to its end), also
-- the ones going on at from; at most 400 days. opts: { tags }. @api public
function Cal:between(from, to, opts)
    local a, b = bound(self, from, false, 'between'), bound(self, to, true, 'between')
    if b <= a or b - a > 400 * 86400 then error('Calendar ' .. self.id .. ':between: from before to, at most 400 days', 2) end
    return self:_list(a, b, opts)
end

--- Occurrences of today (also the ones that started earlier and still go on). @api public
function Cal:today(opts)
    local d = __go_calendar_day(now(), self._holidays)
    return self:_list(d.starts, d.ends, opts)
end

--- Occurrences from now for the next n days (default 7). @api public
function Cal:upcoming(days, opts)
    if type(days) == 'table' then opts, days = days, nil end
    days = tonumber(days) or 7
    if days <= 0 or days > 366 then error('Calendar ' .. self.id .. ':upcoming: days must be 1..366', 2) end
    local t = now()
    return self:_list(t, t + days * 86400, opts)
end

local function event(cal, id, what)
    local ev = cal._byId[id]
    if not ev or not ev.rule then error('Calendar ' .. cal.id .. ':' .. what .. ': unknown event "' .. tostring(id) .. '"', 3) end
    return ev
end

--- The next occurrence of an event (not skipped), or nil. @api public
function Cal:next(id)
    local ev = event(self, id, 'next')
    local t = now()
    local skips = self._st.skips or {}
    for _ = 1, 400 do
        local o = __go_calendar_next(ev.rule, t)
        if not o then return nil end
        if not skips[skipKey(id, o.date)] then return self:_occ(ev, o) end
        t = o.at
    end
end

--- Whether an occurrence of the event is going on now (nil until the clock is trusted). @api public
function Cal:isActive(id)
    local ev = event(self, id, 'isActive')
    if not self._s.ready then return nil end
    local t = now()
    for _, o in ipairs(__go_calendar_between(ev.rule, t - ev.jitter, t + ev.jitter + 1, true)) do
        if startOf(ev, o) <= t and o.ends and o.ends > t and not (self._st.skips or {})[skipKey(id, o.date)]
            and not self:_pausedAt(ev, o.date) then return true end
    end
    return false
end

local function schedule(cal, id, what)
    local sc = cal._byId[id]
    if not sc or sc.rules == nil then error('Calendar ' .. cal.id .. ':' .. what .. ': unknown schedule "' .. tostring(id) .. '"', 3) end
    return sc
end

local function valueAt(sc, idx)
    if idx == 0 then return sc.default end
    return sc.values[idx]
end

--- The value of a schedule now (nil until the clock is trusted). @api public
function Cal:value(id)
    local sc = schedule(self, id, 'value')
    if not self._s.ready then return nil end
    return valueAt(sc, __go_calendar_schedule(sc.rules, now()))
end

--- The next change of a schedule within 8 days: { value, at } or nil. @api public
function Cal:nextChange(id)
    local sc = schedule(self, id, 'nextChange')
    if not self._s.ready then return nil end
    local t0 = now()
    local cur = valueAt(sc, __go_calendar_schedule(sc.rules, t0))
    local t = t0
    for _ = 1, 50 do   -- another window with the same value is not a change
        local _, at, idx = __go_calendar_schedule(sc.rules, t, t0 + 8 * 86400 - t)
        if not at then return nil end
        local v = valueAt(sc, idx)
        if v ~= cur then return { value = v, at = at } end
        t = at
    end
end

--- The colour given to a value of a schedule (default: its value now), or nil. @api public
function Cal:color(id, value)
    local sc = schedule(self, id, 'color')
    if value == nil then value = self:value(id) end
    if value == nil then return nil end
    return sc.colors[value]
end

--- Spans of a schedule: segments(id, from, to) with timestamps or dates, or segments(id, date)
-- for one day: { {from, to, value, color}, ... }. @api public
function Cal:segments(id, from, to)
    local sc = schedule(self, id, 'segments')
    if to == nil then to = from end
    from, to = bound(self, from, false, 'segments'), bound(self, to, true, 'segments')
    if to <= from or to - from > 8 * 86400 then
        error('Calendar ' .. self.id .. ':segments: from before to, at most 8 days', 2)
    end
    local out = {}
    local t = from
    while t < to and #out < 500 do
        local idx, at = __go_calendar_schedule(sc.rules, t, to - t)
        local stop = (at and at < to) and at or to
        local v = valueAt(sc, idx)
        local last = out[#out]
        if last and last.value == v then
            last.to = stop
        else
            out[#out + 1] = { from = t, to = stop, value = v, color = sc.colors[v] }
        end
        t = stop
    end
    return out
end

--- Sunrise, sunset, civil dawn and dusk, and solar noon of a day (date, timestamp or today), or
-- nil without :location(). { sunrise, sunset, dawn, dusk, noon, dayLength } as timestamps;
-- polar = 'day' | 'night' when the sun does not rise or set. @api public
function Cal:sun(date)
    if not self._ctx.lat then return nil end
    return __go_calendar_sun(date, self._ctx.lat, self._ctx.lon)
end

--- Where the sun is now (or at a timestamp): { azimuth (degrees from north, 180 south),
-- elevation (degrees above the horizon) }, or nil without :location(). @api public
function Cal:sunPosition(at)
    if not self._ctx.lat then return nil end
    local az, el = __go_calendar_sun_position(at, self._ctx.lat, self._ctx.lon)
    return { azimuth = az, elevation = el }
end

local function isDayAt(cal, at, what)
    if at ~= nil and type(at) ~= 'number' then error('Calendar ' .. cal.id .. ':' .. what .. ': expected a timestamp', 3) end
    if not cal._s.ready then return nil end
    local dl = cal._daylight
    if cal._ctx.lat then return __go_calendar_is_day(dl.sun, at or now(), cal._ctx.lat, cal._ctx.lon) end
    return __go_calendar_is_day(dl.fixed, at or now())
end

--- Whether it is day now (or at a timestamp): between :daylight()'s from and to, by the sun with
-- :location(), else by the fallback hours. Nil until the clock is trusted. @api public
function Cal:isDay(at) return isDayAt(self, at, 'isDay') end

--- The opposite of isDay(at); nil until the clock is trusted. @api public
function Cal:isNight(at)
    local day = isDayAt(self, at, 'isNight')
    if day == nil then return nil end
    return not day
end

--- Whether a date ('YYYY-MM-DD', a timestamp or today) is a workday. @api public
function Cal:isWorkday(date) return __go_calendar_day(date, self._holidays).workday end
--- Whether a date is a public holiday. @api public
function Cal:isHoliday(date) return __go_calendar_day(date, self._holidays).holiday end
--- Whether the clock is trusted and the calendar fires events. @api public
function Cal:isReady() return self._s.ready end

-- ---------- runtime changes (Persist) ----------

-- Who made the change now: the entry point's context (web, cloud, MCP, a module).
local function cause()
    local c = StateContext and StateContext.current()
    return c and c.origin or 'lua', c and c.actor or nil
end

-- edit, after the change is saved; a failing handler does not undo it.
function Cal:_edited(e)
    e.calendar = self.id
    e.origin, e.actor = cause()
    local item = e.id and self._byId[e.id]
    if item and e.title == nil then e.title = item.title end
    self:_emit('edit', e.id, e)
end

local function editable(cal, id, what)
    local sc = cal._byId[id]
    if not sc or sc.rules == nil then error('Calendar ' .. cal.id .. ':' .. what .. ': unknown schedule "' .. tostring(id) .. '"', 3) end
    if not sc.editable then
        error('Calendar ' .. cal.id .. ':' .. what .. ': schedule "' .. tostring(id) .. '" is not editable (:editable() in the module)', 3)
    end
    return sc
end

local function checkVersion(cal, sc, opts, what)
    if opts ~= nil and type(opts) ~= 'table' then error('Calendar ' .. cal.id .. ':' .. what .. ': options table expected', 3) end
    local v = opts and opts.version
    if v ~= nil and v ~= sc.version then
        error('Calendar ' .. cal.id .. ':' .. what .. ': version ' .. tostring(v) .. ' is not the current one (' .. sc.version .. '): the week changed', 3)
    end
end

--- The week of an editable schedule: { format, version, edited, by, at, week = { mo = { {from,
-- to, value}, ... }, ..., su }, fixed (the module's other windows), values, default, colors,
-- title, roles }. week is the saved one, or the module's weekly windows.
-- @api public
function Cal:week(id)
    local sc = editable(self, id, 'week')
    return { format = WEEK_FORMAT, version = sc.version, edited = sc.saved ~= nil, by = sc.by, at = sc.at,
        week = deepCopy(sc.saved or sc.codeWeek), fixed = deepCopy(sc.fixed), values = copy(sc.valueList),
        default = sc.default, colors = copy(sc.colors), title = sc.title, roles = sc.roles and copy(sc.roles) }
end

function Cal:_saveWeek(sc, week, rules, values)
    local _, actor = cause()
    sc.rules, sc.values, sc.saved = rules, values, week
    sc.version, sc.by, sc.at = sc.version + 1, actor, math.floor(now())
    local all = copy(self._st.schedules)
    all[sc.id] = { week = week, version = sc.version, by = sc.by, at = sc.at }
    self._st.schedules = all
    if self._s.ready and not self._s.stopped then self:_schedulesChanged(now()) end
    self:_replan()
end

--- Save a week for an editable schedule (the module's fixed windows stay after it):
-- setWeek(id, { mo = { { from = '06:00', to = '08:00', value = 'komfort' } }, ... }, { version = n }).
-- With version, raises when the week changed since. The value applies at once.
-- @return number the new version
-- @api public
function Cal:setWeek(id, week, opts)
    local sc = editable(self, id, 'setWeek')
    checkVersion(self, sc, opts, 'setWeek')
    local checked, err = checkWeek(sc, week)
    if not checked then error('Calendar ' .. self.id .. ':setWeek ' .. id .. ': ' .. err, 2) end
    local rules, values, cerr = self:_compileWeek(sc, checked)
    if not rules then error(cerr, 2) end
    self:_saveWeek(sc, checked, rules, values)
    self:_edited({ id = id, kind = 'schedule', action = 'set', version = sc.version })
    return sc.version
end

--- Back to the module's hours. resetWeek(id, { version = n }) as setWeek.
-- @return number the new version
-- @api public
function Cal:resetWeek(id, opts)
    local sc = editable(self, id, 'resetWeek')
    checkVersion(self, sc, opts, 'resetWeek')
    self:_saveWeek(sc, nil, sc.codeRules, sc.codeValues)
    self:_edited({ id = id, kind = 'schedule', action = 'reset', version = sc.version })
    return sc.version
end

-- A user event from add() or update()'s options: the event and what is saved; raises naming
-- what (level 3: the caller of add or update).
local function userEvent(cal, id, opts, what)
    local where = 'Calendar ' .. cal.id .. ':' .. what
    local saved = {}
    for k, v in pairs(opts) do
        if k ~= 'id' then
            if type(v) == 'function' then error(where .. ': ' .. k .. ' cannot be a function', 3) end
            saved[k] = v
        end
    end
    for _, k in ipairs({ 'title', 'icon' }) do
        if saved[k] ~= nil and type(saved[k]) ~= 'string' then error(where .. ': ' .. k .. ' must be a string', 3) end
    end
    for _, tag in ipairs(list(saved.tags)) do
        if type(tag) ~= 'string' then error(where .. ': tags must be strings', 3) end
    end
    if not cal._holidays then
        for _, e in ipairs(list(saved.every)) do
            if e == 'workday' or e == 'holiday' then
                error(where .. ": every = '" .. e .. "' needs the public holidays: :holidays('pl') before :build()", 3)
            end
        end
    end
    local rule, err = tryCompile(where, saved, USER_KEYS, cal._ctx)
    if not rule then error(err, 3) end
    local ev = { id = id, source = 'user', title = saved.title or id, icon = saved.icon, tags = list(saved.tags),
        rule = rule, remind = {}, jitter = 0 }
    for _, r in ipairs(list(saved.remind)) do
        local n, rerr = nil, nil
        if type(r) == 'string' and not r:match('^[%+%-]') then
            rerr = where .. ': remind: "' .. r .. '" needs a sign: "-' .. r .. '" before the start, "+' .. r .. '" after'
        else
            n, rerr = parseSeconds(r, where .. ': remind')
        end
        if not n then error(rerr, 3) end
        ev.remind[#ev.remind + 1] = { s = n, label = r }
    end
    return ev, saved
end

--- Add a user event (saved, survives a restart). Fields as in :event without functions.
-- @return string id
-- @api public
function Cal:add(opts)
    if type(opts) ~= 'table' then error('Calendar ' .. self.id .. ':add: options table expected', 2) end
    local id = opts.id
    if id ~= nil and (type(id) ~= 'string' or self._byId[id]) then
        error('Calendar ' .. self.id .. ':add: id "' .. tostring(id) .. '" is taken or not a string', 2)
    end
    local ev, saved = userEvent(self, id or '', opts, 'add')
    if id == nil then
        repeat
            self._st.seq = (self._st.seq or 0) + 1
            id = 'u' .. self._st.seq
        until not self._byId[id]
        ev.id = id
        if saved.title == nil then ev.title = id end
    end
    self._events[#self._events + 1] = ev
    self._byId[id] = ev
    local evs = copy(self._st.events)
    evs[id] = saved
    self._st.events = evs
    self:_replan()
    self:_edited({ id = id, kind = 'event', action = 'add' })
    return id
end

-- What a user event was saved with (a copy), for a page that edits it; nil for others.
function Cal:_saved(id)
    local o = (self._st.events or {})[id]
    return type(o) == 'table' and deepCopy(o) or nil
end

--- Change a user event: its fields again, as in add (the id stays). An occurrence going on
-- ends first. @api public
function Cal:update(id, opts)
    local old = self._byId[id]
    if not old or old.source ~= 'user' then
        error('Calendar ' .. self.id .. ':update: "' .. tostring(id) .. '" is not a user event (events from code change in the module)', 2)
    end
    if type(opts) ~= 'table' then error('Calendar ' .. self.id .. ':update: options table expected', 2) end
    local ev, saved = userEvent(self, id, opts, 'update')
    self:_endNow(old)
    for i, e in ipairs(self._events) do if e == old then self._events[i] = ev; break end end
    self._byId[id] = ev
    local evs = copy(self._st.events)
    evs[id] = saved
    self._st.events = evs
    self:_replan()
    self:_edited({ id = id, kind = 'event', action = 'update' })
end

--- Remove a user event. @api public
function Cal:remove(id)
    local ev = self._byId[id]
    if not ev or ev.source ~= 'user' then error('Calendar ' .. self.id .. ':remove: "' .. tostring(id) .. '" is not a user event (events from code change in the module)', 2) end
    self:_endNow(ev)
    self._byId[id] = nil
    for i, e in ipairs(self._events) do if e == ev then table.remove(self._events, i); break end end
    local evs = copy(self._st.events)
    evs[id] = nil
    self._st.events = evs
    self:_edited({ id = id, kind = 'event', action = 'remove', title = ev.title })
end

local function checkDate(cal, date, what)
    if type(date) ~= 'string' or not date:match('^%d%d%d%d%-%d%d%-%d%d$') then
        error('Calendar ' .. cal.id .. ':' .. what .. ': date expected as YYYY-MM-DD', 3)
    end
end

--- Cancel one occurrence of an event (by its date). @api public
function Cal:skip(id, date)
    local ev = event(self, id, 'skip')
    checkDate(self, date, 'skip')
    local d = __go_calendar_day(date, self._holidays)
    local found = false
    for _, o in ipairs(__go_calendar_between(ev.rule, d.starts, d.ends, true)) do
        if o.date == date then found = true end
    end
    if not found then
        local after, t = {}, d.starts - 1
        for _ = 1, 2 do
            local o = __go_calendar_next(ev.rule, t)
            if not o then break end
            after[#after + 1] = o.date
            t = o.at
        end
        error('Calendar ' .. self.id .. ':skip: "' .. id .. '" has no occurrence on ' .. date
            .. (#after > 0 and (' (next: ' .. table.concat(after, ', ') .. ')') or ''), 2)
    end
    self:_endNow(ev, function(o) return o.date == date end)
    local s = copy(self._st.skips)
    s[skipKey(id, date)] = true
    self._st.skips = s
    self:_edited({ id = id, kind = 'event', action = 'skip', date = date })
end

--- Undo skip(). @api public
function Cal:unskip(id, date)
    local s = copy(self._st.skips)
    s[skipKey(id, date)] = nil
    self._st.skips = s
    self:_replan()
    if self._byId[id] then self:_edited({ id = id, kind = 'event', action = 'unskip', date = date }) end
end

--- Pause events in a range of days: pause('2026-12-24..2027-01-02', { tags = {'dzieci'} }). @api public
function Cal:pause(range, opts)
    local a, b = tostring(range):match('^(%d%d%d%d%-%d%d%-%d%d)%.%.(%d%d%d%d%-%d%d%-%d%d)$')
    if not a then a = tostring(range):match('^(%d%d%d%d%-%d%d%-%d%d)$'); b = a end
    if not a or b < a then error('Calendar ' .. self.id .. ':pause: range expected as YYYY-MM-DD..YYYY-MM-DD', 2) end
    for _, d in ipairs({ a, b }) do
        if not pcall(__go_calendar_day, d) then error('Calendar ' .. self.id .. ':pause: ' .. d .. ' is not a day', 2) end
    end
    local p = copy(self._st.pauses)
    local pause = { from = a, to = b, tags = opts and opts.tags and list(opts.tags) or nil }
    local tags = {}
    for _, ev in ipairs(self._events) do
        for _, et in ipairs(ev.tags) do tags[et] = true end
    end
    for _, tag in ipairs(pause.tags or {}) do
        if not tags[tag] then
            error('Calendar ' .. self.id .. ':pause: no event has the tag "' .. tostring(tag) .. '" (tags: ' .. quoted(tags) .. ')', 2)
        end
    end
    for _, ev in ipairs(self._events) do
        if pausedBy(pause, ev) then self:_endNow(ev, function(o) return o.date >= a and o.date <= b end) end
    end
    p[#p + 1] = pause
    self._st.pauses = p
    self:_edited({ kind = 'event', action = 'pause', from = a, to = b, tags = pause.tags })
end

--- Remove all pauses. @api public
function Cal:resume()
    self._st.pauses = {}
    self:_replan()
    self:_edited({ kind = 'event', action = 'resume' })
end

--- Pauses not over yet: { {from, to, tags}, ... }. @api public
function Cal:paused()
    local today = __go_calendar_day(now()).date
    local out = {}
    for _, p in ipairs(self._st.pauses or {}) do
        if p.to >= today then out[#out + 1] = copy(p) end
    end
    return out
end

-- ---------- firing ----------

-- Whole days ('-1d') keep the wall-clock time across a DST change; hours are exact.
local function remindAt(at, s)
    if s ~= 0 and s % 86400 == 0 then return __go_calendar_add_days(at, s / 86400) end
    return at + s
end

-- An occurrence going on at t ends now when it is skipped, paused or removed: what its start
-- switched on is switched off. match(o) picks the occurrences (all when nil).
function Cal:_endNow(ev, match)
    local s = self._s
    if not s.ready or s.stopped or not ev.rule then return end
    local t = now()
    for _, o in ipairs(__go_calendar_between(ev.rule, t - ev.jitter, t + ev.jitter + 1, true)) do
        if startOf(ev, o) <= t and o.ends and o.ends > t and (not match or match(o))
            and not (self._st.skips or {})[skipKey(ev.id, o.date)] and not self:_pausedAt(ev, o.date) then
            local e = self:_occ(ev, o)
            e.kind, e.cancelled = 'end', true
            self:_emit('end', ev.id, e)
        end
    end
end

-- Moments in (a, b]: { t, kind, ev, occ, offset }.
function Cal:_moments(a, b)
    local out = {}
    local skips = self._st.skips or {}
    for _, ev in ipairs(self._events) do
        local lead, lag = ev.jitter, ev.jitter
        for _, r in ipairs(ev.remind) do
            if r.s < 0 then lead = math.max(lead, -r.s + 3600) else lag = math.max(lag, r.s + 3600) end   -- an hour for DST
        end
        for _, o in ipairs(__go_calendar_between(ev.rule, a - lag, b + lead + 1, true)) do
            if not skips[skipKey(ev.id, o.date)] then
                local start = startOf(ev, o)
                local function add(t, kind, r)
                    if t > a and t <= b then out[#out + 1] = { t = t, kind = kind, ev = ev, occ = o, r = r } end
                end
                add(start, 'start')
                if o.ends then add(o.ends, 'end') end
                for _, r in ipairs(ev.remind) do add(remindAt(o.at, r.s), 'reminder', r) end
            end
        end
    end
    -- at one instant: ends first, so a light handed from one event to the next stays on
    table.sort(out, function(x, y)
        if x.t ~= y.t then return x.t < y.t end
        if x.kind ~= y.kind then return KIND_ORDER[x.kind] < KIND_ORDER[y.kind] end
        if x.ev.id ~= y.ev.id then return x.ev.id < y.ev.id end
        return x.occ.at < y.occ.at
    end)
    return out
end

function Cal:_text(ev, o)
    local dt = datetime.fromTimestamp(o.at)
    return tostring(ev.title) .. ' ' .. datetime.format(dt, o.allDay and '02.01' or '02.01 15:04')
end

function Cal:_fire(m, extra)
    local ev, o = m.ev, m.occ
    if self:_pausedAt(ev, o.date) or (self._st.skips or {})[skipKey(ev.id, o.date)] then return end
    local e = self:_occ(ev, o)
    e.kind = m.kind
    for k, v in pairs(extra or {}) do e[k] = v end
    if m.kind == 'reminder' then
        e.offset, e.text = m.r.label, self:_text(ev, o)
        local ch = ev.notify or self._notify
        if ch then
            local ok, err
            if type(ch) == 'function' then ok, err = pcall(ch, e) else ok, err = pcall(ch.send, ch, e) end
            if not ok then log():error('[CALENDAR] %s %s notify: %s', self.id, ev.id, tostring(err)) end
        end
    end
    self:_emit(m.kind, ev.id, e)
end

function Cal:_fireSafe(m)
    local ok, err = pcall(self._fire, self, m)
    if not ok then log():error('[CALENDAR] %s %s %s: %s', self.id, m.kind, m.ev.id, tostring(err)) end
end

-- Schedules: fire change for each one whose value differs from the last one seen.
function Cal:_schedulesChanged(t)
    for _, sc in ipairs(self._schedules) do
        local idx = __go_calendar_schedule(sc.rules, t)
        local v = idx == 0 and sc.default or sc.values[idx]
        if not sc.seen or v ~= sc.last then
            local old = sc.last
            sc.last, sc.seen = v, true
            self:_emit('change', sc.id, v, old)
        end
    end
end

function Cal:_catchup(t)
    for _, ev in ipairs(self._events) do
        if ev.catchup then
            for _, o in ipairs(__go_calendar_between(ev.rule, t - ev.jitter, t + ev.jitter + 1, true)) do
                if startOf(ev, o) < t and o.ends and o.ends > t and not (self._st.skips or {})[skipKey(ev.id, o.date)] then
                    self:_fire({ kind = 'start', ev = ev, occ = o }, { catchup = true })
                end
            end
        end
    end
end

-- Plans the timer again after a change that may add a moment sooner (add, unskip, resume).
-- Not from inside _check, which plans at its end; s.last stays, so nothing is lost.
function Cal:_replan()
    local s = self._s
    if not s.ready or s.stopped or s.inCheck then return end
    if s.timer then EventBus:getShared():clearInterval(s.timer) end
    self:_next(now())
end

-- A check that raises still leaves a timer: one bad moment must not stop the calendar.
function Cal:_tick()
    local s = self._s
    s.inCheck = true
    -- a check stopped at the callback time limit (#114) skips the rest of _tick: this timer stays
    local guard = EventBus:getShared():setTimeout(function() self:_stopped() end, Calendar.CHECK_S * 1000)
    local ok, err = pcall(self._check, self)
    EventBus:getShared():clearInterval(guard)
    s.inCheck = false
    if not ok then
        log():error('[CALENDAR] %s: %s', self.id, tostring(err))
        if not s.stopped and not s.timer then
            s.timer = EventBus:getShared():setTimeout(function() self:_tick() end, Calendar.CHECK_S * 1000)
        end
    end
end

-- After a stopped check: its moments do not fire again (the same handler would loop again).
function Cal:_stopped()
    local s = self._s
    if s.timer then EventBus:getShared():clearInterval(s.timer) end
    s.timer = nil
    if s.ready and s.checkAt and s.checkAt > s.last then
        log():warn('[CALENDAR] %s: a check was stopped; moments up to %s do not fire', self.id, os.date('%H:%M:%S', math.floor(s.checkAt)))
        s.last = s.checkAt
    end
    self:_tick()
end

function Cal:_next(t)
    local s = self._s
    local wake = Calendar.CHECK_S
    local m = self:_moments(math.max(t, s.last or t), t + wake)
    if m[1] then wake = m[1].t - t end
    for _, sc in ipairs(self._schedules) do
        local _, at = __go_calendar_schedule(sc.rules, t, wake)
        if at and at - t < wake then wake = at - t end
    end
    wake = math.max(wake, 0.001)
    s.expect = t + wake
    s.timer = EventBus:getShared():setTimeout(function() self:_tick() end, math.ceil(wake * 1000))
end

function Cal:_check()
    local s = self._s
    if s.stopped then return end
    s.timer = nil
    local t = now()
    s.checkAt = t
    if not s.ready then
        local trusted, reason = __go_calendar_clock()
        if not self._clock.wait and os.date('*t', t).year >= 2025 then trusted = true end
        if not trusted and os.date('*t', t).year >= 2025 and t - s.waitSince >= Calendar.MAX_WAIT_S then
            log():warn('[CALENDAR] %s: %s; starting anyway after %d s', self.id, tostring(reason), Calendar.MAX_WAIT_S)
            trusted = true
        end
        if not trusted then
            if not s.warned then
                log():warn('[CALENDAR] %s waits for the clock: %s', self.id, tostring(reason))
                s.warned = true
            end
            s.reason = reason
            s.timer = EventBus:getShared():setTimeout(function() self:_tick() end, Calendar.WAIT_POLL_S * 1000)
            return
        end
        s.ready, s.reason, s.last = true, nil, t
        self:_schedulesChanged(t)
        self:_catchup(t)
        self:_emit('ready', nil, { calendar = self.id })
        if s.stopped then return end
        return self:_next(t)
    end
    -- s.last only grows: after a step back, moments up to it do not fire again
    if s.expect and math.abs(t - s.expect) > Calendar.JUMP_S then
        log():warn('[CALENDAR] %s: clock jumped by %d s; skipped moments do not fire', self.id, math.floor(t - s.expect))
        self:_schedulesChanged(t)
        if t > s.last then
            -- what was going on before the jump and ended during it still ends (a light goes off)
            for _, m in ipairs(self:_moments(s.last, t)) do
                if s.stopped then return end
                if m.kind == 'end' and startOf(m.ev, m.occ) <= s.last then self:_fireSafe(m) end
            end
            s.last = t
            self:_catchup(t)
        end
    else
        for _, m in ipairs(self:_moments(s.last, t)) do
            if s.stopped then return end
            self:_fireSafe(m)
        end
        s.last = math.max(s.last, t)
        self:_schedulesChanged(t)
    end
    if s.stopped then return end
    self:_next(t)
end

--- State for MCP and the panel: ready, reason, events, schedules. @api public
function Cal:info()
    local out = { id = self.id, name = self.name, icon = self.icon, ready = self._s.ready,
        waiting = (not self._s.ready) and self._s.reason or nil, holidays = self._holidays,
        location = self._ctx.lat and { lat = self._ctx.lat, lon = self._ctx.lon } or nil,
        events = {}, schedules = {}, pauses = self:paused() }
    for _, ev in ipairs(self._events) do
        out.events[#out.events + 1] = { id = ev.id, title = ev.title, source = ev.source, tags = ev.tags }
    end
    for _, sc in ipairs(self._schedules) do
        out.schedules[#out.schedules + 1] = { id = sc.id, title = sc.title, value = self:value(sc.id) }
    end
    return out
end

-- ==========================================================================
-- API for the vclu_calendar MCP tool (read only)
-- ==========================================================================

Calendar.API = {}

local function stamp(ts) return ts and datetime.format(datetime.fromTimestamp(ts), '2006-01-02T15:04-07:00') or nil end
local function hm(ts) return ts and datetime.format(datetime.fromTimestamp(ts), '15:04') or nil end

-- 'today', 'tomorrow', a date or nil (default) to a day of the calendar
local function agendaDay(cal, v, default)
    if v == nil then return default end
    local today = cal:day()
    if v == 'today' then return today end
    if v == 'tomorrow' then return cal:day(today.ends) end
    return cal:day(v)
end

-- the days from..to, or nil and an error
local function agendaRange(cal, from, to, maxDays)
    local okA, a = pcall(agendaDay, cal, from, cal:day())
    local okB, b = pcall(agendaDay, cal, to, nil)
    if not okA or not okB then return nil, 'from, to and date expected as YYYY-MM-DD, today or tomorrow' end
    if not b then
        b = a
        for _ = 1, 6 do b = cal:day(b.ends) end
    end
    if b.starts < a.starts then return nil, 'to is before from' end
    -- whole days: a range over a clock change is an hour short or long
    if math.floor((b.starts - a.starts) / 86400 + 0.5) >= maxDays then return nil, 'at most ' .. maxDays .. ' days' end
    return a.date, b.date
end

local function agendaOf(cal, from, to, tags)
    local out = cal:info()
    out.from, out.to = from, to
    local a, b = cal:day(from), cal:day(to)
    local one = a.date == b.date
    local byDate, days = {}, {}
    local d = a
    while d.starts <= b.starts do
        local day = { date = d.date, weekday = d.weekday, workday = d.workday,
            holiday = d.holidayName, events = {} }
        if one then
            day.namedays = d.namedays
            local s = cal:sun(d.date)
            if s then
                day.sun = { dawn = hm(s.dawn), sunrise = hm(s.sunrise), sunset = hm(s.sunset),
                    dusk = hm(s.dusk), polar = s.polar }
            end
        end
        days[#days + 1] = day
        byDate[d.date] = day
        d = cal:day(d.ends)
    end
    for _, e in ipairs(cal:between(a.date, b.date, { tags = tags })) do
        local item = { id = e.id, title = e.title, tags = e.tags, source = e.source, allDay = e.allDay,
            start = stamp(e.at), ['end'] = stamp(e.ends), last = e.last ~= e.date and e.last or nil,
            paused = e.paused or nil }
        local day = byDate[e.date] or days[1]   -- going on since before from
        day.events[#day.events + 1] = item
    end
    out.days = days
    -- a schedule's value and next change are about now, its segments about the day asked for
    for _, sc in ipairs(out.schedules) do
        sc.now, sc.value = sc.value, nil
        if cal:isReady() then
            local n = cal:nextChange(sc.id)
            sc.nextChange = n and { value = n.value, at = stamp(n.at) } or nil
            if one then
                sc.segments = {}
                for _, sg in ipairs(cal:segments(sc.id, a.date)) do
                    sc.segments[#sc.segments + 1] = { from = hm(sg.from),
                        to = sg.to >= a.ends and '24:00' or hm(sg.to), value = sg.value }
                end
            end
        end
    end
    return out
end

--- Agenda of one calendar (or all when id is nil) for the days from..to: 'YYYY-MM-DD',
-- 'today' or 'tomorrow'; from defaults to today, to to a week from from.
-- @return string JSON { calendars = [{ id, ready, from, to, days = [{ date, holiday, events }], schedules }] }
function Calendar.API.agenda(id, from, to, tags, maxDays)
    maxDays = maxDays or 62
    local list = {}
    if id then
        local cal = calendars[id]
        if not cal then return JSON.encode({ error = 'calendar "' .. tostring(id) .. '" not built', known = order }) end
        list[1] = cal
    else
        list = Calendar.list()
    end
    local out = {}
    if list[1] then
        local a, b = agendaRange(list[1], from, to, maxDays)
        if not a then return JSON.encode({ error = b }) end
        from, to = a, b
    end
    for _, cal in ipairs(list) do
        local ok, res = pcall(agendaOf, cal, from, to, tags)
        out[#out + 1] = ok and res or { id = cal.id, error = tostring(res) }
    end
    return JSON.encode({ calendars = out })
end

return Calendar
