package commands

import (
	"bytes"
	"fmt"
)

const (
	RandomBytes = 32
	HashModA    = 0x0D
	HashModB    = 0x13
)

// DiscoveryRequest represents a CLU discovery request
// Format: [encrypted_random(32)]:[iv(16)]:req_discovery_clu:[ip_address]
type DiscoveryRequest struct {
	Encrypted []byte // 32 bytes - encrypted random bytes
	IV        []byte // 16 bytes
	IPAddress string
}

// ParseDiscoveryRequest parses a discovery request from bytes
func ParseDiscoveryRequest(data []byte) (*DiscoveryRequest, error) {
	// Minimum length: 32 (encrypted) + 1 (:) + 16 (iv) + 1 (:) + 17 (command) + 1 (:) + 7 (ip min)
	if len(data) < 75 {
		return nil, fmt.Errorf("discovery request too short")
	}

	// Check delimiters
	if data[32] != ':' || data[49] != ':' {
		return nil, fmt.Errorf("invalid discovery request format")
	}

	encrypted := make([]byte, 32)
	copy(encrypted, data[0:32])

	iv := make([]byte, 16)
	copy(iv, data[33:49])

	// Check command
	if !bytes.HasPrefix(data[50:], []byte("req_discovery_clu:")) {
		return nil, fmt.Errorf("invalid discovery command")
	}

	// Extract IP address
	ipStart := 50 + len("req_discovery_clu:")
	ipEnd := len(data)
	if ipEnd > ipStart && data[ipEnd-1] == '\n' {
		ipEnd--
	}
	if ipEnd > ipStart && data[ipEnd-1] == '\r' {
		ipEnd--
	}

	ipAddress := string(data[ipStart:ipEnd])

	return &DiscoveryRequest{
		Encrypted: encrypted,
		IV:        iv,
		IPAddress: ipAddress,
	}, nil
}

// IsDiscoveryRequest checks if the data contains a discovery request
func IsDiscoveryRequest(data []byte) bool {
	if len(data) < 75 {
		return false
	}

	// Check delimiters and command
	if data[32] != ':' || data[49] != ':' {
		return false
	}

	return bytes.Contains(data[50:], []byte("req_discovery_clu"))
}

// DiscoveryResponse represents a CLU discovery response
// Format: [encrypted_hash(32)]:[temporary_iv(16)]:resp_discovery_clu:[serial(8)]:[mac(12)]
type DiscoveryResponse struct {
	Encrypted    []byte // 32 bytes - encrypted hash
	TemporaryIV  []byte // 16 bytes - temporary IV for further communication
	SerialNumber uint64
	MACAddress   string
}

func (r *DiscoveryResponse) AsBytes() []byte {
	// Serial number as 8 hex chars (32-bit, lower part of 64-bit serial)
	serialHex := fmt.Sprintf("%08x", uint32(r.SerialNumber&0xFFFFFFFF))

	// Build response: encrypted(32):temp_iv(16):command:serial(8):mac(12)
	// Total before padding: 32+1+16+1+18+1+8+1+12 = 90 bytes -> pads to 96
	var buf bytes.Buffer
	buf.Write(r.Encrypted) // 32 raw bytes
	buf.WriteByte(':')
	buf.Write(r.TemporaryIV) // 16 raw bytes
	buf.WriteString(":resp_discovery_clu:")
	buf.WriteString(serialHex) // 8 hex chars
	buf.WriteByte(':')
	buf.WriteString(r.MACAddress) // 12 hex chars (no colons)

	return buf.Bytes()
}

// Hash implements the Grenton discovery hash algorithm
// This is a custom hash function used in the discovery protocol
func Hash(buffer []byte) []byte {
	result := make([]byte, len(buffer))

	// First byte: XOR of first and last byte
	previousValue := int(buffer[0] ^ buffer[len(buffer)-1])
	result[0] = byte(previousValue)

	// Rest of bytes: special hash algorithm
	for i := 1; i < len(buffer); i++ {
		a := previousValue % HashModA
		b := int(buffer[i]) % HashModB

		previousValue = (a + 1) * (b + 1)
		result[i] = byte(previousValue)
	}

	return result
}
