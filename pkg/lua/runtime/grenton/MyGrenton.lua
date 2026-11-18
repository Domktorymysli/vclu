--- MyGrenton: the widgets of a myGrenton interface (designed in Object Manager) as dashboard widgets.
-- The files the Grenton page writes (#220) are lines of MyGrenton.widget calls; a change in
-- Grenton's format is a change here only.
--
--   Dashboard.new('grenton_page_1700000000001'):name('Hall'):tabGroup('Home')
--       :section('Hall')
--   MyGrenton.widget(section, { type = "ON_OFF", id = 2, buttonBistable = { label = "Hall light",
--       state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DOU0001", index = 0 },
--       actionOn = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 0, value = 1 },
--       actionOff = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 0, value = 0 } } })
--
-- A widget is a table as in the interface, checked on import: calls are
-- { callType, cluId, objectName, index, value } (ATTRIBUTE, PSEUDO_METHOD, METHOD, VARIABLE) or
-- { callType = "SCRIPT", cluId, objectName, args = {...} }; value "$value$" is what the user enters.
-- Reads never wait for a CLU: what the registry follows is read from it, anything else is asked
-- in the background and the widget shows the last answer.
-- @module MyGrenton

local MyGrenton = {}

-- seconds between two background reads of one value
MyGrenton.refresh = 5

-- What was left out since the last reload: { { widget = "ON_OFF \"Hall\"", reason = "..." } }
MyGrenton.leftOut = {}

local INPUT = "$value$"

local function warn(fmt, ...)
    Logger:getShared():warn("[MyGrenton] " .. fmt, ...)
end

-- ==========================================================================
-- Calls: checked again here, as the user edits the files
-- ==========================================================================

local function isIdent(s)
    return type(s) == "string" and s:match("^[%a_][%w_]*$") ~= nil
end

local function isIndex(n)
    return type(n) == "number" and n >= 0 and n < 10000 and n == math.floor(n)
end

-- The call's problem, or nil when it can be sent
local function badCall(c)
    if type(c) ~= "table" then return "no call" end
    if type(c.cluId) ~= "string" or not c.cluId:match("^CLU%d+$") then return "CLU " .. tostring(c.cluId) .. " is no CLU ID" end
    local t = c.callType
    if t == "ATTRIBUTE" or t == "PSEUDO_METHOD" or t == "METHOD" then
        if not isIdent(c.objectName) then return "object " .. tostring(c.objectName) .. " is no object ID" end
        if not isIndex(c.index) then return "index " .. tostring(c.index) .. " is no number" end
        local v = c.value
        if v ~= nil and type(v) ~= "number" and type(v) ~= "string" then return "value is no number or string" end
    elseif t == "SCRIPT" then
        if not isIdent(c.objectName) then return "script " .. tostring(c.objectName) .. " is no Lua name" end
        if c.args ~= nil and type(c.args) ~= "table" then return "args are no list" end
        for _, a in ipairs(c.args or {}) do
            if type(a) ~= "number" and type(a) ~= "string" then return "a script argument is no number or string" end
        end
    elseif t == "VARIABLE" then
        if not isIdent(c.index) then return "user feature " .. tostring(c.index) .. " is no Lua name" end
    else
        return "call type " .. tostring(t) .. " unknown"
    end
    return nil
end

local function cluOf(c)
    local clu = rawget(_G, c.cluId)
    if type(clu) == "table" and type(clu.executeAsync) == "function" then return clu end
    return nil
end

local function objectOf(c)
    return _registry and _registry[c.cluId .. "." .. c.objectName]
end

-- What a call names, for messages and describe
local function where(c)
    if c.callType == "SCRIPT" then return c.cluId .. ":" .. tostring(c.objectName) .. "()" end
    if c.callType == "VARIABLE" then return c.cluId .. ":" .. tostring(c.index) end
    return c.cluId .. "." .. tostring(c.objectName) .. "[" .. tostring(c.index) .. "]"
end

