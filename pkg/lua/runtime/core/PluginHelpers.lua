--- Plugin Helpers for vCLU
-- Building blocks for plugin development: HTTP, polling, registry, events, logging
-- @module PluginHelpers

local PluginHelpers = {}

-- Async HTTP request ids must be unique across the runtime: two requests started
-- in the same second must not share one. A table, not a local: see pkg/lua/AGENTS.md (pcall).
local httpIds = { seq = 0 }

-- ============================================
-- CONSTANTS
-- ============================================

-- Secret patterns for log redaction (case-insensitive)
PluginHelpers.REDACT_PATTERNS = {
    "apikey", "api_key", "api-key",
    "token", "access_token", "refresh_token",
    "password", "passwd", "pass",
    "secret", "client_secret",
    "auth", "authorization",
    "key", "private_key", "api_key",
    "credential", "credentials",
    "bearer"
}

-- Error codes for HTTP responses
PluginHelpers.ERR_CODES = {
    TIMEOUT = "timeout",
    DNS = "dns",
    CONN_REFUSED = "conn_refused",
    CONN_RESET = "conn_reset",
    TLS = "tls",
    ENCODE = "encode",
    DECODE = "decode",
    HTTP_4XX = "http_4xx",
    HTTP_5XX = "http_5xx",
    RETRY_EXHAUSTED = "retry_exhausted"
}

-- ============================================
-- STRING HELPERS
-- ============================================

--- URL encode a string
-- @param str string String to encode
-- @return string URL encoded string
function PluginHelpers.urlEncode(str)
    if str == nil then return "" end
    str = tostring(str)
    str = str:gsub("\n", "\r\n")
    str = str:gsub("([^%w _%%%-%.~])", function(c)
        return string.format("%%%02X", string.byte(c))
    end)
    str = str:gsub(" ", "%%20")
    return str
end

