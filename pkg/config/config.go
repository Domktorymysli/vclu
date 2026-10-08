package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	HardwareType    = 0x12
	HardwareVersion = 0x01
	FirmwareType    = 0x03
	FirmwareVersion = 0x000003e8

	// DefaultMQTTInstance is the instance ID for the main MQTT client used by expose/HA integration.
	DefaultMQTTInstance = "MQTT"
)

// VCLUConfig represents the master configuration stored in .vclu.json
// This is the source of truth for all user-configurable settings
type VCLUConfig struct {
	// Metadata
	SetupCompleted bool      `json:"setupCompleted"`
	CreatedAt      time.Time `json:"createdAt"`
	LastModified   time.Time `json:"lastModified"`

	// Device configuration (user can modify)
	Device *VCLUDeviceConfig `json:"device,omitempty"`

	// Encryption keys (user can provide broadcast keys)
	Keys *VCLUKeysConfig `json:"keys,omitempty"`

	// Web GUI authentication
	Auth *VCLUAuthConfig `json:"auth,omitempty"`

	// MQTT Broker configuration
	MQTTBroker *VCLUMQTTBrokerConfig `json:"mqttBroker,omitempty"`

	// MQTT Client configuration (connects to broker for expose/HA)
	MQTTClient *VCLUMQTTClientConfig `json:"mqttClient,omitempty"`

	// HomeKit integration configuration
	HomeKit *VCLUHomeKitConfig `json:"homeKit,omitempty"`

	// Grenton integration toggle (nil = auto: enabled for CLU/Gate, disabled for standalone)
	GrentonEnabled *bool `json:"grentonEnabled,omitempty"`

	// MCP (Model Context Protocol) server configuration
	MCP *VCLUMCPConfig `json:"mcp,omitempty"`

	// OM files from other CLUs (for registry/discovery)
	OMFiles []OMFileConfig `json:"omFiles,omitempty"`

	// Remote sync configuration (whitelist of objects to subscribe to on real CLUs)
	Sync *VCLUSyncConfig `json:"sync,omitempty"`

	// Plugins configuration
	Plugins *VCLUPluginsConfig `json:"plugins,omitempty"`

	// Prometheus metrics endpoint configuration
	Metrics *VCLUMetricsConfig `json:"metrics,omitempty"`

	// Satel Integra alarm integration (ETHM-1 Plus)
	Satel *VCLUSatelConfig `json:"satel,omitempty"`

	// Backup scheduler configuration
	Backup *VCLUBackupConfig `json:"backup,omitempty"`

	// State change history kept in memory (absent = defaults)
	History *VCLUHistoryConfig `json:"history,omitempty"`

	// Pairing state - set after CLU is added to a system via Object Manager
	Paired      bool   `json:"paired,omitempty"`
	PairedAt    string `json:"pairedAt,omitempty"`
	ProjectName string `json:"projectName,omitempty"`
	ProjectID   string `json:"projectId,omitempty"`
	ProjectKey  string `json:"projectKey,omitempty"`
	ProjectIV   string `json:"projectIv,omitempty"`
}

// VCLUMQTTBrokerConfig contains embedded MQTT broker settings
type VCLUMQTTBrokerConfig struct {
	Enabled  bool   `json:"enabled"`
	Port     int    `json:"port"`               // Default: 1883
	WSPort   int    `json:"wsPort,omitempty"`   // WebSocket port (optional, default: 0 = disabled)
	Username string `json:"username,omitempty"` // Optional authentication
	Password string `json:"password,omitempty"` // Optional authentication
}

// VCLUMQTTClientConfig contains MQTT client connection settings for the default instance.
// This client is used by expose() and Home Assistant integration to connect to a broker.
type VCLUMQTTClientConfig struct {
	Enabled       bool   `json:"enabled"`
	Host          string `json:"host"`               // "localhost", "192.168.1.50"
	Port          int    `json:"port"`               // Default: 1883
	ClientID      string `json:"clientId,omitempty"` // Default: "vclu_internal"
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	HAIntegration bool   `json:"haIntegration"`           // Auto-setup HA discovery + commands
	DefaultAccess string `json:"defaultAccess,omitempty"` // "full"|"readonly"|"hidden"
}

// DefaultMQTTClientConfig returns default MQTT client configuration
func DefaultMQTTClientConfig() *VCLUMQTTClientConfig {
	return &VCLUMQTTClientConfig{
		Enabled:       false,
		Host:          "localhost",
		Port:          1883,
		ClientID:      "vclu_internal",
		HAIntegration: true,
		DefaultAccess: "full",
	}
}

// VCLUHomeKitConfig contains Apple HomeKit integration settings
type VCLUHomeKitConfig struct {
	Enabled         bool   `json:"enabled"`
	Pin             string `json:"pin"`                       // 8-digit PIN code (e.g., "12345678")
	Port            int    `json:"port,omitempty"`            // Default: 5123
	BridgeName      string `json:"bridgeName,omitempty"`      // Display name in Home app
	SerialNumber    string `json:"serialNumber,omitempty"`    // Bridge serial number
	RefreshInterval int    `json:"refreshInterval,omitempty"` // Periodic state refresh interval in seconds (0 = disabled, default: 300)
}

// VCLUMCPConfig contains MCP (Model Context Protocol) server settings
type VCLUMCPConfig struct {
	Enabled        bool     `json:"enabled"`
	DevModeEnabled bool     `json:"devModeEnabled"` // Layer 2: development pipeline (read code, sandbox, promote)
	Bots           []MCPBot `json:"bots,omitempty"`
}

// MCPBot represents a named MCP client with its own API key and access profiles.
type MCPBot struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	APIKeyHash string   `json:"apiKeyHash"`
	Profiles   []string `json:"profiles"`
	DevMode    bool     `json:"devMode"`  // Layer 2 sandbox access (requires global DevModeEnabled too)
	ExecMode   bool     `json:"execMode"` // Allow vclu_exec (live Lua execution, requires DevMode too)
	CreatedAt  string   `json:"createdAt"`
}

