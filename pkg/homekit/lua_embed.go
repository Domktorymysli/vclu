package homekit

import _ "embed"

// Lua the bridge runs, kept in lua/ (AGENTS.md rule 4). Each file defines a
// local function; the script is the file followed by a call to it.
//
//go:embed lua/command.lua
var commandLua string

//go:embed lua/current_state.lua
var currentStateLua string
