package commands

import (
	"bytes"
	"net"
	"testing"
)

// ============================================================================
// LuaScriptRequest Tests
// ============================================================================

func TestParseLuaScriptRequest_Valid(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantIP     string
		wantSID    uint32
		wantScript string
	}{
		{
			name:       "basic request",
			input:      "req:192.168.1.100:0000abcd:print('hello')\r\n",
			wantIP:     "192.168.1.100",
			wantSID:    0x0000abcd,
			wantScript: "print('hello')",
		},
		{
			name:       "zero session",
			input:      "req:10.0.0.1:00000000:return 42\r\n",
			wantIP:     "10.0.0.1",
			wantSID:    0,
			wantScript: "return 42",
		},
		{
			name:       "max session",
			input:      "req:172.16.0.1:ffffffff:SYSTEM:test()\r\n",
			wantIP:     "172.16.0.1",
			wantSID:    0xffffffff,
			wantScript: "SYSTEM:test()",
		},
		{
			name:       "script with colons",
			input:      "req:192.168.0.1:12345678:obj:method():call():value\r\n",
			wantIP:     "192.168.0.1",
			wantSID:    0x12345678,
			wantScript: "obj:method():call():value",
		},
		{
			name:       "without CRLF",
			input:      "req:192.168.1.1:aabbccdd:test()",
			wantIP:     "192.168.1.1",
			wantSID:    0xaabbccdd,
			wantScript: "test()",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseLuaScriptRequest([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseLuaScriptRequest() error = %v", err)
			}

			if req.IPAddress.String() != tt.wantIP {
				t.Errorf("IPAddress = %v, want %v", req.IPAddress.String(), tt.wantIP)
			}
			if req.SessionID != tt.wantSID {
				t.Errorf("SessionID = %x, want %x", req.SessionID, tt.wantSID)
			}
			if req.Script != tt.wantScript {
				t.Errorf("Script = %v, want %v", req.Script, tt.wantScript)
			}
		})
	}
}

func TestParseLuaScriptRequest_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"wrong prefix", "response:192.168.1.1:00000000:test"},
		{"missing parts", "req:192.168.1.1"},
		{"invalid IP", "req:invalid:00000000:test"},
		{"invalid session hex", "req:192.168.1.1:ghijklmn:test"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLuaScriptRequest([]byte(tt.input))
			if err == nil {
				t.Error("ParseLuaScriptRequest() expected error, got nil")
			}
		})
	}
}