-- Why a call cannot run here (nil when it can): a failed check, a CLU or object not registered
local function unusable(c)
    local bad = badCall(c)
    if bad then return bad end
    if not cluOf(c) then return "CLU " .. c.cluId .. " is not imported" end
    if (c.callType == "PSEUDO_METHOD" or c.callType == "METHOD") and c.objectName ~= c.cluId and not objectOf(c) then
        return where(c) .. " is not in the registry"
    end
    return nil
end

-- A number when the CLU answered one as text
local function typed(v)
    if type(v) == "string" then return tonumber(v) or v end
    return v
end

-- ==========================================================================
-- Reads
-- ==========================================================================

local cache = {} -- key -> { value, at, busy }

-- A read that asks in the background: ask(done) sends, done(value, err) stores the answer
local function background(key, ask)
    return function()
        local e = cache[key]
        if not e then
            e = {}
            cache[key] = e
        end
        local now = os.time()
        if not e.busy and (e.at == nil or now - e.at >= MyGrenton.refresh) then
            e.busy = true
            local ok, err = pcall(ask, function(v, aerr)
                e.busy, e.at = false, os.time()
                if aerr == nil then e.value = typed(v) end
            end)
            if not ok then
                e.busy, e.at = false, now
                warn("%s: %s", key, tostring(err))
            end
        end
        return e.value
    end
end

-- A function that returns the call's value without waiting
local function reader(c)
    if c.callType == "VARIABLE" then
        local clu = cluOf(c)
        return background(where(c), function(done) clu:getAsync(c.index, done) end)
    end
    local obj = objectOf(c)
    -- the registry follows an object's value (feature 0)
    if c.index == 0 and obj and type(obj.getValue) == "function" then
        return function() return obj:getValue() end
    end
    local clu = cluOf(c)
    local script = c.objectName .. ":get(" .. c.index .. ")"
    return background(where(c), function(done) clu:executeAsync(script, done) end)
end

-- ==========================================================================
-- Commands, through the proxies so their guards apply
-- ==========================================================================

local function argsText(args)
    local out = {}
    for i, a in ipairs(args or {}) do out[i] = RemoteObject.literal(a) end
    return table.concat(out, ", ")
end

-- A function that runs the call; input is what the user entered for "$value$"
local function runner(c)
    return function(input)
        local v = c.value
        if v == INPUT then v = input end
        if c.callType == "SCRIPT" then
            cluOf(c):executeNoReply(0, c.objectName .. "(" .. argsText(c.args) .. ")")
            return
        end
        local obj = objectOf(c)
        if c.callType == "PSEUDO_METHOD" then
            if obj then
                obj:set(c.index, v)
            else
                cluOf(c):executeNoReply(0, c.objectName .. ":set(" .. c.index .. ", " .. RemoteObject.literal(v) .. ")")
            end
        elseif obj then
            if v == nil then obj:execute(c.index) else obj:execute(c.index, v) end
        else
            local a = v == nil and "" or ", " .. RemoteObject.literal(v)
            cluOf(c):executeNoReply(0, c.objectName .. ":execute(" .. c.index .. a .. ")")
        end
    end
end

-- ==========================================================================
-- Widgets
-- ==========================================================================

local function text(v, default)
    if type(v) == "string" and v ~= "" then return v end
    return default
end

local function on(v)
    if type(v) == "boolean" then return v end
    if type(v) == "number" then return v ~= 0 end
    if type(v) == "string" then return v ~= "" and v ~= "0" and v ~= "false" end
    return false
end

