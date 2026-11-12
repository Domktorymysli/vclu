--- Cloud - notifications to the members' phones through vclu cloud.
--
--   Cloud.notify("Pralka skończyła")
--   Cloud.notify{ title = "Alarm!", body = "Strefa 2: ruch", priority = "high",
--       to = "admins", tag = "alarm", open = "alarm",
--       actions = {
--           { label = "Rozbrój", roles = { "admin" },
--             run = function(who) Satel.partition(1):disarm(); return "Rozbrojono (" .. who.name .. ")" end },
--           { label = "Otwórz bramę", run = function() BRAMA:execute(0) end },
--       } }
--   Calendar.new('dom'):notify(Cloud.channel{ to = "all" })
--
--   Cloud.onPresence(function(e)
--       if not e.anyone then Satel.partition(1):arm() end      -- the last one left
--       if e.home and e.name == "Anna" then SALON_LAMPA:switchOn() end
--   end)
--   if Cloud.anyoneHome() then ... end
--
--   Satel.partition(1):onArmed(function() Cloud.disable() end)   -- no outside access while armed
--   Cloud.enable(); if Cloud.enabled() then ... end
--
-- vCLU sends the notification to the cloud, which picks the people (to) and sends a push to
-- their phones; the cloud never sees the buttons' code, only their labels. A pressed button
-- comes back with who pressed it, and its run(who) runs here, as that person (origin "remote",
-- actor "cloud:<id>" in history). While the cloud is unreachable notifications wait up to
-- 10 minutes. Buttons work for `expires` seconds (default 1 h, at most 24 h) and until a Lua
-- reload. The cloud limits a home to 5 notifications a minute and 30 an hour.
--
-- Presence comes from the members' phones: each person who turned it on in the app is "home" or
-- "away" (a geofence on the phone; the cloud never learns where the home or the phone is).
-- A change of someone known calls the onPresence handlers, as that person (origin "remote");
-- the first list after vCLU starts, and someone starting or stopping to share, only set the state.
-- @module core.Cloud

local Cloud = { DEFAULT_EXPIRES = 3600, MAX_EXPIRES = 86400, MAX_ACTIONS = 3 }

local sent = {}   -- notification id -> { expires = unix s, buttons = { id -> { run, roles } } }
local seq = { n = 0 }

local NOTIFY_KEYS = { id = true, title = true, body = true, priority = true, to = true, tag = true,
    open = true, actions = true, expires = true }

local presence = { known = false, people = {} }   -- user -> { user, name, home, since }
local handlers = {}

local function log() return Logger:getShared() end
local function now() return __go_time_ms and __go_time_ms() / 1000 or os.time() end

local function users(list)
    local out = {}
    for i, u in ipairs(list) do
        if type(u) == 'number' then
            out[i] = 'cloud:' .. string.format('%d', u)
        elseif type(u) == 'string' and u:match('^cloud:%d+$') then
            out[i] = u
        else
            error('Cloud.notify: to lists people as cloud:<id> or numbers, got ' .. tostring(u), 3)
        end
    end
    return out
end

local function prune(t)
    for id, n in pairs(sent) do
        if n.expires <= t then sent[id] = nil end
    end
end