func TestLuaScriptRequest_AsBytes(t *testing.T) {
	req := &LuaScriptRequest{
		IPAddress: net.ParseIP("192.168.1.100"),
		SessionID: 0x0000abcd,
		Script:    "print('hello')",
	}

	got := string(req.AsBytes())
	want := "req:192.168.1.100:0000abcd:print('hello')\r\n"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

func TestLuaScriptResponse_AsBytes(t *testing.T) {
	resp := &LuaScriptResponse{
		IPAddress:   net.ParseIP("192.168.1.100"),
		SessionID:   0x12345678,
		ReturnValue: "42",
	}

	got := string(resp.AsBytes())
	want := "resp:192.168.1.100:12345678:42"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

func TestLuaScriptRequest_RoundTrip(t *testing.T) {
	original := &LuaScriptRequest{
		IPAddress: net.ParseIP("10.20.30.40"),
		SessionID: 0xdeadbeef,
		Script:    "SYSTEM:clientRegister('192.168.1.1', 4344, 123)",
	}

	// Serialize
	data := original.AsBytes()

	// Parse back
	parsed, err := ParseLuaScriptRequest(data)
	if err != nil {
		t.Fatalf("RoundTrip parse error: %v", err)
	}

	if !original.IPAddress.Equal(parsed.IPAddress) {
		t.Errorf("IPAddress mismatch: %v != %v", original.IPAddress, parsed.IPAddress)
	}
	if original.SessionID != parsed.SessionID {
		t.Errorf("SessionID mismatch: %x != %x", original.SessionID, parsed.SessionID)
	}
	if original.Script != parsed.Script {
		t.Errorf("Script mismatch: %q != %q", original.Script, parsed.Script)
	}
}

// ============================================================================
// DiscoverCLUsRequest/Response Tests
// ============================================================================

func TestDiscoverCLUsRequest_AsBytes(t *testing.T) {
	req := &DiscoverCLUsRequest{}
	got := string(req.AsBytes())
	want := "dscvr\r\n"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

func TestDiscoverCLUsResponse_AsBytes(t *testing.T) {
	resp := &DiscoverCLUsResponse{
		IPAddress:       net.ParseIP("192.168.1.100"),
		SerialNumber:    0x123456789ABCDEF0,
		MACAddress:      "AABBCCDDEEFF",
		HardwareType:    0x01,
		HardwareVersion: 0x02,
		FirmwareType:    0x03,
		FirmwareVersion: 0x12345678,
	}

	got := string(resp.AsBytes())
	// Serial hex is lower 32 bits: 0x9ABCDEF0
	want := "dscvr:192.168.1.100:123456789abcdef0:AABBCCDDEEFF:01:02:03:12345678\r\n"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

// ============================================================================
// ErrorResponse Tests
// ============================================================================

func TestErrorResponse_AsBytes(t *testing.T) {
	resp := &ErrorResponse{
		IPAddress: net.ParseIP("192.168.1.1"),
		SessionID: 0x00001234,
		Error:     "execution failed",
	}

	got := string(resp.AsBytes())
	want := "error:192.168.1.1:00001234:execution failed\r\n"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

// ============================================================================
// Discovery Protocol Tests
// ============================================================================

func TestIsDiscoveryRequest(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "valid discovery request",
			data: buildDiscoveryRequest("192.168.1.100"),
			want: true,
		},
		{
			name: "too short",
			data: []byte("short"),
			want: false,
		},
		{
			name: "wrong delimiters",
			data: bytes.Repeat([]byte("a"), 75),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDiscoveryRequest(tt.data); got != tt.want {
				t.Errorf("IsDiscoveryRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseDiscoveryRequest_Valid(t *testing.T) {
	data := buildDiscoveryRequest("192.168.1.100")

	req, err := ParseDiscoveryRequest(data)
	if err != nil {
		t.Fatalf("ParseDiscoveryRequest() error = %v", err)
	}

	if len(req.Encrypted) != 32 {
		t.Errorf("Encrypted length = %d, want 32", len(req.Encrypted))
	}
	if len(req.IV) != 16 {
		t.Errorf("IV length = %d, want 16", len(req.IV))
	}
	if req.IPAddress != "192.168.1.100" {
		t.Errorf("IPAddress = %s, want 192.168.1.100", req.IPAddress)
	}
}

func TestParseDiscoveryRequest_Invalid(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too short", []byte("short")},
		{"missing first delimiter", bytes.Repeat([]byte("a"), 80)},
		{"wrong command", buildWrongCommandRequest()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseDiscoveryRequest(tt.data)
			if err == nil {
				t.Error("ParseDiscoveryRequest() expected error")
			}
		})
	}
}

func TestDiscoveryResponse_AsBytes(t *testing.T) {
	encrypted := bytes.Repeat([]byte{0xAB}, 32)
	tempIV := bytes.Repeat([]byte{0xCD}, 16)

	resp := &DiscoveryResponse{
		Encrypted:    encrypted,
		TemporaryIV:  tempIV,
		SerialNumber: 0x12345678,
		MACAddress:   "AABBCCDDEEFF",
	}

	data := resp.AsBytes()

	// Check structure
	if len(data) < 90 {
		t.Errorf("Response too short: %d bytes", len(data))
	}

	// Check encrypted part (first 32 bytes)
	if !bytes.Equal(data[:32], encrypted) {
		t.Error("Encrypted bytes mismatch")
	}

	// Check delimiter
	if data[32] != ':' {
		t.Error("Missing first delimiter")
	}

	// Check temp IV
	if !bytes.Equal(data[33:49], tempIV) {
		t.Error("TemporaryIV mismatch")
	}

	// Check second delimiter
	if data[49] != ':' {
		t.Error("Missing second delimiter")
	}

	// Check contains response command
	if !bytes.Contains(data[50:], []byte("resp_discovery_clu:")) {
		t.Error("Missing response command")
	}
}

func TestHash(t *testing.T) {
	tests := []struct {
		name   string
		input  []byte
		expect byte // First byte of result
	}{
		{
			name:   "simple input",
			input:  []byte{0x10, 0x20, 0x30, 0x40},
			expect: 0x10 ^ 0x40, // XOR of first and last
		},
		{
			name:   "all zeros",
			input:  []byte{0x00, 0x00, 0x00, 0x00},
			expect: 0x00,
		},
		{
			name:   "all ones",
			input:  []byte{0xFF, 0xFF, 0xFF, 0xFF},
			expect: 0x00,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Hash(tt.input)

			if len(result) != len(tt.input) {
				t.Errorf("Hash length = %d, want %d", len(result), len(tt.input))
			}

			if result[0] != tt.expect {
				t.Errorf("First byte = %x, want %x", result[0], tt.expect)
			}
		})
	}
}

func TestHash_Deterministic(t *testing.T) {
	input := []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0}

	result1 := Hash(input)
	result2 := Hash(input)

	if !bytes.Equal(result1, result2) {
		t.Error("Hash is not deterministic")
	}
}

func TestHash_DifferentInputs(t *testing.T) {
	input1 := []byte{0x12, 0x34, 0x56, 0x78}
	input2 := []byte{0x12, 0x34, 0x56, 0x79} // One byte different

	result1 := Hash(input1)
	result2 := Hash(input2)

	if bytes.Equal(result1, result2) {
		t.Error("Different inputs produced same hash")
	}
}

// ============================================================================
// SetKey Tests
// ============================================================================

func TestIsSetKeyRequest(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{
			name: "valid setkey",
			data: buildSetKeyRequest(),
			want: true,
		},
		{
			name: "too short",
			data: []byte("short"),
			want: false,
		},
		{
			name: "wrong command",
			data: buildDiscoveryRequest("192.168.1.1"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSetKeyRequest(tt.data); got != tt.want {
				t.Errorf("IsSetKeyRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSetKeyRequest_Valid(t *testing.T) {
	data := buildSetKeyRequest()

	req, err := ParseSetKeyRequest(data)
	if err != nil {
		t.Fatalf("ParseSetKeyRequest() error = %v", err)
	}

	if len(req.Encrypted) != 32 {
		t.Errorf("Encrypted length = %d, want 32", len(req.Encrypted))
	}
	if len(req.IV) != 16 {
		t.Errorf("IV length = %d, want 16", len(req.IV))
	}
	if len(req.Key) != 16 {
		t.Errorf("Key length = %d, want 16", len(req.Key))
	}
}

func TestParseSetKeyRequest_Invalid(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"too short", []byte("short")},
		{"wrong delimiters", bytes.Repeat([]byte("a"), 80)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSetKeyRequest(tt.data)
			if err == nil {
				t.Error("ParseSetKeyRequest() expected error")
			}
		})
	}
}

func TestSetKeyResponse_AsBytes(t *testing.T) {
	resp := &SetKeyResponse{}
	got := string(resp.AsBytes())
	want := "resp:OK"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

// ============================================================================
// Reset Tests
// ============================================================================

func TestIsResetRequest(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"valid reset", []byte("req_reset:192.168.1.1"), true},
		{"with CRLF", []byte("req_reset:192.168.1.1\r\n"), true},
		{"wrong command", []byte("req_other:192.168.1.1"), false},
		{"empty", []byte(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsResetRequest(tt.data); got != tt.want {
				t.Errorf("IsResetRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseResetRequest_Valid(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantIP string
	}{
		{"basic", "req_reset:192.168.1.100", "192.168.1.100"},
		{"with CRLF", "req_reset:10.0.0.1\r\n", "10.0.0.1"},
		{"with spaces", "  req_reset:172.16.0.1  ", "172.16.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseResetRequest([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseResetRequest() error = %v", err)
			}

			if req.IPAddress.String() != tt.wantIP {
				t.Errorf("IPAddress = %v, want %v", req.IPAddress.String(), tt.wantIP)
			}
		})
	}
}

func TestParseResetRequest_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"wrong command", "req_other:192.168.1.1"},
		{"missing IP", "req_reset:"},
		{"invalid IP", "req_reset:not-an-ip"},
		{"too many parts", "req_reset:192.168.1.1:extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseResetRequest([]byte(tt.input))
			if err == nil {
				t.Error("ParseResetRequest() expected error")
			}
		})
	}
}

