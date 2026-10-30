package lua

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

// CloudBridge connects the Cloud module (runtime/core/Cloud.lua) to vclu cloud (pkg/cloud).
type CloudBridge struct {
	// Notify sends a notification, encoded by Cloud.notify as the protocol's notify message.
	Notify func(notifyJSON string) error
	// Presence returns the last presence list from the cloud as JSON (the protocol's people),
	// "" before the first: Cloud.lua starts from it after a reload.
	Presence func() string
	// SetEnabled switches the connection off or on, keeping the pairing.
	SetEnabled func(on bool) error
	// Enabled reports whether vCLU is paired and the connection is switched on.
	Enabled func() bool
	// ObjectsChanged is called on every change of what is exposed and how (ExposedObjects.lua,
	// changed()). It runs on the Lua loop: it must only mark the device list dirty and return,
	// never call back into Lua.
	ObjectsChanged func()
}

// SetCloud connects the Cloud module to the cloud client. Without it Cloud.notify returns nil
// and an error, and nobody is known to be at home.
func (le *LuaEngine) SetCloud(b CloudBridge) {
	le.executeFast("SetCloud", func(L *lua.LState) (string, error) {
		le.cloud = &b
		registerCloudFunctions(L, &b)
		return "", nil
	})
}

// registerCloudFunctions registers __go_cloud_notify(json) -> true | nil, err,
// __go_cloud_presence() -> json | nil, __go_cloud_set_enabled(on) -> true | nil, err,
// __go_cloud_enabled() -> bool, __go_cloud_objectsChanged() and __go_cloud_now_ms().
func registerCloudFunctions(L *lua.LState, b *CloudBridge) {
	// The wall clock the cloud client computes a command's deadline with (Cloud._command), not
	// the Lua clock, which a test may replace.
	L.SetGlobal("__go_cloud_now_ms", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(time.Now().UnixMilli()))
		return 1
	}))
	if b == nil {
		return
	}
	if b.ObjectsChanged != nil {
		L.SetGlobal("__go_cloud_objectsChanged", L.NewFunction(func(L *lua.LState) int {
			b.ObjectsChanged()
			return 0
		}))
	}
	if b.Notify != nil {
		L.SetGlobal("__go_cloud_notify", L.NewFunction(func(L *lua.LState) int {
			if err := b.Notify(L.CheckString(1)); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))
	}
	if b.Presence != nil {
		L.SetGlobal("__go_cloud_presence", L.NewFunction(func(L *lua.LState) int {
			if s := b.Presence(); s != "" {
				L.Push(lua.LString(s))
			} else {
				L.Push(lua.LNil)
			}
			return 1
		}))
	}
	if b.SetEnabled != nil {
		L.SetGlobal("__go_cloud_set_enabled", L.NewFunction(func(L *lua.LState) int {
			if err := b.SetEnabled(L.ToBool(1)); err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			return 1
		}))
	}
	if b.Enabled != nil {
		L.SetGlobal("__go_cloud_enabled", L.NewFunction(func(L *lua.LState) int {
			L.Push(lua.LBool(b.Enabled()))
			return 1
		}))
	}
}
