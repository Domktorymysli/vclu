--- HTTP Server module for vCLU
-- Provides an embedded HTTP server controllable from Lua
-- @module HTTPServer

HTTPServer = {}
HTTPServer._handlers = {}

--- Start the HTTP server
-- @param port Server port (default 8081)
-- @param opts Options table with auth credentials
-- @return success boolean
-- @return error string or nil
-- @usage HTTPServer:start(8081)
-- @usage HTTPServer:start(8081, { auth = { username = "admin", password = "secret" } })
function HTTPServer:start(port, opts)
    opts = opts or {}
    local auth = opts.auth or {}
    return __go_http_server_start(
        port or 8081,
        auth.username or "",
        auth.password or ""
    )
end

--- Stop the HTTP server
function HTTPServer:stop()
    __go_http_server_stop()
    self._handlers = {}
end

--- Check if the server is running
-- @return boolean
function HTTPServer:isRunning()
    return __go_http_server_is_running()
end

--- Register an endpoint handler
-- @param path URL path (e.g., "/api/status")
-- @param methods Allowed methods (e.g., "GET", "POST", "GET,POST")
-- @param handler Function that receives request and returns response table
-- @param opts Options table with auth or public flag
-- @usage HTTPServer:on("/health", "GET", function(req) return { status = 200, body = "OK" } end)
-- @usage HTTPServer:on("/api/admin", "POST", handler, { auth = { username = "admin", password = "secret" } })
-- @usage HTTPServer:on("/public", "GET", handler, { public = true })
function HTTPServer:on(path, methods, handler, opts)
    opts = opts or {}
    local auth = opts.auth or {}

    -- Generate unique callback name
    local callbackName = "__http_handler_" .. path:gsub("[^%w]", "_")

    -- Store handler globally so Go can call it
    _G[callbackName] = handler
    self._handlers[path] = callbackName

    -- Register with Go
    __go_http_server_add_endpoint(
        path,
        methods,
        callbackName,
        auth.username or "",
        auth.password or "",
        opts.public or false
    )
end

--- Remove an endpoint handler
-- @param path URL path to remove
function HTTPServer:off(path)
    local callbackName = self._handlers[path]
    if callbackName then
        _G[callbackName] = nil
        self._handlers[path] = nil
    end
    __go_http_server_remove_endpoint(path)
end

--- Get list of registered endpoints
-- @return table of paths
function HTTPServer:endpoints()
    local paths = {}
    for path, _ in pairs(self._handlers) do
        table.insert(paths, path)
    end
    return paths
end

--- Internal: Handle incoming request (called from Go)
-- @param reqJson JSON string with request data
function HTTPServer:_handleRequest(reqJson)
    local req = JSON:decode(reqJson)
    if not req then
        __go_http_server_respond(reqJson, 400, "Invalid request", "{}")
        return
    end

    local handler = _G[self._handlers[req.path]]
    if not handler then
        __go_http_server_respond(req.id, 404, "Handler not found", "{}")
        return
    end

    local ok, resp = pcall(handler, req)
    if not ok then
        Logger:error("HTTP handler error: " .. tostring(resp))
        __go_http_server_respond(req.id, 500, "Internal Server Error", "{}")
        return
    end

    if not resp then
        __go_http_server_respond(req.id, 200, "", "{}")
        return
    end

    __go_http_server_respond(
        req.id,
        resp.status or 200,
        resp.body or "",
        JSON:encode(resp.headers or {})
    )
end

return HTTPServer