func TestResetResponse_AsBytes(t *testing.T) {
	resp := &ResetResponse{
		IPAddress: net.ParseIP("192.168.1.100"),
	}

	got := string(resp.AsBytes())
	want := "resp_reset:192.168.1.100"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

// ============================================================================
// SetIp Tests
// ============================================================================

func TestIsSetIpRequest(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"valid", []byte("req_set_clu_ip:1234:192.168.1.1:192.168.1.254"), true},
		{"wrong prefix", []byte("req_other:1234:192.168.1.1:192.168.1.254"), false},
		{"empty", []byte(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSetIpRequest(tt.data); got != tt.want {
				t.Errorf("IsSetIpRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseSetIpRequest_Valid(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantSerial  uint64
		wantIP      string
		wantGateway string
	}{
		{
			name:        "basic",
			input:       "req_set_clu_ip:00001234:192.168.1.100:192.168.1.1",
			wantSerial:  0x00001234,
			wantIP:      "192.168.1.100",
			wantGateway: "192.168.1.1",
		},
		{
			name:        "full serial",
			input:       "req_set_clu_ip:123456789abcdef0:10.0.0.50:10.0.0.1",
			wantSerial:  0x123456789abcdef0,
			wantIP:      "10.0.0.50",
			wantGateway: "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := ParseSetIpRequest([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseSetIpRequest() error = %v", err)
			}

			if req.SerialNumber != tt.wantSerial {
				t.Errorf("SerialNumber = %x, want %x", req.SerialNumber, tt.wantSerial)
			}
			if req.IPAddress.String() != tt.wantIP {
				t.Errorf("IPAddress = %v, want %v", req.IPAddress.String(), tt.wantIP)
			}
			if req.GatewayAddress.String() != tt.wantGateway {
				t.Errorf("GatewayAddress = %v, want %v", req.GatewayAddress.String(), tt.wantGateway)
			}
		})
	}
}

