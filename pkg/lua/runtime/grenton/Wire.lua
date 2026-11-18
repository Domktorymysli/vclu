--- Wire: reads and commands for dashboards made from the myGrenton interface.
-- The Grenton page writes the dashboards as plain Dashboard code (#220); what reads a CLU or
-- sends it a command goes through these helpers, so it never waits for the CLU and is checked:
--
--   s:toggle("w2", "Hall light", nil,
--       Wire.isOn(Wire.value("CLU221000002.DOU0001")),
--       Wire.onOff(Wire.set("CLU221000002.DOU0001", 0, 1), Wire.set("CLU221000002.DOU0001", 0, 0)))
--   s:status("w4", "Setpoint", nil, Wire.variable("CLU221000002", "setpoint"), "°C")
--   s:slider("w9", "Dimmer", 0, 100, Wire.value("CLU221000002.DOU0001"),
--       Wire.set("CLU221000002.DOU0001", 0, Wire.INPUT))
--   s:button("w7", "Evening", nil, Wire.script("CLU221000002", "setup", "evening", 2))
--
-- A path is "<CLU>.<object>" as the registry has it, or "<CLU>" for the CLU's own features.
-- What the registry follows (an object's value) is read from it; anything else is asked in
-- the background and the widget shows the last answer.
-- @module Wire

local Wire = {}

-- seconds between two background reads of one value
Wire.refresh = 5
-- seconds after which a read whose answer never came is asked again
Wire.lost = 60

--- The value the user enters (a slider, a thermostat's set point), as set's or execute's value.
-- @api public
Wire.INPUT = setmetatable({}, { __tostring = function() return "Wire.INPUT" end })

-- Pages that failed to load: { { module = "dashboards/imported/page_1", error = "..." } }
Wire.errors = {}

local function warn(fmt, ...)
    Logger:getShared():warn("[Wire] " .. fmt, ...)
end

-- ==========================================================================
-- Checks: the files are the user's, so every argument is checked when the page loads
-- ==========================================================================

local KEYWORDS = {
    ["and"] = true, ["break"] = true, ["do"] = true, ["else"] = true, ["elseif"] = true, ["end"] = true,
    ["false"] = true, ["for"] = true, ["function"] = true, ["if"] = true, ["in"] = true, ["local"] = true,
    ["nil"] = true, ["not"] = true, ["or"] = true, ["repeat"] = true, ["return"] = true, ["then"] = true,
    ["true"] = true, ["until"] = true, ["while"] = true,
}

local function isName(s)
    return type(s) == "string" and s:match("^[%a_][%w_]*$") ~= nil and not KEYWORDS[s]
end

local function isIndex(n)
    return type(n) == "number" and n >= 0 and n < 10000 and n == math.floor(n)
end

-- up: the frames between check's caller and the public function, so the error names the page's line
local function check(ok, fn, what, up)
    if not ok then error("Wire." .. fn .. ": " .. what, 3 + (up or 0)) end
end

-- clu, object from "CLU….OBJ" or "CLU…"; object nil for the CLU itself
local function split(fn, path, needObject)
    check(type(path) == "string", fn, "path must be a string like \"CLU221000002.DOU0001\"", 2)
    local clu, object = path:match("^(CLU%d+)%.([%a][%w_]*)$")
    if not clu then
        clu = path:match("^(CLU%d+)$")
        check(clu ~= nil, fn, "path " .. path .. " is no \"CLU….object\"", 2)
        check(not needObject, fn, "commands go to an object, not to the CLU itself: " .. path, 2)
    end
    check(object == nil or not KEYWORDS[object], fn, "object " .. tostring(object) .. " is no object ID", 2)
    return clu, object
end

local function isValue(v)
    return v == nil or v == Wire.INPUT or type(v) == "number" or type(v) == "string"
end

-- The CLU proxy, nil while it is not imported
local function cluOf(id)
    local clu = rawget(_G, id)
    if type(clu) == "table" and type(clu.executeAsync) == "function" then return clu end
    return nil
end

local function objectOf(clu, object)
    return _registry and _registry[clu .. "." .. object]
end

-- Warns once per thing that is missing, not on every poll
local missing = {}
local function gone(key, what)
    if not missing[key] then
        missing[key] = true
        warn("%s %s", key, what)
    end
end

-- A number when the CLU answered one as text
local function typed(v)
    if type(v) == "string" then return tonumber(v) or v end
    return v
end

-- ==========================================================================
-- Reads
-- ==========================================================================

local cache = {} -- key -> { value, at, sentAt }

-- A read that asks in the background: ask(done) sends, done(value, err) stores the answer
local function background(key, ask)
    return function()
        local e = cache[key]
        if not e then
            e = {}
            cache[key] = e
        end
        local now = os.time()
        -- an answer that never came (a reply lost on the way) does not stop the next ask
        local waiting = e.sentAt and now - e.sentAt < Wire.lost
        if not waiting and (e.at == nil or now - e.at >= Wire.refresh) then
            e.sentAt = now
            local ok, err = pcall(ask, function(v, aerr)
                e.sentAt, e.at = nil, os.time()
                if aerr == nil then e.value = typed(v) end
            end)
            if not ok then
                e.sentAt, e.at = nil, now
                gone(key, tostring(err))
            end
        end
        return e.value
    end
end

local function feature(fn, path, index)
    local clu, object = split(fn, path, false)
    check(isIndex(index), fn, "feature must be a number 0-9999, got " .. tostring(index), 1)
    local target = object or clu
    local script = target .. ":get(" .. index .. ")"
    local ask = background(path .. "[" .. index .. "]", function(done)
        local c = cluOf(clu)
        if not c then
            gone(clu, "is not imported")
            return done(nil, "not imported")
        end
        c:executeAsync(script, done)
    end)
    if index ~= 0 or not object then return ask end
    -- the registry follows an object's value
    return function()
        local obj = objectOf(clu, object)
        if obj and type(obj.getValue) == "function" then return obj:getValue() end
        return ask()
    end
end

--- An object's value, as the registry follows it (feature 0).
-- @api public
-- @param path string "CLU221000002.DOU0001", or "CLU221000002" for the CLU's own
-- @return function A read for a widget
function Wire.value(path)
    local read = feature("value", path, 0)
    return read
end

--- A feature of an object (or of the CLU) by its number, asked in the background.
-- @api public
-- @param path string "CLU221000002.DIN0001", or "CLU221000002"
-- @param index number Feature number
-- @return function A read for a widget
function Wire.feature(path, index)
    local read = feature("feature", path, index)
    return read
end

--- A user feature of the CLU (a variable defined in Object Manager), asked in the background.
-- @api public
-- @param clu string "CLU221000002"
-- @param name string The user feature, e.g. "setpoint"
-- @return function A read for a widget
function Wire.variable(clu, name)
    check(type(clu) == "string" and clu:match("^CLU%d+$"), "variable", "clu must be like \"CLU221000002\"")
    check(isName(name), "variable", "user feature " .. tostring(name) .. " is no Lua name")
    return background(clu .. ":" .. name, function(done)
        local c = cluOf(clu)
        if not c then
            gone(clu, "is not imported")
            return done(nil, "not imported")
        end
        c:getAsync(name, done)
    end)
end

local function on(v)
    if type(v) == "boolean" then return v end
    if type(v) == "number" then return v ~= 0 end
    if type(v) == "string" then return v ~= "" and v ~= "0" and v ~= "false" end
    return false
end

--- A read as on or off: 0, "", "0", false and nothing are off.
-- @api public
-- @param read function A read (Wire.value, ...)
-- @return function A read of true or false
function Wire.isOn(read)
    check(type(read) == "function", "isOn", "takes a read")
    return function() return on(read()) end
end

--- A read shown as one of two texts, as myGrenton's indications.
-- @api public
-- @param read function A read
-- @param onText string Shown when on
-- @param offText string Shown when off
-- @return function A read of the text
function Wire.text(read, onText, offText)
    check(type(read) == "function", "text", "takes a read")
    check(type(onText) == "string" and type(offText) == "string", "text", "takes two texts")
    return function()
        local v = read()
        if v == nil then return nil end
        return on(v) and onText or offText
    end
end

-- ==========================================================================
-- Commands, through the proxies so their guards apply
-- ==========================================================================

local function command(fn, path, index, value, send)
    local clu, object = split(fn, path, true)
    check(isIndex(index), fn, "index must be a number 0-9999, got " .. tostring(index), 1)
    check(isValue(value), fn, "value must be a number, a string or Wire.INPUT", 1)
    return function(input)
        local v = value
        if v == Wire.INPUT then
            if type(input) ~= "number" and type(input) ~= "string" then return "no value entered" end
            v = input
        end
        local obj = objectOf(clu, object)
        if not obj then
            gone(path, "is not in the registry")
            return path .. " is not imported"
        end
        send(obj, index, v)
    end
end

--- Sets a feature of an object: obj:set(index, value).
-- @api public
-- @param path string "CLU221000002.DOU0001"
-- @param index number Feature number
-- @param value any A number, a string, or Wire.INPUT for what the user enters
-- @return function An action for a widget
function Wire.set(path, index, value)
    check(value ~= nil, "set", "takes a value")
    local action = command("set", path, index, value, function(obj, i, v) obj:set(i, v) end)
    return action
end

--- Runs a method of an object: obj:execute(index[, value]).
-- @api public
-- @param path string "CLU221000002.DOU0001"
-- @param index number Method number
-- @param value any Optional: a number, a string, or Wire.INPUT
-- @return function An action for a widget
function Wire.execute(path, index, value)
    local action = command("execute", path, index, value, function(obj, i, v)
        if v == nil then obj:execute(i) else obj:execute(i, v) end
    end)
    return action
end

--- Runs a script of the CLU with the given arguments.
-- @api public
-- @param clu string "CLU221000002"
-- @param name string The script
-- @param ... any Numbers and strings
-- @return function An action for a widget
function Wire.script(clu, name, ...)
    check(type(clu) == "string" and clu:match("^CLU%d+$"), "script", "clu must be like \"CLU221000002\"")
    check(isName(name), "script", "script " .. tostring(name) .. " is no Lua name")
    local args = {}
    for i = 1, select("#", ...) do
        local a = select(i, ...)
        check(type(a) == "number" or type(a) == "string", "script", "arguments must be numbers or strings")
        args[i] = RemoteObject.literal(a)
    end
    local script = name .. "(" .. table.concat(args, ", ") .. ")"
    return function()
        local c = cluOf(clu)
        if not c then
            gone(clu, "is not imported")
            return clu .. " is not imported"
        end
        c:executeNoReply(0, script)
    end
end

--- A toggle's action from the actions for on and for off (either may be nil).
-- @api public
-- @param onAction function|nil Run to switch on
-- @param offAction function|nil Run to switch off
-- @return function The toggle's action
function Wire.onOff(onAction, offAction)
    check(onAction == nil or type(onAction) == "function", "onOff", "takes actions")
    check(offAction == nil or type(offAction) == "function", "onOff", "takes actions")
    return function(want)
        if want and onAction then return onAction() end
        if not want and offAction then return offAction() end
    end
end

--- Loads a dashboard page on its own: one that fails is logged and listed in
-- Wire.errors, and the others still load.
-- @api public
-- @param module string The page's module, e.g. "dashboards/imported/page_1700000000001"
-- @return boolean Whether it loaded
function Wire.load(module)
    check(type(module) == "string", "load", "takes a module name")
    local ok, err = pcall(require, module)
    if not ok then
        Wire.errors[#Wire.errors + 1] = { module = module, error = tostring(err) }
        warn("%s failed: %s", module, tostring(err))
    end
    return ok
end

--- The pages that failed to load since the last reload, as "module: error" lines in JSON.
-- @return string A JSON list
function Wire.errorsJSON()
    local out = {}
    for _, e in ipairs(Wire.errors) do out[#out + 1] = e.module .. ": " .. e.error end
    return JSON.encode(out)
end

return Wire
