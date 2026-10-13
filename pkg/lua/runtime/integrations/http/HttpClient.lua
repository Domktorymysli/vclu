--
-- HTTP/HTTPS Module for Virtual CLU
--

local HTTP = {}
HTTP.__index = HTTP

-- HTTP status codes (convenience constants)
HTTP.Status = {
    OK = 200,
    Created = 201,
    NoContent = 204,
    BadRequest = 400,
    Unauthorized = 401,
    Forbidden = 403,
    NotFound = 404,
    ServerError = 500,
}

-- Instance registry
HTTP._instances = {}
HTTP._nextInstanceId = 1
HTTP._requestId = 1

-- Create new HTTP instance
function HTTP:new(id)
    local instanceId = id or ("http_" .. HTTP._nextInstanceId)
    HTTP._nextInstanceId = HTTP._nextInstanceId + 1

    local instance = {
        _id = instanceId,
        _instanceId = instanceId,
        _type = "HTTP",

        -- Properties
        _props = {
            baseUrl = "",
            timeout = 30000,
            contentType = "application/json",
            authorization = nil,
            followRedirects = true,
            verifySSL = true,
        },

        -- Default headers
        _defaultHeaders = {},

        -- Pending requests (for async)
        _pendingRequests = {},

        -- Response queue (for testing without Go)
        _responseQueue = {},
        _requestLog = {},
    }
    setmetatable(instance, self)

    HTTP._instances[instanceId] = instance
    return instance
end

-- Get instance by ID
function HTTP.getInstance(instanceId)
    return HTTP._instances[instanceId]
end

function HTTP:getId()
    return self._instanceId
end

-- ============================================
-- Configuration (chainable)
-- ============================================

--- Set base URL for relative paths
-- @param url string Base URL (e.g., "https://api.example.com")
-- @return self
function HTTP:setBaseUrl(url)
    self._props.baseUrl = url
    return self
end

--- Get base URL
-- @return string
function HTTP:getBaseUrl()
    return self._props.baseUrl or ""
end

--- Set request timeout
-- @param ms number Timeout in milliseconds
-- @return self
function HTTP:setTimeout(ms)
    self._props.timeout = ms
    return self
end

--- Set default Content-Type
-- @param contentType string Content-Type header value
-- @return self
function HTTP:setContentType(contentType)
    self._props.contentType = contentType
    return self
end

--- Set Authorization header
-- @param auth string Authorization value (e.g., "Bearer token123")
-- @return self
function HTTP:setAuthorization(auth)
    self._props.authorization = auth
    return self
end

--- Set Bearer token (convenience method)
-- @param token string Bearer token
-- @return self
function HTTP:setBearerToken(token)
    return self:setAuthorization("Bearer " .. token)
end

--- Set Basic auth (convenience method)
-- @param username string Username
-- @param password string Password
-- @return self
function HTTP:setBasicAuth(username, password)
    -- Base64 encode (simple implementation)
    local credentials = username .. ":" .. password
    local encoded = self:_base64Encode(credentials)
    return self:setAuthorization("Basic " .. encoded)
end

--- Enable/disable SSL verification
-- @param verify boolean
-- @return self
function HTTP:setVerifySSL(verify)
    self._props.verifySSL = verify
    return self
end

--- Enable/disable redirect following
-- @param follow boolean
-- @return self
function HTTP:setFollowRedirects(follow)
    self._props.followRedirects = follow
    return self
end

--- Set default header
-- @param name string Header name
-- @param value string Header value
-- @return self
function HTTP:setHeader(name, value)
    self._defaultHeaders[name] = value
    return self
end

--- Remove default header
-- @param name string Header name
-- @return self
function HTTP:removeHeader(name)
    self._defaultHeaders[name] = nil
    return self
end

-- ============================================
-- Request methods
-- ============================================

--- Make HTTP request
-- @param method string HTTP method (GET, POST, PUT, DELETE, PATCH, HEAD)
-- @param url string URL (relative to baseUrl or absolute)
-- @param body string|table Request body (table will be JSON encoded)
-- @param headers table Additional headers
-- @return table Response {status, body, headers} or nil on error
function HTTP:request(method, url, body, headers)
    local fullUrl = self:_buildUrl(url)
    local requestHeaders = self:_buildHeaders(headers)
    local requestBody = self:_encodeBody(body)

    local requestId = HTTP._requestId
    HTTP._requestId = HTTP._requestId + 1

    if Logger then
        Logger:debug("HTTP[%s]:%s %s", self._instanceId, method, fullUrl)
    end

    -- Log request for testing
    table.insert(self._requestLog, {
        id = requestId,
        method = method,
        url = fullUrl,
        headers = requestHeaders,
        body = requestBody,
        timestamp = os.time(),
    })

    -- Call Go backend if available (defensive type check)
    if type(__go_http_request) == "function" then
        local status, responseBody, responseHeaders = __go_http_request(
            self._instanceId,
            method,
            fullUrl,
            self:_encodeHeaders(requestHeaders),
            requestBody,
            self._props.timeout,
            self._props.verifySSL,
            self._props.followRedirects
        )

        return {
            status = status,
            body = responseBody,
            headers = responseHeaders,
            ok = status >= 200 and status < 300,
        }
    end

    -- Stub for testing - return from queue or mock response
    local mockResponse = table.remove(self._responseQueue, 1)
    if mockResponse then
        return mockResponse
    end

    -- Default mock response
    return {
        status = 200,
        body = "{}",
        headers = {},
        ok = true,
        _mock = true,
    }