func TestParseSetIpRequest_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"wrong command", "req_other:1234:192.168.1.1:192.168.1.254"},
		{"missing parts", "req_set_clu_ip:1234:192.168.1.1"},
		{"invalid serial", "req_set_clu_ip:not-hex:192.168.1.1:192.168.1.254"},
		{"invalid IP", "req_set_clu_ip:1234:invalid:192.168.1.254"},
		{"invalid gateway", "req_set_clu_ip:1234:192.168.1.1:invalid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseSetIpRequest([]byte(tt.input))
			if err == nil {
				t.Error("ParseSetIpRequest() expected error")
			}
		})
	}
}

func TestSetIpResponse_AsBytes(t *testing.T) {
	resp := &SetIpResponse{
		SerialNumber: 0x123456789abcdef0,
		IPAddress:    net.ParseIP("192.168.1.100"),
	}

	got := string(resp.AsBytes())
	want := "resp_set_clu_ip:123456789abcdef0:192.168.1.100"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

// ============================================================================
// StartTFTPd Tests
// ============================================================================

func TestIsStartTFTPdRequest(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"valid", []byte("req_start_ftp"), true},
		{"with CRLF", []byte("req_start_ftp\r\n"), true},
		{"wrong command", []byte("req_other"), false},
		{"partial", []byte("req_start"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsStartTFTPdRequest(tt.data); got != tt.want {
				t.Errorf("IsStartTFTPdRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseStartTFTPdRequest_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"basic", "req_start_ftp"},
		{"with CRLF", "req_start_ftp\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseStartTFTPdRequest([]byte(tt.input))
			if err != nil {
				t.Fatalf("ParseStartTFTPdRequest() error = %v", err)
			}
		})
	}
}

func TestParseStartTFTPdRequest_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"wrong command", "req_other"},
		{"extra data", "req_start_ftp:extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseStartTFTPdRequest([]byte(tt.input))
			if err == nil {
				t.Error("ParseStartTFTPdRequest() expected error")
			}
		})
	}
}

func TestStartTFTPdResponse_AsBytes(t *testing.T) {
	resp := &StartTFTPdResponse{}
	got := string(resp.AsBytes())
	want := "resp:OK"

	if got != want {
		t.Errorf("AsBytes() = %q, want %q", got, want)
	}
}

// ============================================================================
// Helpers
// ============================================================================

func buildDiscoveryRequest(ip string) []byte {
	// Format: [encrypted(32)]:[iv(16)]:req_discovery_clu:[ip]
	encrypted := bytes.Repeat([]byte{0xAB}, 32)
	iv := bytes.Repeat([]byte{0xCD}, 16)
	command := []byte("req_discovery_clu:" + ip)

	var buf bytes.Buffer
	buf.Write(encrypted)
	buf.WriteByte(':')
	buf.Write(iv)
	buf.WriteByte(':')
	buf.Write(command)

	return buf.Bytes()
}

func buildWrongCommandRequest() []byte {
	encrypted := bytes.Repeat([]byte{0xAB}, 32)
	iv := bytes.Repeat([]byte{0xCD}, 16)
	command := []byte("req_wrong_command:192.168.1.1")

	var buf bytes.Buffer
	buf.Write(encrypted)
	buf.WriteByte(':')
	buf.Write(iv)
	buf.WriteByte(':')
	buf.Write(command)

	return buf.Bytes()
}

func buildSetKeyRequest() []byte {
	// Format: [encrypted(32)]:[iv(16)]:req_set_key:[key(16)]
	encrypted := bytes.Repeat([]byte{0xAB}, 32)
	iv := bytes.Repeat([]byte{0xCD}, 16)
	key := bytes.Repeat([]byte{0xEF}, 16)

	var buf bytes.Buffer
	buf.Write(encrypted)
	buf.WriteByte(':')
	buf.Write(iv)
	buf.WriteByte(':')
	buf.WriteString("req_set_key:")
	buf.Write(key)

	return buf.Bytes()
}