--- Sends a notification to the members' phones. opts: title (required), body, priority ("normal" or "high"), to ("all", "admins" or a list of cloud ids), tag (a newer one with the same tag replaces it), open (a dashboard id), actions (up to 3 buttons { label, run = function(who) ... end, roles = { "admin" } (who may press it; "admin" includes the owner), id }; run returns the text shown, or false and why), expires (seconds the buttons work, default 3600). Cloud.notify(title, body) is the short form.
-- @param opts table|string The options, or the title
-- @param body string|nil The body, with a title
-- @return string|nil The notification's id; nil and the reason when not sent
-- @api public
function Cloud.notify(opts, body)
    if type(opts) == 'string' then opts = { title = opts, body = body } end
    if type(opts) ~= 'table' then error('Cloud.notify: expects a title or a table', 2) end
    for k in pairs(opts) do
        if not NOTIFY_KEYS[k] then error('Cloud.notify: unknown option "' .. tostring(k) .. '"', 2) end
    end
    if type(opts.title) ~= 'string' or opts.title:match('^%s*$') then
        error('Cloud.notify: title is required', 2)
    end
    local t = now()
    seq.n = seq.n + 1
    local id = opts.id or string.format('n%d-%d', math.floor(t), seq.n)
    local msg = { type = 'notify', id = id, title = opts.title, body = opts.body, tag = opts.tag }

    if opts.priority ~= nil and opts.priority ~= 'normal' and opts.priority ~= 'high' then
        error('Cloud.notify: priority is "normal" or "high"', 2)
    end
    msg.priority = opts.priority
    if type(opts.to) == 'table' then
        msg.to, msg.users = 'users', users(opts.to)
    elseif opts.to == nil or opts.to == 'all' or opts.to == 'admins' then
        msg.to = opts.to
    else
        error('Cloud.notify: to is "all", "admins" or a list of people', 2)
    end
    if opts.open ~= nil then
        local d = tostring(opts.open):gsub('^/d/', '')
        msg.open = '/d/' .. d
    end

    local buttons
    if opts.actions ~= nil then
        if type(opts.actions) ~= 'table' or #opts.actions > Cloud.MAX_ACTIONS then
            error('Cloud.notify: actions is a list of at most ' .. Cloud.MAX_ACTIONS .. ' buttons', 2)
        end
        buttons, msg.actions = {}, {}
        for i, a in ipairs(opts.actions) do
            if type(a) ~= 'table' or type(a.label) ~= 'string' or type(a.run) ~= 'function' then
                error('Cloud.notify: action ' .. i .. ' needs a label and run = function(who)', 2)
            end
            local aid = a.id or tostring(i)
            buttons[aid] = { run = a.run, roles = a.roles }
            msg.actions[i] = { id = aid, label = a.label }
        end
    end

    if not __go_cloud_notify then return nil, 'vclu cloud is not available' end
    local ok, err = __go_cloud_notify(JSON.encode(msg))
    if not ok then
        log():warn('[CLOUD] notification not sent: %s', tostring(err))
        return nil, err
    end
    prune(t)
    if buttons then
        local exp = math.min(tonumber(opts.expires) or Cloud.DEFAULT_EXPIRES, Cloud.MAX_EXPIRES)
        sent[id] = { expires = t + exp, buttons = buttons }
    end
    return id
end

--- A channel for Calendar:notify and other code that takes an object with :send(e): each event becomes a notification with the event's title.
-- @param opts table|nil Options of Cloud.notify for every event (to, priority, open)
-- @return table An object with :send(e)
-- @api public
function Cloud.channel(opts)
    opts = opts or {}
    local kinds = { reminder = 'Przypomnienie', start = 'Teraz', ['end'] = 'Koniec' }
    return {
        send = function(_self, e)
            local when = e.at and os.date and os.date('%H:%M', math.floor(e.at)) or nil
            local body = kinds[e.kind] or e.kind
            if when and not e.allDay then body = (body and body .. ' · ' or '') .. when end
            return Cloud.notify({
                title = e.title or e.id or 'Kalendarz', body = body, to = opts.to, priority = opts.priority,
                open = opts.open, tag = 'calendar:' .. tostring(e.calendar) .. ':' .. tostring(e.id),
            })
        end,
    }
end

local function decodePeople(json)
    local out = {}
    local ok, list = pcall(JSON.decode, json)
    if not ok or type(list) ~= 'table' then return out end
    for _, p in ipairs(list) do
        if type(p) == 'table' and type(p.user) == 'string' then
            out[p.user] = { user = p.user, name = p.name or p.user, home = p.state == 'home', since = p.since }
        end
    end
    return out
end

local function anyone(people)
    for _, p in pairs(people) do if p.home then return true end end
    return false
end

