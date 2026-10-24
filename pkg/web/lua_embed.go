package web

import _ "embed"

// Lua the web handlers run, kept in lua/ (AGENTS.md rule 4).
//
//go:embed lua/ha_discovery_refresh.lua
var haDiscoveryRefresh string

//go:embed lua/diag_devices.lua
var diagDevicesLua string

// defaultInitLua is written to modules/init.lua when it is missing.
//
//go:embed lua/default_init.lua
var defaultInitLua string
