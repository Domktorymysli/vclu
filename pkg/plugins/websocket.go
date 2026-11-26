package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// ManifestWebSocket is "websocket" in plugin.json: the ports a plugin with lan reaches by
// WebSocket on the local network, {"lanPorts": [80, 8123]}. Internet hosts need none.
type ManifestWebSocket struct {
	LANPorts []int `json:"lanPorts,omitempty"`
}

// UnmarshalJSON refuses unknown keys and ports that are not whole numbers 1-65535, are
// repeated or belong to another permission, as tcp.ports.
func (w *ManifestWebSocket) UnmarshalJSON(data []byte) error {
	type plain ManifestWebSocket
	var v plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("websocket: %w (\"websocket\": {\"lanPorts\": [80]})", err)
	}
	if len(v.LANPorts) > maxTCPPorts {
		return fmt.Errorf("websocket.lanPorts: at most %d ports", maxTCPPorts)
	}
	for i, p := range v.LANPorts {
		if p < 1 || p > 65535 {
			return fmt.Errorf("websocket.lanPorts: %d is not a port 1-65535", p)
		}
		if perm, ok := tcpReservedPorts[p]; ok {
			return fmt.Errorf("websocket.lanPorts: port %d belongs to the %s permission", p, perm)
		}
		if slices.Contains(v.LANPorts[:i], p) {
			return fmt.Errorf("websocket.lanPorts: %d twice", p)
		}
	}
	*w = ManifestWebSocket(v)
	return nil
}

// checkWebSocket: "websocket" in plugin.json goes with lan (API 1 asks for it).
func (m PluginManifest) checkWebSocket() error {
	if m.WebSocket != nil && !requestedPermissions(m).LAN {
		return fmt.Errorf("\"websocket\" in plugin.json needs \"lan\": true in its permissions")
	}
	return nil
}

// WebSocketPorts: the local ports the plugin's WebSocket reaches, nil without lan.
func (m PluginManifest) WebSocketPorts() []int {
	return requestedPermissions(m).WebSocketPorts
}
