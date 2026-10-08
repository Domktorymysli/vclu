package commands

import (
	"bytes"
	"fmt"
)

// StartTFTPdRequest represents a request to start TFTP server
type StartTFTPdRequest struct{}

// ParseStartTFTPdRequest parses a StartTFTPd request from bytes
func ParseStartTFTPdRequest(data []byte) (*StartTFTPdRequest, error) {
	// Remove trailing CRLF if present
	data = bytes.TrimSuffix(data, []byte(CRLF))

	if string(data) != "req_start_ftp" {
		return nil, fmt.Errorf("not a StartTFTPd request")
	}

	return &StartTFTPdRequest{}, nil
}

// IsStartTFTPdRequest checks if the data contains a StartTFTPd request
func IsStartTFTPdRequest(data []byte) bool {
	data = bytes.TrimSuffix(data, []byte(CRLF))
	return string(data) == "req_start_ftp"
}

// StartTFTPdResponse represents a StartTFTPd response
type StartTFTPdResponse struct{}

func (r *StartTFTPdResponse) AsBytes() []byte {
	return []byte("resp:OK")
}
