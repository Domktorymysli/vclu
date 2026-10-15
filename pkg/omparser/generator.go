package omparser

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/lua"
)

// GeneratorConfig holds configuration for code generation
type GeneratorConfig struct {
	// Namespace for the generated objects (e.g., "salon", "kuchnia")
	// If empty, uses CLU name from om.lua
	Namespace string

	// IncludeBaseClasses - if true, includes RemoteObject base classes
	// Set to false if base classes are already defined elsewhere
	IncludeBaseClasses bool

	// IncludeRegistry - if true, includes registry table definitions
	// Set to false if registry is already defined elsewhere
	IncludeRegistry bool

	// RegisterObjects - if true, registers objects in _registry for lookup
	RegisterObjects bool

	// IncludeHelpers - if true, includes helper functions (scenes, timers, math)
	IncludeHelpers bool
}

// DefaultConfig returns default generator configuration
// Base classes and registry are loaded from runtime, so not included by default
// Objects are registered in _registry for lookup
func DefaultConfig() GeneratorConfig {
	return GeneratorConfig{
		IncludeBaseClasses: false,
		IncludeRegistry:    false,
		RegisterObjects:    true,
		IncludeHelpers:     false,
	}
}

// GenerateProxy generates Lua proxy code from parsed om.lua
func GenerateProxy(parsed *ParsedOM, config GeneratorConfig) string {
	var sb strings.Builder

	// Header
	sb.WriteString("-- ============================================\n")
	sb.WriteString(fmt.Sprintf("-- Proxy generated from %s (%s)\n", parsed.CLU.ID, parsed.CLU.Name))
	sb.WriteString(fmt.Sprintf("-- IP: %s\n", IPToString(parsed.CLU.IP)))
	sb.WriteString("-- ============================================\n\n")

	// Base classes
	if config.IncludeBaseClasses {
		sb.WriteString(generateBaseClasses())
		sb.WriteString("\n")
	}

	// Registry
	if config.IncludeRegistry {
		sb.WriteString(generateRegistry())
		sb.WriteString("\n")
	}

	// Helpers (scenes, timers, math)
	if config.IncludeHelpers {
		sb.WriteString(generateHelpers())
		sb.WriteString("\n")
	}

	// Namespace
	namespace := config.Namespace
	if namespace == "" {
		namespace = strings.ToLower(parsed.CLU.Name)
		if namespace == "" {
			namespace = strings.ToLower(parsed.CLU.ID)
		}
	}

	// Extract serial from CLU ID (e.g., "CLU212345678" -> 212345678)
	serial := strings.TrimPrefix(parsed.CLU.ID, "CLU")

	// Create CLU_REMOTE reference
	sb.WriteString("-- Create remote CLU reference\n")
	sb.WriteString(fmt.Sprintf("%s = CLU_REMOTE:new(%s, 0x%08X, system:getEventBus())\n",
		parsed.CLU.ID, serial, parsed.CLU.IP))
	sb.WriteString(fmt.Sprintf("local _clu = %s\n\n", parsed.CLU.ID))

	// Create namespace table
	sb.WriteString(fmt.Sprintf("-- Namespace: %s\n", namespace))
	sb.WriteString(fmt.Sprintf("%s = {}\n\n", namespace))

	// Generate objects by type
	generateObjectsForClass(&sb, parsed, namespace, ClassDOUT, "RemoteSwitch")
	generateObjectsForClass(&sb, parsed, namespace, ClassDIN, "RemoteSensor")
	generateObjectsForClass(&sb, parsed, namespace, ClassDIMMER, "RemoteLight")
	generateObjectsForClass(&sb, parsed, namespace, ClassLEDRGB, "RemoteRGBLight")
	generateObjectsForClass(&sb, parsed, namespace, ClassROLLER_SH, "RemoteCover")
	generateObjectsForClass(&sb, parsed, namespace, ClassROLLER, "RemoteCover")
	generateObjectsForClass(&sb, parsed, namespace, ClassAnalogIN, "RemoteSensor")
	generateObjectsForClass(&sb, parsed, namespace, ClassAnalogOUT, "RemoteNumber")
	generateObjectsForClass(&sb, parsed, namespace, ClassONE_WIRE, "RemoteThermometer")
	generateTypedSensorsForClass(&sb, parsed, namespace, ClassPanelTemp, "PANELSENSTEMP")
	generateTypedSensorsForClass(&sb, parsed, namespace, ClassPanelLight, "PANELSENSLIGHT")
	generateTypedSensorsForClass(&sb, parsed, namespace, ClassHumidity, "HUMIDITY_SENSOR")

	// Register objects in _registry for lookup (use registerObject for name indexing)
	if config.RegisterObjects {
		sb.WriteString("-- Register objects for event forwarding\n")
		for _, obj := range parsed.GetExposableObjects() {
			objName := getObjectLuaName(obj)
			path := fmt.Sprintf("%s.%s", parsed.CLU.ID, obj.ID)
			sb.WriteString(fmt.Sprintf("registerObject(\"%s\", %s.%s)\n", path, namespace, objName))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func generateObjectsForClass(sb *strings.Builder, parsed *ParsedOM, namespace string, class ObjectClass, proxyClass string) {
	objects := parsed.GetObjectsByClass(class)
	if len(objects) == 0 {
		return
	}

	sb.WriteString(fmt.Sprintf("-- %s (%d objects)\n", class.String(), len(objects)))
	for _, obj := range objects {
		objName := getObjectLuaName(obj)
		// Pass name as 3rd argument to new()
		nameArg := ""
		if obj.Name != "" {
			// Escape quotes in name
			escapedName := strings.ReplaceAll(obj.Name, `"`, `\"`)
			nameArg = fmt.Sprintf(`, "%s"`, escapedName)
		}
		sb.WriteString(fmt.Sprintf("%s.%s = %s:new(_clu, \"%s\"%s)\n",
			namespace, objName, proxyClass, obj.ID, nameArg))
		if class == ClassROLLER {
			// Feature 7 Position (not on fv01 firmware, which om.lua does not tell apart)
			sb.WriteString(fmt.Sprintf("%s.%s._hasPosition = true\n", namespace, objName))
		}
	}
	sb.WriteString("\n")
}

// generateTypedSensorsForClass emits read-only RemoteValueSensor proxies whose Grenton
// type is passed explicitly (so it maps to the right expose type: temperature/humidity/illuminance).
func generateTypedSensorsForClass(sb *strings.Builder, parsed *ParsedOM, namespace string, class ObjectClass, grentonType string) {
	objects := parsed.GetObjectsByClass(class)
	if len(objects) == 0 {
		return
	}

	sb.WriteString(fmt.Sprintf("-- %s (%d objects)\n", class.String(), len(objects)))
	for _, obj := range objects {
		objName := getObjectLuaName(obj)
		nameArg := ""
		if obj.Name != "" {
			escapedName := strings.ReplaceAll(obj.Name, `"`, `\"`)
			nameArg = fmt.Sprintf(`, "%s"`, escapedName)
		}
		sb.WriteString(fmt.Sprintf("%s.%s = RemoteValueSensor:new(_clu, \"%s\"%s, \"%s\")\n",
			namespace, objName, obj.ID, nameArg, grentonType))
	}
	sb.WriteString("\n")
}

// GenerateLocalProxy generates Lua code that registers existing local CLU objects
// in _registry with human-readable names from om.lua NAME_IO comments.
// Unlike GenerateProxy, it does NOT create new objects (RemoteSwitch, CLU_REMOTE etc.)
// — the objects already exist in Lua, created by LoadCLUData → om.lua → ObjectFactory.
func GenerateLocalProxy(parsed *ParsedOM) string {
	objects := parsed.GetExposableObjects()
	if len(objects) == 0 {
		return ""
	}

	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("-- Local CLU object registration (%s)\n", parsed.CLU.ID))
	sb.WriteString("-- Register existing objects in _registry with human-readable names\n\n")

	for _, obj := range objects {
		// Set _name if NAME_IO comment provided a friendly name
		if obj.Name != "" {
			escapedName := strings.ReplaceAll(obj.Name, `"`, `\"`)
			sb.WriteString(fmt.Sprintf("%s._name = \"%s\"\n", obj.ID, escapedName))
		}

		// Set _type for ExposedObjects type mapping
		sb.WriteString(fmt.Sprintf("%s._type = \"%s\"\n", obj.ID, obj.Class.String()))

		// Set _registryPath so StateBus events use the correct path format
		path := fmt.Sprintf("%s.%s", parsed.CLU.ID, obj.ID)
		sb.WriteString(fmt.Sprintf("%s._registryPath = \"%s\"\n", obj.ID, path))

		// Register in _registry under CLU.ObjectID path
		sb.WriteString(fmt.Sprintf("registerObject(\"%s\", %s)\n\n", path, obj.ID))
	}

	return sb.String()
}

// GenerateLocalProxyFile parses a/om.lua and saves the local proxy as
// proxy_local.lua in importsDir. LoadProxyFiles will pick it up like any other proxy.
func GenerateLocalProxyFile(cluDataPath string, importsDir string) {
	omPath := filepath.Join(cluDataPath, "om.lua")
	content, err := os.ReadFile(omPath)
	if err != nil {
		return // no om.lua — nothing to register
	}

	parsed, err := Parse(string(content))
	if err != nil {
		log.Printf("[OMPARSER] failed to parse om.lua for local registration: %v", err)
		return
	}

	code := GenerateLocalProxy(parsed)
	if code == "" {
		return // no exposable objects
	}

	// Ensure imports directory exists
	if err := os.MkdirAll(importsDir, 0755); err != nil {
		log.Printf("[OMPARSER] failed to create imports dir: %v", err)
		return
	}

	proxyPath := filepath.Join(importsDir, "proxy_local.lua")
	if err := os.WriteFile(proxyPath, []byte(code), 0644); err != nil {
		log.Printf("[OMPARSER] failed to write proxy_local.lua: %v", err)
		return
	}

	count := len(parsed.GetExposableObjects())
	log.Printf("[OMPARSER] generated proxy_local.lua (%d objects)", count)
}

func getObjectLuaName(obj GrentonObject) string {
	// Use friendly name if available, otherwise use ID
	if obj.Name != "" && !strings.HasPrefix(obj.Name, "x") {
		// Clean up name - replace non-alphanumeric with underscore
		name := obj.Name
		name = strings.ReplaceAll(name, "-", "_")
		name = strings.ReplaceAll(name, " ", "_")
		return name
	}
	return obj.ID
}

func generateBaseClasses() string {
	return lua.BaseClasses()
}

func generateRegistry() string {
	return lua.Registry()
}

func generateHelpers() string {
	return lua.Helpers
}
