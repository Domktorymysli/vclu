package lua

import (
	"encoding/json"
	"log"

	lua "github.com/yuin/gopher-lua"

	"github.com/Domktorymysli/vclu/pkg/satel"
)

// SatelOptions configures which Satel objects the Lua integration creates.
type SatelOptions struct {
	Zones         []int `json:"zones"`
	Partitions    []int `json:"partitions"`
	Outputs       []int `json:"outputs"`
	TempZones     []int `json:"tempZones"`
	AllowDisarm   bool  `json:"allowDisarm"`
	AllowForceArm bool  `json:"allowForceArm"`
	ArmMode       int   `json:"armMode"`
}

// SatelManager bridges a satel.Panel to the Lua runtime. All Lua-facing
// calls are non-blocking: state reads hit the Go-side cache, control
// commands are queued (results arrive via __go_satel_poll_event).
type SatelManager struct {
	panel *satel.Panel
	opts  SatelOptions
}

// NewSatelManager creates a manager wrapping the given panel client.
func NewSatelManager(panel *satel.Panel, opts SatelOptions) *SatelManager {
	return &SatelManager{panel: panel, opts: opts}
}

// Start launches the panel connection goroutine.
func (sm *SatelManager) Start() {
	sm.panel.Start()
}

// Close stops the panel connection.
func (sm *SatelManager) Close() {
	sm.panel.Stop()
}

// Panel returns the underlying panel client (for web/status handlers).
func (sm *SatelManager) Panel() *satel.Panel {
	return sm.panel
}

// Options returns the monitored object configuration (for web handlers).
func (sm *SatelManager) Options() SatelOptions {
	return sm.opts
}

// GetPendingEvent returns the next panel event (non-blocking).
func (sm *SatelManager) GetPendingEvent() *satel.Event {
	select {
	case ev := <-sm.panel.Events():
		return &ev
	default:
		return nil
	}
}

// jsonString marshals v for handing to Lua, returning "{}" on failure.
func jsonString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// registerSatelFunctions registers the __go_satel_* bridge in Lua.
func registerSatelFunctions(L *lua.LState, sm *SatelManager) {
	if sm == nil {
		return
	}

	// __go_satel_config() -> JSON {zones, partitions, outputs, tempZones, allowDisarm, armMode}
	L.SetGlobal("__go_satel_config", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(jsonString(sm.opts)))
		return 1
	}))

	// __go_satel_status() -> JSON {connected, panelType}
	L.SetGlobal("__go_satel_status", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(jsonString(map[string]interface{}{
			"connected": sm.panel.Connected(),
			"panelType": sm.panel.PanelType(),
		})))
		return 1
	}))

	// __go_satel_get_zone(n) -> JSON ZoneState
	L.SetGlobal("__go_satel_get_zone", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(jsonString(sm.panel.GetZone(L.CheckInt(1)))))
		return 1
	}))

	// __go_satel_get_partition(n) -> JSON PartitionState
	L.SetGlobal("__go_satel_get_partition", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(jsonString(sm.panel.GetPartition(L.CheckInt(1)))))
		return 1
	}))

	// __go_satel_get_output(n) -> JSON OutputState
	L.SetGlobal("__go_satel_get_output", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(jsonString(sm.panel.GetOutput(L.CheckInt(1)))))
		return 1
	}))

	// __go_satel_get_temp(n) -> number or nil
	L.SetGlobal("__go_satel_get_temp", L.NewFunction(func(L *lua.LState) int {
		if t, ok := sm.panel.GetTemperature(L.CheckInt(1)); ok {
			L.Push(lua.LNumber(t))
			return 1
		}
		return 0
	}))

	// Control bridges: queue async command, return (ok, error|nil).
	pushResult := func(L *lua.LState, err error) int {
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}

	// __go_satel_arm(partition, mode)
	L.SetGlobal("__go_satel_arm", L.NewFunction(func(L *lua.LState) int {
		return pushResult(L, sm.panel.Arm(L.CheckInt(1), satel.ArmMode(L.OptInt(2, sm.opts.ArmMode))))
	}))

	// __go_satel_force_arm(partition, mode)
	L.SetGlobal("__go_satel_force_arm", L.NewFunction(func(L *lua.LState) int {
		return pushResult(L, sm.panel.ForceArm(L.CheckInt(1), satel.ArmMode(L.OptInt(2, sm.opts.ArmMode))))
	}))

	// __go_satel_disarm(partition) — blocked unless allowDisarm in config
	L.SetGlobal("__go_satel_disarm", L.NewFunction(func(L *lua.LState) int {
		return pushResult(L, sm.panel.Disarm(L.CheckInt(1)))
	}))

	// __go_satel_clear_alarm(partition) — blocked unless allowDisarm in config
	L.SetGlobal("__go_satel_clear_alarm", L.NewFunction(func(L *lua.LState) int {
		return pushResult(L, sm.panel.ClearAlarm(L.CheckInt(1)))
	}))

	// __go_satel_set_output(output, on)
	L.SetGlobal("__go_satel_set_output", L.NewFunction(func(L *lua.LState) int {
		return pushResult(L, sm.panel.SetOutput(L.CheckInt(1), L.CheckBool(2)))
	}))

	// __go_satel_poll_event() -> JSON event string, or nothing when queue empty
	L.SetGlobal("__go_satel_poll_event", L.NewFunction(func(L *lua.LState) int {
		ev := sm.GetPendingEvent()
		if ev == nil {
			return 0
		}
		L.Push(lua.LString(jsonString(ev)))
		return 1
	}))
}

// SetSatelManager injects the Satel manager and boots the Lua-side
// integration (object creation + event polling timer). bootstrap_go.lua
// ran before this was called, so its __go_satel_poll_event guard was nil;
// on Reinitialize the bootstrap guard takes over (manager already set).
func (le *LuaEngine) SetSatelManager(sm *SatelManager) {
	le.executeFast("SetSatelManager", func(L *lua.LState) (string, error) {
		le.satelManager = sm
		registerSatelFunctions(L, sm)
		if err := L.DoString(`
			if __go_satel_poll_event and Satel then
				Satel.setup()
				EventBus:getShared():setInterval(function()
					Satel.pollEvents()
				end, 100)
			end
		`); err != nil {
			log.Printf("[SATEL] failed to start Lua integration: %v", err)
		}
		return "", nil
	})
}

// GetSatelManager returns the Satel manager instance (nil when disabled).
func (le *LuaEngine) GetSatelManager() *SatelManager {
	return le.satelManager
}
