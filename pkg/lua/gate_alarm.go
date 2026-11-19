package lua

import lua "github.com/yuin/gopher-lua"

// SetGateAlarmAllowDisarm sets whether a GATE Alarm's zones and sections may be disarmed
// from vCLU (grenton.gateAlarm.allowDisarm in .vclu.json, default false). It holds across
// reloads; RemoteGateAlarm.lua asks for it on every disarm.
func (le *LuaEngine) SetGateAlarmAllowDisarm(allow bool) {
	le.gateAlarmAllowDisarm.Store(allow)
}

// registerGateAlarmFunctions gives Lua __go_gate_alarm_allow_disarm() -> boolean.
func (le *LuaEngine) registerGateAlarmFunctions(L *lua.LState) {
	L.SetGlobal("__go_gate_alarm_allow_disarm", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LBool(le.gateAlarmAllowDisarm.Load()))
		return 1
	}))
}
