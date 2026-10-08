package commands

import (
	"bytes"
	"fmt"
)

// SetKeyRequest represents a key update request
// Format: [encrypted_random(32)]:[iv(16)]:req_set_key:[new_key(16)]
type SetKeyRequest struct {
	Encrypted []byte // 32 bytes - encrypted random bytes for verification
	IV        []byte // 16 bytes - new IV
	Key       []byte // 16 bytes - new key
}

// ParseSetKeyRequest parses a SetKey request from bytes
func ParseSetKeyRequest(data []byte) (*SetKeyRequest, error) {
	// Expected length: 32 (encrypted) + 1 (:) + 16 (iv) + 1 (:) + 11 (command) + 1 (:) + 16 (key) = 78
	// May have \r\n at the end
	if len(data) < 78 {
		return nil, fmt.Errorf("setkey request too short: %d bytes", len(data))
	}

	// Check delimiters
	if data[32] != ':' || data[49] != ':' {
		return nil, fmt.Errorf("invalid setkey request format - wrong delimiters")
	}

	// Check command
	if !bytes.HasPrefix(data[50:], []byte("req_set_key:")) {
		return nil, fmt.Errorf("invalid setkey command")
	}

	encrypted := make([]byte, 32)
	copy(encrypted, data[0:32])

	iv := make([]byte, 16)
	copy(iv, data[33:49])

	key := make([]byte, 16)
	keyStart := 50 + len("req_set_key:")
	copy(key, data[keyStart:keyStart+16])

	return &SetKeyRequest{
		Encrypted: encrypted,
		IV:        iv,
		Key:       key,
	}, nil
}

// IsSetKeyRequest checks if the data contains a SetKey request
func IsSetKeyRequest(data []byte) bool {
	if len(data) < 78 {
		return false
	}

	// Check delimiters and command
	if data[32] != ':' || data[49] != ':' {
		return false
	}

	return bytes.HasPrefix(data[50:], []byte("req_set_key:"))
}

// SetKeyResponse represents a SetKey response
type SetKeyResponse struct{}

func (r *SetKeyResponse) AsBytes() []byte {
	return []byte("resp:OK")
}
