package config

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

type OMGenerator struct {
	device  *VCLUDeviceConfig
	localIP uint32
}

func NewOMGenerator(device *VCLUDeviceConfig, localIP uint32) *OMGenerator {
	return &OMGenerator{
		device:  device,
		localIP: localIP,
	}
}

func (g *OMGenerator) Generate() (string, error) {
	if g.device == nil {
		return "", fmt.Errorf("device config is nil")
	}

	switch g.device.Category {
	case DeviceCategoryGate:
		return g.generateGate()
	case DeviceCategoryCLU:
		return g.generateCLU()
	case DeviceCategoryStandalone:
		return g.generateStandalone()
	default:
		return g.generateGate()
	}
}

func (g *OMGenerator) generateGate() (string, error) {
	tmpl := `-- FwType {{.FwTypeHex}}
-- FwVersion {{.FwVersionHex}}
-- HwType {{.HwTypeHex}}
-- HwVersion {{.HwVersionHex}}

CLU{{.Serial}} = GATE:new(5000, 0x{{.IPHex}}, 0xFFFFFF00, 0x{{.GatewayHex}}, "", "")
-- NAME_CLU {{.Name}}=CLU{{.Serial}}

-- MODULES

-- IO_MODULES

function setVar(name, value)
	_G[name] = value
end

function getVar(name)
	return _G[name]
end

function OnInit()
-- INIT_CLU_OBJECTS
CLU{{.Serial}}:set(1, 1000)
CLU{{.Serial}}:set(2, "8.8.8.8")
CLU{{.Serial}}:set(3, "8.8.4.4")

end

CLU{{.Serial}}:add_event(0, OnInit)
`
	return g.renderTemplate(tmpl)
}

func (g *OMGenerator) generateCLU() (string, error) {
	tmpl := `-- FwType {{.FwTypeHex}}
-- FwVersion {{.FwVersionHex}}
-- HwType {{.HwTypeHex}}
-- HwVersion {{.HwVersionHex}}

CLU{{.Serial}} = OBJECT:new(0, 0x{{.IPHex}})
-- NAME_CLU {{.Name}}=CLU{{.Serial}}

-- MODULES

-- IO_MODULES

function setVar(name, value)
	_G[name] = value
end

function getVar(name)
	return _G[name]
end

function OnInit()

-- INIT_CLU_OBJECTS
CLU{{.Serial}}:set(18, 0)

end

CLU{{.Serial}}:add_event(0, OnInit)
`
	return g.renderTemplate(tmpl)
}

func (g *OMGenerator) generateStandalone() (string, error) {
	// Standalone mode: minimal om.lua with no Grenton objects
	// Just define setVar/getVar helpers and an empty OnInit
	tmpl := `-- Standalone mode (no Grenton integration)

function setVar(name, value)
	_G[name] = value
end

function getVar(name)
	return _G[name]
end

function OnInit()
end
`
	return tmpl, nil
}

func (g *OMGenerator) renderTemplate(tmplStr string) (string, error) {
	tmpl, err := template.New("om").Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	data := g.prepareTemplateData()

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

type templateModule struct {
	Serial    string
	SerialHex string
	HwTypeHex string
}

type templateIO struct {
	ID           string
	TypeID       string
	LuaType      int
	ModuleSerial string
	Index        int
	Name         string
}

func (g *OMGenerator) prepareTemplateData() map[string]interface{} {
	modules := make([]templateModule, 0, len(g.device.Modules))
	moduleSerialMap := make(map[string]string)

	for _, m := range g.device.Modules {
		serialNum := parseSerialToUint32(m.Serial)
		modules = append(modules, templateModule{
			Serial:    m.Serial,
			SerialHex: fmt.Sprintf("%08x", serialNum),
			HwTypeHex: fmt.Sprintf("%02x", m.HardwareType),
		})
		moduleSerialMap[m.ID] = m.Serial
	}

	ioObjects := make([]templateIO, 0, len(g.device.IOObjects))
	for _, io := range g.device.IOObjects {
		moduleSerial := moduleSerialMap[io.ModuleID]
		ioObjects = append(ioObjects, templateIO{
			ID:           io.ID,
			TypeID:       io.TypeID,
			LuaType:      io.LuaType,
			ModuleSerial: moduleSerial,
			Index:        io.Index,
			Name:         io.Name,
		})
	}

	gatewayIP := (g.localIP & 0xFFFFFF00) | 1
	name := g.device.Name
	if name == "" {
		name = fmt.Sprintf("CLU%d", g.device.SerialNumber)
	}

	return map[string]interface{}{
		"Serial":       g.device.SerialNumber,
		"Name":         name,
		"IPHex":        fmt.Sprintf("%08X", g.localIP),
		"GatewayHex":   fmt.Sprintf("%08X", gatewayIP),
		"FwTypeHex":    fmt.Sprintf("%08x", g.device.FirmwareType),
		"FwVersionHex": fmt.Sprintf("%08x", g.device.FirmwareVersion),
		"HwTypeHex":    fmt.Sprintf("%08x", g.device.HardwareType),
		"HwVersionHex": fmt.Sprintf("%08x", g.device.HardwareVersion),
		"Modules":      modules,
		"IOObjects":    ioObjects,
	}
}

func parseSerialToUint32(serial string) uint32 {
	cleaned := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, serial)

	val, err := strconv.ParseUint(cleaned, 10, 32)
	if err != nil {
		return 0
	}
	return uint32(val)
}

func IPToUint32(ip string) uint32 {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return 0
	}

	var result uint32
	for _, p := range parts {
		val, err := strconv.Atoi(p)
		if err != nil || val < 0 || val > 255 {
			return 0
		}
		result = (result << 8) | uint32(val)
	}
	return result
}

func Uint32ToIP(ip uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		(ip>>24)&0xFF,
		(ip>>16)&0xFF,
		(ip>>8)&0xFF,
		ip&0xFF)
}
