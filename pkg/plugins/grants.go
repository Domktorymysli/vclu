package plugins

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// ParseHosts reads the local hosts a plugin may reach: IP addresses or CIDR prefixes.
func ParseHosts(hosts []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if p, err := netip.ParsePrefix(h); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(h)
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP address or a prefix such as 192.168.1.0/24", h)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// validTopicFilter: an MQTT topic filter, + for one whole level, # only as the last one.
func validTopicFilter(f string) bool {
	if f == "" {
		return false
	}
	levels := strings.Split(f, "/")
	for i, level := range levels {
		if strings.Contains(level, "#") && (level != "#" || i != len(levels)-1) {
			return false
		}
		if strings.Contains(level, "+") && level != "+" {
			return false
		}
	}
	return true
}

// maxGrantList: entries in one list of a grant.
const maxGrantList = 256

// ValidateGrants checks the hosts and topic filters the user narrowed a plugin to.
func ValidateGrants(g config.PluginGrants) error {
	var lists [][]string
	if g.LAN != nil {
		lists = append(lists, g.LAN.Hosts)
	}
	if g.MQTT != nil {
		lists = append(lists, g.MQTT.Subscribe, g.MQTT.Publish)
	}
	for _, h := range []*config.HostsGrant{g.GrentonRaw, g.TFTP} {
		if h != nil {
			lists = append(lists, h.Hosts)
		}
	}
	if g.Serial != nil {
		lists = append(lists, g.Serial.Ports)
	}
	for _, l := range lists {
		if len(l) > maxGrantList {
			return fmt.Errorf("a list of %d entries, at most %d", len(l), maxGrantList)
		}
	}
	if g.LAN != nil {
		if _, err := ParseHosts(g.LAN.Hosts); err != nil {
			return fmt.Errorf("lan hosts: %w", err)
		}
	}
	for name, h := range map[string]*config.HostsGrant{"grenton_raw": g.GrentonRaw, "tftp": g.TFTP} {
		if h == nil {
			continue
		}
		if _, err := ParseHosts(h.Hosts); err != nil {
			return fmt.Errorf("%s hosts: %w", name, err)
		}
	}
	if g.Serial != nil {
		for _, port := range g.Serial.Ports {
			if !strings.HasPrefix(port, "/dev/") || strings.Contains(port, "..") {
				return fmt.Errorf("serial: %q is not a device path such as /dev/ttyUSB0", port)
			}
		}
	}
	if g.MQTT != nil {
		for _, list := range [][]string{g.MQTT.Subscribe, g.MQTT.Publish} {
			for _, f := range list {
				if !validTopicFilter(f) {
					return fmt.Errorf("mqtt: %q is not a topic filter (+ for a whole level, # only at the end)", f)
				}
			}
		}
	}
	return nil
}
