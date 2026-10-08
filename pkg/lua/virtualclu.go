package lua

import (
	"fmt"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// CreateVirtualCLU creates a VirtualCLU object in Lua
// This is the main CLU object that represents the virtual device
func CreateVirtualCLU(L *lua.LState, name string) *lua.LTable {
	vclu := L.NewTable()

	// Features (read-only properties)
	features := L.NewTable()
	features.RawSetString("UPTIME", lua.LNumber(0))
	features.RawSetString("STATE", lua.LString("RUNNING"))
	features.RawSetString("DATE", lua.LString(time.Now().Format("2006-01-02")))
	features.RawSetString("TIME", lua.LString(time.Now().Format("15:04:05")))
	features.RawSetString("DAY_OF_MONTH", lua.LNumber(time.Now().Day()))
	features.RawSetString("MONTH", lua.LNumber(time.Now().Month()))
	features.RawSetString("YEAR", lua.LNumber(time.Now().Year()))
	features.RawSetString("DAY_OF_WEEK", lua.LNumber(time.Now().Weekday()))
	features.RawSetString("HOUR", lua.LNumber(time.Now().Hour()))
	features.RawSetString("MINUTE", lua.LNumber(time.Now().Minute()))
	features.RawSetString("TIMESTAMP", lua.LNumber(time.Now().Unix()))
	features.RawSetString("TIME_ZONE", lua.LString("UTC"))

	// addToLog(message) - adds a message to the log
	vclu.RawSetString("addToLog", L.NewFunction(func(L *lua.LState) int {
		message := L.CheckString(1)
		fmt.Printf("[VCLU LOG] %s\n", message)
		return 0
	}))

	// clearLog() - clears the log
	vclu.RawSetString("clearLog", L.NewFunction(func(L *lua.LState) int {
		fmt.Println("[VCLU] Log cleared")
		return 0
	}))

	// Helper method to get feature values
	vclu.RawSetString("get", L.NewFunction(func(L *lua.LState) int {
		featureName := L.CheckString(1)
		features := vclu.RawGetString("_features")
		if tbl, ok := features.(*lua.LTable); ok {
			val := tbl.RawGetString(featureName)
			L.Push(val)
			return 1
		}
		L.Push(lua.LNil)
		return 1
	}))

	// Helper method to set feature values
	vclu.RawSetString("set", L.NewFunction(func(L *lua.LState) int {
		featureName := L.CheckString(1)
		value := L.CheckAny(2)
		features := vclu.RawGetString("_features")
		if tbl, ok := features.(*lua.LTable); ok {
			tbl.RawSetString(featureName, value)
		}
		return 0
	}))

	return vclu
}
