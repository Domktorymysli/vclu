package commands

import (
	"bytes"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// SetIpRequest represents a request to set CLU IP configuration
// Format: req_set_clu_ip:[serial]:[ip_address]:[gateway_ip]
type SetIpRequest struct {
	SerialNumber   uint64
	IPAddress      net.IP
	GatewayAddress net.IP
}

// ParseSetIpRequest parses a SetIp request from bytes
func ParseSetIpRequest(data []byte) (*SetIpRequest, error) {
	str := string(bytes.TrimSpace(data))
	parts := strings.Split(str, ":")

	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid setip request format, expected 4 parts, got %d", len(parts))
	}

	if parts[0] != "req_set_clu_ip" {
		return nil, fmt.Errorf("invalid setip command: %s", parts[0])
	}

	// Parse serial number
	serialNumber, err := strconv.ParseUint(parts[1], 16, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid serial number: %w", err)
	}

	// Parse IP address
	ipAddress := net.ParseIP(parts[2])
	if ipAddress == nil {
		return nil, fmt.Errorf("invalid IP address: %s", parts[2])
	}

	// Parse gateway address
	gatewayAddress := net.ParseIP(parts[3])
	if gatewayAddress == nil {
		return nil, fmt.Errorf("invalid gateway address: %s", parts[3])
	}

	return &SetIpRequest{
		SerialNumber:   serialNumber,
		IPAddress:      ipAddress,
		GatewayAddress: gatewayAddress,
	}, nil
}

// IsSetIpRequest checks if the data contains a SetIp request
func IsSetIpRequest(data []byte) bool {
	return bytes.HasPrefix(data, []byte("req_set_clu_ip:"))
}

// SetIpResponse represents a response to SetIp request
// Format: resp_set_clu_ip:[serial]:[ip_address]
type SetIpResponse struct {
	SerialNumber uint64
	IPAddress    net.IP
}

// AsBytes converts the response to bytes
func (r *SetIpResponse) AsBytes() []byte {
	// Serial number as 16 hex chars (64-bit)
	serialHex := fmt.Sprintf("%016x", r.SerialNumber)

	return []byte(fmt.Sprintf("resp_set_clu_ip:%s:%s", serialHex, r.IPAddress.String()))
}