// VCLUMetricsConfig contains Prometheus metrics endpoint settings
type VCLUMetricsConfig struct {
	Enabled  bool   `json:"enabled"`            // Whether /metrics endpoint is enabled (default: true)
	Auth     bool   `json:"auth"`               // Whether to require authentication (default: false)
	Username string `json:"username,omitempty"` // Basic auth username (if auth enabled)
	Password string `json:"password,omitempty"` // Basic auth password (if auth enabled)
}

// VCLUSatelConfig contains Satel Integra alarm integration settings.
// The integration connects to an ETHM-1 Plus module over TCP and exposes
// zones/partitions/outputs/temperatures as vCLU objects (satel.zone.N, ...).
type VCLUSatelConfig struct {
	Enabled       bool   `json:"enabled"`
	Host          string `json:"host"`                    // ETHM-1 Plus IP/hostname
	Port          int    `json:"port,omitempty"`          // Default: 7094
	UserCode      string `json:"userCode,omitempty"`      // PIN for control commands
	EncryptionKey string `json:"encryptionKey,omitempty"` // Integration key (empty = no encryption)
	PollInterval  int    `json:"pollInterval,omitempty"`  // State poll in ms (default: 1000)
	TempPoll      int    `json:"tempPoll,omitempty"`      // Temperature poll in ms (default: 30000)
	Zones         string `json:"zones,omitempty"`         // Monitored zones, e.g. "1-16,20" (default: "1-32")
	Partitions    string `json:"partitions,omitempty"`    // Monitored partitions (default: "1-4")
	Outputs       string `json:"outputs,omitempty"`       // Controllable outputs (default: "1-16")
	TempZones     string `json:"tempZones,omitempty"`     // Zones with ATD-100 sensors (default: none)
	ArmMode       int    `json:"armMode,omitempty"`       // Default arm mode 0-3 (default: 0)
	AllowDisarm   bool   `json:"allowDisarm,omitempty"`   // Allow disarm/clear alarm (default: false = blocked)
	AllowForceArm bool   `json:"allowForceArm,omitempty"` // Allow force arm — bypasses violated zones (default: false = blocked)
	ReadNames     *bool  `json:"readNames,omitempty"`     // Read device names from panel (default: true)
}

// DefaultSatelConfig returns default Satel configuration (disabled)
func DefaultSatelConfig() *VCLUSatelConfig {
	return &VCLUSatelConfig{
		Enabled:      false,
		Port:         7094,
		PollInterval: 1000,
		TempPoll:     30000,
		Zones:        "1-32",
		Partitions:   "1-4",
		Outputs:      "1-16",
	}
}

// VCLUBackupConfig contains auto-backup scheduler settings
type VCLUBackupConfig struct {
	AutoEnabled bool `json:"autoEnabled"` // Whether auto-backup is enabled (default: false)
	IntervalH   int  `json:"intervalH"`   // Hours between backups (default: 24)
	MaxBackups  int  `json:"maxBackups"`  // Max auto-backups to keep (default: 7, 0=unlimited)
}

// DefaultBackupConfig returns default backup configuration
func DefaultBackupConfig() *VCLUBackupConfig {
	return &VCLUBackupConfig{
		AutoEnabled: false,
		IntervalH:   24,
		MaxBackups:  7,
	}
}

// VCLUSyncConfig contains remote CLU sync/subscription settings
type VCLUSyncConfig struct {
	Enabled    bool     `json:"enabled"`    // Master on/off switch (default: false)
	TTL        int      `json:"ttl"`        // Subscription TTL in seconds (default: 60)
	BatchSize  int      `json:"batchSize"`  // Objects per subscription request (default: 4, min: 2, max: 8)
	BatchDelay int      `json:"batchDelay"` // Milliseconds delay between batches (default: 50, min: 30, max: 100)
	Objects    []string `json:"objects"`    // Whitelist of object paths to sync
}

// DefaultSyncConfig returns default sync configuration (disabled, empty whitelist)
func DefaultSyncConfig() *VCLUSyncConfig {
	return &VCLUSyncConfig{
		Enabled:    false,
		TTL:        60,
		BatchSize:  4,
		BatchDelay: 50,
		Objects:    []string{},
	}
}

// DefaultMetricsConfig returns default metrics configuration
func DefaultMetricsConfig() *VCLUMetricsConfig {
	return &VCLUMetricsConfig{
		Enabled:  true,
		Auth:     false,
		Username: "",
		Password: "",
	}
}

// DefaultHomeKitConfig returns default HomeKit configuration
func DefaultHomeKitConfig() *VCLUHomeKitConfig {
	return &VCLUHomeKitConfig{
		Enabled:         false,
		Pin:             "00102003", // Default HAP PIN
		Port:            5123,
		BridgeName:      "vCLU Bridge",
		SerialNumber:    "VCLU-001",
		RefreshInterval: 300, // Default: refresh every 5 minutes
	}
}

// VCLUPluginsConfig contains plugin management configuration
type VCLUPluginsConfig struct {
	// Directory where plugins are installed
	PluginsDir string `json:"pluginsDir,omitempty"` // Default: ~/.vclu/plugins

	// Plugin repositories (sources for downloading plugins)
	Repositories []PluginRepository `json:"repositories,omitempty"`

	// Installed plugins with their configuration
	Installed []InstalledPlugin `json:"installed,omitempty"`
}

// PluginRepository represents a source of plugins
type PluginRepository struct {
	Name    string `json:"name"`              // Display name
	URL     string `json:"url"`               // Base URL (GitHub repo or custom)
	Enabled bool   `json:"enabled"`           // Whether to fetch from this repo
	Default bool   `json:"default,omitempty"` // Is this the default/official repo
}

// InstalledPlugin represents an installed plugin and its configuration
type InstalledPlugin struct {
	ID          string                 `json:"id"`                    // Plugin ID (e.g., "weather")
	Version     string                 `json:"version"`               // Installed version
	Enabled     bool                   `json:"enabled"`               // Whether plugin is active
	Repository  string                 `json:"repository,omitempty"`  // Which repo it came from
	InstalledAt string                 `json:"installedAt,omitempty"` // ISO timestamp
	Config      map[string]interface{} `json:"config,omitempty"`      // Plugin-specific config (simple key-value)
}

