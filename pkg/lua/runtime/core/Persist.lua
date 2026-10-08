--- Persist - automation state that survives a Lua reload and a restart.
--
--   local st = persist("wiatrolap", { pauseUntil = 0 })
--   st.pauseUntil = os.time() + 600   -- saved to KV a moment later (debounced)
--
-- The returned table is an empty proxy over hidden data, so every assignment
-- (also to an existing key) goes through __newindex and is saved. V1 keeps a
-- flat map of JSON-serializable values: replace a nested table as a whole,
-- or call st:save() after mutating it. Methods: get, set, save, toTable
-- (gopher-lua has no __pairs); these names are reserved keys.
--
-- Data lives in KV namespace "module:<name>", key "state". Persist.flushAll()
-- writes pending changes; Go calls it before a reload and at shutdown.
--
-- @module core.Persist

Persist = { DEBOUNCE_MS = 1000, errors = {} }

local RESERVED = { get = true, set = true, save = true, toTable = true }
local KEY = "state"
local instances = {}   -- name -> proxy (one per name in a Lua state)
local pending = {}     -- namespace -> data waiting for the debounce timer

local function copy(t)
    local out = {}
    for k, v in pairs(t) do out[k] = v end
    return out
end

local function write(ns, data)
    pending[ns] = nil
    if type(__go_kv_set) ~= "function" then return end
    local err = __go_kv_set(ns, KEY, data)
    if err then
        Persist.errors[ns] = tostring(err)
        Logger:getShared():error("[PERSIST] %s not saved: %s", ns, tostring(err))
    else
        Persist.errors[ns] = nil
    end
end

local function schedule(ns, data)
    if pending[ns] then return end
    pending[ns] = data
    local id = type(__go_setTimeout) == "function" and __go_setTimeout(function()
        if pending[ns] then write(ns, pending[ns]) end
    end, Persist.DEBOUNCE_MS)
    -- no timer (none available, or the timer limit was hit): write now
    if not id or id < 0 then write(ns, data) end
end

local function checkValue(k, v)
    if type(k) ~= "string" then error("persist: keys must be strings, got " .. type(k), 3) end
    if RESERVED[k] then error("persist: '" .. k .. "' is a reserved name", 3) end
    local t = type(v)
    if t == "function" or t == "userdata" or t == "thread" then
        error("persist: " .. k .. " cannot hold a " .. t, 3)
    end
end

--- Writes every pending change now (before a reload or shutdown).
function Persist.flushAll()
    for ns, data in pairs(pending) do write(ns, data) end
end

--- Returns the persistent state of an automation.
-- @param name string Letters, digits, _ - . (max 64), e.g. "wiatrolap"; case-insensitive
-- @param defaults table|nil Values for keys not saved yet
-- @return table Proxy: st.key = value saves; st:get/set/save/toTable
function persist(name, defaults)
    if type(name) ~= "string" or #name > 64 or not name:match("^[%w][%w_%.%-]*$") or name:find("..", 1, true) then
        error("persist: name must be letters, digits, _ - . (max 64), got " .. tostring(name), 2)
    end
    name = name:lower()  -- one KV file per name also on case-insensitive disks
    defaults = defaults or {}
    if type(defaults) ~= "table" then error("persist: defaults must be a table", 2) end
    for k, v in pairs(defaults) do checkValue(k, v) end
    local existing = instances[name]
    if existing then
        -- another module may need keys the first caller did not declare
        for k, v in pairs(defaults) do
            if existing:get(k) == nil then existing:set(k, v) end
        end
        return existing
    end

    local ns = "module:" .. name
    local data = {}
    if type(__go_kv_get) == "function" then
        local saved, exists = __go_kv_get(ns, KEY)
        if exists and type(saved) == "table" then
            data = copy(saved)
        elseif exists then
            Persist.errors[ns] = "saved state is not a table, using defaults"
            Logger:getShared():error("[PERSIST] %s: saved state is not a table, using defaults", ns)
        end
    end
    for k, v in pairs(defaults) do
        if data[k] == nil then data[k] = v end
    end

    local methods = {
        get = function(_, k) return data[k] end,
        set = function(_, k, v)
            checkValue(k, v)
            data[k] = v
            schedule(ns, data)
        end,
        save = function() write(ns, data) end,
        toTable = function() return copy(data) end,
    }
    local proxy = setmetatable({}, {
        __index = function(_, k)
            if methods[k] then return methods[k] end
            return data[k]
        end,
        __newindex = function(_, k, v) methods.set(nil, k, v) end,
    })
    instances[name] = proxy
    return proxy
end

return Persist
