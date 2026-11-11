package lua

import (
	"fmt"
	"net"
	"net/netip"
	"syscall"
	"time"
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

// noLANDialer refuses local addresses after DNS resolution, so a name that
// resolves to the LAN and a redirect to one are refused as well.
func noLANDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("refused %s: not an IP address", address)
			}
			if isLocalAddr(ip) || onHostNetwork(ip) {
				return fmt.Errorf("refused %s: a local address needs \"lan\" in the plugin's permissions", host)
			}
			return nil
		},
	}
}