// Example .vclu.json plugins section:
// {
//   "plugins": {
//     "repositories": [
//       {"name": "vCLU Official", "url": "https://github.com/Domktorymysli/vclu-plugins", "enabled": true}
//     ],
//     "installed": [
//       {
//         "id": "weather",
//         "version": "1.0.0",
//         "enabled": true,
//         "config": {
//           "apiKey": "abc123def456",
//           "city": "Warsaw",
//           "interval": 3600
//         }
//       }
//     ]
//   }
// }

// DefaultPluginsConfig returns default plugins configuration
func DefaultPluginsConfig() *VCLUPluginsConfig {
	return &VCLUPluginsConfig{
		PluginsDir: "", // Empty means use default ~/.vclu/plugins
		Repositories: []PluginRepository{
			{
				Name:    "vCLU Official",
				URL:     "https://github.com/Domktorymysli/vclu-plugins",
				Enabled: true,
				Default: true,
			},
		},
		Installed: []InstalledPlugin{},
	}
}

// OMFileConfig contains metadata about an uploaded OM file from another CLU
type OMFileConfig struct {
	Filename    string `json:"filename"`    // om_285279991.lua
	Serial      uint64 `json:"serial"`      // 285279991
	CLUName     string `json:"cluName"`     // DOM20
	CLUIP       string `json:"cluIp"`       // 192.168.0.3
	ObjectCount int    `json:"objectCount"` // Number of objects in the file
	ModuleCount int    `json:"moduleCount"` // Number of modules in the file
	UploadedAt  string `json:"uploadedAt"`  // ISO timestamp
}

