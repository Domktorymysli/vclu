package lua

import (
	"github.com/Domktorymysli/vclu/pkg/camera"
	lua "github.com/yuin/gopher-lua"
)

// registerCameraFunctions registers the __go_camera_register bridge function.
// Called from Lua's Camera.register() to register a camera in the Go manager.
func registerCameraFunctions(L *lua.LState, cm *camera.Manager) {
	if cm == nil {
		return
	}

	// __go_camera_register(id, url, auth, username, password)
	L.SetGlobal("__go_camera_register", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		url := L.CheckString(2)
		auth := L.OptString(3, "")
		username := L.OptString(4, "")
		password := L.OptString(5, "")

		cm.Register(&camera.Camera{
			ID:       id,
			URL:      url,
			Auth:     auth,
			Username: username,
			Password: password,
		})

		return 0
	}))

	// __go_camera_clear() — called on reload to clear all registrations
	L.SetGlobal("__go_camera_clear", L.NewFunction(func(L *lua.LState) int {
		cm.Clear()
		return 0
	}))
}
