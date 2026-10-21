package mcp

import (
	_ "embed"

	lua "github.com/yuin/gopher-lua"
)

// Lua the sandbox runs, kept in lua/ (AGENTS.md rule 4).
//
//go:embed lua/logger_hook.lua
var sandboxLoggerHookLua string

//go:embed lua/load_user.lua
var sandboxLoadUserLua string

//go:embed lua/inject_event.lua
var sandboxInjectEventLua string

//go:embed lua/clock.lua
var sandboxClockLua string

//go:embed lua/helpers.lua
var sandboxHelpers string

//go:embed lua/stop_mqtt_poller.lua
var sandboxStopMQTTPollerLua string

// runLuaChunk runs an embedded chunk with args as its `...`.
func runLuaChunk(L *lua.LState, src string, args ...lua.LValue) error {
	fn, err := L.LoadString(src)
	if err != nil {
		return err
	}
	return L.CallByParam(lua.P{Fn: fn, Protect: true}, args...)
}