end

--- GET request
-- @param url string URL
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:get(url, headers)
    return self:request("GET", url, nil, headers)
end

--- POST request
-- @param url string URL
-- @param body string|table Request body
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:post(url, body, headers)
    return self:request("POST", url, body, headers)
end

--- PUT request
-- @param url string URL
-- @param body string|table Request body
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:put(url, body, headers)
    return self:request("PUT", url, body, headers)
end

--- DELETE request
-- @param url string URL
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:delete(url, headers)
    return self:request("DELETE", url, nil, headers)
end

--- PATCH request
-- @param url string URL
-- @param body string|table Request body
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:patch(url, body, headers)
    return self:request("PATCH", url, body, headers)
end

--- HEAD request
-- @param url string URL
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:head(url, headers)
    return self:request("HEAD", url, nil, headers)
end

-- ============================================
-- Async requests (with callbacks)
-- ============================================

--- Async GET request
-- @param url string URL
-- @param callback function Callback function(response, error)
-- @return number Request ID
function HTTP:asyncGet(url, callback)
    return self:asyncRequest("GET", url, nil, nil, callback)
end

--- Async POST request
-- @param url string URL
-- @param body string|table Request body
-- @param callback function Callback function(response, error)
-- @return number Request ID
function HTTP:asyncPost(url, body, callback)
    return self:asyncRequest("POST", url, body, nil, callback)
end

--- Async request
-- @param method string HTTP method
-- @param url string URL
-- @param body string|table Request body
-- @param headers table Additional headers
-- @param callback function Callback function(response, error)
-- @return number Request ID
function HTTP:asyncRequest(method, url, body, headers, callback)
    local requestId = HTTP._requestId
    HTTP._requestId = HTTP._requestId + 1

    self._pendingRequests[requestId] = {
        callback = callback,
        method = method,
        url = url,
    }

    local fullUrl = self:_buildUrl(url)
    local requestHeaders = self:_buildHeaders(headers)
    local requestBody = self:_encodeBody(body)

    if Logger then
        Logger:debug("HTTP[%s]:async %s %s (id=%d)",
            self._instanceId, method, fullUrl, requestId)
    end

    -- Call Go backend if available (defensive type check)
    if type(__go_http_async_request) == "function" then
        __go_http_async_request(
            self._instanceId,
            "client_" .. requestId,
            method,
            fullUrl,
            self:_encodeHeaders(requestHeaders),
            requestBody,
            self._props.timeout,
            self._props.verifySSL,
            self._props.followRedirects
        )

        -- Start polling for response
        local selfRef = self
        local pollCount = 0
        local maxPolls = math.ceil((self._props.timeout or 30000) / 100) + 10

        local function pollForResponse()
            pollCount = pollCount + 1

            -- Ask for this request's own response: nil until it is ready.
            if type(__go_http_get_async_response) == "function" then
                local status, body, headersJson, err = __go_http_get_async_response("client_" .. requestId)
                if status ~= nil then
                    local respHeaders = {}
                    if headersJson and headersJson ~= "" then
                        pcall(function() respHeaders = JSON:decode(headersJson) or {} end)
                    end
                    selfRef:_handleAsyncResponse(requestId, status, body, respHeaders, err)
                    return
                end
            end

            -- Timeout check
            if pollCount >= maxPolls then
                selfRef:_handleAsyncResponse(requestId, 0, "", {},
                    "timeout after " .. tostring(selfRef._props.timeout or 30000) .. "ms")
                return
            end

            -- Continue polling
            if type(__go_setTimeout) == "function" then
                __go_setTimeout(function() pollForResponse() end, 100)
            end
        end

        -- Start polling after short delay
        if type(__go_setTimeout) == "function" then
            __go_setTimeout(function() pollForResponse() end, 50)
            if Logger then
                Logger:debug("HTTP[%s]:polling started for request %d", self._instanceId, requestId)
            end
        else
            if Logger then
                Logger:error("HTTP[%s]:polling FAILED - __go_setTimeout not available!", self._instanceId)
            end
        end
    else
        -- Simulate async with timer (for testing)
        if TIMERS then
            TIMERS:setTimeout(10, function()
                self:_handleAsyncResponse(requestId, 200, "{}", {})
            end)
        end
    end

    return requestId
end

