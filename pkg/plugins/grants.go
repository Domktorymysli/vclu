package plugins

import (
	"fmt"
	"net/netip"
	"slices"
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

// ValidateGrants checks the hosts, ports and topic filters the user narrowed a plugin to.
func ValidateGrants(g config.PluginGrants) error {
	var lists [][]string
	if g.Internet != nil {
		lists = append(lists, g.Internet.Hosts)
	}
	if g.LAN != nil {
		lists = append(lists, g.LAN.Hosts)
	}
	if g.MQTT != nil {
		lists = append(lists, g.MQTT.Subscribe, g.MQTT.Publish)
	}
	for _, h := range []*config.HostsGrant{g.UDP, g.TCP, g.TFTP} {
		if h != nil {
			lists = append(lists, h.Hosts)
		}
	}
	if g.Modbus != nil {
		lists = append(lists, g.Modbus.Hosts)
	}
	if g.CLU != nil {
		lists = append(lists, g.CLU.Hosts)
	}
	if g.Serial != nil {
		lists = append(lists, g.Serial.Ports)
	}
	if g.Actions != nil {
		lists = append(lists, *g.Actions)
	}
	for _, l := range lists {
		if len(l) > maxGrantList {
			return fmt.Errorf("a list of %d entries, at most %d", len(l), maxGrantList)
		}
	}
	if g.Internet != nil {
		if _, err := NormalizeInternetHosts(g.Internet.Hosts); err != nil {
			return err
		}
	}
	if g.LAN != nil {
		if _, err := ParseHosts(g.LAN.Hosts); err != nil {
			return fmt.Errorf("lan hosts: %w", err)
		}
		if err := validPorts("lan", g.LAN.Ports); err != nil {
			return err
		}
	}
	hosts := map[string][]string{}
	for name, h := range map[string]*config.HostsGrant{"udp": g.UDP, "tcp": g.TCP, "tftp": g.TFTP} {
		if h == nil {
			continue
		}
		hosts[name] = h.Hosts
		for _, ports := range []*[]int{h.Broadcast, h.Ports} {
			if err := validPorts(name, ports); err != nil {
				return err
			}
		}
	}
	if g.Modbus != nil {
		hosts["modbus"] = g.Modbus.Hosts
	}
	if g.CLU != nil {
		hosts["clu"] = g.CLU.Hosts
	}
	for name, list := range hosts {
		if _, err := ParseHosts(list); err != nil {
			return fmt.Errorf("%s hosts: %w", name, err)
		}
	}
	if g.Serial != nil {
		for _, port := range g.Serial.Ports {
			if !strings.HasPrefix(port, "/dev/") || strings.Contains(port, "..") || len(port) > 128 {
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
	if g.Actions != nil {
		for _, a := range *g.Actions {
			if !ValidActionName(a) {
				return fmt.Errorf("actions: %q is not an action name", a)
			}
		}
	}
	return nil
}

// WithinRequest drops from g what the plugin does not ask for: ports, topic filters and
// actions are only ever narrowed by the user, never widened past plugin.json.
func WithinRequest(g config.PluginGrants, requested Permissions) config.PluginGrants {
	keep := func(granted *[]int, asked []int) *[]int {
		if granted == nil {
			return nil
		}
		out := []int{}
		for _, p := range *granted {
			if slices.Contains(asked, p) {
				out = append(out, p)
			}
		}
		return &out
	}
	if g.LAN != nil && requested.LANPorts != nil {
		g.LAN.AnyPort = false
		g.LAN.Ports = keep(g.LAN.Ports, requested.LANPorts)
	}
	if g.UDP != nil {
		g.UDP.Ports, g.UDP.Broadcast = keep(g.UDP.Ports, requested.UDPPorts), keep(g.UDP.Broadcast, requested.BroadcastPorts)
	}
	if g.TCP != nil {
		g.TCP.Ports = keep(g.TCP.Ports, requested.TCPPorts)
	}
	if g.MQTT != nil {
		within := func(granted, asked []string) []string {
			if granted == nil || asked == nil {
				return granted
			}
			out := []string{}
			for _, f := range granted {
				if slices.ContainsFunc(asked, func(a string) bool { return filterCovers(a, f) }) {
					out = append(out, f)
				}
			}
			return out
		}
		g.MQTT.Subscribe = within(g.MQTT.Subscribe, requested.mqttAsk(requested.MQTTSubscribe))
		g.MQTT.Publish = within(g.MQTT.Publish, requested.mqttAsk(requested.MQTTPublish))
		// the user allows what plugin.json asks for now
		g.MQTT.SubscribeAsked = topicsGrant(requested.mqttAsk(requested.MQTTSubscribe))
		g.MQTT.PublishAsked = topicsGrant(requested.mqttAsk(requested.MQTTPublish))
	}
	if g.Actions != nil {
		out := []string{}
		for _, a := range *g.Actions {
			if slices.Contains(requested.Actions, a) {
				out = append(out, a)
			}
		}
		g.Actions = &out
	}
	// what the user allows now is the actions as plugin.json declares them, confirmation too
	confirmed := []string{}
	if g.Actions != nil {
		for _, a := range *g.Actions {
			if slices.Contains(requested.ConfirmActions, a) {
				confirmed = append(confirmed, a)
			}
		}
	}
	g.ConfirmedActions = &confirmed
	return g
}

// validPorts: a granted list of whole ports 1-65535.
func validPorts(name string, ports *[]int) error {
	if ports == nil {
		return nil
	}
	if len(*ports) > maxGrantList {
		return fmt.Errorf("%s: %d ports, at most %d", name, len(*ports), maxGrantList)
	}
	for _, p := range *ports {
		if p < 1 || p > 65535 {
			return fmt.Errorf("%s: %d is not a port 1-65535", name, p)
		}
	}
	return nil
}
