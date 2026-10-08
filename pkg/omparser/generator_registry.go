package omparser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RegistryEntry represents a single CLU entry in the registry
type RegistryEntry struct {
	Serial  uint64
	Name    string
	IP      string
	Objects []GrentonObject
	Modules []Module
}

// GenerateRegistryLua generates a Lua file containing a registry of all CLU objects
// This registry can be loaded into the Lua runtime to access objects from other CLUs
func GenerateRegistryLua(entries []RegistryEntry) string {
	var sb strings.Builder

	sb.WriteString("-- Auto-generated registry from OM files\n")
	sb.WriteString("-- Contains object definitions from other CLUs\n\n")
	sb.WriteString("_REGISTRY = {\n")

	for _, entry := range entries {
		sb.WriteString(fmt.Sprintf("  [%d] = {\n", entry.Serial))
		sb.WriteString(fmt.Sprintf("    name = %q,\n", entry.Name))
		sb.WriteString(fmt.Sprintf("    ip = %q,\n", entry.IP))
		sb.WriteString("    objects = {\n")

		for _, obj := range entry.Objects {
			// Skip non-exposable objects
			if !obj.Class.IsExposable() {
				continue
			}
			sb.WriteString(fmt.Sprintf("      %s = {name=%q, class=%d, module=%q, index=%d},\n",
				obj.ID, obj.Name, obj.Class, obj.ModuleID, obj.Index))
		}

		sb.WriteString("    },\n")
		sb.WriteString("    modules = {\n")

		for _, mod := range entry.Modules {
			sb.WriteString(fmt.Sprintf("      %s = {serial=%q, type=0x%x},\n",
				mod.ID, mod.Serial, mod.ModuleType))
		}

		sb.WriteString("    },\n")
		sb.WriteString("  },\n")
	}

	sb.WriteString("}\n\n")
	sb.WriteString("-- Setup metatable (methods defined in registry.lua)\n")
	sb.WriteString("if _REGISTRY_METHODS then setmetatable(_REGISTRY, {__index = _REGISTRY_METHODS}) end\n\n")
	sb.WriteString("-- Aliases\n")
	sb.WriteString("REGISTRY = _REGISTRY\n")
	sb.WriteString("_ = _REGISTRY\n")

	return sb.String()
}

// LoadAndGenerateRegistry loads all OM files from a directory and generates the registry
func LoadAndGenerateRegistry(importsDir string) (string, error) {
	// Find all om_*.lua files
	pattern := filepath.Join(importsDir, "om_*.lua")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", fmt.Errorf("failed to list OM files: %w", err)
	}

	if len(files) == 0 {
		// Return empty registry
		return "-- No OM files found\n_REGISTRY = {}\nif _REGISTRY_METHODS then setmetatable(_REGISTRY, {__index = _REGISTRY_METHODS}) end\nREGISTRY = _REGISTRY\n_ = _REGISTRY\n", nil
	}

	var entries []RegistryEntry

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			continue // Skip files that can't be read
		}

		parsed, err := Parse(string(content))
		if err != nil {
			continue // Skip files that can't be parsed
		}

		// Extract serial from CLU ID
		serial := extractSerialFromCLUID(parsed.CLU.ID)
		if serial == 0 {
			continue
		}

		entry := RegistryEntry{
			Serial:  serial,
			Name:    parsed.CLU.Name,
			IP:      IPToString(parsed.CLU.IP),
			Objects: parsed.Objects,
			Modules: parsed.Modules,
		}
		entries = append(entries, entry)
	}

	return GenerateRegistryLua(entries), nil
}

// extractSerialFromCLUID extracts the numeric serial from a CLU ID like "CLU212345678"
func extractSerialFromCLUID(cluID string) uint64 {
	if len(cluID) < 4 {
		return 0
	}
	numStr := cluID[3:]
	var serial uint64
	fmt.Sscanf(numStr, "%d", &serial)
	return serial
}

// WriteRegistryFile generates and writes the registry.lua file
func WriteRegistryFile(importsDir, outputPath string) error {
	content, err := LoadAndGenerateRegistry(importsDir)
	if err != nil {
		return err
	}

	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write registry file: %w", err)
	}

	return nil
}
