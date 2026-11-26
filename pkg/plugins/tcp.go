package plugins

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// maxTCPPorts: ports one plugin may connect to.
const maxTCPPorts = 8

// tcpReservedPorts: ports of protocols with a permission of their own.
var tcpReservedPorts = map[int]string{1234: "grenton_raw", 69: "tftp", 502: "modbus", 1883: "mqtt", 8883: "mqtt"}

// ManifestTCP is "tcp" in plugin.json: the ports a plugin with the tcp permission connects
// to on the local network, {"ports": [23]}.
type ManifestTCP struct {
	Ports []int `json:"ports,omitempty"`
}

// UnmarshalJSON refuses unknown keys and ports that are not whole numbers 1-65535, are
// repeated or belong to another permission.
func (t *ManifestTCP) UnmarshalJSON(data []byte) error {
	type plain ManifestTCP
	var v plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return fmt.Errorf("tcp: %w (\"tcp\": {\"ports\": [23]})", err)
	}
	if len(v.Ports) > maxTCPPorts {
		return fmt.Errorf("tcp.ports: at most %d ports", maxTCPPorts)
	}
	for i, p := range v.Ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("tcp.ports: %d is not a port 1-65535", p)
		}
		if perm, ok := tcpReservedPorts[p]; ok {
			return fmt.Errorf("tcp.ports: port %d belongs to the %s permission", p, perm)
		}
		if slices.Contains(v.Ports[:i], p) {
			return fmt.Errorf("tcp.ports: %d twice", p)
		}
	}
	*t = ManifestTCP(v)
	return nil
}

// checkTCP: "tcp" in plugin.json goes with the tcp permission, on plugin API 2, and the
// permission needs at least one port.
func (m PluginManifest) checkTCP() error {
	asks := manifestAPI(m) >= 2 && m.Permissions != nil && m.Permissions.TCP
	if m.TCP != nil && !asks {
		return fmt.Errorf("\"tcp\" in plugin.json needs \"api\": 2 and \"tcp\": true in its permissions")
	}
	if asks && (m.TCP == nil || len(m.TCP.Ports) == 0) {
		return fmt.Errorf("the tcp permission needs the ports it connects to: \"tcp\": {\"ports\": [23]} in plugin.json")
	}
	return nil
}

// TCPPorts: where the plugin may connect, nil when it does not ask for tcp.
func (m PluginManifest) TCPPorts() []int {
	if m.TCP == nil || !requestedPermissions(m).TCP {
		return nil
	}
	return slices.Clone(m.TCP.Ports)
}
