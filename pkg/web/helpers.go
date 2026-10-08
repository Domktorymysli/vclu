package web

import (
	"net"
)

// getLocalIP returns the local IP address from the primary network interface
func (s *Server) getLocalIP() string {
	if s.deviceConfig != nil && s.deviceConfig.MACAddress != "" {
		// If we have a MAC, find the IP of that interface
		if ip := getIPForMAC(s.deviceConfig.MACAddress); ip != "" {
			return ip
		}
	}
	// Find first non-loopback IP
	return getPrimaryIP()
}

// getMACAddress returns the MAC address from the primary network interface
func (s *Server) getMACAddress() string {
	if s.deviceConfig != nil && s.deviceConfig.MACAddress != "" {
		return s.deviceConfig.MACAddress
	}
	return getPrimaryMAC()
}

// getPrimaryIP returns the IP of the primary network interface
func getPrimaryIP() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "Unknown"
	}

	for _, iface := range interfaces {
		// Skip loopback, down interfaces, and interfaces without MAC
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || len(iface.HardwareAddr) == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "Unknown"
}

// getPrimaryMAC returns the MAC of the primary network interface
func getPrimaryMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "00:00:00:00:00:00"
	}

	for _, iface := range interfaces {
		// Skip loopback, down interfaces, and interfaces without MAC
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 || len(iface.HardwareAddr) == 0 {
			continue
		}

		// Check if interface has an IPv4 address (to filter out virtual interfaces)
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return iface.HardwareAddr.String()
			}
		}
	}
	return "00:00:00:00:00:00"
}

// getIPForMAC returns the IP address for a given MAC address
func getIPForMAC(mac string) string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range interfaces {
		if iface.HardwareAddr.String() == mac {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
					return ipnet.IP.String()
				}
			}
		}
	}
	return ""
}