// VCLUAuthConfig contains web GUI authentication settings
type VCLUAuthConfig struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`         // bcrypt hash (or legacy SHA-1 for backwards compat)
	APIKeyHash   string `json:"apiKeyHash,omitempty"` // SHA-256 hash of API key
}

// VCLUDeviceConfig contains user-configurable device settings
type VCLUDeviceConfig struct {
	// Basic device info
	Name               string `json:"name,omitempty"`
	SerialNumber       uint64 `json:"serialNumber"`
	HardwareType       uint8  `json:"hardwareType"`
	HardwareVersion    uint8  `json:"hardwareVersion"`
	FirmwareType       uint8  `json:"firmwareType"`
	FirmwareVersion    uint32 `json:"firmwareVersion"`
	FirmwareVersionStr string `json:"firmwareVersionStr,omitempty"` // Human readable version for CLU:get(17)

	// Device type (locked after setup)
	DeviceTypeID string         `json:"deviceTypeId,omitempty"` // "gate" or "clu"
	Category     DeviceCategory `json:"category,omitempty"`     // Derived from device type

	// Modules and I/O objects (only for CLU category)
	Modules   []ModuleInstance `json:"modules,omitempty"`   // Hardware modules
	IOObjects []IOInstance     `json:"ioObjects,omitempty"` // I/O objects on modules
}

// IsDeviceTypeLocked checks if device type can be changed (false = can change, true = locked)
func (d *VCLUDeviceConfig) IsDeviceTypeLocked() bool {
	return d != nil && d.DeviceTypeID != ""
}

// SupportsModules checks if this device supports I/O modules
func (d *VCLUDeviceConfig) SupportsModules() bool {
	return d != nil && d.Category == DeviceCategoryCLU
}

// IsStandalone checks if this device is in standalone mode (no Grenton integration)
func (d *VCLUDeviceConfig) IsStandalone() bool {
	return d != nil && d.Category == DeviceCategoryStandalone
}

// IsGrentonEnabled returns whether Grenton integration should be active.
// If explicitly set, uses that value. Otherwise: enabled for CLU/Gate, disabled for standalone.
func (c *VCLUConfig) IsGrentonEnabled() bool {
	if c.GrentonEnabled != nil {
		return *c.GrentonEnabled
	}
	if c.Device != nil {
		return !c.Device.IsStandalone()
	}
	return false
}

// VCLUKeysConfig contains encryption key settings
type VCLUKeysConfig struct {
	Key              string `json:"key"`          // Base64 encoded
	IV               string `json:"iv"`           // Base64 encoded
	DefaultIV        string `json:"defaultIV"`    // Base64 encoded
	PIN              string `json:"pin"`          // Plain text PIN (8 chars)
	BroadcastKey     string `json:"broadcastKey"` // Base64 encoded
	BroadcastIV      string `json:"broadcastIV"`  // Base64 encoded
	BroadcastEnabled bool   `json:"broadcastEnabled"`
}

// CLUKeys represents the encryption keys for the CLU
type CLUKeys struct {
	Key              []byte `json:"key"`
	IV               []byte `json:"iv"`
	DefaultIV        []byte `json:"defaultIV"`
	PIN              []byte `json:"pin"`
	BroadcastKey     []byte `json:"broadcastKey,omitempty"`     // User must provide from their own device
	BroadcastIV      []byte `json:"broadcastIV,omitempty"`      // User must provide from their own device
	BroadcastEnabled bool   `json:"broadcastEnabled,omitempty"` // Whether broadcast keys are configured
}

// CLUDeviceConfig represents the CLU device configuration
type CLUDeviceConfig struct {
	SerialNumber       uint64         `json:"serialNumber"`
	MACAddress         string         `json:"macAddress"`
	HardwareType       uint8          `json:"hardwareType"`
	HardwareVersion    uint8          `json:"hardwareVersion"`
	FirmwareType       uint8          `json:"firmwareType"`
	FirmwareVersion    uint32         `json:"firmwareVersion"`
	FirmwareVersionStr string         `json:"firmwareVersionString"`
	Category           DeviceCategory `json:"category,omitempty"`
	Status             string         `json:"status"`
	TFBusDevices       []string       `json:"tfBusDevices"`
}

// IsStandaloneMode checks if this runtime device config is in standalone mode
func (d *CLUDeviceConfig) IsStandaloneMode() bool {
	return d != nil && d.Category == DeviceCategoryStandalone
}

// ReadOrGenerateKeys reads keys from file or generates new ones
func ReadOrGenerateKeys(cluDir string) (*CLUKeys, error) {
	keysPath := filepath.Join(cluDir, "keys.json")

	// Try to read existing keys
	if _, err := os.Stat(keysPath); err == nil {
		data, err := os.ReadFile(keysPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read keys file: %w", err)
		}

		// Decode base64 encoded keys
		type encodedKeys struct {
			Key              string `json:"key"`
			IV               string `json:"iv"`
			DefaultIV        string `json:"defaultIV"`
			PIN              string `json:"pin"`
			BroadcastKey     string `json:"broadcastKey,omitempty"`
			BroadcastIV      string `json:"broadcastIV,omitempty"`
			BroadcastEnabled bool   `json:"broadcastEnabled,omitempty"`
		}

		var encoded encodedKeys
		if err := json.Unmarshal(data, &encoded); err != nil {
			return nil, fmt.Errorf("failed to parse keys file: %w", err)
		}

		// Decode base64
		key, _ := base64.StdEncoding.DecodeString(encoded.Key)
		iv, _ := base64.StdEncoding.DecodeString(encoded.IV)
		defaultIV, _ := base64.StdEncoding.DecodeString(encoded.DefaultIV)
		pin, _ := base64.StdEncoding.DecodeString(encoded.PIN)
		broadcastKey, _ := base64.StdEncoding.DecodeString(encoded.BroadcastKey)
		broadcastIV, _ := base64.StdEncoding.DecodeString(encoded.BroadcastIV)

		keys := &CLUKeys{
			Key:              key,
			IV:               iv,
			DefaultIV:        defaultIV,
			PIN:              pin,
			BroadcastKey:     broadcastKey,
			BroadcastIV:      broadcastIV,
			BroadcastEnabled: encoded.BroadcastEnabled,
		}

		return keys, nil
	}

	// Generate new keys
	key := make([]byte, 16)
	iv := make([]byte, 16)
	pin := make([]byte, 8)

	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	if _, err := rand.Read(iv); err != nil {
		return nil, fmt.Errorf("failed to generate IV: %w", err)
	}

	// Generate random hex PIN (default: "00000000")
	copy(pin, "00000000")

	keys := &CLUKeys{
		Key:       key,
		IV:        iv,
		DefaultIV: iv,
		PIN:       pin,
	}

	// Write keys to file
	if err := WriteKeys(cluDir, keys); err != nil {
		return nil, err
	}

	return keys, nil
}

// WriteKeys writes keys to file
func WriteKeys(cluDir string, keys *CLUKeys) error {
	keysPath := filepath.Join(cluDir, "keys.json")

	// Marshal with base64 encoding
	type encodedKeys struct {
		Key              string `json:"key"`
		IV               string `json:"iv"`
		DefaultIV        string `json:"defaultIV"`
		PIN              string `json:"pin"`
		BroadcastKey     string `json:"broadcastKey,omitempty"`
		BroadcastIV      string `json:"broadcastIV,omitempty"`
		BroadcastEnabled bool   `json:"broadcastEnabled,omitempty"`
	}

	encoded := encodedKeys{
		Key:              base64.StdEncoding.EncodeToString(keys.Key),
		IV:               base64.StdEncoding.EncodeToString(keys.IV),
		DefaultIV:        base64.StdEncoding.EncodeToString(keys.DefaultIV),
		PIN:              base64.StdEncoding.EncodeToString(keys.PIN),
		BroadcastEnabled: keys.BroadcastEnabled,
	}

	if len(keys.BroadcastKey) > 0 {
		encoded.BroadcastKey = base64.StdEncoding.EncodeToString(keys.BroadcastKey)
	}
	if len(keys.BroadcastIV) > 0 {
		encoded.BroadcastIV = base64.StdEncoding.EncodeToString(keys.BroadcastIV)
	}

	data, err := json.MarshalIndent(encoded, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal keys: %w", err)
	}

	if err := os.WriteFile(keysPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write keys file: %w", err)
	}

	return nil
}

// ReadOrGenerateDeviceConfig reads device config or generates a new one
func ReadOrGenerateDeviceConfig(aDriveDir string, macAddr net.HardwareAddr) (*CLUDeviceConfig, error) {
	configPath := filepath.Join(aDriveDir, "config.json")

	// Try to read existing config
	if _, err := os.Stat(configPath); err == nil {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}

		var config CLUDeviceConfig
		if err := json.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}

		// Update MAC address
		config.MACAddress = macAddr.String()
		return &config, nil
	}

	// Generate new config
	serialNum := make([]byte, 8)
	if _, err := rand.Read(serialNum); err != nil {
		return nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	var serial uint64
	for _, b := range serialNum {
		serial = (serial << 8) | uint64(b)
	}

	config := &CLUDeviceConfig{
		SerialNumber:       serial,
		MACAddress:         macAddr.String(),
		HardwareType:       HardwareType,
		HardwareVersion:    HardwareVersion,
		FirmwareType:       FirmwareType,
		FirmwareVersion:    FirmwareVersion,
		FirmwareVersionStr: fmt.Sprintf("%08x-0000", FirmwareVersion),
		Status:             "OK",
		TFBusDevices:       []string{},
	}

	// Write config to file
	if err := WriteDeviceConfig(aDriveDir, config); err != nil {
		return nil, err
	}

	return config, nil
}

// WriteDeviceConfig writes device config to file
func WriteDeviceConfig(aDriveDir string, config *CLUDeviceConfig) error {
	configPath := filepath.Join(aDriveDir, "config.json")

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	// Also write config.txt for compatibility
	configTxtPath := filepath.Join(aDriveDir, "config.txt")
	configTxt := fmt.Sprintf("00000000\n%016x\n%s\n%02x\n%08x\n%02x\n%02x\n",
		config.SerialNumber,
		config.MACAddress,
		config.FirmwareType,
		config.FirmwareVersion,
		config.HardwareType,
		config.HardwareVersion)

	if err := os.WriteFile(configTxtPath, []byte(configTxt), 0644); err != nil {
		return fmt.Errorf("failed to write config.txt: %w", err)
	}

	return nil
}

// OMConfigJSON represents the CONFIG.JSON format that Object Manager expects
type OMConfigJSON struct {
	SN           uint64   `json:"sn"`
	MAC          string   `json:"mac"`
	HWType       uint8    `json:"hwType"`
	HWVer        uint8    `json:"hwVer"`
	FWType       uint8    `json:"fwType"`
	FWApiVer     uint32   `json:"fwApiVer"`
	FWVer        string   `json:"fwVer"`
	TFBusDevices []string `json:"tfbusDevices"`
	ZWaveDevices []string `json:"zwaveDevices"`
}

// writeConfigJSON writes CONFIG.JSON to a specific path
func writeConfigJSON(configPath string, config *CLUDeviceConfig) error {

	// Convert firmware version to string format (e.g., "1.0.1" from 0x00010001)
	major := (config.FirmwareVersion >> 16) & 0xFF
	minor := (config.FirmwareVersion >> 8) & 0xFF
	patch := config.FirmwareVersion & 0xFF
	fwVerStr := fmt.Sprintf("%d.%d.%d", major, minor, patch)

	// Calculate fwApiVer (typically derived from firmware version)
	// Format: first 3 hex digits as decimal (e.g., 0x00aa55aa -> 0x0aa = 170 -> 1000 for compatibility)
	fwApiVer := uint32(1000) // Default API version

	tfbusDevices := config.TFBusDevices
	if tfbusDevices == nil {
		tfbusDevices = []string{}
	}

	omConfig := OMConfigJSON{
		SN:           config.SerialNumber,
		MAC:          config.MACAddress,
		HWType:       config.HardwareType,
		HWVer:        config.HardwareVersion,
		FWType:       config.FirmwareType,
		FWApiVer:     fwApiVer,
		FWVer:        fwVerStr,
		TFBusDevices: tfbusDevices,
		ZWaveDevices: []string{},
	}

	data, err := json.Marshal(omConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal config.json: %w", err)
	}

	// Add newline at end
	data = append(data, '\n')

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write config.json: %w", err)
	}

	return nil
}

// ReadMasterConfig reads the master configuration from .vclu.json
func ReadMasterConfig(cluDir string) (*VCLUConfig, error) {
	configPath := filepath.Join(cluDir, ".vclu.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // File doesn't exist yet
		}
		return nil, fmt.Errorf("failed to read .vclu.json: %w", err)
	}

	var config VCLUConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse .vclu.json: %w", err)
	}

	return &config, nil
}

// masterConfigMu serializes read-modify-write of .vclu.json (UpdateMasterConfig).
var masterConfigMu sync.Mutex

// UpdateMasterConfig reads .vclu.json, lets fn change it and writes it back
// under a lock, so concurrent edits of different sections do not overwrite
// each other. fn returning an error leaves the file unchanged.
func UpdateMasterConfig(cluDir string, fn func(*VCLUConfig) error) error {
	masterConfigMu.Lock()
	defer masterConfigMu.Unlock()
	cfg, err := ReadMasterConfig(cluDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &VCLUConfig{}
	}
	if err := fn(cfg); err != nil {
		return err
	}
	return WriteMasterConfig(cluDir, cfg)
}

// WriteMasterConfig writes the master configuration to .vclu.json
func WriteMasterConfig(cluDir string, config *VCLUConfig) error {
	configPath := filepath.Join(cluDir, ".vclu.json")

	config.LastModified = time.Now()

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal .vclu.json: %w", err)
	}

	// Temp file + rename: a crash or power cut mid-write leaves the old file,
	// never a truncated one. 0600 — it holds secrets (auth hashes, MQTT
	// credentials, Satel PIN); the temp file is created with that mode.
	tmp, err := os.CreateTemp(cluDir, ".vclu.json.tmp-*")
	if err != nil {
		return fmt.Errorf("failed to write .vclu.json: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write .vclu.json: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write .vclu.json: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write .vclu.json: %w", err)
	}
	if err := os.Rename(tmp.Name(), configPath); err != nil {
		return fmt.Errorf("failed to write .vclu.json: %w", err)
	}
	return nil
}

// LoadAndPropagateConfig loads config from .vclu.json and propagates to all other config files
// This is the main entry point for loading configuration
// Returns deviceConfig, keys, and error
func LoadAndPropagateConfig(cluDir string, localIP net.IP, macAddr net.HardwareAddr) (*CLUDeviceConfig, *CLUKeys, error) {
	// Read master config
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read master config: %w", err)
	}

	// Convert master config to runtime structures
	deviceConfig := masterConfigToDeviceConfig(masterConfig, macAddr)
	keys := masterConfigToKeys(masterConfig)

	// Propagate to all config files
	if err := PropagateConfig(cluDir, localIP, deviceConfig, keys); err != nil {
		return nil, nil, fmt.Errorf("failed to propagate config: %w", err)
	}

	return deviceConfig, keys, nil
}

// generateSerial generates a serial number with device type prefix
// Format: PREFIX + 5 random digits (e.g., 5000XXXXX for Gate)
func generateSerial(prefix uint32) uint64 {
	// Generate 5 random digits (00000-99999)
	randomPart := make([]byte, 3)
	if _, err := rand.Read(randomPart); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	random5 := uint64(randomPart[0])<<16 | uint64(randomPart[1])<<8 | uint64(randomPart[2])
	random5 = random5 % 100000 // 0-99999

	// Combine: prefix * 100000 + random5
	return uint64(prefix)*100000 + random5
}

// masterConfigToDeviceConfig converts VCLUConfig to CLUDeviceConfig
func masterConfigToDeviceConfig(masterConfig *VCLUConfig, macAddr net.HardwareAddr) *CLUDeviceConfig {
	defaultFwVerStr := "1.0.0-vclu"

	if masterConfig == nil || masterConfig.Device == nil {
		// Generate defaults (32-bit serial like real Grenton CLU)
		serialNum := make([]byte, 4)
		if _, err := rand.Read(serialNum); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		var serial uint64
		for _, b := range serialNum {
			serial = (serial << 8) | uint64(b)
		}
		return &CLUDeviceConfig{
			SerialNumber:       serial,
			MACAddress:         macAddr.String(),
			HardwareType:       HardwareType,
			HardwareVersion:    HardwareVersion,
			FirmwareType:       FirmwareType,
			FirmwareVersion:    FirmwareVersion,
			FirmwareVersionStr: defaultFwVerStr,
			Status:             "OK",
			TFBusDevices:       []string{},
		}
	}

	fwVerStr := masterConfig.Device.FirmwareVersionStr
	if fwVerStr == "" {
		fwVerStr = defaultFwVerStr
	}

	// Build TFBusDevices from modules (format: "serialHex:hwTypeHex:02:01")
	tfbusDevices := make([]string, 0, len(masterConfig.Device.Modules))
	for _, m := range masterConfig.Device.Modules {
		serialNum := parseSerialToUint32(m.Serial)
		entry := fmt.Sprintf("%08x:%02x:02:01", serialNum, m.HardwareType)
		tfbusDevices = append(tfbusDevices, entry)
	}

	return &CLUDeviceConfig{
		SerialNumber:       masterConfig.Device.SerialNumber,
		MACAddress:         macAddr.String(),
		HardwareType:       masterConfig.Device.HardwareType,
		HardwareVersion:    masterConfig.Device.HardwareVersion,
		FirmwareType:       masterConfig.Device.FirmwareType,
		FirmwareVersion:    masterConfig.Device.FirmwareVersion,
		FirmwareVersionStr: fwVerStr,
		Category:           masterConfig.Device.Category,
		Status:             "OK",
		TFBusDevices:       tfbusDevices,
	}
}

// masterConfigToKeys converts VCLUConfig to CLUKeys
func masterConfigToKeys(masterConfig *VCLUConfig) *CLUKeys {
	if masterConfig == nil || masterConfig.Keys == nil {
		// Generate new keys
		key := make([]byte, 16)
		iv := make([]byte, 16)
		if _, err := rand.Read(key); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		if _, err := rand.Read(iv); err != nil {
			panic("crypto/rand failed: " + err.Error())
		}
		return &CLUKeys{
			Key:       key,
			IV:        iv,
			DefaultIV: iv,
			PIN:       []byte("00000000"),
		}
	}

	key, _ := base64.StdEncoding.DecodeString(masterConfig.Keys.Key)
	iv, _ := base64.StdEncoding.DecodeString(masterConfig.Keys.IV)
	defaultIV, _ := base64.StdEncoding.DecodeString(masterConfig.Keys.DefaultIV)
	broadcastKey, _ := base64.StdEncoding.DecodeString(masterConfig.Keys.BroadcastKey)
	broadcastIV, _ := base64.StdEncoding.DecodeString(masterConfig.Keys.BroadcastIV)

	// Handle PIN - can be plain text or base64
	var pin []byte
	if masterConfig.Keys.PIN != "" {
		// Try to use as plain text first (8 chars)
		if len(masterConfig.Keys.PIN) == 8 {
			pin = []byte(masterConfig.Keys.PIN)
		} else {
			// Try base64 decode
			decoded, err := base64.StdEncoding.DecodeString(masterConfig.Keys.PIN)
			if err == nil {
				pin = decoded
			} else {
				pin = []byte("00000000")
			}
		}
	} else {
		pin = []byte("00000000")
	}

	return &CLUKeys{
		Key:              key,
		IV:               iv,
		DefaultIV:        defaultIV,
		PIN:              pin,
		BroadcastKey:     broadcastKey,
		BroadcastIV:      broadcastIV,
		BroadcastEnabled: masterConfig.Keys.BroadcastEnabled,
	}
}

// SaveToMasterConfig converts runtime config to master config and saves to .vclu.json
// This should be called whenever device config or keys are modified
func SaveToMasterConfig(cluDir string, deviceConfig *CLUDeviceConfig, keys *CLUKeys) error {
	// Read existing config to preserve wizard/pairing/auth data
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		masterConfig = &VCLUConfig{
			CreatedAt: time.Now(),
		}
	}

	masterConfig.SetupCompleted = true
	masterConfig.LastModified = time.Now()

	// Update device fields (preserve DeviceTypeID, Category, Modules, IOObjects)
	if masterConfig.Device == nil {
		masterConfig.Device = &VCLUDeviceConfig{}
	}
	masterConfig.Device.SerialNumber = deviceConfig.SerialNumber
	masterConfig.Device.HardwareType = deviceConfig.HardwareType
	masterConfig.Device.HardwareVersion = deviceConfig.HardwareVersion
	masterConfig.Device.FirmwareType = deviceConfig.FirmwareType
	masterConfig.Device.FirmwareVersion = deviceConfig.FirmwareVersion
	masterConfig.Device.FirmwareVersionStr = deviceConfig.FirmwareVersionStr

	// Update keys
	masterConfig.Keys = &VCLUKeysConfig{
		Key:              base64.StdEncoding.EncodeToString(keys.Key),
		IV:               base64.StdEncoding.EncodeToString(keys.IV),
		DefaultIV:        base64.StdEncoding.EncodeToString(keys.DefaultIV),
		PIN:              string(keys.PIN),
		BroadcastEnabled: keys.BroadcastEnabled,
	}

	if len(keys.BroadcastKey) > 0 {
		masterConfig.Keys.BroadcastKey = base64.StdEncoding.EncodeToString(keys.BroadcastKey)
	}
	if len(keys.BroadcastIV) > 0 {
		masterConfig.Keys.BroadcastIV = base64.StdEncoding.EncodeToString(keys.BroadcastIV)
	}

	return WriteMasterConfig(cluDir, masterConfig)
}

// PropagateConfig generates derived config files in a/ directory from master config
// Source of truth is .vclu.json - this function only generates files for TFTP/Object Manager
func PropagateConfig(cluDir string, localIP net.IP, deviceConfig *CLUDeviceConfig, keys *CLUKeys) error {
	// Ensure a/ directory exists
	aDriveDir := filepath.Join(cluDir, "a")
	if err := os.MkdirAll(aDriveDir, 0755); err != nil {
		return fmt.Errorf("failed to create a/ directory: %w", err)
	}

	// Create template variables with actual IP and PIN
	pin := string(keys.PIN)
	templateVars := NewTemplateVars(deviceConfig, localIP, nil, pin)

	// CONFIG.JSON + config.txt only needed for Grenton (TFTP/Object Manager)
	if deviceConfig.Category != DeviceCategoryStandalone {
		// Generate a/CONFIG.JSON - for Object Manager (TFTP) - uppercase DOS format
		if err := writeConfigJSON(filepath.Join(aDriveDir, "CONFIG.JSON"), deviceConfig); err != nil {
			return fmt.Errorf("failed to write a/CONFIG.JSON: %w", err)
		}

		// Generate a/config.txt - for TFTP
		if err := renderTemplateToFile("config.txt", filepath.Join(aDriveDir, "config.txt"), templateVars); err != nil {
			return fmt.Errorf("failed to write a/config.txt: %w", err)
		}

		// Append TFBus module entries to config.txt (format: serialHex:hwTypeHex:02:01)
		if len(deviceConfig.TFBusDevices) > 0 {
			configTxtPath := filepath.Join(aDriveDir, "config.txt")
			f, err := os.OpenFile(configTxtPath, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return fmt.Errorf("failed to open config.txt for appending modules: %w", err)
			}
			for _, entry := range deviceConfig.TFBusDevices {
				fmt.Fprintf(f, "%s\n", entry)
			}
			f.Close()
		}
	}

	// 5. Generate Lua files only if they don't exist (preserve user modifications)
	// Lowercase for Linux compatibility
	if err := renderTemplateToFileIfNotExists("main.lua", filepath.Join(aDriveDir, "main.lua"), templateVars); err != nil {
		return fmt.Errorf("failed to create main.lua: %w", err)
	}

	if err := renderTemplateToFileIfNotExists("om.lua", filepath.Join(aDriveDir, "om.lua"), templateVars); err != nil {
		return fmt.Errorf("failed to create om.lua: %w", err)
	}

	if err := renderTemplateToFileIfNotExists("user.lua", filepath.Join(aDriveDir, "user.lua"), templateVars); err != nil {
		return fmt.Errorf("failed to create user.lua: %w", err)
	}

	// 6. Update main.lua checkAlive with current serial (if file exists)
	if err := UpdateMainLuaCheckAlive(aDriveDir, deviceConfig.SerialNumber); err != nil {
		// Non-fatal - main.lua might have been just created
		fmt.Printf("Warning: could not update main.lua checkAlive: %v\n", err)
	}

	return nil
}

// renderTemplateToFile renders a template and writes it to file (always overwrites)
func renderTemplateToFile(templateName, destPath string, vars *TemplateVars) error {
	content, err := RenderTemplate(templateName, vars)
	if err != nil {
		return err
	}
	return os.WriteFile(destPath, []byte(content), 0644)
}

// renderTemplateToFileIfNotExists renders a template only if destination doesn't exist
// Also checks for lowercase version (e.g., don't create MAIN.LUA if main.lua exists)
func renderTemplateToFileIfNotExists(templateName, destPath string, vars *TemplateVars) error {
	// Check if uppercase version exists
	if _, err := os.Stat(destPath); err == nil {
		// File exists, don't overwrite
		return nil
	}

	// Check if lowercase version exists (Linux compatibility)
	dir := filepath.Dir(destPath)
	base := filepath.Base(destPath)
	lowerPath := filepath.Join(dir, strings.ToLower(base))
	if lowerPath != destPath {
		if _, err := os.Stat(lowerPath); err == nil {
			// Lowercase version exists, don't create uppercase
			return nil
		}
	}

	return renderTemplateToFile(templateName, destPath, vars)
}

// UpdateMainLuaCheckAlive updates the checkAlive function in main.lua with the serial number
// The checkAlive function should return the last 8 hex characters of the serial number
func UpdateMainLuaCheckAlive(aDriveDir string, serialNumber uint64) error {
	// Try lowercase first (Linux standard), then uppercase (legacy)
	mainLuaPath := filepath.Join(aDriveDir, "main.lua")
	data, err := os.ReadFile(mainLuaPath)
	if err != nil {
		// Try uppercase
		mainLuaPath = filepath.Join(aDriveDir, "MAIN.LUA")
		data, err = os.ReadFile(mainLuaPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // File doesn't exist, nothing to update
			}
			return fmt.Errorf("failed to read main.lua: %w", err)
		}
	}

	content := string(data)

	// Calculate checkAlive value (last 8 hex chars of serial)
	serialHex := fmt.Sprintf("%016x", serialNumber)
	checkAliveValue := serialHex[len(serialHex)-8:]

	// Find and replace checkAlive function
	// Look for pattern: function checkAlive() ... return "XXXXXXXX" ... end
	// We need to handle multiple possible formats

	// Pattern 1: Single line return
	// function checkAlive() return "e643eac3" end
	// Pattern 2: Multi-line with comment
	// function checkAlive()
	//     -- Return a hash/identifier like real CLU does
	//     -- Format: 8 hex characters
	//     return "e643eac3"
	// end

	// Find the checkAlive function and update the return value
	lines := strings.Split(content, "\n")
	var newLines []string
	inCheckAlive := false
	updated := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "function checkAlive()") {
			inCheckAlive = true
			newLines = append(newLines, line)
			continue
		}

		if inCheckAlive {
			// Check if this line has a return statement with a string
			if strings.HasPrefix(trimmed, "return \"") && strings.HasSuffix(trimmed, "\"") {
				// Replace the return value
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				newLines = append(newLines, fmt.Sprintf("%sreturn \"%s\"", indent, checkAliveValue))
				updated = true
				inCheckAlive = false
				continue
			}
			// Check for end of function
			if trimmed == "end" {
				inCheckAlive = false
			}
		}

		newLines = append(newLines, line)
	}

	if !updated {
		// Function not found or couldn't update, leave file unchanged
		return nil
	}

	// Write updated content
	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(mainLuaPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to write main.lua: %w", err)
	}

	return nil
}

// GetMQTTBrokerConfig returns MQTT broker configuration from master config
func GetMQTTBrokerConfig(cluDir string) *VCLUMQTTBrokerConfig {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		return nil
	}
	return masterConfig.MQTTBroker
}

// SaveMQTTBrokerConfig saves MQTT broker configuration to master config
func SaveMQTTBrokerConfig(cluDir string, brokerConfig *VCLUMQTTBrokerConfig) error {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return fmt.Errorf("failed to read master config: %w", err)
	}
	if masterConfig == nil {
		masterConfig = &VCLUConfig{}
	}

	masterConfig.MQTTBroker = brokerConfig
	return WriteMasterConfig(cluDir, masterConfig)
}

// GetMQTTClientConfig returns MQTT client configuration from master config
func GetMQTTClientConfig(cluDir string) *VCLUMQTTClientConfig {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		return nil
	}
	return masterConfig.MQTTClient
}

// SaveMQTTClientConfig saves MQTT client configuration to master config
func SaveMQTTClientConfig(cluDir string, clientConfig *VCLUMQTTClientConfig) error {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return fmt.Errorf("failed to read master config: %w", err)
	}
	if masterConfig == nil {
		masterConfig = &VCLUConfig{}
	}

	masterConfig.MQTTClient = clientConfig
	return WriteMasterConfig(cluDir, masterConfig)
}

// GetMetricsConfig returns Prometheus metrics configuration from master config
func GetMetricsConfig(cluDir string) *VCLUMetricsConfig {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil || masterConfig.Metrics == nil {
		return DefaultMetricsConfig()
	}
	return masterConfig.Metrics
}

// GetSatelConfig returns Satel integration configuration from master config
// (nil when not configured).
func GetSatelConfig(cluDir string) *VCLUSatelConfig {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		return nil
	}
	return masterConfig.Satel
}

// GetOMFiles returns list of OM files from master config
func GetOMFiles(cluDir string) []OMFileConfig {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		return nil
	}
	return masterConfig.OMFiles
}

// AddOMFile adds an OM file config to master config
func AddOMFile(cluDir string, omFile OMFileConfig) error {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return fmt.Errorf("failed to read master config: %w", err)
	}
	if masterConfig == nil {
		masterConfig = &VCLUConfig{}
	}

	// Check if file with same serial already exists, update it
	found := false
	for i, existing := range masterConfig.OMFiles {
		if existing.Serial == omFile.Serial {
			masterConfig.OMFiles[i] = omFile
			found = true
			break
		}
	}

	if !found {
		masterConfig.OMFiles = append(masterConfig.OMFiles, omFile)
	}

	return WriteMasterConfig(cluDir, masterConfig)
}

// DeleteOMFile removes an OM file config from master config
func DeleteOMFile(cluDir string, serial uint64) error {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return fmt.Errorf("failed to read master config: %w", err)
	}
	if masterConfig == nil {
		return nil // Nothing to delete
	}

	// Filter out the file with matching serial
	var newFiles []OMFileConfig
	for _, f := range masterConfig.OMFiles {
		if f.Serial != serial {
			newFiles = append(newFiles, f)
		}
	}

	masterConfig.OMFiles = newFiles
	return WriteMasterConfig(cluDir, masterConfig)
}

// DeleteOMFileAndCleanSync deletes an OM file entry and removes related sync objects.
// Sync object paths start with "CLU<serial>." so we filter those out.
func DeleteOMFileAndCleanSync(cluDir string, serial uint64) error {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return fmt.Errorf("failed to read master config: %w", err)
	}
	if masterConfig == nil {
		return nil
	}

	// Filter out the OM file
	var newFiles []OMFileConfig
	for _, f := range masterConfig.OMFiles {
		if f.Serial != serial {
			newFiles = append(newFiles, f)
		}
	}
	masterConfig.OMFiles = newFiles

	// Clean sync objects belonging to this CLU
	if masterConfig.Sync != nil && len(masterConfig.Sync.Objects) > 0 {
		prefix := fmt.Sprintf("CLU%d.", serial)
		var cleaned []string
		for _, path := range masterConfig.Sync.Objects {
			if !strings.HasPrefix(path, prefix) {
				cleaned = append(cleaned, path)
			}
		}
		masterConfig.Sync.Objects = cleaned
	}

	return WriteMasterConfig(cluDir, masterConfig)
}

// GetOMFilesDir returns the directory where OM files are stored
func GetOMFilesDir(cluDir string) string {
	return filepath.Join(cluDir, "imports")
}

// WriteOMLua writes the om.lua content to the a/ directory
func WriteOMLua(cluDir string, content string) error {
	aDriveDir := filepath.Join(cluDir, "a")

	// Ensure a/ directory exists
	if err := os.MkdirAll(aDriveDir, 0755); err != nil {
		return fmt.Errorf("failed to create a/ directory: %w", err)
	}

	// Write om.lua (lowercase for Linux compatibility)
	omLuaPath := filepath.Join(aDriveDir, "om.lua")
	if err := os.WriteFile(omLuaPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write om.lua: %w", err)
	}

	return nil
}

// VCLUHistoryConfig holds the history limits; zero values mean the defaults
// (enabled, 24 h, 10000 per object, 50000 in total, disk on, 90 days,
// 1024 MB, a write every 300 s).
type VCLUHistoryConfig struct {
	Enabled              *bool `json:"enabled,omitempty"`
	MemoryHours          int   `json:"memoryHours,omitempty"`
	MaxEntriesPerObject  int   `json:"maxEntriesPerObject,omitempty"`
	MaxEntriesTotal      int   `json:"maxEntriesTotal,omitempty"`
	DiskEnabled          *bool `json:"diskEnabled,omitempty"`
	RetentionDays        int   `json:"retentionDays,omitempty"`
	MaxDiskMB            int   `json:"maxDiskMB,omitempty"`
	FlushIntervalSeconds int   `json:"flushIntervalSeconds,omitempty"`
}
