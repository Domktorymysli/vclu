package config

import "time"

// VCLULuaConfig is the "lua" section of .vclu.json, read at start.
type VCLULuaConfig struct {
	// CallbackTimeoutSeconds stops one Lua callback after this long; 0 the default (60), negative no limit
	CallbackTimeoutSeconds int `json:"callbackTimeoutSeconds,omitempty"`
}

// LuaCallbackTimeout is the setting for lua.LuaEngine.SetCallbackTimeout: 0 the default,
// negative no limit. A nil config gives the default.
func (c *VCLUConfig) LuaCallbackTimeout() time.Duration {
	if c == nil || c.Lua == nil {
		return 0
	}
	if c.Lua.CallbackTimeoutSeconds < 0 {
		return -1
	}
	return time.Duration(c.Lua.CallbackTimeoutSeconds) * time.Second
}