--- Handle async response (called by the response poller)
-- @param requestId number Request ID
-- @param status number HTTP status code
-- @param body string Response body
-- @param headers table Response headers
-- @param transportErr string|nil Error from the transport (connection, timeout); nil on a response
function HTTP:_handleAsyncResponse(requestId, status, body, headers, transportErr)
    local request = self._pendingRequests[requestId]
    if not request then return end

    self._pendingRequests[requestId] = nil

    local response = {
        status = status,
        body = body,
        headers = headers,
        ok = status >= 200 and status < 300,
    }

    if request.callback then
        local err = transportErr
        if not err and not response.ok then
            err = "HTTP " .. status
        end
        pcall(request.callback, response, err)
    end
end

-- ============================================
-- JSON helpers
-- ============================================

--- Parse JSON response body
-- @param response table Response from request
-- @return table|nil Parsed JSON or nil
function HTTP:parseJSON(response)
    if not response or not response.body then
        return nil
    end
    return self:_jsonDecode(response.body)
end

--- POST JSON data
-- @param url string URL
-- @param data table Data to send as JSON
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:postJSON(url, data, headers)
    headers = headers or {}
    headers["Content-Type"] = "application/json"
    return self:post(url, data, headers)
end

--- PUT JSON data
-- @param url string URL
-- @param data table Data to send as JSON
-- @param headers table Additional headers (optional)
-- @return table Response
function HTTP:putJSON(url, data, headers)
    headers = headers or {}
    headers["Content-Type"] = "application/json"
    return self:put(url, data, headers)
end

-- ============================================
-- Internal helpers
-- ============================================

function HTTP:_buildUrl(url)
    if not url then return self:getBaseUrl() end

    -- Check if absolute URL
    if url:match("^https?://") then
        return url
    end

    -- Relative URL - prepend base
    local base = self:getBaseUrl()
    if base == "" then
        return url
    end

    -- Handle trailing/leading slashes
    if base:sub(-1) == "/" and url:sub(1, 1) == "/" then
        return base .. url:sub(2)
    elseif base:sub(-1) ~= "/" and url:sub(1, 1) ~= "/" then
        return base .. "/" .. url
    else
        return base .. url
    end
end

function HTTP:_buildHeaders(extraHeaders)
    local headers = {}

    -- Copy default headers
    for k, v in pairs(self._defaultHeaders) do
        headers[k] = v
    end

    -- Add Content-Type if set
    local contentType = self._props.contentType
    if contentType and contentType ~= "" then
        headers["Content-Type"] = contentType
    end

    -- Add Authorization if set
    local auth = self._props.authorization
    if auth then
        headers["Authorization"] = auth
    end

    -- Merge extra headers (override defaults)
    if extraHeaders then
        for k, v in pairs(extraHeaders) do
            headers[k] = v
        end
    end

    return headers
end

function HTTP:_encodeBody(body)
    if body == nil then
        return ""
    elseif type(body) == "table" then
        return self:_jsonEncode(body)
    else
        return tostring(body)
    end
end

function HTTP:_encodeHeaders(headers)
    -- Convert headers table to JSON for Go
    return self:_jsonEncode(headers)
end

function HTTP:_jsonEncode(value)
    -- Use global JSON helper
    if JSON and JSON.encode then
        return JSON.encode(value)
    end
    -- Fallback for simple values
    if type(value) ~= "table" then
        return tostring(value)
    end
    return "{}"
end

function HTTP:_jsonDecode(str)
    -- Use global JSON helper
    if JSON and JSON.decode then
        return JSON.decode(str)
    end
    -- Fallback
    if not str or str == "" then return nil end
    if str == "null" then return nil end
    if str == "true" then return true end
    if str == "false" then return false end
    if str:match("^%-?%d+%.?%d*$") then return tonumber(str) end

    -- Return raw string if can't parse
    return str
end

function HTTP:_base64Encode(data)
    local b = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/'
    return ((data:gsub('.', function(x)
        local r, b = '', x:byte()
        for i = 8, 1, -1 do r = r .. (b % 2 ^ i - b % 2 ^ (i - 1) > 0 and '1' or '0') end
        return r;
    end) .. '0000'):gsub('%d%d%d?%d?%d?%d?', function(x)
        if (#x < 6) then return '' end
        local c = 0
        for i = 1, 6 do c = c + (x:sub(i, i) == '1' and 2 ^ (6 - i) or 0) end
        return b:sub(c + 1, c + 1)
    end) .. ({'', '==', '='})[#data % 3 + 1])
end

-- ============================================
-- Testing helpers
-- ============================================

--- Get request log (for testing)
-- @return table Array of logged requests
function HTTP:getRequestLog()
    return self._requestLog
end

--- Clear request log
function HTTP:clearRequestLog()
    self._requestLog = {}
end

--- Queue mock response (for testing without Go)
-- @param response table Mock response {status, body, headers}
function HTTP:queueMockResponse(response)
    table.insert(self._responseQueue, response)
end

-- ============================================
-- Module export
-- ============================================

local defaultInstance = HTTP:new("HTTP")

return setmetatable({
    new = function(_, id) return HTTP:new(id) end,
    getInstance = HTTP.getInstance,
    Status = HTTP.Status,
}, {
    __index = defaultInstance,
    __newindex = function(_, k, v) rawset(defaultInstance, k, v) end,
    __call = function(_, ...) return HTTP:new(...) end
})