package commands

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	MaxSessionChars = 8
	CRLF            = "\r\n"
)

// Command interface for Grenton protocol commands
type Command interface {
	AsBytes() []byte
}

// LuaScriptRequest represents a Lua script execution request
type LuaScriptRequest struct {
	IPAddress net.IP
	SessionID uint32
	Script    string
}

func (r *LuaScriptRequest) AsBytes() []byte {
	sessionHex := fmt.Sprintf("%0*x", MaxSessionChars, r.SessionID)
	return []byte(fmt.Sprintf("req:%s:%s:%s%s", r.IPAddress.String(), sessionHex, r.Script, CRLF))
}

// ParseLuaScriptRequest parses a Lua script request from bytes
func ParseLuaScriptRequest(data []byte) (*LuaScriptRequest, error) {
	str := string(data)
	if !strings.HasPrefix(str, "req:") {
		return nil, fmt.Errorf("not a Lua script request")
	}

	parts := strings.SplitN(str, ":", 4)
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid request format")
	}

	ip := net.ParseIP(parts[1])
	if ip == nil {
		return nil, fmt.Errorf("invalid IP address: %s", parts[1])
	}

	sessionID, err := strconv.ParseUint(parts[2], 16, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid session ID: %w", err)
	}

	script := strings.TrimSuffix(parts[3], CRLF)

	return &LuaScriptRequest{
		IPAddress: ip,
		SessionID: uint32(sessionID),
		Script:    script,
	}, nil
}

// LuaScriptResponse represents a Lua script execution response
type LuaScriptResponse struct {
	IPAddress   net.IP
	SessionID   uint32
	ReturnValue string
}

func (r *LuaScriptResponse) AsBytes() []byte {
	sessionHex := fmt.Sprintf("%0*x", MaxSessionChars, r.SessionID)
	return []byte(fmt.Sprintf("resp:%s:%s:%s", r.IPAddress.String(), sessionHex, r.ReturnValue))
}

// DiscoverCLUsRequest represents a CLU discovery request
type DiscoverCLUsRequest struct{}

func (r *DiscoverCLUsRequest) AsBytes() []byte {
	return []byte("dscvr" + CRLF)
}

// DiscoverCLUsResponse represents a CLU discovery response
type DiscoverCLUsResponse struct {
	IPAddress       net.IP
	SerialNumber    uint64
	MACAddress      string
	HardwareType    uint8
	HardwareVersion uint8
	FirmwareType    uint8
	FirmwareVersion uint32
}

func (r *DiscoverCLUsResponse) AsBytes() []byte {
	serialHex := fmt.Sprintf("%016x", r.SerialNumber)
	hwType := fmt.Sprintf("%02x", r.HardwareType)
	hwVer := fmt.Sprintf("%02x", r.HardwareVersion)
	fwType := fmt.Sprintf("%02x", r.FirmwareType)
	fwVer := fmt.Sprintf("%08x", r.FirmwareVersion)

	return []byte(fmt.Sprintf("dscvr:%s:%s:%s:%s:%s:%s:%s%s", r.IPAddress.String(), serialHex, r.MACAddress, hwType, hwVer, fwType, fwVer, CRLF))
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	IPAddress net.IP
	SessionID uint32
	Error     string
}

func (r *ErrorResponse) AsBytes() []byte {
	sessionHex := fmt.Sprintf("%0*x", MaxSessionChars, r.SessionID)
	return []byte(fmt.Sprintf("error:%s:%s:%s%s", r.IPAddress.String(), sessionHex, r.Error, CRLF))
}
