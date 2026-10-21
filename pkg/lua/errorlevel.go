package lua

import lua "github.com/yuin/gopher-lua"

// RegisterErrorLevel replaces error() so that its level means what it does in Lua 5.1:
// gopher-lua counts one frame less, and error(msg, 2) named the function itself, not its caller.
func RegisterErrorLevel(L *lua.LState) {
	L.SetGlobal("error", L.NewFunction(func(L *lua.LState) int {
		level := L.OptInt(2, 1)
		if level > 1 {
			level++
		}
		L.Error(L.CheckAny(1), level)
		return 0
	}))
}
