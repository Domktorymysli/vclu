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
local KINDS = { start = true, ['end'] = true, reminder = true, change = true, ready = true }
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
        if type(k) ~= 'number' and k ~= 'default' and k ~= 'title' and k ~= 'colors' and k ~= 'values' then
            error(where .. ': unknown option "' .. tostring(k) .. '" ("colors", "default", "title", "values", windows)', 2)
        end
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
    local item = { id = id, kind = 'schedule', opts = opts }
    self._schedules[#self._schedules + 1] = item
    self._byId[id] = item
    return self
end

-- ==========================================================================
-- Calendar instance
-- ==========================================================================

local Cal = {}
Cal.__index = Cal

--- Register the calendar and start it. Replaces a calendar with the same id. @api public
function Builder:build()
    local hol = self._holidays
    local ctx = { holidays = hol, lat = self._lat, lon = self._lon }
    local where = 'Calendar ' .. self._id
    local cal = setmetatable({ id = self._id, name = self._name, icon = self._icon,
        _holidays = hol, _ctx = ctx, _notify = self._notify, _clock = self._clock,
        _events = {}, _byId = {}, _schedules = {},
        _handlers = { start = {}, ['end'] = {}, reminder = {}, change = {}, ready = {} },
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
        cal._schedules[#cal._schedules + 1] = sc
        cal._byId[it.id] = sc
    end

    cal._st = persist('calendar.' .. cal.id, { events = {}, skips = {}, pauses = {}, seq = 0 })
    cal:_checkSaved()
    cal:_loadUserEvents()

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
    for _, k in ipairs({ 'events', 'skips', 'pauses' }) do
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

--- Subscribe: on(kind, fn) or on(kind, id, fn); kinds start, end, reminder, change, ready.
-- At one moment end comes before start; an end from skip, pause or remove has e.cancelled = true.
-- @return function unsubscribe
-- @api public
function Cal:on(kind, id, fn)
    if fn == nil then fn, id = id, nil end
    if not KINDS[kind] then error('Calendar ' .. self.id .. ':on: unknown kind "' .. tostring(kind) .. '" (start, end, reminder, change, ready)', 2) end
    if type(fn) ~= 'function' then error('Calendar ' .. self.id .. ':on: function expected', 2) end
    if id ~= nil and not self._byId[id] then error('Calendar ' .. self.id .. ':on: unknown id "' .. tostring(id) .. '"', 2) end
    if id ~= nil then
        local isSchedule = self._byId[id].rules ~= nil
        if kind == 'change' and not isSchedule then
            error('Calendar ' .. self.id .. ":on: '" .. id .. "' is an event: it has start, end and reminder, change is for schedules", 2)
        elseif kind ~= 'change' and kind ~= 'ready' and isSchedule then
            error('Calendar ' .. self.id .. ":on: '" .. id .. "' is a schedule: it has change, " .. kind .. ' is for events', 2)
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
                if not skips[skipKey(ev.id, o.date)] then
                    local e = self:_occ(ev, o)
                    e.paused = self:_pausedAt(ev, o.date)
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
        if startOf(ev, o) <= t and o.ends and o.ends > t and not (self._st.skips or {})[skipKey(id, o.date)] then return true end
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

--- Whether a date ('YYYY-MM-DD', a timestamp or today) is a workday. @api public
function Cal:isWorkday(date) return __go_calendar_day(date, self._holidays).workday end
--- Whether a date is a public holiday. @api public
function Cal:isHoliday(date) return __go_calendar_day(date, self._holidays).holiday end
--- Whether the clock is trusted and the calendar fires events. @api public
function Cal:isReady() return self._s.ready end

-- ---------- runtime changes (Persist) ----------

--- Add a user event (saved, survives a restart). Fields as in :event without functions.
-- @return string id
-- @api public
function Cal:add(opts)
    if type(opts) ~= 'table' then error('Calendar ' .. self.id .. ':add: options table expected', 2) end
    local id = opts.id
    if id == nil then
        repeat
            self._st.seq = (self._st.seq or 0) + 1
            id = 'u' .. self._st.seq
        until not self._byId[id]
    elseif type(id) ~= 'string' or self._byId[id] then
        error('Calendar ' .. self.id .. ':add: id "' .. tostring(id) .. '" is taken or not a string', 2)
    end
    local saved = {}
    for k, v in pairs(opts) do
        if k ~= 'id' then
            if type(v) == 'function' then error('Calendar ' .. self.id .. ':add: ' .. k .. ' cannot be a function', 2) end
            saved[k] = v
        end
    end
    for _, k in ipairs({ 'title', 'icon' }) do
        if saved[k] ~= nil and type(saved[k]) ~= 'string' then error('Calendar ' .. self.id .. ':add: ' .. k .. ' must be a string', 2) end
    end
    for _, tag in ipairs(list(saved.tags)) do
        if type(tag) ~= 'string' then error('Calendar ' .. self.id .. ':add: tags must be strings', 2) end
    end
    checkHolidaysNeeded('Calendar ' .. self.id .. ':add', saved, self._holidays)
    local rule = compile('Calendar ' .. self.id .. ':add', saved, USER_KEYS, self._ctx)
    local ev = { id = id, source = 'user', title = saved.title or id, icon = saved.icon, tags = list(saved.tags),
        rule = rule, remind = {}, jitter = 0 }
    for _, r in ipairs(list(saved.remind)) do ev.remind[#ev.remind + 1] = { s = remindSeconds(r, 'Calendar ' .. self.id .. ':add: remind'), label = r } end
    self._events[#self._events + 1] = ev
    self._byId[id] = ev
    local evs = copy(self._st.events)
    evs[id] = saved
    self._st.events = evs
    self:_replan()
    return id
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
end

--- Undo skip(). @api public
function Cal:unskip(id, date)
    local s = copy(self._st.skips)
    s[skipKey(id, date)] = nil
    self._st.skips = s
    self:_replan()
end

--- Pause events in a range of days: pause('2026-12-24..2027-01-02', { tags = {'dzieci'} }). @api public
function Cal:pause(range, opts)
    local a, b = tostring(range):match('^(%d%d%d%d%-%d%d%-%d%d)%.%.(%d%d%d%d%-%d%d%-%d%d)$')
    if not a then a = tostring(range):match('^(%d%d%d%d%-%d%d%-%d%d)$'); b = a end
    if not a or b < a then error('Calendar ' .. self.id .. ':pause: range expected as YYYY-MM-DD..YYYY-MM-DD', 2) end
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
end

--- Remove all pauses. @api public
function Cal:resume()
    self._st.pauses = {}
    self:_replan()
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
    local ok, err = pcall(self._check, self)
    s.inCheck = false
    if not ok then
        log():error('[CALENDAR] %s: %s', self.id, tostring(err))
        if not s.stopped and not s.timer then
            s.timer = EventBus:getShared():setTimeout(function() self:_tick() end, Calendar.CHECK_S * 1000)
        end
    end
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
    if (b.starts - a.starts) / 86400 >= maxDays then return nil, 'at most ' .. maxDays .. ' days' end
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
