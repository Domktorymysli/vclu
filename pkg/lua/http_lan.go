package lua

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
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
	resolve    map[string]string     // tests: a name's address instead of DNS
	own        func(netip.Addr) bool // set: this machine's addresses refused (a plugin's WebSocket)
	ownAddr    func(netip.Addr) bool // set: on this machine, ownPorts refused (a plugin's HTTP)
	ownPorts   func() []int          // nil or returning nil: every port
}

func (p dialPolicy) notListed(host string) error {
	return fmt.Errorf("%s is not on the host list of %s (permissions.internet)", host, p.plugin)
}

// checkURL refuses, before DNS, an internet host not on the list: the URL of a request
// and of every redirect. A name off the list may still be a device in the house for a
// plugin with "lan"; the dialer decides that once the name is resolved.
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
	if _, err := netip.ParseAddr(host); err == nil || p.noLAN {
		return p.notListed(host)
	}
	return nil
}

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
			if p.ownAddr != nil && p.ownAddr(ip.WithZone("").Unmap()) {
				n, _ := strconv.Atoi(port)
				if ports := p.ownPortList(); ports == nil || slices.Contains(ports, n) {
					return fmt.Errorf("refused %s: an address of this vclu", address)
				}
			}
			return p.allows(host, ip)
		},
	}
	return d.DialContext(ctx, network, target)
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
