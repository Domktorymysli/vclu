package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// maxBroadcastPorts: ports one plugin may broadcast to.
const maxBroadcastPorts = 8

// cluPorts: the CLU's own ports, reached only with grenton_raw (1234) and tftp (69).
var cluPorts = []int{1234, 69}

// ManifestUDP is "udp" in plugin.json: the ports a plugin with the udp permission may
// broadcast to on the local network (discovery), {"broadcastPorts": [7000]}.
type ManifestUDP struct {
	BroadcastPorts []int `json:"broadcastPorts,omitempty"`
}

// UnmarshalJSON refuses unknown keys and ports that are not whole numbers 1-65535, are
// repeated or are the CLU's own.
func (u *ManifestUDP) UnmarshalJSON(data []byte) error {
	type plain ManifestUDP
	var v plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("udp: %w (\"udp\": {\"broadcastPorts\": [7000]})", err)
	}
	if len(v.BroadcastPorts) > maxBroadcastPorts {
		return fmt.Errorf("udp.broadcastPorts: at most %d ports", maxBroadcastPorts)
	}
	for i, p := range v.BroadcastPorts {
		if p < 1 || p > 65535 {
			return fmt.Errorf("udp.broadcastPorts: %d is not a port 1-65535", p)
		}
		if slices.Contains(cluPorts, p) {
			return fmt.Errorf("udp.broadcastPorts: %d is a CLU port, reached only with grenton_raw or tftp", p)
		}
		if slices.Contains(v.BroadcastPorts[:i], p) {
			return fmt.Errorf("udp.broadcastPorts: %d twice", p)
		}
	}
	*u = ManifestUDP(v)
	return nil
}

// checkUDP: "udp" in plugin.json goes with the udp permission, on plugin API 2.
func (m PluginManifest) checkUDP() error {
	if m.UDP == nil {
		return nil
	}
	if manifestAPI(m) < 2 || m.Permissions == nil || !m.Permissions.UDP {
		return fmt.Errorf("\"udp\" in plugin.json needs \"api\": 2 and \"udp\": true in its permissions")
	}
	return nil
}

// BroadcastPorts: where the plugin may broadcast, nil when it does not ask for udp.
func (m PluginManifest) BroadcastPorts() []int {
	return requestedPermissions(m).BroadcastPorts
}
