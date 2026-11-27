package lua

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

var (
	cgnatPrefix  = netip.MustParsePrefix("100.64.0.0/10")
	thisNetwork  = netip.MustParsePrefix("0.0.0.0/8")
	nat64Prefix  = netip.MustParsePrefix("64:ff9b::/96")
	siteLocal    = netip.MustParsePrefix("fec0::/10")
	sixToFour    = netip.MustParsePrefix("2002::/16")
	v4Compatible = netip.MustParsePrefix("::/96")
	v4Translated = netip.MustParsePrefix("::ffff:0:0:0/96")
	nat64Local   = netip.MustParsePrefix("64:ff9b:1::/48")
	benchmarking = netip.MustParsePrefix("198.18.0.0/15")
	ietfProtocol = netip.MustParsePrefix("192.0.0.0/24")
	broadcastIP4 = netip.MustParseAddr("255.255.255.255")
)

// isLocalAddr: an address in the house's network or on this machine, not the internet.
func isLocalAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if ip.Is6() {
		b := ip.As16()
		switch {
		case nat64Prefix.Contains(ip), nat64Local.Contains(ip), v4Translated.Contains(ip),
			v4Compatible.Contains(ip) && !ip.IsLoopback() && !ip.IsUnspecified():
			ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
		case sixToFour.Contains(ip):
			ip = netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]})
		case siteLocal.Contains(ip):
			return true
		}
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() ||
		cgnatPrefix.Contains(ip) || thisNetwork.Contains(ip) || benchmarking.Contains(ip) ||
		ietfProtocol.Contains(ip) || ip == broadcastIP4
}

// onHostNetwork: an address of this machine or in a network it is on, also a global
// one (the house's IPv6 prefix, the router at prefix::1).
func onHostNetwork(ip netip.Addr) bool {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return true
	}
	std := net.IP(ip.Unmap().AsSlice())
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.Contains(std) {
			return true
		}
	}
	return false
}

// dialPolicy is where a plugin's HTTP may connect. The LAN and the internet are two
// boundaries: an address after DNS is local or not, and only a local one needs "lan" (and
// its hosts in the grant); an internet one needs the URL's host name on the internet list.
// A listed name resolving to a local address is therefore still refused without "lan",
// and a plugin with "lan" reaches a local IP or a local name without any internet list.
type dialPolicy struct {
	plugin     string
	noLAN      bool
	narrowed   bool           // local addresses only in lanHosts
	lanHosts   []netip.Prefix //
	restricted bool           // internet hosts only those in internet
	internet   plugins.InternetHosts
	isLocal    func(netip.Addr) bool
	resolve    map[string]string                   // tests: a name's address instead of DNS
	own        func(netip.Addr) bool               // set: this machine's addresses refused (a plugin's WebSocket)
	ownAddr    func(netip.Addr) bool               // set: on this machine, ownPorts refused (a plugin's HTTP)
	ownPorts   func() []int                        // nil or returning nil: every port
	lanPort    func(ip netip.Addr, port int) error // set: a local address only on the ports it allows
	// a plugin's lan.ports: with lanPortsListed only these local ports
	lanPorts       []int
	lanPortsListed bool
}

// pluginLANPort: a plugin's HTTP to a local address goes only to plugin.json's lan.ports (any
// without the list), never to a port of another permission.
func (p dialPolicy) pluginLANPort(_ netip.Addr, port int) error {
	if perm, ok := lanPortOwners[port]; ok {
		return fmt.Errorf("refused port %d: it belongs to the %s permission", port, perm)
	}
	if p.lanPortsListed && !slices.Contains(p.lanPorts, port) {
		return fmt.Errorf("refused port %d: not in plugin.json's permissions.lan.ports", port)
	}
	return nil
}

// lanPortOwners: ports of protocols with a permission of their own.
var lanPortOwners = map[int]string{1234: "clu", 69: "tftp", 502: "modbus", 1883: "mqtt", 8883: "mqtt"}

func (p dialPolicy) notListed(host string) error {
	return fmt.Errorf("%s is not on the host list of %s (permissions.internet)", host, p.plugin)
}