local function copy(p) return { user = p.user, name = p.name, home = p.home, since = p.since } end

-- After a Lua reload: the list the cloud client already has, so the next change is a change.
if __go_cloud_presence then
    local json = __go_cloud_presence()
    if json then presence.people, presence.known = decodePeople(json), true end
end

--- Everyone who shares their presence with this home, sorted by name.
-- @return table List of { user = "cloud:<id>", name, home (boolean), since (unix seconds of the last change) }
-- @api public
function Cloud.presence()
    local out = {}
    for _, p in pairs(presence.people) do out[#out + 1] = copy(p) end
    table.sort(out, function(a, b) return a.name < b.name end)
    return out
end

--- Whether someone is at home, by their name or cloud id.
-- @param who string A name as in the cloud ("Anna") or "cloud:<id>"
-- @return boolean|nil true or false, nil when they do not share their presence
-- @api public
function Cloud.isHome(who)
    for _, p in pairs(presence.people) do
        if p.user == who or p.name == who then return p.home end
    end
    return nil
end

--- Switches the connection to the cloud off, keeping the pairing: dashboards, the app and
-- notifications stop until Cloud.enable() or the switch on the cloud page. It is saved and
-- survives a restart; the cloud shows the home as switched off and sends no offline alert.
-- @return boolean|nil true, or nil and an error (e.g. not paired)
-- @api public
function Cloud.disable()
    if not __go_cloud_set_enabled then return nil, 'vclu cloud is not available' end
    return __go_cloud_set_enabled(false)
end

--- Switches the connection to the cloud on again, with the same pairing.
-- @return boolean|nil true, or nil and an error (e.g. not paired)
-- @api public
function Cloud.enable()
    if not __go_cloud_set_enabled then return nil, 'vclu cloud is not available' end
    return __go_cloud_set_enabled(true)
end

--- Whether vCLU is paired with the cloud and the connection is switched on.
-- @return boolean
-- @api public
function Cloud.enabled()
    return __go_cloud_enabled ~= nil and __go_cloud_enabled()
end

--- Whether anyone who shares their presence is at home (false when nobody shares it).
-- @return boolean
-- @api public
function Cloud.anyoneHome()
    return anyone(presence.people)
end

--- Calls fn(e) when someone comes home or leaves: e = { user, name, home, since, anyone } (anyone: whether anyone is at home after this change). Runs as that person (origin "remote").
-- @param fn function The handler
-- @return function Call it to stop
-- @api public
function Cloud.onPresence(fn)
    if type(fn) ~= 'function' then error('Cloud.onPresence: expects a function', 2) end
    local h = { fn = fn }
    handlers[#handlers + 1] = h
    return function()
        for i, x in ipairs(handlers) do
            if x == h then table.remove(handlers, i) return end
        end
    end
end

--- Takes the presence list from the cloud (called by Go) and calls the handlers for each change.
function Cloud._presence(json)
    local new = decodePeople(json)
    local old, known = presence.people, presence.known
    presence.people, presence.known = new, true
    if not known then return end
    local now_anyone = anyone(new)
    for user, p in pairs(new) do
        local was = old[user]
        if was and was.home ~= p.home then
            local e = copy(p)
            e.anyone = now_anyone
            for _, h in ipairs({ table.unpack and table.unpack(handlers) or unpack(handlers) }) do
                StateContext.push({ origin = 'remote', actor = user })
                local ok, err = pcall(h.fn, e)
                StateContext.pop()
                if not ok then log():error('[CLOUD] onPresence handler: %s', tostring(err)) end
            end
        end
    end
end

--- Runs a pressed button (called by Go with the cloud's notify_action). Returns the answer as
-- JSON: { ok, reason?, message? }.
function Cloud._action(notifyId, actionId, user, name, role)
    local function answer(t) return JSON.encode(t) end
    local n = sent[notifyId]
    if not n or n.expires <= now() then
        return answer({ ok = false, reason = 'not_found', message = 'To powiadomienie już nie działa.' })
    end
    local b = n.buttons[actionId]
    if not b then return answer({ ok = false, reason = 'not_found' }) end
    if b.roles then
        local allowed = false
        -- The owner has every right of an admin.
        for _, r in ipairs(b.roles) do
            if r == role or (r == 'admin' and role == 'owner') then allowed = true end
        end
        if not allowed then
            return answer({ ok = false, reason = 'forbidden', message = 'Nie masz do tego uprawnień.' })
        end
    end
    local ok, r1, r2 = pcall(b.run, { user = user, name = name, role = role })
    if not ok then
        log():error('[CLOUD] button %s of %s: %s', tostring(actionId), tostring(notifyId), tostring(r1))
        return answer({ ok = false, reason = 'error', message = 'Błąd w skrypcie vCLU.' })
    end
    if r1 == false then
        return answer({ ok = false, reason = 'error', message = type(r2) == 'string' and r2 or 'Nie udało się.' })
    end
    return answer({ ok = true, message = type(r1) == 'string' and r1 or nil })
end

-- Remote commands (the protocol's cmd) for devices exposed with :remote() (ExposedObjects.lua).
-- Only these actions, each on the device's own method; never a method named by the cloud.
local ACTIONS = {
    switch = { on = true, off = true },
    light = { on = true, off = true },
    dimmer = { on = true, off = true, set = true },
    cover = { open = true, close = true, stop = true, set = true },
    scene = { execute = true },
}
-- Five wrong PINs in a row lock the device, for 30 s, then twice as long each time, up to an
-- hour: a 4-digit PIN would otherwise fall to a few hundred guesses an hour. The right PIN
-- clears it all.
local PIN_TRIES, PIN_WAIT_MS, PIN_WAIT_MAX_MS = 5, 30000, 3600000
local pinFails = {} -- path -> { n = wrong PINs in a row, locks = locks so far, untilMs = locked until }

-- The clock the cloud client computed the deadline with (Go's wall clock)
local function wallMs()
    if __go_cloud_now_ms then return __go_cloud_now_ms() end
    return os.time() * 1000
end

local function hasMethod(obj, name)
    return type(obj) == 'table' and type(obj[name]) == 'function'
end

-- Runs fn(...) protected: true, or false and the device's own message
local function run(fn, ...)
    local ok, r1, r2 = pcall(fn, ...)
    if not ok then
        log():error('[CLOUD] command: %s', tostring(r1))
        return false, 'Błąd w skrypcie vCLU.'
    end
    if r1 == false then return false, type(r2) == 'string' and r2 or nil end
    return true
end

-- Calls the device's own method for action (set with value 0..100): true, false and why, or nil
-- when it has none
local function perform(h, action, value)
    local obj = h.object
    if action == 'set' then
        if h.type == 'dimmer' then
            if hasMethod(obj, 'setBrightness') then return run(obj.setBrightness, obj, value) end
            return run(h.setValue, h, value / 100)
        end
        -- a Grenton roller without a position (class 11) only opens and closes
        if type(obj) == 'table' and obj._hasPosition == false then return nil end
        if not hasMethod(obj, 'setPosition') then return nil end
        return run(obj.setPosition, obj, value)
    end
    if action == 'on' or action == 'off' then
        local method = action == 'on' and 'switchOn' or 'switchOff'
        if hasMethod(obj, method) then return run(obj[method], obj) end
        return run(h.setValue, h, action == 'on' and 1 or 0)
    elseif action == 'open' or action == 'close' or action == 'stop' then
        -- a roller moves by its own methods; setValue would write its feature 0
        if not hasMethod(obj, action) then return nil end
        return run(obj[action], obj)
    elseif action == 'execute' then
        if not hasMethod(obj, 'execute') then return nil end
        -- false is the cooldown after a run a moment ago: the scene ran
        local ok, err = pcall(h.executeScene, h)
        if not ok then
            log():error('[CLOUD] command: %s', tostring(err))
            return false, 'Błąd w skrypcie vCLU.'
        end
        return true
    end
    return nil
end

-- Whether the person's roles (JSON array from the cloud) reach a device with these roles
local function rolesAllow(want, rolesJSON)
    local ok, roles = pcall(JSON.decode, rolesJSON)
    if not ok or type(roles) ~= 'table' then return false end
    if #want == 0 then return true end
    for i = 1, #roles do
        for j = 1, #want do
            if roles[i] == want[j] then return true end
        end
    end
    return false
end

-- Runs a command on h once who asks may (Cloud._command, Cloud._clientCommand), with access the
-- device's for them: a readonly one refuses, then the confirmation, the PIN, the action and the
-- deadline, the same for every way in. Returns the answer as JSON: { ok, reason?, message? }.
local function command(h, path, access, action, confirmed, pin, deadlineMs, value)
    local function answer(ok, reason, message)
        return JSON.encode({ ok = ok, reason = reason, message = message })
    end
    local needPin, needConfirm = ExposedObjects._guards(h)
    if access == 'readonly' then
        return answer(false, 'forbidden', 'To urządzenie jest tylko do podglądu.')
    end
    if needConfirm and confirmed ~= true then
        return answer(false, 'confirm', 'Potwierdź polecenie.')
    end
    if needPin then
        local f = pinFails[path]
        if f and f.untilMs and wallMs() < f.untilMs then
            return answer(false, 'rate_limited', 'Za dużo błędnych PIN-ów. Spróbuj za chwilę.')
        end
        if pin == nil or tostring(pin) == '' then
            return answer(false, 'pin_required', 'Podaj PIN.')
        end
        if tostring(pin) ~= h._pin then
            f = f or { n = 0, locks = 0 }
            f.n, f.untilMs = f.n + 1, nil
            if f.n >= PIN_TRIES then
                local wait = math.min(PIN_WAIT_MS * 2 ^ f.locks, PIN_WAIT_MAX_MS)
                f.n, f.locks, f.untilMs = 0, f.locks + 1, wallMs() + wait
                log():warn('[CLOUD] %s: %d wrong PINs in a row, locked for %d s', tostring(path), PIN_TRIES, math.floor(wait / 1000))
            end
            pinFails[path] = f
            return answer(false, 'pin_wrong', 'Zły PIN.')
        end
        pinFails[path] = nil
    end

    local fits = ACTIONS[h.type]
    if not fits or not fits[action] then
        return answer(false, 'invalid', 'Tego nie da się zrobić z tym urządzeniem.')
    end
    value = tonumber(value)
    if action == 'set' and (not value or value - value ~= 0 or value < 0 or value > 100) then
        return answer(false, 'invalid', 'Poziom musi być od 0 do 100%.')
    end
    -- the Lua queue may have held the command past the time the caller waits
    local deadline = tonumber(deadlineMs)
    if deadline and wallMs() > deadline then
        return answer(false, 'expired', 'Polecenie przyszło za późno.')
    end

    local ok, err = perform(h, action, value)
    if ok == nil then
        return answer(false, 'invalid', 'Tego nie da się zrobić z tym urządzeniem.')
    end
    if not ok then
        log():warn('[CLOUD] command %s on %s: %s', tostring(action), tostring(path), tostring(err))
        return answer(false, 'error', err or 'Nie udało się.')
    end
    return answer(true)
end

local function notFound()
    return JSON.encode({ ok = false, reason = 'not_found', message = 'Nie ma takiego urządzenia.' })
end

--- Runs a remote command on a device the cloud reaches (:remote() or the Access page; called by Go with the cloud's cmd,
-- as the person). Returns the answer as JSON: { ok, reason?, message? }.
function Cloud._command(path, action, rolesJSON, confirmed, pin, deadlineMs, value)
    local h = ExposedObjects and ExposedObjects:_remoteHandle(path)
    if not h then return notFound() end
    if not rolesAllow(ExposedObjects._remoteRoles(h), rolesJSON) then
        return JSON.encode({ ok = false, reason = 'forbidden', message = 'Nie masz do tego uprawnień.' })
    end
    return command(h, path, ExposedObjects._remoteAccess(h), action, confirmed, pin, deadlineMs, value)
end

--- The same command for a client of the device API (pkg/web/clients.go, as the client): any
-- exposed device, level the client's access profiles give it ("full" or "readonly"; Go
-- answers not_found for hidden ones). The device's PIN and confirmation hold here too.
function Cloud._clientCommand(path, action, level, confirmed, pin, deadlineMs, value)
    local h = ExposedObjects and ExposedObjects:_deviceHandle(path)
    if not h or (level ~= 'full' and level ~= 'readonly') then return notFound() end
    local access = level == 'readonly' and 'readonly' or ExposedObjects._deviceAccess(h)
    return command(h, path, access, action, confirmed, pin, deadlineMs, value)
end

-- What each type's state is (the protocol's states): on for these, a cover's position, a reading
local ON_TYPES = { light = true, switch = true, dimmer = true, fan = true, binary_sensor = true,
    motion = true, sensor = true }
local READING_TYPES = { temperature = true, humidity = true, illuminance = true }

-- A finite number, or nil
local function finite(v)
    v = tonumber(v)
    if v and v == v and v ~= math.huge and v ~= -math.huge then return v end
    return nil
end

-- One device's state for the cloud, or nil when its type has none or it cannot be read
local function remoteState(path, h)
    local obj = h.object
    if h.type == 'cover' then
        -- a Grenton roller without a position (class 11) has none to tell
        if type(obj) ~= 'table' or obj._hasPosition == false or not hasMethod(obj, 'getPosition') then return nil end
        local ok, p = pcall(obj.getPosition, obj)
        p = ok and finite(p)
        if not p then return nil end
        return { path = path, position = math.max(0, math.min(100, p)) }
    end
    if not ON_TYPES[h.type] and not READING_TYPES[h.type] then return nil end
    local ok, v = pcall(h.getValue, h)
    if not ok or v == nil then return nil end
    if READING_TYPES[h.type] then
        v = finite(v)
        return v and { path = path, value = v } or nil
    end
    local n = finite(v)
    return { path = path, on = v == true or (n ~= nil and n > 0) }
end

--- What remote devices are doing, for the cloud's states (called by Go, as nobody: reading
-- changes nothing): those the cloud reaches (:remote() or the Access page) whose roles reach the
-- person's. Others, and devices that cannot be read, are left out.
-- @param pathsJSON string JSON array of paths
-- @param rolesJSON string JSON array of the person's roles
-- @return string JSON array of {path, on?, position?, value?}
function Cloud._states(pathsJSON, rolesJSON)
    local out = {}
    local ok, paths = pcall(JSON.decode, pathsJSON)
    if not ok or type(paths) ~= 'table' then return JSON.encode(out) end
    for i = 1, #paths do
        local path = paths[i]
        local h = type(path) == 'string' and ExposedObjects and ExposedObjects:_remoteHandle(path)
        if h and rolesAllow(ExposedObjects._remoteRoles(h), rolesJSON) then
            local st = remoteState(path, h)
            if st then out[#out + 1] = st end
        end
    end
    return JSON.encode(out)
end

--- What devices are doing, for a client of the device API: Go passes only the paths the client's
-- profiles let it read. Devices that cannot be read are left out.
-- @param pathsJSON string JSON array of paths
-- @return string JSON array of {path, on?, position?, value?}
function Cloud._clientStates(pathsJSON)
    local out = {}
    local ok, paths = pcall(JSON.decode, pathsJSON)
    if not ok or type(paths) ~= 'table' then return JSON.encode(out) end
    for i = 1, #paths do
        local path = paths[i]
        local h = type(path) == 'string' and ExposedObjects and ExposedObjects:_deviceHandle(path)
        if h then
            local st = remoteState(path, h)
            if st then out[#out + 1] = st end
        end
    end
    return JSON.encode(out)
end

return Cloud
