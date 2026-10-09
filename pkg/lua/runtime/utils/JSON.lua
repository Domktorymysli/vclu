--
-- JSON utility for Lua
-- Global JSON encode/decode functions
--

JSON = {}

-- Encode Lua value to JSON string
-- Supports both JSON.encode(value) and JSON:encode(value) calling styles
function JSON.encode(selfOrValue, valueIfMethod)
    -- Support both calling styles
    local value
    if selfOrValue == JSON then
        value = valueIfMethod  -- Called as JSON:encode(value)
    else
        value = selfOrValue    -- Called as JSON.encode(value)
    end
    local t = type(value)

    if t == "nil" then
        return "null"
    elseif t == "boolean" then
        return value and "true" or "false"
    elseif t == "number" then
        -- NaN and ±inf have no JSON form; v - v is 0 only for finite numbers
        -- (gopher-lua's math.huge is the largest float, not inf)
        if value ~= value or value - value ~= 0 then
            return "null"
        else
            return tostring(value)
        end
    elseif t == "string" then
        return JSON._encodeString(value)
    elseif t == "table" then
        return JSON._encodeTable(value)
    elseif t == "function" or t == "userdata" or t == "thread" then
        return "null"
    else
        return "null"
    end
end

-- Encode string with proper escaping
function JSON._encodeString(s)
    if s == nil then
        return '""'
    end
    -- %c: every control character, JSON allows none of them raw
    local escaped = s:gsub('[%c\\"]', function(c)
        local replacements = {
            ['\\'] = '\\\\',
            ['"'] = '\\"',
            ['\b'] = '\\b',
            ['\f'] = '\\f',
            ['\n'] = '\\n',
            ['\r'] = '\\r',
            ['\t'] = '\\t',
        }
        return replacements[c] or string.format("\\u%04x", c:byte())
    end)
    return '"' .. escaped .. '"'
end

-- Check if table is an array
function JSON._isArray(t)
    local i = 0
    for _ in pairs(t) do
        i = i + 1
        if t[i] == nil then
            return false
        end
    end
    return true
end

-- Encode table (array or object)
function JSON._encodeTable(t)
    local parts = {}

    if JSON._isArray(t) then
        -- Array
        for _, v in ipairs(t) do
            parts[#parts + 1] = JSON.encode(v)
        end
        return "[" .. table.concat(parts, ",") .. "]"
    else
        -- Object
        for k, v in pairs(t) do
            -- Skip functions, userdata, threads (non-serializable)
            if type(v) ~= "function" and type(v) ~= "userdata" and type(v) ~= "thread" then
                local key = type(k) == "string" and k or tostring(k)
                parts[#parts + 1] = JSON._encodeString(key) .. ":" .. JSON.encode(v)
            end
        end
        return "{" .. table.concat(parts, ",") .. "}"
    end
end

-- Decode JSON string to Lua value (basic implementation)
-- Supports both JSON.decode(str) and JSON:decode(str) calling styles
function JSON.decode(selfOrStr, strIfMethod)
    -- Support both calling styles
    local str = type(selfOrStr) == "string" and selfOrStr or strIfMethod

    if not str or str == "" then
        return nil
    end

    -- Use Go decoder if available (defensive type check)
    if type(__go_json_decode) == "function" then
        return __go_json_decode(str)
    end

    -- Basic Lua parsing (limited)
    str = str:match("^%s*(.-)%s*$") -- trim

    if str == "null" then
        return nil
    elseif str == "true" then
        return true
    elseif str == "false" then
        return false
    elseif str:match("^%-?%d") then
        return tonumber(str)
    elseif str:match('^"') then
        -- String - basic unescape
        local inner = str:match('^"(.*)"$')
        if inner then
            return inner:gsub('\\n', '\n'):gsub('\\r', '\r'):gsub('\\t', '\t'):gsub('\\"', '"'):gsub('\\\\', '\\')
        end
    end

    -- For complex objects/arrays, would need full parser
    return nil
end

return JSON