// checkURL refuses, before DNS, an internet host not on the list: the URL of a request
// and of every redirect. A name off the list passes only as a house name (localName) for a
// plugin with "lan": a query for any other name would reach a DNS server outside the house.
// The dialer decides on the address once the name is resolved.
func (p dialPolicy) checkURL(u *url.URL) error {
	if !p.restricted {
		return nil
	}
	host := u.Hostname()
	if ip, err := netip.ParseAddr(host); err == nil && p.isLocal(ip.WithZone("")) {
		return nil
	}
	if p.internet.Allows(host) {
		return nil
	}
	// a number a resolver may read as an IPv4 address (16843009, 0x7f.1) is not a device's name
	if _, err := netip.ParseAddr(host); err == nil || p.noLAN || numericHost(host) {
		return p.notListed(host)
	}
	if !localName(host) {
		return fmt.Errorf("%w; a device in the house by its IP address or a house name (name.local, name.lan)", p.notListed(host))
	}
	return nil
}

// localSuffixes: names no public DNS zone answers for, so a query for one tells no outside server anything.
var localSuffixes = []string{".local", ".lan", ".home", ".home.arpa", ".internal", ".localdomain", ".localhost"}

// localName: a name of a device in the house: one label (shelly-1a2b) or under a local suffix.
func localName(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || strings.ContainsFunc(host, func(r rune) bool { return r >= 0x80 }) {
		return false
	}
	if !strings.Contains(host, ".") {
		return true
	}
	return slices.ContainsFunc(localSuffixes, func(s string) bool { return strings.HasSuffix(host, s) })
}

var numericHostRe = regexp.MustCompile(`(?i)^(0x[0-9a-f]*|[0-9]+)(\.(0x[0-9a-f]*|[0-9]+)){0,3}\.?$`)

func numericHost(host string) bool { return numericHostRe.MatchString(host) }

func (p dialPolicy) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	target := addr
	if ip, ok := p.resolve[strings.ToLower(strings.TrimSuffix(host, "."))]; ok {
		target = net.JoinHostPort(ip, port)
	}
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			h, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(h)
			if err != nil {
				return fmt.Errorf("refused %s: not an IP address", address)
			}
			if err := p.allowsPort(ip, port); err != nil {
				return err
			}
			if err := p.allows(host, ip); err != nil {
				return err
			}
			if p.lanPort != nil && p.isLocal(ip.WithZone("").Unmap()) {
				n, _ := strconv.Atoi(port)
				return p.lanPort(ip.WithZone("").Unmap(), n)
			}
			return nil
		},
	}
	return d.DialContext(ctx, network, target)
}

// allowsPort refuses this machine's addresses on vclu's own ports (all of them when not set).
func (p dialPolicy) allowsPort(ip netip.Addr, port string) error {
	ip = ip.WithZone("").Unmap()
	if p.ownAddr == nil || !p.ownAddr(ip) {
		return nil
	}
	n, _ := strconv.Atoi(port)
	if ports := p.ownPortList(); ports == nil || slices.Contains(ports, n) {
		return fmt.Errorf("refused %s: an address of this vclu", net.JoinHostPort(ip.String(), port))
	}
	return nil
}

func (p dialPolicy) ownPortList() []int {
	if p.ownPorts == nil {
		return nil
	}
	return p.ownPorts()
}

// allows: the address host resolved to may be reached, as the dialer decides after DNS.
func (p dialPolicy) allows(host string, ip netip.Addr) error {
	ip = ip.WithZone("").Unmap()
	if p.own != nil && p.own(ip) {
		return fmt.Errorf("refused %s: an address of this vclu", ip)
	}
	if !p.isLocal(ip) {
		if p.restricted && !p.internet.Allows(host) {
			return p.notListed(host)
		}
		return nil
	}
	if p.noLAN {
		return fmt.Errorf(`refused %s: a local address needs "lan" in the plugin's permissions`, ip)
	}
	if p.narrowed && !slices.ContainsFunc(p.lanHosts, func(n netip.Prefix) bool { return n.Contains(ip) }) {
		return fmt.Errorf("refused %s: not among the hosts allowed in the plugin's settings", ip)
	}
	return nil
}

func defaultIsLocal(ip netip.Addr) bool { return isLocalAddr(ip) || onHostNetwork(ip) }
