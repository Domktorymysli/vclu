--- Logger implementation for Go runtime backend.
-- This module provides a simple print-based logging system for the Virtual CLU Go backend.
-- It supports multiple log levels (debug, info, warn, error) and printf-style formatting.
-- The logger uses a singleton pattern accessible via Logger:getShared().
-- @module Logger

Logger = {}

local shared = nil

--- Creates a new Logger instance.
-- Initializes a logger object with metatable setup for OOP-style usage.
-- @return Logger A new logger instance
function Logger:new()
    local logger = {
        id = 'logger',
    }
    setmetatable(logger, self)
    self.__index = self
    return logger
end

--- Returns the shared singleton Logger instance.
-- Provides access to the global logger instance created at module initialization.
-- @return Logger The shared logger singleton
function Logger:getShared()
    return shared
end

--- Converts a message to string representation.
-- Handles nil values and tables with special formatting.
-- @param msg any The message to convert
-- @return string String representation of the message
function Logger:toString(msg)
    if msg == nil then
        return "nil"
    end
    if type(msg) == "table" then
        return "[table]"
    end
    return tostring(msg)
end

--- Formats a message with optional printf-style arguments.
-- Attempts to use string.format for formatting. Falls back to toString if formatting fails.
-- @param message string The message format string
-- @param ... any Optional format arguments
-- @return string The formatted message
function Logger:format(message, ...)
    if message == nil then
        return "nil"
    end
    local args = {...}
    if #args > 0 then
        -- Use string.format if there are additional arguments
        local ok, result = pcall(string.format, message, ...)
        if ok then
            return result
        else
            -- Fallback: just return message if format fails
            return self:toString(message)
        end
    end
    return self:toString(message)
end

--- Logs a debug-level message.
-- Outputs message with [DEBUG] prefix to standard output.
-- @param message string The message format string
-- @param ... any Optional format arguments for printf-style formatting
function Logger:debug(message, ...)
    print("[DEBUG] " .. self:format(message, ...))
end

--- Logs an info-level message.
-- Outputs message with [INFO] prefix to standard output.
-- @param message string The message format string
-- @param ... any Optional format arguments for printf-style formatting
function Logger:info(message, ...)
    print("[INFO] " .. self:format(message, ...))
end

--- Logs a warning-level message.
-- Outputs message with [WARN] prefix to standard output.
-- @param message string The message format string
-- @param ... any Optional format arguments for printf-style formatting
function Logger:warn(message, ...)
    print("[WARN] " .. self:format(message, ...))
end

--- Logs an error-level message.
-- Outputs message with [ERROR] prefix to standard output.
-- @param message string The message format string
-- @param ... any Optional format arguments for printf-style formatting
function Logger:error(message, ...)
    print("[ERROR] " .. self:format(message, ...))
end

--- Logs a CLU-specific message.
-- Outputs message with [CLU] prefix to standard output for CLU-related logging.
-- @param message string The message format string
-- @param ... any Optional format arguments for printf-style formatting
function Logger:cluLogger(message, ...)
    print("[CLU] " .. self:format(message, ...))
end

shared = Logger:new()

return Logger