--- Base64 encode a string
-- @param data string String to encode
-- @return string Base64 encoded string
function PluginHelpers.base64Encode(data)
    local b = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
    return ((data:gsub('.', function(x)
        local r, byte = '', x:byte()
        for i = 8, 1, -1 do r = r .. (byte % 2 ^ i - byte % 2 ^ (i - 1) > 0 and '1' or '0') end
        return r
    end) .. '0000'):gsub('%d%d%d?%d?%d?%d?', function(x)
        if (#x < 6) then return '' end
        local c = 0
        for i = 1, 6 do c = c + (x:sub(i, i) == '1' and 2 ^ (6 - i) or 0) end
        return b:sub(c + 1, c + 1)
    end) .. ({'', '==', '='})[#data % 3 + 1])
end

--- Calculate Levenshtein distance between two strings
-- @param s1 string First string
-- @param s2 string Second string
-- @return number Distance
function PluginHelpers.levenshtein(s1, s2)
    if s1 == s2 then return 0 end
    if #s1 == 0 then return #s2 end
    if #s2 == 0 then return #s1 end

    local v0 = {}
    local v1 = {}

    for i = 0, #s2 do v0[i] = i end

    for i = 0, #s1 - 1 do
        v1[0] = i + 1
        for j = 0, #s2 - 1 do
            local cost = (s1:sub(i + 1, i + 1) == s2:sub(j + 1, j + 1)) and 0 or 1
            v1[j + 1] = math.min(v1[j] + 1, v0[j + 1] + 1, v0[j] + cost)
        end
        for j = 0, #s2 do v0[j] = v1[j] end
    end

    return v1[#s2]
end

--- Find similar strings (for fuzzy matching)
-- @param target string Target string to match
-- @param candidates table Array of candidate strings
-- @param maxDistance number Maximum Levenshtein distance (default: 3)
-- @param maxResults number Maximum results to return (default: 3)
-- @return table Array of similar strings sorted by distance
function PluginHelpers.findSimilar(target, candidates, maxDistance, maxResults)
    maxDistance = maxDistance or 3
    maxResults = maxResults or 3

    local matches = {}
    for _, candidate in ipairs(candidates) do
        local dist = PluginHelpers.levenshtein(target:lower(), candidate:lower())
        -- Also consider max distance as 50% of target length
        local dynamicMax = math.min(maxDistance, math.floor(#target / 2))
        if dist <= math.max(dynamicMax, 2) then
            table.insert(matches, { name = candidate, distance = dist })
        end
    end

    -- Sort by distance
    table.sort(matches, function(a, b) return a.distance < b.distance end)

    -- Return top N
    local result = {}
    for i = 1, math.min(#matches, maxResults) do
        table.insert(result, matches[i].name)
    end
    return result
end

-- ============================================
-- URL BUILDER
-- ============================================

--- Build URL with query parameters
-- Keys are sorted alphabetically, array values preserve order
-- @param base string Base URL
-- @param params table Query parameters
-- @return string Full URL with query string
function PluginHelpers.buildUrl(base, params)
    if not params or next(params) == nil then
        return base
    end

    -- Collect and sort keys
    local keys = {}
    for k, _ in pairs(params) do
        table.insert(keys, k)
    end
    table.sort(keys)

    -- Build query string
    local parts = {}
    for _, k in ipairs(keys) do
        local v = params[k]

        if v == nil or v == "" then
            -- Skip nil and empty strings
        elseif type(v) == "table" then
            -- Array: preserve order of values
            for _, item in ipairs(v) do
                if item ~= nil and item ~= "" then
                    local encoded = PluginHelpers.encodeValue(item)
                    if encoded then
                        table.insert(parts, PluginHelpers.urlEncode(k) .. "=" .. encoded)
                    end
                end
            end
        elseif type(v) == "function" then
            error("url(): cannot encode function for key '" .. k .. "'")
        else
            local encoded = PluginHelpers.encodeValue(v)
            if encoded then
                table.insert(parts, PluginHelpers.urlEncode(k) .. "=" .. encoded)
            end
        end
    end

    if #parts == 0 then
        return base
    end

    local separator = base:find("?") and "&" or "?"
    return base .. separator .. table.concat(parts, "&")
end

--- Encode a value for URL query string
-- @param v any Value to encode
-- @return string|nil Encoded value or nil
function PluginHelpers.encodeValue(v)
    if v == nil or v == "" then
        return nil
    elseif type(v) == "boolean" then
        return v and "true" or "false"
    elseif type(v) == "number" then
        -- Use tostring (dot decimal, no locale)
        return tostring(v)
    elseif type(v) == "string" then
        return PluginHelpers.urlEncode(v)
    elseif type(v) == "table" then
        error("url(): nested tables not supported, use JSON body")
    else
        return PluginHelpers.urlEncode(tostring(v))
    end
end

-- ============================================
-- AUTH HELPERS
-- ============================================

--- Build Basic Auth header value
-- @param username string Username
-- @param password string Password
-- @return string Header value "Basic xxx"
function PluginHelpers.basicAuth(username, password)
    local credentials = (username or "") .. ":" .. (password or "")
    return "Basic " .. PluginHelpers.base64Encode(credentials)
end

--- Build Bearer Auth header value
-- @param token string Bearer token
-- @return string Header value "Bearer xxx"
function PluginHelpers.bearerAuth(token)
    return "Bearer " .. (token or "")
end

-- ============================================
-- JSON HELPERS
-- ============================================

--- Check if Content-Type indicates JSON
-- @param contentType string Content-Type header value
-- @return boolean
function PluginHelpers.isJsonContentType(contentType)
    if not contentType then return false end
    return contentType:lower():find("json") ~= nil
end

--- Safely decode JSON
-- @param str string JSON string
-- @return any|nil Decoded value or nil
-- @return string|nil Error message or nil
function PluginHelpers.decodeJson(str)
    if not str or str == "" then
        return nil, nil
    end

    if not JSON or not JSON.decode then
        return nil, "JSON module not available"
    end

    local ok, result = pcall(function()
        return JSON:decode(str)
    end)

    if ok then
        return result, nil
    else
        return nil, tostring(result)
    end
end

--- Safely encode JSON
-- @param data any Data to encode
-- @return string|nil JSON string or nil
-- @return string|nil Error message or nil
function PluginHelpers.encodeJson(data)
    if data == nil then
        return "null", nil
    end

    if not JSON or not JSON.encode then
        return nil, "JSON module not available"
    end

    local ok, result = pcall(function()
        return JSON:encode(data)
    end)

    if ok then
        return result, nil
    else
        return nil, tostring(result)
    end
end

-- ============================================
-- COERCION HELPERS
-- ============================================

--- Coerce value to number
-- @param val any Value to coerce
-- @param default number Default if coercion fails
-- @return number
function PluginHelpers.coerceNumber(val, default)
    if val == nil then return default end
    local num = tonumber(val)
    if num ~= nil then return num end
    return default
end

--- Coerce value to string
-- @param val any Value to coerce
-- @param default string Default if coercion fails
-- @return string
function PluginHelpers.coerceString(val, default)
    if val == nil then return default end
    if type(val) == "string" then return val end
    if type(val) == "number" or type(val) == "boolean" then
        return tostring(val)
    end
    return default
end

--- Coerce value to boolean
-- @param val any Value to coerce
-- @param default boolean Default if coercion fails
-- @return boolean
function PluginHelpers.coerceBool(val, default)
    if val == nil then return default end
    if type(val) == "boolean" then return val end
    if val == "true" or val == "1" or val == 1 then return true end
    if val == "false" or val == "0" or val == 0 then return false end
    return default
end

-- ============================================
-- LOG REDACTION
-- ============================================

--- Redact secrets from a string
-- @param str string String to redact
-- @return string Redacted string
function PluginHelpers.redactSecrets(str)
    if not str then return str end

    local result = str

    -- Redact URL query params with secret-like names
    for _, pattern in ipairs(PluginHelpers.REDACT_PATTERNS) do
        -- Match: key=value in query string (case insensitive)
        result = result:gsub(
            "([?&]" .. pattern .. "=)([^&%s]+)",
            function(prefix, _) return prefix .. "***" end
        )
        -- Also match without ? or & (for logged key=value pairs)
        result = result:gsub(
            "(" .. pattern .. "%s*[=:]%s*)([^%s,}&]+)",
            function(prefix, _) return prefix .. "***" end
        )
    end

    -- Redact Authorization header values
    result = result:gsub(
        "([Aa]uthorization%s*[=:]%s*)([^%s,}]+)",
        function(prefix, _) return prefix .. "***" end
    )

    -- Redact Bearer tokens
    result = result:gsub(
        "([Bb]earer%s+)([^%s,}]+)",
        function(prefix, _) return prefix .. "***" end
    )

    -- Redact Basic auth
    result = result:gsub(
        "([Bb]asic%s+)([^%s,}]+)",
        function(prefix, _) return prefix .. "***" end
    )

    return result
end

--- Log with optional secret redaction
-- @param logger table Logger instance
-- @param pluginId string Plugin ID for prefix
-- @param level string Log level
-- @param msg string Message
-- @param fields table Optional fields to include
-- @param redact boolean Whether to redact secrets (default true)
function PluginHelpers.logSafe(logger, pluginId, level, msg, fields, redact)
    if redact == nil then redact = true end

    local prefix = "[" .. (pluginId or "PLUGIN"):upper() .. "] "
    local fullMsg = msg

    -- Append fields
    if fields then
        local parts = {}
        for k, v in pairs(fields) do
            local value = tostring(v)
            if redact then
                value = PluginHelpers.redactSecrets(value)
            end
            table.insert(parts, k .. "=" .. value)
        end
        if #parts > 0 then
            fullMsg = fullMsg .. " " .. table.concat(parts, " ")
        end
    end

    -- Redact message itself
    if redact then
        fullMsg = PluginHelpers.redactSecrets(fullMsg)
    end

    -- Log
    if logger then
        if level == "error" then
            logger:error(prefix .. fullMsg)
        elseif level == "warn" then
            logger:warn(prefix .. fullMsg)
        elseif level == "debug" then
            logger:debug(prefix .. fullMsg)
        else
            logger:info(prefix .. fullMsg)
        end
    end
end

-- ============================================
-- FORM ENCODING
-- ============================================

--- Encode table as application/x-www-form-urlencoded
-- @param data table Data to encode
-- @return string Encoded form data
function PluginHelpers.encodeForm(data)
    if not data then return "" end

    local parts = {}

    -- Sort keys for deterministic output
    local keys = {}
    for k, _ in pairs(data) do
        table.insert(keys, k)
    end
    table.sort(keys)

    for _, k in ipairs(keys) do
        local v = data[k]
        if v ~= nil then
            local encodedKey = PluginHelpers.urlEncode(tostring(k))
            if type(v) == "table" then
                for _, item in ipairs(v) do
                    table.insert(parts, encodedKey .. "=" .. PluginHelpers.urlEncode(tostring(item)))
                end
            else
                table.insert(parts, encodedKey .. "=" .. PluginHelpers.urlEncode(tostring(v)))
            end
        end
    end

    return table.concat(parts, "&")
end

-- ============================================
-- ERROR CODE CLASSIFICATION
-- ============================================

--- Classify error into error code
-- @param err string Error message
-- @param status number HTTP status code (0 for network errors)
-- @return string Error code
function PluginHelpers.classifyError(err, status)
    if not err and status and status >= 200 and status < 300 then
        return nil
    end

    local errLower = (err or ""):lower()

    -- Network errors
    if errLower:find("timeout") then
        return PluginHelpers.ERR_CODES.TIMEOUT
    elseif errLower:find("dns") or errLower:find("resolve") or errLower:find("no such host") then
        return PluginHelpers.ERR_CODES.DNS
    elseif errLower:find("refused") then
        return PluginHelpers.ERR_CODES.CONN_REFUSED
    elseif errLower:find("reset") or errLower:find("broken pipe") then
        return PluginHelpers.ERR_CODES.CONN_RESET
    elseif errLower:find("tls") or errLower:find("ssl") or errLower:find("certificate") then
        return PluginHelpers.ERR_CODES.TLS
    elseif errLower:find("encode") then
        return PluginHelpers.ERR_CODES.ENCODE
    elseif errLower:find("decode") or errLower:find("parse") or errLower:find("json") then
        return PluginHelpers.ERR_CODES.DECODE
    end

    -- HTTP status errors
    if status then
        if status >= 400 and status < 500 then
            return PluginHelpers.ERR_CODES.HTTP_4XX
        elseif status >= 500 then
            return PluginHelpers.ERR_CODES.HTTP_5XX
        end
    end

    return nil
end

--- Check if error is a network error (retryable by default)
-- @param errCode string Error code
-- @return boolean
function PluginHelpers.isNetworkError(errCode)
    return errCode == PluginHelpers.ERR_CODES.TIMEOUT
        or errCode == PluginHelpers.ERR_CODES.DNS
        or errCode == PluginHelpers.ERR_CODES.CONN_REFUSED
        or errCode == PluginHelpers.ERR_CODES.CONN_RESET
        or errCode == PluginHelpers.ERR_CODES.TLS
end

-- ============================================
-- THROTTLE STATE
-- ============================================

-- Per-plugin throttle state
PluginHelpers._throttleState = {}

--- Get or create throttle state for a key
-- @param key string Throttle key
-- @return table Throttle state
function PluginHelpers.getThrottleState(key)
    if not PluginHelpers._throttleState[key] then
        PluginHelpers._throttleState[key] = {
            lastEmit = 0,
            pending = false,
            buffer = nil,
            timerId = nil
        }
    end
    return PluginHelpers._throttleState[key]
end

--- Clear throttle state for a plugin (cleanup)
-- @param pluginId string Plugin ID
function PluginHelpers.clearThrottleState(pluginId)
    local prefix = pluginId .. ":"
    local toRemove = {}
    for k, _ in pairs(PluginHelpers._throttleState) do
        if k:sub(1, #prefix) == prefix then
            table.insert(toRemove, k)
        end
    end
    for _, k in ipairs(toRemove) do
        local state = PluginHelpers._throttleState[k]
        if state.timerId and type(__go_clearTimer) == "function" then
            __go_clearTimer(state.timerId)
        end
        PluginHelpers._throttleState[k] = nil
    end
end

-- ============================================
-- HTTP REQUEST
-- ============================================

--- Make HTTP request with retry, parseJson, errCode support
-- @param pluginId string Plugin identifier
-- @param opts table Request options
-- @param callback function Callback(response, err)
function PluginHelpers.httpRequest(pluginId, opts, callback)
    -- Validate required fields
    if not opts then
        error("httpRequest: opts is required")
    end
    if not opts.url then
        error("httpRequest: 'url' is required")
    end
    if not callback then
        error("httpRequest: callback is required")
    end

    -- Validate body options (mutually exclusive)
    local bodyCount = 0
    if opts.body then bodyCount = bodyCount + 1 end
    if opts.json then bodyCount = bodyCount + 1 end
    if opts.form then bodyCount = bodyCount + 1 end
    if bodyCount > 1 then
        error("httpRequest: only one of 'body', 'json', 'form' allowed")
    end

    -- Validate method
    local method = (opts.method or "GET"):upper()
    local validMethods = { GET = true, POST = true, PUT = true, PATCH = true, DELETE = true }
    if not validMethods[method] then
        error("httpRequest: invalid method '" .. method .. "', allowed: GET, POST, PUT, PATCH, DELETE")
    end

    -- Defaults
    local timeout = opts.timeout or 10000
    local parseJson = opts.parseJson or "success"
    local logRedact = opts.log and opts.log.redact
    if logRedact == nil then logRedact = true end
    local logLevel = opts.log and opts.log.level or "debug"

    -- TLS options
    local insecure = opts.tls and opts.tls.insecure or false

    -- Retry options
    local retryAttempts = opts.retry and opts.retry.attempts or 1
    local retryBackoff = opts.retry and opts.retry.backoff or 1000
    local retryMaxBackoff = opts.retry and opts.retry.maxBackoff or 30000
    local retryShouldRetry = opts.retry and opts.retry.shouldRetry

    -- Build URL with query params
    local url = opts.url
    if opts.query then
        url = PluginHelpers.buildUrl(url, opts.query)
    end

    -- Build headers
    local headers = {}
    if opts.headers then
        for k, v in pairs(opts.headers) do
            headers[k] = v
        end
    end

    -- Build body
    local body = ""
    if opts.body then
        body = tostring(opts.body)
    elseif opts.json then
        local encoded, encErr = PluginHelpers.encodeJson(opts.json)
        if encErr then
            -- Return immediately with encode error
            local response = {
                status = 0,
                body = "",
                headers = {},
                elapsed = 0,
                attempt = 0,
                ok = false,
                errCode = PluginHelpers.ERR_CODES.ENCODE,
                json = nil,
                jsonErr = nil,
                retries = {}
            }
            callback(response, "json encode error: " .. encErr)
            return
        end
        body = encoded
        if not headers["Content-Type"] then
            headers["Content-Type"] = "application/json"
        end
    elseif opts.form then
        body = PluginHelpers.encodeForm(opts.form)
        if not headers["Content-Type"] then
            headers["Content-Type"] = "application/x-www-form-urlencoded"
        end
    end

    -- Check for retry + mutating request without Idempotency-Key
    if retryAttempts > 1 and (method == "POST" or method == "PUT" or method == "PATCH" or method == "DELETE") then
        if not headers["Idempotency-Key"] and not headers["idempotency-key"] then
            if Logger then
                Logger:warn("[" .. pluginId .. "] Retry enabled for " .. method ..
                    " without Idempotency-Key header - duplicate requests possible on network errors")
            end
        end
    end

    -- Retry state
    local retries = {}
    local totalElapsed = 0
    local startTime = os.clock() * 1000

    -- Log function
    local function logRequest(msg, fields)
        if Logger then
            PluginHelpers.logSafe(Logger, pluginId, logLevel, msg, fields, logRedact)
        end
    end

    -- Default retry decision
    local function defaultShouldRetry(response, attempt, err)
        local errCode = response and response.errCode
        -- Only retry network errors by default
        return PluginHelpers.isNetworkError(errCode)
    end

    -- Calculate backoff for attempt
    local function calculateBackoff(attempt)
        -- backoff * 2^(attempt-1)
        local backoff = retryBackoff * (2 ^ (attempt - 1))
        return math.min(backoff, retryMaxBackoff)
    end

    -- Build response object
    local function buildResponse(status, respBody, respHeaders, elapsed, attempt, err)
        local response = {
            status = status or 0,
            body = respBody or "",
            headers = respHeaders or {},
            elapsed = totalElapsed,
            attempt = attempt,
            ok = status and status >= 200 and status < 300,
            errCode = nil,
            json = nil,
            jsonErr = nil,
            retries = retries
        }

        -- Set error code
        if err or not response.ok then
            response.errCode = PluginHelpers.classifyError(err, status)
        end

        -- Parse JSON based on mode
        local shouldParse = false
        if parseJson == "always" then
            shouldParse = true
        elseif parseJson == "success" and response.ok then
            shouldParse = true
        elseif parseJson == "never" then
            shouldParse = false
        end

        if shouldParse and respBody and respBody ~= "" then
            local contentType = respHeaders and respHeaders["content-type"]
            if PluginHelpers.isJsonContentType(contentType) then
                local decoded, decErr = PluginHelpers.decodeJson(respBody)
                response.json = decoded
                response.jsonErr = decErr
            end
        end

        return response
    end

    -- Perform single request attempt
    local function doRequest(attempt)
        local attemptStart = os.clock() * 1000

        logRequest("HTTP " .. method .. " " .. url .. " (attempt " .. attempt .. ")", nil)

        -- Encode headers to JSON for Go
        local headersJson = PluginHelpers.encodeJson(headers) or "{}"

        -- Make async request
        if type(__go_http_async_request) ~= "function" then
            local response = buildResponse(0, "", {}, 0, attempt, "HTTP backend not available")
            response.errCode = "backend_unavailable"
            callback(response, "HTTP backend not available")
            return
        end

        httpIds.seq = httpIds.seq + 1
        local requestId = pluginId .. "_" .. httpIds.seq

        __go_http_async_request(
            pluginId,
            requestId,
            method,
            url,
            headersJson,
            body,
            timeout,
            not insecure,  -- verifySSL
            true           -- followRedirects
        )

        -- Poll for response
        local pollCount = 0
        local maxPolls = math.ceil(timeout / 50) + 20

        local function pollResponse()
            pollCount = pollCount + 1

            -- Ask for this request's own response: nil until it is ready.
            if type(__go_http_get_async_response) == "function" then
                local status, respBody, respHeadersJson, err = __go_http_get_async_response(requestId)
                if status ~= nil then
                    local attemptElapsed = (os.clock() * 1000) - attemptStart
                    totalElapsed = (os.clock() * 1000) - startTime

                    -- Parse response headers
                    local respHeaders = {}
                    if respHeadersJson and respHeadersJson ~= "" then
                        local decoded = PluginHelpers.decodeJson(respHeadersJson)
                        if decoded then
                            -- Lowercase header keys
                            for k, v in pairs(decoded) do
                                respHeaders[k:lower()] = v
                            end
                        end
                    end

                    -- Record retry info
                    table.insert(retries, {
                        attempt = attempt,
                        elapsed = attemptElapsed,
                        errCode = PluginHelpers.classifyError(err, status)
                    })

                    -- Check if we should retry
                    local shouldRetry = false
                    local backoffMs = nil

                    if attempt < retryAttempts and (err or (status and status >= 400)) then
                        local response = buildResponse(status, respBody, respHeaders, attemptElapsed, attempt, err)

                        if retryShouldRetry then
                            -- Custom retry logic
                            local retryResult, customBackoff = retryShouldRetry(response, attempt, err)
                            if retryResult == nil then
                                -- Fallback to default
                                shouldRetry = defaultShouldRetry(response, attempt, err)
                                backoffMs = calculateBackoff(attempt)
                            else
                                shouldRetry = retryResult
                                backoffMs = customBackoff or calculateBackoff(attempt)
                            end
                        else
                            -- Default retry logic
                            shouldRetry = defaultShouldRetry(response, attempt, err)
                            backoffMs = calculateBackoff(attempt)
                        end
                    end

                    if shouldRetry and attempt < retryAttempts then
                        logRequest("Retry in " .. backoffMs .. "ms (attempt " .. (attempt + 1) .. "/" .. retryAttempts .. ")", nil)

                        -- Schedule retry after backoff
                        if type(__go_setTimeout) == "function" then
                            __go_setTimeout(function()
                                doRequest(attempt + 1)
                            end, backoffMs)
                            totalElapsed = totalElapsed + backoffMs
                        end
                    else
                        -- Final response
                        local response = buildResponse(status, respBody, respHeaders, attemptElapsed, attempt, err)

                        if attempt > 1 and (err or not response.ok) then
                            response.errCode = PluginHelpers.ERR_CODES.RETRY_EXHAUSTED
                        end

                        local errMsg = nil
                        if err then
                            errMsg = err
                        elseif not response.ok then
                            errMsg = "HTTP " .. status
                        end

                        if retryAttempts > 1 and errMsg then
                            errMsg = "all " .. attempt .. " retry attempts failed, last: " .. errMsg
                        end

                        logRequest("HTTP " .. (status or 0) .. " in " .. math.floor(totalElapsed) .. "ms", nil)
                        callback(response, errMsg)
                    end
                    return
                end
            end

            -- Timeout check
            if pollCount >= maxPolls then
                totalElapsed = (os.clock() * 1000) - startTime
                table.insert(retries, {
                    attempt = attempt,
                    elapsed = timeout,
                    errCode = PluginHelpers.ERR_CODES.TIMEOUT
                })

                local response = buildResponse(0, "", {}, timeout, attempt, "timeout after " .. timeout .. "ms")
                response.errCode = PluginHelpers.ERR_CODES.TIMEOUT

                -- Check retry
                if attempt < retryAttempts then
                    local shouldRetry = false
                    local backoffMs = calculateBackoff(attempt)

                    if retryShouldRetry then
                        local retryResult, customBackoff = retryShouldRetry(response, attempt, "timeout")
                        if retryResult == nil then
                            shouldRetry = true  -- Timeout is network error, retry by default
                        else
                            shouldRetry = retryResult
                            backoffMs = customBackoff or backoffMs
                        end
                    else
                        shouldRetry = true  -- Timeout is network error
                    end

                    if shouldRetry then
                        logRequest("Retry in " .. backoffMs .. "ms (attempt " .. (attempt + 1) .. "/" .. retryAttempts .. ")", nil)
                        if type(__go_setTimeout) == "function" then
                            __go_setTimeout(function()
                                doRequest(attempt + 1)
                            end, backoffMs)
                        end
                        return
                    end
                end

                local errMsg = "timeout after " .. timeout .. "ms"
                if retryAttempts > 1 then
                    response.errCode = PluginHelpers.ERR_CODES.RETRY_EXHAUSTED
                    errMsg = "all " .. attempt .. " retry attempts failed, last: " .. errMsg
                end

                callback(response, errMsg)
                return
            end

            -- Continue polling
            if type(__go_setTimeout) == "function" then
                __go_setTimeout(pollResponse, 50)
            end
        end

        -- Start polling
        if type(__go_setTimeout) == "function" then
            __go_setTimeout(pollResponse, 50)
        end
    end

    -- Start first request
    doRequest(1)
end

-- ============================================
-- POLLER
-- ============================================

--- Create a poller for periodic data fetching
-- @param pluginId string Plugin identifier
-- @param name string Poller name
-- @param opts table Poller options
-- @return table Poller object
function PluginHelpers.createPoller(pluginId, name, opts)
    opts = opts or {}

    local poller = {
        _pluginId = pluginId,
        _name = name,
        _interval = opts.interval or 60000,
        _initialDelay = opts.initialDelay or 0,
        _immediate = opts.immediate or false,
        _tickTimeout = opts.tickTimeout or opts.timeout or 30000,
        _pollMode = opts.pollMode or "drop",
        _autoStart = opts.autoStart or false,

        -- Backoff options
        _backoffEnabled = opts.backoff and opts.backoff.enabled or false,
        _backoffBase = opts.backoff and opts.backoff.base or 1000,
        _backoffMax = opts.backoff and opts.backoff.max or 60000,
        _backoffResetOnSuccess = opts.backoff and opts.backoff.resetOnSuccess ~= false,

        -- State
        _running = false,
        _inFlight = false,
        _pending = false,
        _timerId = nil,
        _timeoutTimerId = nil,
        _currentBackoff = 0,
        _doneCalled = false,

        -- Handlers (can be set via opts or methods)
        _onTick = opts.onTick or nil,
        _onData = opts.onData or nil,
        _onError = opts.onError or nil,

        -- Stats
        _stats = {
            ticks = 0,
            success = 0,
            failed = 0,
            skipped = 0,
            dropped = 0,
            timeouts = 0,
            exceptions = 0,
            lastTick = 0,
            lastSuccess = 0,
            lastError = 0,
            lastErrorMsg = nil,
            avgDuration = 0
        },
        _totalDuration = 0
    }

    --- Set tick handler
    function poller:onTick(handler)
        self._onTick = handler
        return self
    end

    --- Set data handler
    function poller:onData(handler)
        self._onData = handler
        return self
    end

    --- Set error handler
    function poller:onError(handler)
        self._onError = handler
        return self
    end

    --- Start the poller
    function poller:start()
        if self._running then return end
        self._running = true

        local function scheduleTick()
            if not self._running then return end

            local interval = self._interval
            if self._currentBackoff > 0 then
                interval = self._currentBackoff
            end

            self._timerId = __go_setInterval(function()
                self:_executeTick()
            end, interval)
        end

        -- Initial delay or immediate execution
        if self._initialDelay > 0 then
            __go_setTimeout(function()
                self:_executeTick()
                scheduleTick()
            end, self._initialDelay)
        elseif self._immediate then
            -- Execute first tick immediately, then start interval
            self:_executeTick()
            scheduleTick()
        else
            scheduleTick()
        end

        if Logger then
            Logger:debug("[" .. self._pluginId .. "] Poller '" .. self._name .. "' started (interval=" .. self._interval .. "ms)")
        end
    end

    --- Stop the poller
    function poller:stop()
        self._running = false
        self._pending = false

        if self._timerId then
            __go_clearTimer(self._timerId)
            self._timerId = nil
        end
        if self._timeoutTimerId then
            __go_clearTimer(self._timeoutTimerId)
            self._timeoutTimerId = nil
        end

        if Logger then
            Logger:debug("[" .. self._pluginId .. "] Poller '" .. self._name .. "' stopped")
        end
    end

    --- Manually trigger a poll
    function poller:poll()
        if self._inFlight then
            if self._pollMode == "pending" then
                if self._pending then
                    self._stats.dropped = self._stats.dropped + 1
                    return false
                end
                self._pending = true
                return "pending"
            else
                self._stats.dropped = self._stats.dropped + 1
                return false
            end
        end

        self:_executeTick()
        return true
    end

    --- Check if running
    function poller:isRunning()
        return self._running
    end

    --- Get stats
    function poller:stats()
        return {
            running = self._running,
            ticks = self._stats.ticks,
            success = self._stats.success,
            failed = self._stats.failed,
            skipped = self._stats.skipped,
            dropped = self._stats.dropped,
            timeouts = self._stats.timeouts,
            exceptions = self._stats.exceptions,
            lastTick = self._stats.lastTick,
            lastSuccess = self._stats.lastSuccess,
            lastError = self._stats.lastError,
            lastErrorMsg = self._stats.lastErrorMsg,
            avgDuration = self._stats.ticks > 0 and (self._totalDuration / self._stats.ticks) or 0,
            currentBackoff = self._currentBackoff,
            inFlight = self._inFlight,
            pendingNow = self._pending
        }
    end

    --- Execute a tick
    function poller:_executeTick()
        if self._inFlight then
            self._stats.skipped = self._stats.skipped + 1
            return
        end

        if not self._onTick then
            if Logger then
                Logger:warn("[" .. self._pluginId .. "] Poller '" .. self._name .. "' has no onTick handler")
            end
            return
        end

        self._inFlight = true
        self._doneCalled = false
        self._stats.ticks = self._stats.ticks + 1
        self._stats.lastTick = os.time()

        local tickStart = os.clock() * 1000

        -- Create done function
        local function done(data, err)
            if self._doneCalled then
                if Logger then
                    Logger:warn("[" .. self._pluginId .. "] Poller '" .. self._name .. "' done() called multiple times - ignoring")
                end
                return
            end
            self._doneCalled = true

            -- Clear timeout timer
            if self._timeoutTimerId then
                __go_clearTimer(self._timeoutTimerId)
                self._timeoutTimerId = nil
            end

            local elapsed = (os.clock() * 1000) - tickStart
            self._totalDuration = self._totalDuration + elapsed
            self._inFlight = false

            if err then
                self._stats.failed = self._stats.failed + 1
                self._stats.lastError = os.time()
                self._stats.lastErrorMsg = tostring(err)

                -- Apply backoff
                if self._backoffEnabled then
                    if self._currentBackoff == 0 then
                        self._currentBackoff = self._backoffBase
                    else
                        self._currentBackoff = math.min(self._currentBackoff * 2, self._backoffMax)
                    end
                end

                if self._onError then
                    pcall(self._onError, err)
                end
            else
                self._stats.success = self._stats.success + 1
                self._stats.lastSuccess = os.time()

                -- Reset backoff
                if self._backoffEnabled and self._backoffResetOnSuccess then
                    self._currentBackoff = 0
                end

                if self._onData then
                    pcall(self._onData, data)
                end
            end

            -- Execute pending poll
            if self._pending and self._running then
                self._pending = false
                __go_setTimeout(function()
                    self:_executeTick()
                end, 0)
            end
        end

        -- Set tick timeout
        self._timeoutTimerId = __go_setTimeout(function()
            if not self._doneCalled then
                self._doneCalled = true
                self._inFlight = false
                self._stats.timeouts = self._stats.timeouts + 1
                self._stats.failed = self._stats.failed + 1
                self._stats.lastError = os.time()
                self._stats.lastErrorMsg = "tick timeout after " .. self._tickTimeout .. "ms"

                if Logger then
                    Logger:warn("[" .. self._pluginId .. "] Poller '" .. self._name .. "' tick timeout - done() was not called")
                end

                if self._onError then
                    pcall(self._onError, "tick timeout after " .. self._tickTimeout .. "ms")
                end

                -- Execute pending poll
                if self._pending and self._running then
                    self._pending = false
                    __go_setTimeout(function()
                        self:_executeTick()
                    end, 0)
                end
            end
        end, self._tickTimeout)

        -- Execute tick handler with exception protection
        local ok, tickErr = pcall(self._onTick, done)
        if not ok then
            if not self._doneCalled then
                self._doneCalled = true
                self._inFlight = false
                self._stats.exceptions = self._stats.exceptions + 1
                self._stats.failed = self._stats.failed + 1
                self._stats.lastError = os.time()
                self._stats.lastErrorMsg = "tick exception: " .. tostring(tickErr)

                if self._timeoutTimerId then
                    __go_clearTimer(self._timeoutTimerId)
                    self._timeoutTimerId = nil
                end

                if Logger then
                    Logger:warn("[" .. self._pluginId .. "] Poller '" .. self._name .. "' tick exception: " .. tostring(tickErr))
                end

                if self._onError then
                    pcall(self._onError, "tick exception: " .. tostring(tickErr))
                end
            end
        end
    end

    -- Auto-start if requested
    if poller._autoStart then
        poller:start()
    end

    return poller
end

-- ============================================
-- EVENT EMIT WITH THROTTLE
-- ============================================

--- Emit event with optional throttling and coalescing
-- @param pluginId string Plugin identifier
-- @param emitFn function Actual emit function
-- @param event string Event name
-- @param data table Event data
-- @param opts table Options (throttle, key, coalesce, leading, trailing)
-- @return boolean|string true=emitted, false=dropped, "pending"=queued
function PluginHelpers.emitThrottled(pluginId, emitFn, event, data, opts)
    opts = opts or {}

    -- Validate event name format (warning, not error)
    if not event:find(":") then
        if Logger then
            Logger:warn("[" .. pluginId .. "] Event name '" .. event .. "' should follow 'namespace:action' format")
        end
    end

    -- No throttle = direct emit
    local throttle = opts.throttle or 0
    if throttle <= 0 then
        local ok, err = pcall(emitFn, event, data)
        if not ok and Logger then
            Logger:error("[" .. pluginId .. "] Emit error: " .. tostring(err))
        end
        return true
    end

    -- Throttle key
    local key = pluginId .. ":" .. event .. ":" .. (opts.key or "_default")
    local state = PluginHelpers.getThrottleState(key)

    -- Use os.time() for more reliable timing (seconds, not CPU time)
    local now = os.time() * 1000
    local leading = opts.leading ~= false  -- default true
    local trailing = opts.trailing or false
    local coalesce = opts.coalesce or false

    -- Check if in throttle window
    local inWindow = (now - state.lastEmit) < throttle

    if not inWindow then
        -- New window
        state.lastEmit = now
        state.buffer = nil

        if leading then
            -- Safe emit with pcall
            local ok, err = pcall(emitFn, event, data)
            if not ok and Logger then
                Logger:error("[" .. pluginId .. "] Emit error: " .. tostring(err))
            end

            -- Schedule trailing emit if enabled
            if trailing then
                state.pending = true
                if state.timerId then
                    __go_clearTimer(state.timerId)
                end
                -- Store event name for timer callback (avoid closure issues)
                state.eventName = event
                state.emitFn = emitFn
                state.timerId = __go_setTimeout(function()
                    if state.buffer and state.emitFn then
                        local ok2, err2 = pcall(state.emitFn, state.eventName, state.buffer)
                        if not ok2 and Logger then
                            Logger:error("[" .. pluginId .. "] Trailing emit error: " .. tostring(err2))
                        end
                    end
                    state.pending = false
                    state.buffer = nil
                    state.timerId = nil
                    state.emitFn = nil
                    state.eventName = nil
                end, throttle)
            end

            return true
        else
            -- No leading, buffer for trailing
            state.buffer = data
            state.pending = true
            state.eventName = event
            state.emitFn = emitFn

            if state.timerId then
                __go_clearTimer(state.timerId)
            end
            state.timerId = __go_setTimeout(function()
                if state.buffer and state.emitFn then
                    local ok2, err2 = pcall(state.emitFn, state.eventName, state.buffer)
                    if not ok2 and Logger then
                        Logger:error("[" .. pluginId .. "] Trailing emit error: " .. tostring(err2))
                    end
                    state.lastEmit = os.time() * 1000
                end
                state.pending = false
                state.buffer = nil
                state.timerId = nil
                state.emitFn = nil
                state.eventName = nil
            end, throttle)

            return "pending"
        end
    else
        -- In throttle window
        if coalesce then
            -- Merge into buffer
            if not state.buffer then
                state.buffer = {}
            end
            for k, v in pairs(data) do
                state.buffer[k] = v
            end
        elseif trailing then
            -- Just replace buffer
            state.buffer = data
        end

        return false
    end
end

-- ============================================
-- REGISTRY UPDATE WITH VALIDATION
-- ============================================

--- Update registry object with validation (fail if not exists)
-- @param pluginId string Plugin identifier
-- @param registry table Registry table
-- @param path string Object path
-- @param patch table Fields to update
-- @return boolean Success
-- @return string|nil Error message
function PluginHelpers.updateObject(pluginId, registry, path, patch)
    -- Build full path
    local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
    local pluginKey = shortId or pluginId
    if namespace then
        pluginKey = namespace .. "." .. pluginKey
    end
    local fullPath = "plugins." .. pluginKey .. "." .. path

    -- Check if object exists
    if not registry or not registry[fullPath] then
        -- Build error message with suggestions
        local errMsg = "updateObject: object '" .. path .. "' does not exist in plugin '" .. (shortId or pluginId) .. "'."

        -- Find similar objects in plugin namespace
        local prefix = "plugins." .. pluginKey .. "."
        local candidates = {}
        for k, _ in pairs(registry or {}) do
            if k:sub(1, #prefix) == prefix then
                local objName = k:sub(#prefix + 1)
                -- Only top-level objects
                if not objName:find("%.") then
                    table.insert(candidates, objName)
                end
            end
        end

        local similar = PluginHelpers.findSimilar(path, candidates, 3, 3)
        if #similar > 0 then
            errMsg = errMsg .. " Did you mean: '" .. table.concat(similar, "', '") .. "'?"
        elseif #candidates > 0 then
            table.sort(candidates)
            local shown = {}
            for i = 1, math.min(5, #candidates) do
                table.insert(shown, candidates[i])
            end
            errMsg = errMsg .. " Available objects: " .. table.concat(shown, ", ")
        else
            errMsg = errMsg .. " Use createObject() first."
        end

        return false, errMsg
    end

    -- Shallow merge
    local obj = registry[fullPath]
    for k, v in pairs(patch) do
        if v == nil then
            -- Explicit nil removes field
            obj[k] = nil
        else
            obj[k] = v
        end
    end

    -- Auto-update timestamp
    obj._lastUpdate = os.time()

    return true, nil
end

--- Upsert registry object (create or update)
-- @param pluginId string Plugin identifier
-- @param registry table Registry table
-- @param path string Object path
-- @param data table Object data
-- @return table The object
function PluginHelpers.upsertObject(pluginId, registry, path, data)
    local namespace, shortId = pluginId:match("^@([^/]+)/(.+)$")
    local pluginKey = shortId or pluginId
    if namespace then
        pluginKey = namespace .. "." .. pluginKey
    end
    local fullPath = "plugins." .. pluginKey .. "." .. path

    if registry[fullPath] then
        -- Update existing
        for k, v in pairs(data) do
            if v == nil then
                registry[fullPath][k] = nil
            else
                registry[fullPath][k] = v
            end
        end
        registry[fullPath]._lastUpdate = os.time()
    else
        -- Create new
        data._lastUpdate = os.time()
        data._created = os.time()
        registry[fullPath] = data
    end

    return registry[fullPath]
end

-- ============================================
-- MODULE EXPORT
-- ============================================

if Logger then
    Logger:info("[CORE] PluginHelpers loaded")
end

return PluginHelpers