-- A part's call by key, nil when it has none or it cannot run (the reason is collected)
local function use(ctx, part, key)
    local c = type(part) == "table" and part[key] or nil
    if c == nil then return nil end
    local why = unusable(c)
    if why then
        ctx.problems[#ctx.problems + 1] = key .. ": " .. why
        return nil
    end
    return c
end

local mappers = {}

mappers.HEADER = function(s, w, ctx)
    s:group(text(type(w.text) == "table" and w.text.label, text(w.label, "")))
    return true
end

mappers.ON_OFF = function(s, w, ctx)
    local b = w.buttonBistable or {}
    local state, onC, offC = use(ctx, b, "state"), use(ctx, b, "actionOn"), use(ctx, b, "actionOff")
    if not state then return false end
    local read = reader(state)
    local doOn, doOff = onC and runner(onC), offC and runner(offC)
    s:toggle(ctx.id, ctx.label, nil, function() return on(read()) end, function(want)
        if want and doOn then doOn() elseif not want and doOff then doOff() end
    end, { disabled = (not doOn and not doOff) and function() return true end or nil })
    return true
end

mappers.VALUE = function(s, w, ctx)
    local v = w.value or {}
    local state, act = use(ctx, v, "state"), use(ctx, v, "actionClick")
    if not state then return false end
    local read = reader(state)
    if act and act.value == INPUT and type(v.min) == "number" and type(v.max) == "number" and v.min < v.max then
        local set = runner(act)
        s:slider(ctx.id, ctx.label, v.min, v.max, function() return tonumber(read()) or v.min end,
            function(n) set(n) end, { unit = text(v.unit, nil) })
        return true
    end
    s:status(ctx.id, ctx.label, nil, read, text(v.unit, nil))
    return true
end

mappers.VALUE_V2 = function(s, w, ctx)
    local c = use(ctx, w, "value")
    if not c then return false end
    local opts = nil
    if type(w.precision) == "number" and w.valueType ~= "STRING" then
        opts = { decimals = math.max(0, math.min(6, math.floor(w.precision))) }
    end
    s:status(ctx.id, ctx.label, nil, reader(c), text(w.unit, nil), opts)
    return true
end

mappers.TEXT = function(s, w, ctx)
    local c = use(ctx, w, "text")
    if not c then return false end
    local read = reader(c)
    s:status(ctx.id, ctx.label, nil, function()
        local v = read()
        if v == nil then return nil end
        return tostring(v)
    end)
    return true
end

mappers.CONTACT_SENSOR = function(s, w, ctx)
    local o = w.object or {}
    local c, click = use(ctx, o, "value"), use(ctx, o, "clickAction")
    if not c then return false end
    local read = reader(c)
    local onText, offText = text(w.onIndication, nil), text(w.offIndication, nil)
    s:status(ctx.id, ctx.label, nil, function()
        local v = read()
        if v == nil then return nil end
        if onText and offText then return on(v) and onText or offText end
        return v
    end)
    if click then
        local run = runner(click)
        s:button(ctx.id .. "_click", ctx.label, nil, function() run() end)
    end
    return true
end

local function sceneButton(s, ctx, part, id, default)
    local c = use(ctx, part, "actionClick")
    if not c or c.value == INPUT then return false end
    local run = runner(c)
    s:button(id, text(type(part) == "table" and part.label, default), nil, function() run() end)
    return true
end

mappers.SCENE = function(s, w, ctx)
    return sceneButton(s, ctx, w.buttonMonostable, ctx.id, ctx.label)
end

mappers.SCENE_DOUBLE = function(s, w, ctx)
    local first = sceneButton(s, ctx, w.buttonMonostableFirst, ctx.id .. "_1", ctx.label)
    local second = sceneButton(s, ctx, w.buttonMonostableSecond, ctx.id .. "_2", ctx.label)
    return first or second
end

mappers.THERMOSTAT = function(s, w, ctx)
    local cur = use(ctx, w.thermostatCurrentTemp, "state")
    local target = w.thermostatTargetTemp or {}
    local get, set = use(ctx, target, "state"), use(ctx, target, "actionClick")
    if not cur or not get then return false end
    local fns = { getCurrent = reader(cur), getTarget = reader(get) }
    if set and set.value == INPUT then
        local run = runner(set)
        fns.setTarget = function(v) run(v) end
    end
    local state = use(ctx, w.thermostatState, "state")
    if state then
        local read = reader(state)
        fns.getState = function() return on(read()) end
    end
    local opts = {}
    if type(target.min) == "number" and type(target.max) == "number" and target.min < target.max then
        opts.min, opts.max = target.min, target.max
    end
    if w.thermostatMode or w.thermostatSchedule then
        ctx.problems[#ctx.problems + 1] = "mode and schedule are not shown"
    end
    s:thermostat(ctx.id, ctx.label, nil, fns, opts)
    return true
end

-- The label a widget shows: its own, its first part's, or its type
local function labelOf(w)
    if text(w.label, nil) then return w.label end
    for _, k in ipairs({ "buttonBistable", "value", "buttonMonostable", "buttonMonostableFirst", "thermostatState", "text" }) do
        if type(w[k]) == "table" and text(w[k].label, nil) then return w[k].label end
    end
    return tostring(w.type)
end

local counter = 0

--- Adds a myGrenton widget to a dashboard section.
-- A widget vclu cannot show (a type it has no widget for, calls that fail their checks or
-- name objects not imported) adds nothing and is listed in MyGrenton.leftOut.
-- @api public
-- @param section table A section of a dashboard (Dashboard.new(...):section(...))
-- @param w table The widget as in the interface
-- @return table The section, so calls chain
function MyGrenton.widget(section, w)
    if type(w) ~= "table" then error("MyGrenton.widget: the widget must be a table", 2) end
    local label = labelOf(w)
    local id
    if isIndex(w.id) or (type(w.id) == "number" and w.id >= 0 and w.id == math.floor(w.id)) then
        id = string.format("mg%d", w.id)
    else
        counter = counter + 1
        id = "mg_" .. counter
    end
    local name = tostring(w.type) .. ' "' .. label .. '"'
    local map = mappers[w.type]
    if not map then
        MyGrenton.leftOut[#MyGrenton.leftOut + 1] = { widget = name, reason = "no vclu widget for this type" }
        warn("%s: no vclu widget for this type", name)
        return section
    end
    local ctx = { id = id, label = label, problems = {} }
    local ok, added = pcall(map, section, w, ctx)
    if not ok then
        ctx.problems[#ctx.problems + 1] = tostring(added)
        added = false
    end
    if not added then
        local reason = #ctx.problems > 0 and table.concat(ctx.problems, "; ") or "nothing to show"
        MyGrenton.leftOut[#MyGrenton.leftOut + 1] = { widget = name, reason = reason }
        warn("%s left out: %s", name, reason)
    elseif #ctx.problems > 0 then
        MyGrenton.leftOut[#MyGrenton.leftOut + 1] = { widget = name, reason = table.concat(ctx.problems, "; ") }
        warn("%s: %s", name, table.concat(ctx.problems, "; "))
    end
    return section
end

--- What MyGrenton.widget would add for a widget, without a dashboard: the vclu widgets with their
-- labels, what they read and what they do.
-- @api public
-- @param w table The widget as in the interface
-- @return table { { kind = "toggle", id = "mg2", label = "Hall light" }, ... }, and the reads and the calls
function MyGrenton.describe(w)
    local added = {}
    local fake = {}
    for _, kind in ipairs({ "group", "toggle", "slider", "status", "button", "thermostat" }) do
        fake[kind] = function(self, a, b)
            if kind == "group" then
                added[#added + 1] = { kind = kind, label = a }
            else
                added[#added + 1] = { kind = kind, id = a, label = b }
            end
            return self
        end
    end
    local before = #MyGrenton.leftOut
    MyGrenton.widget(fake, w)
    local reads, does = {}, {}
    local function walk(t)
        for _, v in pairs(t) do
            if type(v) == "table" then
                if v.callType == "ATTRIBUTE" or v.callType == "VARIABLE" then
                    reads[#reads + 1] = where(v)
                elseif v.callType then
                    does[#does + 1] = where(v)
                else
                    walk(v)
                end
            end
        end
    end
    walk(w)
    table.sort(reads)
    table.sort(does)
    local left = MyGrenton.leftOut[before + 1]
    -- describing is no use of the widget
    for i = #MyGrenton.leftOut, before + 1, -1 do table.remove(MyGrenton.leftOut, i) end
    return { widgets = added, reads = reads, does = does, leftOut = left and left.reason or nil }
end

return MyGrenton
