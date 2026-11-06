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
	broadcastIP4 = netip.MustParseAddr("255.255.255.255")
)

// isLocalAddr: an address in the house's network or on this machine, not the internet.
func isLocalAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if nat64Prefix.Contains(ip) {
		b := ip.As16()
		ip = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() ||
		cgnatPrefix.Contains(ip) || thisNetwork.Contains(ip) || ip == broadcastIP4
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
			if isLocalAddr(ip) {
				return fmt.Errorf("refused %s: a local address needs \"lan\" in the plugin's permissions", host)
			}
			return nil
		},
	}
}
