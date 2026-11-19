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

// ValidateGrants checks the hosts and topic filters the user narrowed a plugin to.
func ValidateGrants(g config.PluginGrants) error {
	if g.LAN != nil {
		if _, err := ParseHosts(g.LAN.Hosts); err != nil {
			return fmt.Errorf("lan hosts: %w", err)
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
