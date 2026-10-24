package plugins

import _ "embed"

// Lua the loader runs, kept in lua/ (AGENTS.md rule 4). Each file defines a
// local function; the script is the file followed by a call to it.
//
//go:embed lua/execute.lua
var executeLua string

//go:embed lua/unload.lua
var unloadLua string
