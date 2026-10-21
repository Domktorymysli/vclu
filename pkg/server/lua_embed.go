package server

import _ "embed"

// connectBrokerLua defines connectBroker(port, username, password); the
// script is the file followed by a call to it (AGENTS.md rule 4).
//
//go:embed lua/connect_broker.lua
var connectBrokerLua string
