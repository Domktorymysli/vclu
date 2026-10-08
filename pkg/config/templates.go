package config

import (
	"embed"
	"fmt"
	"net"
	"strings"
)

//go:embed templates/*
var templatesFS embed.FS

// TemplateVars contains all variables for template rendering
type TemplateVars struct {
	// Serial number
	SerialNumber   uint64
	SerialHex      string // 16 hex chars
	SerialHexShort string // last 8 hex chars (for checkAlive)

	// Network
	MACAddress   string // aa:bb:cc:dd:ee:ff
	IPAddress    string // 192.168.1.100
	IPAddressInt uint32 // IP as integer for Lua
	Gateway      string // 192.168.1.1
	GatewayInt   uint32 // Gateway as integer for Lua

	// Hardware
	HardwareType        uint8
	HardwareTypeHex     string // 2 hex chars
	HardwareTypeHex8    string // 8 hex chars (for om.lua headers)
	HardwareVersion     uint8
	HardwareVersionHex  string // 2 hex chars
	HardwareVersionHex8 string // 8 hex chars (for om.lua headers)

	// Firmware
	FirmwareType          uint8
	FirmwareTypeHex       string // 2 hex chars
	FirmwareTypeHex8      string // 8 hex chars (for om.lua headers)
	FirmwareVersion       uint32
	FirmwareVersionHex    string // 8 hex chars
	FirmwareVersionString string // "x.y.z" format
	FirmwareAPIVersion    uint32

	// CLU
	CLUName string // e.g., "VCLU01" or "CLU520000006"

	// Security
	PIN string // 8 character PIN
}

// NewTemplateVars creates template variables from device config
func NewTemplateVars(deviceConfig *CLUDeviceConfig, localIP net.IP, gateway net.IP, pin string) *TemplateVars {
	serialHex := fmt.Sprintf("%016x", deviceConfig.SerialNumber)

	// Default PIN if not provided
	if pin == "" {
		pin = "00000000"
	}

	// Convert IP to uint32 for Lua
	ipInt := ipToUint32(localIP)
	gatewayInt := uint32(0)
	if gateway != nil {
		gatewayInt = ipToUint32(gateway)
	} else {
		// Default gateway: same subnet .1
		if localIP != nil && len(localIP) >= 4 {
			gateway = net.IPv4(localIP[len(localIP)-4], localIP[len(localIP)-3], localIP[len(localIP)-2], 1)
			gatewayInt = ipToUint32(gateway)
		}
	}

	// Generate CLU name from serial (e.g., CLU520000006)
	cluName := fmt.Sprintf("CLU%d", deviceConfig.SerialNumber)

	// Firmware version string
	major := (deviceConfig.FirmwareVersion >> 16) & 0xFF
	minor := (deviceConfig.FirmwareVersion >> 8) & 0xFF
	patch := deviceConfig.FirmwareVersion & 0xFF
	fwVerStr := fmt.Sprintf("%d.%d.%d", major, minor, patch)

	return &TemplateVars{
		SerialNumber:   deviceConfig.SerialNumber,
		SerialHex:      serialHex,
		SerialHexShort: serialHex[len(serialHex)-8:],

		MACAddress:   deviceConfig.MACAddress,
		IPAddress:    localIP.String(),
		IPAddressInt: ipInt,
		Gateway:      gateway.String(),
		GatewayInt:   gatewayInt,

		HardwareType:        deviceConfig.HardwareType,
		HardwareTypeHex:     fmt.Sprintf("%02x", deviceConfig.HardwareType),
		HardwareTypeHex8:    fmt.Sprintf("%08x", deviceConfig.HardwareType),
		HardwareVersion:     deviceConfig.HardwareVersion,
		HardwareVersionHex:  fmt.Sprintf("%02x", deviceConfig.HardwareVersion),
		HardwareVersionHex8: fmt.Sprintf("%08x", deviceConfig.HardwareVersion),

		FirmwareType:          deviceConfig.FirmwareType,
		FirmwareTypeHex:       fmt.Sprintf("%02x", deviceConfig.FirmwareType),
		FirmwareTypeHex8:      fmt.Sprintf("%08x", deviceConfig.FirmwareType),
		FirmwareVersion:       deviceConfig.FirmwareVersion,
		FirmwareVersionHex:    fmt.Sprintf("%08x", deviceConfig.FirmwareVersion),
		FirmwareVersionString: fwVerStr,
		FirmwareAPIVersion:    1000, // Default API version

		CLUName: cluName,

		PIN: pin,
	}
}

// ipToUint32 converts net.IP to uint32
func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	if ip == nil {
		return 0
	}
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// RenderTemplate renders a template with the given variables
func RenderTemplate(templateName string, vars *TemplateVars) (string, error) {
	// Read template from embedded FS
	content, err := templatesFS.ReadFile("templates/" + templateName)
	if err != nil {
		return "", fmt.Errorf("failed to read template %s: %w", templateName, err)
	}

	return RenderTemplateString(string(content), vars), nil
}

// RenderTemplateString renders a template string with the given variables
func RenderTemplateString(template string, vars *TemplateVars) string {
	// Simple string replacement for all placeholders
	replacements := map[string]string{
		"{{SERIAL_NUMBER}}":           fmt.Sprintf("%d", vars.SerialNumber),
		"{{SERIAL_HEX}}":              vars.SerialHex,
		"{{SERIAL_HEX_SHORT}}":        vars.SerialHexShort,
		"{{MAC_ADDRESS}}":             vars.MACAddress,
		"{{IP_ADDRESS}}":              vars.IPAddress,
		"{{IP_ADDRESS_INT}}":          fmt.Sprintf("0x%08X", vars.IPAddressInt),
		"{{GATEWAY}}":                 vars.Gateway,
		"{{GATEWAY_INT}}":             fmt.Sprintf("0x%08X", vars.GatewayInt),
		"{{HARDWARE_TYPE}}":           fmt.Sprintf("%d", vars.HardwareType),
		"{{HARDWARE_TYPE_HEX}}":       vars.HardwareTypeHex,
		"{{HARDWARE_TYPE_HEX8}}":      vars.HardwareTypeHex8,
		"{{HARDWARE_VERSION}}":        fmt.Sprintf("%d", vars.HardwareVersion),
		"{{HARDWARE_VERSION_HEX}}":    vars.HardwareVersionHex,
		"{{HARDWARE_VERSION_HEX8}}":   vars.HardwareVersionHex8,
		"{{FIRMWARE_TYPE}}":           fmt.Sprintf("%d", vars.FirmwareType),
		"{{FIRMWARE_TYPE_HEX}}":       vars.FirmwareTypeHex,
		"{{FIRMWARE_TYPE_HEX8}}":      vars.FirmwareTypeHex8,
		"{{FIRMWARE_VERSION}}":        fmt.Sprintf("%d", vars.FirmwareVersion),
		"{{FIRMWARE_VERSION_HEX}}":    vars.FirmwareVersionHex,
		"{{FIRMWARE_VERSION_STRING}}": vars.FirmwareVersionString,
		"{{FIRMWARE_API_VERSION}}":    fmt.Sprintf("%d", vars.FirmwareAPIVersion),
		"{{CLU_NAME}}":                vars.CLUName,
		"{{PIN}}":                     vars.PIN,
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// GetTemplate returns the raw template content
func GetTemplate(templateName string) (string, error) {
	content, err := templatesFS.ReadFile("templates/" + templateName)
	if err != nil {
		return "", fmt.Errorf("failed to read template %s: %w", templateName, err)
	}
	return string(content), nil
}
