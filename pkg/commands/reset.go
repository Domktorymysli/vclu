package commands

import (
	"bytes"
	"fmt"
	"net"
	"strings"
)

// ResetRequest represents a request to reset the CLU
// Format: req_reset:[ip_address]
type ResetRequest struct {
	IPAddress net.IP
}

// IsResetRequest checks if the data contains a reset request
func IsResetRequest(data []byte) bool {
	str := string(bytes.TrimSpace(data))
	return strings.HasPrefix(str, "req_reset:")
}

// ParseResetRequest parses a reset request
func ParseResetRequest(data []byte) (*ResetRequest, error) {
	str := string(bytes.TrimSpace(data))
	parts := strings.Split(str, ":")

	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid reset request format, expected 2 parts, got %d", len(parts))
	}

	if parts[0] != "req_reset" {
		return nil, fmt.Errorf("invalid reset command: %s", parts[0])
	}

	ipAddress := net.ParseIP(parts[1])
	if ipAddress == nil {
		return nil, fmt.Errorf("invalid IP address: %s", parts[1])
	}

	return &ResetRequest{
		IPAddress: ipAddress,
	}, nil
}

// ResetResponse represents a response to a reset request
// Format: resp_reset:[ip_address]
type ResetResponse struct {
	IPAddress net.IP
}

// AsBytes converts the reset response to bytes
func (r *ResetResponse) AsBytes() []byte {
	return []byte(fmt.Sprintf("resp_reset:%s", r.IPAddress.String()))
}
