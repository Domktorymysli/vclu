package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- VCLUDeviceConfig methods ---

func TestIsDeviceTypeLocked_NilReceiver(t *testing.T) {
	var d *VCLUDeviceConfig
	if d.IsDeviceTypeLocked() {
		t.Error("nil receiver should return false")
	}
}

func TestIsDeviceTypeLocked_EmptyDeviceTypeID(t *testing.T) {
	d := &VCLUDeviceConfig{}
	if d.IsDeviceTypeLocked() {
		t.Error("empty DeviceTypeID should return false")
	}
}

func TestIsDeviceTypeLocked_WithDeviceTypeID(t *testing.T) {
	d := &VCLUDeviceConfig{DeviceTypeID: "gate"}
	if !d.IsDeviceTypeLocked() {
		t.Error("non-empty DeviceTypeID should return true")
	}
}

func TestSupportsModules_NilReceiver(t *testing.T) {
	var d *VCLUDeviceConfig
	if d.SupportsModules() {
		t.Error("nil receiver should return false")
	}
}

func TestSupportsModules_GateCategory(t *testing.T) {
	d := &VCLUDeviceConfig{Category: DeviceCategoryGate}
	if d.SupportsModules() {
		t.Error("GATE category should not support modules")
	}
}

func TestSupportsModules_CLUCategory(t *testing.T) {
	d := &VCLUDeviceConfig{Category: DeviceCategoryCLU}
	if !d.SupportsModules() {
		t.Error("CLU category should support modules")
	}
}

// --- IsStandalone ---

func TestIsStandalone_NilReceiver(t *testing.T) {
	var d *VCLUDeviceConfig
	if d.IsStandalone() {
		t.Error("nil receiver should return false")
	}
}

func TestIsStandalone_StandaloneCategory(t *testing.T) {
	d := &VCLUDeviceConfig{Category: DeviceCategoryStandalone}
	if !d.IsStandalone() {
		t.Error("STANDALONE category should return true")
	}
}

func TestIsStandalone_GateCategory(t *testing.T) {
	d := &VCLUDeviceConfig{Category: DeviceCategoryGate}
	if d.IsStandalone() {
		t.Error("GATE category should return false")
	}
}

func TestIsStandaloneMode_Runtime(t *testing.T) {
	d := &CLUDeviceConfig{Category: DeviceCategoryStandalone}
	if !d.IsStandaloneMode() {
		t.Error("runtime config with STANDALONE category should return true")
	}
}

func TestIsStandaloneMode_NilReceiver(t *testing.T) {
	var d *CLUDeviceConfig
	if d.IsStandaloneMode() {
		t.Error("nil receiver should return false")
	}
}

// --- ReadMasterConfig / WriteMasterConfig ---

func TestReadMasterConfig_NonExistentFile(t *testing.T) {
	cfg, err := ReadMasterConfig(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Error("expected nil config for non-existent file")
	}
}

func TestWriteMasterConfigPermissions(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, ".vclu.json")

	// Fresh file must be created with 0600 (contains secrets).
	if err := WriteMasterConfig(dir, &VCLUConfig{SetupCompleted: true}); err != nil {
		t.Fatalf("WriteMasterConfig failed: %v", err)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("fresh .vclu.json perms = %o, want 0600", perm)
	}

	// Upgrade path: a pre-existing 0644 file (older versions) must be tightened.
	if err := os.Chmod(configPath, 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteMasterConfig(dir, &VCLUConfig{SetupCompleted: true}); err != nil {
		t.Fatalf("WriteMasterConfig failed: %v", err)
	}
	info, err = os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("rewritten .vclu.json perms = %o, want 0600", perm)
	}
}

func TestWriteAndReadMasterConfig(t *testing.T) {
	dir := t.TempDir()

	original := &VCLUConfig{
		SetupCompleted: true,
		Device: &VCLUDeviceConfig{
			DeviceTypeID: "clu",
			SerialNumber: 123456,
			Category:     DeviceCategoryCLU,
		},
		Keys: &VCLUKeysConfig{
			PIN:              "12345678",
			BroadcastEnabled: true,
			BroadcastKey:     "dGVzdGtleXRlc3RrZXkx", // base64
		},
	}

	if err := WriteMasterConfig(dir, original); err != nil {
		t.Fatalf("WriteMasterConfig failed: %v", err)
	}

	loaded, err := ReadMasterConfig(dir)
	if err != nil {
		t.Fatalf("ReadMasterConfig failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded config is nil")
	}

	if !loaded.SetupCompleted {
		t.Error("SetupCompleted should be true")
	}
	if loaded.Device.DeviceTypeID != "clu" {
		t.Errorf("DeviceTypeID = %q, want %q", loaded.Device.DeviceTypeID, "clu")
	}
	if loaded.Device.SerialNumber != 123456 {
		t.Errorf("SerialNumber = %d, want %d", loaded.Device.SerialNumber, 123456)
	}
	if loaded.Keys.PIN != "12345678" {
		t.Errorf("PIN = %q, want %q", loaded.Keys.PIN, "12345678")
	}
	if !loaded.Keys.BroadcastEnabled {
		t.Error("BroadcastEnabled should be true")
	}
}

func TestWriteMasterConfig_SetsLastModified(t *testing.T) {
	dir := t.TempDir()
	cfg := &VCLUConfig{}

	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig failed: %v", err)
	}

	loaded, err := ReadMasterConfig(dir)
	if err != nil {
		t.Fatalf("ReadMasterConfig failed: %v", err)
	}
	if loaded.LastModified.IsZero() {
		t.Error("LastModified should be set")
	}
}

func TestReadMasterConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".vclu.json")
	os.WriteFile(path, []byte("not json"), 0644)

	_, err := ReadMasterConfig(dir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- masterConfigToKeys ---

func TestMasterConfigToKeys_NilConfig(t *testing.T) {
	keys := masterConfigToKeys(nil)
	if keys == nil {
		t.Fatal("keys should not be nil")
	}
	if len(keys.Key) != 16 {
		t.Errorf("Key length = %d, want 16", len(keys.Key))
	}
	if len(keys.IV) != 16 {
		t.Errorf("IV length = %d, want 16", len(keys.IV))
	}
	if string(keys.PIN) != "00000000" {
		t.Errorf("PIN = %q, want %q", string(keys.PIN), "00000000")
	}
}

func TestMasterConfigToKeys_NilKeys(t *testing.T) {
	cfg := &VCLUConfig{Device: &VCLUDeviceConfig{}}
	keys := masterConfigToKeys(cfg)
	if len(keys.Key) != 16 {
		t.Errorf("Key length = %d, want 16", len(keys.Key))
	}
}

func TestMasterConfigToKeys_WithPlainTextPIN(t *testing.T) {
	keyBytes := make([]byte, 16)
	ivBytes := make([]byte, 16)
	copy(keyBytes, "0123456789abcdef")
	copy(ivBytes, "fedcba9876543210")

	cfg := &VCLUConfig{
		Keys: &VCLUKeysConfig{
			Key:       base64.StdEncoding.EncodeToString(keyBytes),
			IV:        base64.StdEncoding.EncodeToString(ivBytes),
			DefaultIV: base64.StdEncoding.EncodeToString(ivBytes),
			PIN:       "ABCDEFGH", // 8 chars = plain text
		},
	}

	keys := masterConfigToKeys(cfg)
	if string(keys.PIN) != "ABCDEFGH" {
		t.Errorf("PIN = %q, want %q", string(keys.PIN), "ABCDEFGH")
	}
}

func TestMasterConfigToKeys_EmptyPIN(t *testing.T) {
	cfg := &VCLUConfig{
		Keys: &VCLUKeysConfig{
			Key: base64.StdEncoding.EncodeToString(make([]byte, 16)),
			IV:  base64.StdEncoding.EncodeToString(make([]byte, 16)),
			PIN: "",
		},
	}

	keys := masterConfigToKeys(cfg)
	if string(keys.PIN) != "00000000" {
		t.Errorf("PIN = %q, want %q", string(keys.PIN), "00000000")
	}
}

func TestMasterConfigToKeys_BroadcastKeys(t *testing.T) {
	bk := make([]byte, 16)
	bi := make([]byte, 16)
	copy(bk, "broadcastkey1234")
	copy(bi, "broadcastiv12345")

	cfg := &VCLUConfig{
		Keys: &VCLUKeysConfig{
			Key:              base64.StdEncoding.EncodeToString(make([]byte, 16)),
			IV:               base64.StdEncoding.EncodeToString(make([]byte, 16)),
			DefaultIV:        base64.StdEncoding.EncodeToString(make([]byte, 16)),
			PIN:              "00000000",
			BroadcastKey:     base64.StdEncoding.EncodeToString(bk),
			BroadcastIV:      base64.StdEncoding.EncodeToString(bi),
			BroadcastEnabled: true,
		},
	}

	keys := masterConfigToKeys(cfg)
	if !keys.BroadcastEnabled {
		t.Error("BroadcastEnabled should be true")
	}
	if string(keys.BroadcastKey) != "broadcastkey1234" {
		t.Errorf("BroadcastKey = %q, want %q", string(keys.BroadcastKey), "broadcastkey1234")
	}
	if string(keys.BroadcastIV) != "broadcastiv12345" {
		t.Errorf("BroadcastIV = %q, want %q", string(keys.BroadcastIV), "broadcastiv12345")
	}
}

// --- masterConfigToDeviceConfig ---

func TestMasterConfigToDeviceConfig_NilConfig(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	dc := masterConfigToDeviceConfig(nil, mac)

	if dc == nil {
		t.Fatal("device config should not be nil")
	}
	if dc.MACAddress != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MACAddress = %q, want %q", dc.MACAddress, "aa:bb:cc:dd:ee:ff")
	}
	if dc.HardwareType != HardwareType {
		t.Errorf("HardwareType = %d, want %d", dc.HardwareType, HardwareType)
	}
	if dc.Status != "OK" {
		t.Errorf("Status = %q, want %q", dc.Status, "OK")
	}
	if dc.FirmwareVersionStr != "1.0.0-vclu" {
		t.Errorf("FirmwareVersionStr = %q, want %q", dc.FirmwareVersionStr, "1.0.0-vclu")
	}
}

func TestMasterConfigToDeviceConfig_WithDevice(t *testing.T) {
	mac, _ := net.ParseMAC("11:22:33:44:55:66")
	cfg := &VCLUConfig{
		Device: &VCLUDeviceConfig{
			SerialNumber:       999999,
			HardwareType:       0x13,
			HardwareVersion:    0x02,
			FirmwareType:       0x05,
			FirmwareVersion:    0x00010002,
			FirmwareVersionStr: "2.0.0-custom",
		},
	}

	dc := masterConfigToDeviceConfig(cfg, mac)
	if dc.SerialNumber != 999999 {
		t.Errorf("SerialNumber = %d, want %d", dc.SerialNumber, 999999)
	}
	if dc.MACAddress != "11:22:33:44:55:66" {
		t.Errorf("MACAddress = %q, want %q", dc.MACAddress, "11:22:33:44:55:66")
	}
	if dc.FirmwareVersionStr != "2.0.0-custom" {
		t.Errorf("FirmwareVersionStr = %q, want %q", dc.FirmwareVersionStr, "2.0.0-custom")
	}
}

func TestMasterConfigToDeviceConfig_EmptyFirmwareVersionStr(t *testing.T) {
	mac, _ := net.ParseMAC("11:22:33:44:55:66")
	cfg := &VCLUConfig{
		Device: &VCLUDeviceConfig{
			FirmwareVersionStr: "",
		},
	}

	dc := masterConfigToDeviceConfig(cfg, mac)
	if dc.FirmwareVersionStr != "1.0.0-vclu" {
		t.Errorf("FirmwareVersionStr = %q, want default %q", dc.FirmwareVersionStr, "1.0.0-vclu")
	}
}

// --- ReadOrGenerateKeys ---

func TestReadOrGenerateKeys_GeneratesNew(t *testing.T) {
	dir := t.TempDir()

	keys, err := ReadOrGenerateKeys(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys.Key) != 16 {
		t.Errorf("Key length = %d, want 16", len(keys.Key))
	}
	if len(keys.IV) != 16 {
		t.Errorf("IV length = %d, want 16", len(keys.IV))
	}
	if string(keys.PIN) != "00000000" {
		t.Errorf("PIN = %q, want %q", string(keys.PIN), "00000000")
	}

	// File should be written
	keysPath := filepath.Join(dir, "keys.json")
	if _, err := os.Stat(keysPath); os.IsNotExist(err) {
		t.Error("keys.json should be created")
	}
}

func TestReadOrGenerateKeys_ReadsExisting(t *testing.T) {
	dir := t.TempDir()

	// Write keys first
	original := &CLUKeys{
		Key:       []byte("1234567890123456"),
		IV:        []byte("abcdefghijklmnop"),
		DefaultIV: []byte("abcdefghijklmnop"),
		PIN:       []byte("99999999"),
	}
	if err := WriteKeys(dir, original); err != nil {
		t.Fatalf("WriteKeys failed: %v", err)
	}

	// Read them back
	keys, err := ReadOrGenerateKeys(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(keys.Key) != "1234567890123456" {
		t.Errorf("Key = %q, want %q", string(keys.Key), "1234567890123456")
	}
	if string(keys.PIN) != "99999999" {
		t.Errorf("PIN = %q, want %q", string(keys.PIN), "99999999")
	}
}

// --- WriteKeys ---

func TestWriteKeys_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	keys := &CLUKeys{
		Key:       make([]byte, 16),
		IV:        make([]byte, 16),
		DefaultIV: make([]byte, 16),
		PIN:       []byte("00000000"),
	}

	if err := WriteKeys(dir, keys); err != nil {
		t.Fatalf("WriteKeys failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "keys.json"))
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("file permissions = %o, want %o", perm, 0600)
	}
}

func TestWriteKeys_BroadcastKeysIncluded(t *testing.T) {
	dir := t.TempDir()
	keys := &CLUKeys{
		Key:              make([]byte, 16),
		IV:               make([]byte, 16),
		DefaultIV:        make([]byte, 16),
		PIN:              []byte("00000000"),
		BroadcastKey:     []byte("broadcastkey1234"),
		BroadcastIV:      []byte("broadcastiv12345"),
		BroadcastEnabled: true,
	}

	if err := WriteKeys(dir, keys); err != nil {
		t.Fatalf("WriteKeys failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "keys.json"))
	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	if _, ok := parsed["broadcastKey"]; !ok {
		t.Error("broadcastKey should be present in JSON")
	}
	if _, ok := parsed["broadcastIV"]; !ok {
		t.Error("broadcastIV should be present in JSON")
	}
}

// --- DefaultMetricsConfig ---

func TestDefaultMetricsConfig(t *testing.T) {
	cfg := DefaultMetricsConfig()
	if !cfg.Enabled {
		t.Error("Enabled should be true by default")
	}
	if cfg.Auth {
		t.Error("Auth should be false by default")
	}
}

// --- DefaultHomeKitConfig ---

func TestDefaultHomeKitConfig(t *testing.T) {
	cfg := DefaultHomeKitConfig()
	if cfg.Enabled {
		t.Error("Enabled should be false by default")
	}
	if cfg.Pin != "00102003" {
		t.Errorf("Pin = %q, want %q", cfg.Pin, "00102003")
	}
	if cfg.Port != 5123 {
		t.Errorf("Port = %d, want %d", cfg.Port, 5123)
	}
	if cfg.RefreshInterval != 300 {
		t.Errorf("RefreshInterval = %d, want %d", cfg.RefreshInterval, 300)
	}
}

// --- generateSerial ---

func TestGenerateSerial_Range(t *testing.T) {
	serial := generateSerial(5200)
	if serial < 520000000 || serial > 520099999 {
		t.Errorf("serial = %d, want range [520000000, 520099999]", serial)
	}
}

func TestGenerateSerial_DifferentPrefix(t *testing.T) {
	serial := generateSerial(2210)
	if serial < 221000000 || serial > 221099999 {
		t.Errorf("serial = %d, want range [221000000, 221099999]", serial)
	}
}

// --- WriteOMLua ---

func TestWriteOMLua(t *testing.T) {
	dir := t.TempDir()
	content := "-- test om.lua\nCLU = {}\n"

	if err := WriteOMLua(dir, content); err != nil {
		t.Fatalf("WriteOMLua failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "om.lua"))
	if err != nil {
		t.Fatalf("reading om.lua failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestWriteOMLua_CreatesADirectory(t *testing.T) {
	dir := t.TempDir()

	if err := WriteOMLua(dir, "test"); err != nil {
		t.Fatalf("WriteOMLua failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "a"))
	if err != nil {
		t.Fatal("a/ directory should be created")
	}
	if !info.IsDir() {
		t.Error("a/ should be a directory")
	}
}

// --- SaveToMasterConfig ---

func TestSaveToMasterConfig(t *testing.T) {
	dir := t.TempDir()

	dc := &CLUDeviceConfig{
		SerialNumber:    42,
		HardwareType:    0x12,
		FirmwareVersion: 0x100,
	}
	keys := &CLUKeys{
		Key:       []byte("0123456789abcdef"),
		IV:        []byte("fedcba9876543210"),
		DefaultIV: []byte("fedcba9876543210"),
		PIN:       []byte("TESTPIN!"),
	}

	if err := SaveToMasterConfig(dir, dc, keys); err != nil {
		t.Fatalf("SaveToMasterConfig failed: %v", err)
	}

	loaded, err := ReadMasterConfig(dir)
	if err != nil {
		t.Fatalf("ReadMasterConfig failed: %v", err)
	}
	if !loaded.SetupCompleted {
		t.Error("SetupCompleted should be true")
	}
	if loaded.Device.SerialNumber != 42 {
		t.Errorf("SerialNumber = %d, want 42", loaded.Device.SerialNumber)
	}
	if loaded.Keys.PIN != "TESTPIN!" {
		t.Errorf("PIN = %q, want %q", loaded.Keys.PIN, "TESTPIN!")
	}
}

// --- GetOMFilesDir ---

func TestGetOMFilesDir(t *testing.T) {
	result := GetOMFilesDir("/some/dir")
	expected := filepath.Join("/some/dir", "imports")
	if result != expected {
		t.Errorf("GetOMFilesDir = %q, want %q", result, expected)
	}
}

func TestDeleteOMFileAndCleanSync(t *testing.T) {
	tmpDir := t.TempDir()

	// Create config with two OM files and sync objects from both CLUs
	cfg := &VCLUConfig{
		SetupCompleted: true,
		OMFiles: []OMFileConfig{
			{Filename: "om_220000592.lua", Serial: 220000592, CLUName: "DOM10"},
			{Filename: "om_212345678.lua", Serial: 212345678, CLUName: "DOM20"},
		},
		Sync: &VCLUSyncConfig{
			Enabled:    true,
			TTL:        60,
			BatchSize:  4,
			BatchDelay: 50,
			Objects: []string{
				"CLU220000592.DOU0276",
				"CLU220000592.DOU0277",
				"CLU220000592.DOU0789",
				"CLU212345678.DOU0319",
				"CLU212345678.DOU0610",
			},
		},
	}
	if err := WriteMasterConfig(tmpDir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	// Delete CLU 220000592
	if err := DeleteOMFileAndCleanSync(tmpDir, 220000592); err != nil {
		t.Fatalf("DeleteOMFileAndCleanSync: %v", err)
	}

	got, err := ReadMasterConfig(tmpDir)
	if err != nil {
		t.Fatalf("ReadMasterConfig: %v", err)
	}

	// Should have 1 OM file left
	if len(got.OMFiles) != 1 {
		t.Errorf("OMFiles count = %d, want 1", len(got.OMFiles))
	}
	if got.OMFiles[0].Serial != 212345678 {
		t.Errorf("remaining OMFile serial = %d, want 212345678", got.OMFiles[0].Serial)
	}

	// Sync objects should only have CLU212345678 entries
	if len(got.Sync.Objects) != 2 {
		t.Errorf("sync objects count = %d, want 2", len(got.Sync.Objects))
	}
	for _, obj := range got.Sync.Objects {
		if !strings.HasPrefix(obj, "CLU212345678.") {
			t.Errorf("unexpected sync object after cleanup: %s", obj)
		}
	}
}

func TestDeleteOMFileAndCleanSync_NoSync(t *testing.T) {
	tmpDir := t.TempDir()

	// Config with OM file but no sync section
	cfg := &VCLUConfig{
		SetupCompleted: true,
		OMFiles: []OMFileConfig{
			{Filename: "om_220000592.lua", Serial: 220000592, CLUName: "DOM10"},
		},
	}
	if err := WriteMasterConfig(tmpDir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	// Should not panic when sync is nil
	if err := DeleteOMFileAndCleanSync(tmpDir, 220000592); err != nil {
		t.Fatalf("DeleteOMFileAndCleanSync: %v", err)
	}

	got, _ := ReadMasterConfig(tmpDir)
	if len(got.OMFiles) != 0 {
		t.Errorf("OMFiles count = %d, want 0", len(got.OMFiles))
	}
}

func TestDeleteOMFileAndCleanSync_AllDeleted(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &VCLUConfig{
		SetupCompleted: true,
		OMFiles: []OMFileConfig{
			{Filename: "om_123.lua", Serial: 123, CLUName: "TEST"},
		},
		Sync: &VCLUSyncConfig{
			Enabled: true,
			Objects: []string{
				"CLU123.DOU001",
				"CLU123.DOU002",
			},
		},
	}
	if err := WriteMasterConfig(tmpDir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	if err := DeleteOMFileAndCleanSync(tmpDir, 123); err != nil {
		t.Fatalf("DeleteOMFileAndCleanSync: %v", err)
	}

	got, _ := ReadMasterConfig(tmpDir)
	if len(got.OMFiles) != 0 {
		t.Errorf("OMFiles count = %d, want 0", len(got.OMFiles))
	}
	if len(got.Sync.Objects) != 0 {
		t.Errorf("sync objects count = %d, want 0", len(got.Sync.Objects))
	}
}

// =============================================================================
// TFBusDevices — modules in config.txt and CONFIG.JSON
// =============================================================================

// Helper: create a VCLUConfig with CLU device and modules for testing
func testCLUConfigWithModules() *VCLUConfig {
	return &VCLUConfig{
		SetupCompleted: true,
		Device: &VCLUDeviceConfig{
			DeviceTypeID:    "clu",
			Category:        DeviceCategoryCLU,
			Name:            "TestCLU",
			SerialNumber:    212345678,
			HardwareType:    0x13,
			HardwareVersion: 0x01,
			FirmwareType:    0x03,
			FirmwareVersion: 0x000001f6,
			Modules: []ModuleInstance{
				{ID: "mod-1", TypeID: "dout8t", Serial: "330000027", Name: "OutputModule", HardwareType: 0x1e},
				{ID: "mod-2", TypeID: "din8", Serial: "181000015", Name: "InputModule", HardwareType: 0x14},
			},
			IOObjects: []IOInstance{
				{ID: "DOU5048", ModuleID: "mod-1", TypeID: "dout", Index: 0, Name: "Light1", LuaType: 4},
				{ID: "DIN9518", ModuleID: "mod-2", TypeID: "din", Index: 0, Name: "Switch1", LuaType: 3},
			},
		},
		Keys: &VCLUKeysConfig{
			Key:       base64.StdEncoding.EncodeToString(make([]byte, 16)),
			IV:        base64.StdEncoding.EncodeToString(make([]byte, 16)),
			DefaultIV: base64.StdEncoding.EncodeToString(make([]byte, 16)),
			PIN:       "00000000",
		},
	}
}

// Helper: create a VCLUConfig with Gate device (no modules)
func testGateConfig() *VCLUConfig {
	return &VCLUConfig{
		SetupCompleted: true,
		Device: &VCLUDeviceConfig{
			DeviceTypeID:    "gate",
			Category:        DeviceCategoryGate,
			Name:            "TestGate",
			SerialNumber:    520000006,
			HardwareType:    0x12,
			HardwareVersion: 0x01,
			FirmwareType:    0x03,
			FirmwareVersion: 0x000003e8,
		},
		Keys: &VCLUKeysConfig{
			Key:       base64.StdEncoding.EncodeToString(make([]byte, 16)),
			IV:        base64.StdEncoding.EncodeToString(make([]byte, 16)),
			DefaultIV: base64.StdEncoding.EncodeToString(make([]byte, 16)),
			PIN:       "00000000",
		},
	}
}

func TestMasterConfigToDeviceConfig_TFBusDevicesFromModules(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	cfg := testCLUConfigWithModules()

	dc := masterConfigToDeviceConfig(cfg, mac)

	if len(dc.TFBusDevices) != 2 {
		t.Fatalf("TFBusDevices length = %d, want 2", len(dc.TFBusDevices))
	}

	// Module 330000027 (serial) = 0x13ab669b, HW type 0x1e
	expected0 := "13ab669b:1e:02:01"
	if dc.TFBusDevices[0] != expected0 {
		t.Errorf("TFBusDevices[0] = %q, want %q", dc.TFBusDevices[0], expected0)
	}

	// Module 181000015 (serial) = 0x0ac9d74f, HW type 0x14
	expected1 := "0ac9d74f:14:02:01"
	if dc.TFBusDevices[1] != expected1 {
		t.Errorf("TFBusDevices[1] = %q, want %q", dc.TFBusDevices[1], expected1)
	}
}

func TestMasterConfigToDeviceConfig_NoModulesEmptyTFBus(t *testing.T) {
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	cfg := testGateConfig()

	dc := masterConfigToDeviceConfig(cfg, mac)

	if len(dc.TFBusDevices) != 0 {
		t.Errorf("TFBusDevices length = %d, want 0 for Gate", len(dc.TFBusDevices))
	}
}

func TestMasterConfigToDeviceConfig_TFBusEntryFormat(t *testing.T) {
	// Verify the exact format matches real Grenton CLU config.txt entries:
	// "serialHex:hwTypeHex:02:01"
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	cfg := &VCLUConfig{
		Device: &VCLUDeviceConfig{
			SerialNumber:       212345678,
			HardwareType:       0x13,
			HardwareVersion:    0x01,
			FirmwareType:       0x03,
			FirmwareVersion:    0x000001f6,
			FirmwareVersionStr: "1.0.0-vclu",
			Modules: []ModuleInstance{
				{Serial: "330000027", HardwareType: 0x1e},
				{Serial: "330000028", HardwareType: 0x1e},
				{Serial: "201000014", HardwareType: 0x15},
				{Serial: "181000015", HardwareType: 0x14},
				{Serial: "181000016", HardwareType: 0x14},
			},
		},
	}

	dc := masterConfigToDeviceConfig(cfg, mac)

	// These must match the real Grenton CLU212345678/config.txt entries exactly
	expectedEntries := []string{
		"13ab669b:1e:02:01",
		"13ab669c:1e:02:01",
		"0bfb044e:15:02:01",
		"0ac9d74f:14:02:01",
		"0ac9d750:14:02:01",
	}

	if len(dc.TFBusDevices) != len(expectedEntries) {
		t.Fatalf("TFBusDevices length = %d, want %d", len(dc.TFBusDevices), len(expectedEntries))
	}
	for i, expected := range expectedEntries {
		if dc.TFBusDevices[i] != expected {
			t.Errorf("TFBusDevices[%d] = %q, want %q", i, dc.TFBusDevices[i], expected)
		}
	}
}

// --- PropagateConfig: config.txt ---

func TestPropagateConfig_ConfigTxtContainsModuleEntries(t *testing.T) {
	dir := t.TempDir()

	cfg := testCLUConfigWithModules()
	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	dc := masterConfigToDeviceConfig(cfg, mac)
	keys := masterConfigToKeys(cfg)
	localIP := net.ParseIP("192.168.1.100")

	if err := PropagateConfig(dir, localIP, dc, keys); err != nil {
		t.Fatalf("PropagateConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "config.txt"))
	if err != nil {
		t.Fatalf("read config.txt: %v", err)
	}
	content := string(data)

	// Must contain module entries
	if !contains(content, "13ab669b:1e:02:01") {
		t.Error("config.txt missing module entry 13ab669b:1e:02:01")
	}
	if !contains(content, "0ac9d74f:14:02:01") {
		t.Error("config.txt missing module entry 0ac9d74f:14:02:01")
	}

	// Verify structure: first 7 lines are base config, then modules
	lines := splitLines(content)
	if len(lines) < 9 {
		t.Fatalf("config.txt has %d lines, want at least 9 (7 base + 2 modules)", len(lines))
	}

	// Line 1: PIN
	if lines[0] != "00000000" {
		t.Errorf("config.txt line 1 (PIN) = %q, want %q", lines[0], "00000000")
	}

	// Line 4: firmware type
	if lines[3] != "00000003" {
		t.Errorf("config.txt line 4 (FwType) = %q, want %q", lines[3], "00000003")
	}

	// Line 5: firmware version
	if lines[4] != "000001f6" {
		t.Errorf("config.txt line 5 (FwVersion) = %q, want %q", lines[4], "000001f6")
	}

	// Line 6: hardware type
	if lines[5] != "00000013" {
		t.Errorf("config.txt line 6 (HwType) = %q, want %q", lines[5], "00000013")
	}

	// Line 7: hardware version
	if lines[6] != "00000001" {
		t.Errorf("config.txt line 7 (HwVersion) = %q, want %q", lines[6], "00000001")
	}

	// Lines 8+: module entries
	if lines[7] != "13ab669b:1e:02:01" {
		t.Errorf("config.txt line 8 (module 1) = %q, want %q", lines[7], "13ab669b:1e:02:01")
	}
	if lines[8] != "0ac9d74f:14:02:01" {
		t.Errorf("config.txt line 9 (module 2) = %q, want %q", lines[8], "0ac9d74f:14:02:01")
	}
}

func TestPropagateConfig_ConfigTxtNoModulesForGate(t *testing.T) {
	dir := t.TempDir()

	cfg := testGateConfig()
	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	dc := masterConfigToDeviceConfig(cfg, mac)
	keys := masterConfigToKeys(cfg)
	localIP := net.ParseIP("192.168.1.100")

	if err := PropagateConfig(dir, localIP, dc, keys); err != nil {
		t.Fatalf("PropagateConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "config.txt"))
	if err != nil {
		t.Fatalf("read config.txt: %v", err)
	}

	lines := splitLines(string(data))
	// Gate: 7 base lines, no module entries
	if len(lines) != 7 {
		t.Errorf("config.txt has %d lines, want exactly 7 for Gate (no modules)", len(lines))
	}

	// Verify Gate-specific values
	if lines[5] != "00000012" {
		t.Errorf("config.txt line 6 (HwType) = %q, want %q (Gate)", lines[5], "00000012")
	}
}

// --- PropagateConfig: CONFIG.JSON ---

func TestPropagateConfig_ConfigJSONContainsTFBusDevices(t *testing.T) {
	dir := t.TempDir()

	cfg := testCLUConfigWithModules()
	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	dc := masterConfigToDeviceConfig(cfg, mac)
	keys := masterConfigToKeys(cfg)
	localIP := net.ParseIP("192.168.1.100")

	if err := PropagateConfig(dir, localIP, dc, keys); err != nil {
		t.Fatalf("PropagateConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "CONFIG.JSON"))
	if err != nil {
		t.Fatalf("read CONFIG.JSON: %v", err)
	}

	var parsed OMConfigJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse CONFIG.JSON: %v", err)
	}

	// Device info
	if parsed.SN != 212345678 {
		t.Errorf("CONFIG.JSON sn = %d, want %d", parsed.SN, 212345678)
	}
	if parsed.HWType != 0x13 {
		t.Errorf("CONFIG.JSON hwType = %d, want %d", parsed.HWType, 0x13)
	}
	if parsed.HWVer != 0x01 {
		t.Errorf("CONFIG.JSON hwVer = %d, want %d", parsed.HWVer, 0x01)
	}
	if parsed.FWType != 0x03 {
		t.Errorf("CONFIG.JSON fwType = %d, want %d", parsed.FWType, 0x03)
	}

	// TFBusDevices must list modules
	if len(parsed.TFBusDevices) != 2 {
		t.Fatalf("CONFIG.JSON tfbusDevices length = %d, want 2", len(parsed.TFBusDevices))
	}
	if parsed.TFBusDevices[0] != "13ab669b:1e:02:01" {
		t.Errorf("CONFIG.JSON tfbusDevices[0] = %q, want %q", parsed.TFBusDevices[0], "13ab669b:1e:02:01")
	}
	if parsed.TFBusDevices[1] != "0ac9d74f:14:02:01" {
		t.Errorf("CONFIG.JSON tfbusDevices[1] = %q, want %q", parsed.TFBusDevices[1], "0ac9d74f:14:02:01")
	}
}

func TestPropagateConfig_ConfigJSONEmptyTFBusForGate(t *testing.T) {
	dir := t.TempDir()

	cfg := testGateConfig()
	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	dc := masterConfigToDeviceConfig(cfg, mac)
	keys := masterConfigToKeys(cfg)
	localIP := net.ParseIP("192.168.1.100")

	if err := PropagateConfig(dir, localIP, dc, keys); err != nil {
		t.Fatalf("PropagateConfig: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "CONFIG.JSON"))
	if err != nil {
		t.Fatalf("read CONFIG.JSON: %v", err)
	}

	var parsed OMConfigJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse CONFIG.JSON: %v", err)
	}

	if parsed.HWType != 0x12 {
		t.Errorf("CONFIG.JSON hwType = %d, want %d (Gate)", parsed.HWType, 0x12)
	}
	if len(parsed.TFBusDevices) != 0 {
		t.Errorf("CONFIG.JSON tfbusDevices should be empty for Gate, got %d entries", len(parsed.TFBusDevices))
	}
}

// --- PropagateConfig: om.lua with modules ---

func TestPropagateConfig_OMLuaBaseObjectOnly(t *testing.T) {
	dir := t.TempDir()

	cfg := testCLUConfigWithModules()
	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	// Generate om.lua via OMGenerator (same as wizard finish)
	localIP := IPToUint32("192.168.1.100")
	generator := NewOMGenerator(cfg.Device, localIP)
	omContent, err := generator.Generate()
	if err != nil {
		t.Fatalf("OMGenerator.Generate: %v", err)
	}

	if err := WriteOMLua(dir, omContent); err != nil {
		t.Fatalf("WriteOMLua: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "a", "om.lua"))
	if err != nil {
		t.Fatalf("read om.lua: %v", err)
	}
	content := string(data)

	// Header must use device config values
	if !contains(content, "-- FwType 00000003") {
		t.Error("om.lua missing correct FwType header")
	}
	if !contains(content, "-- HwType 00000013") {
		t.Error("om.lua missing correct HwType header")
	}

	// CLU base object
	if !contains(content, "CLU212345678 = OBJECT:new(0, 0xC0A80164)") {
		t.Error("om.lua missing CLU OBJECT:new line")
	}

	// Modules/IO NOT in om.lua — OM reads config.txt and generates them
	if contains(content, "mm_330000027") {
		t.Error("om.lua should NOT contain module objects")
	}
	if contains(content, "DOU5048") {
		t.Error("om.lua should NOT contain I/O objects")
	}
}

// --- Regression: real Grenton CLU212345678 config.txt format ---

func TestRealGrentonConfigTxtFormat(t *testing.T) {
	// This test verifies that our serial→hex conversion produces
	// exactly the same values as found in a real Grenton CLU212345678.
	// Reference: /Grenton/CLU212345678/config.txt
	testCases := []struct {
		serial   string
		hwType   uint8
		expected string
	}{
		{"330000027", 0x1e, "13ab669b:1e:02:01"},
		{"330000028", 0x1e, "13ab669c:1e:02:01"},
		{"201000014", 0x15, "0bfb044e:15:02:01"},
		{"181000015", 0x14, "0ac9d74f:14:02:01"},
		{"181000016", 0x14, "0ac9d750:14:02:01"},
	}

	for _, tc := range testCases {
		serialNum := parseSerialToUint32(tc.serial)
		entry := fmt.Sprintf("%08x:%02x:02:01", serialNum, tc.hwType)
		if entry != tc.expected {
			t.Errorf("serial %s → %q, want %q", tc.serial, entry, tc.expected)
		}
	}
}

// --- Regression: om.lua must NOT contain modules/IO (OM manages those) ---

func TestOMGenerator_NoModulesInOutput(t *testing.T) {
	device := &VCLUDeviceConfig{
		SerialNumber:    212345678,
		Category:        DeviceCategoryCLU,
		HardwareType:    0x13,
		HardwareVersion: 0x01,
		FirmwareType:    0x03,
		FirmwareVersion: 0x000001f6,
		Modules: []ModuleInstance{
			{ID: "mod-1", TypeID: "dout8t", Serial: "330000027", HardwareType: 0x1e},
			{ID: "mod-2", TypeID: "din8", Serial: "181000015", HardwareType: 0x14},
		},
		IOObjects: []IOInstance{
			{ID: "ANA8466", ModuleID: "mod-1", TypeID: "analogin", Index: 0, Name: "Analog1", LuaType: 12},
			{ID: "DIN9518", ModuleID: "mod-2", TypeID: "din", Index: 0, Name: "Switch1", LuaType: 3},
		},
	}

	generator := NewOMGenerator(device, IPToUint32("192.168.0.3"))
	content, err := generator.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Base CLU object must be present
	if !contains(content, "OBJECT:new(0,") {
		t.Error("om.lua must contain CLU base object")
	}

	// Modules and I/O must NOT be present — OM discovers them from config.txt
	for _, shouldNotContain := range []string{
		"mm_330000027", "mm_181000015", "ANA8466", "DIN9518",
	} {
		if contains(content, shouldNotContain) {
			t.Errorf("om.lua should NOT contain %q (OM manages modules/IO)", shouldNotContain)
		}
	}
}

// --- End-to-end: full pipeline wizard config → propagated files ---

func TestFullPipeline_WizardConfigToFiles(t *testing.T) {
	dir := t.TempDir()

	// 1. Simulate wizard: write master config
	cfg := testCLUConfigWithModules()
	if err := WriteMasterConfig(dir, cfg); err != nil {
		t.Fatalf("WriteMasterConfig: %v", err)
	}

	// 2. Generate om.lua (like wizard finish)
	localIP := net.ParseIP("192.168.1.100")
	localIPUint := IPToUint32("192.168.1.100")

	generator := NewOMGenerator(cfg.Device, localIPUint)
	omContent, err := generator.Generate()
	if err != nil {
		t.Fatalf("OMGenerator.Generate: %v", err)
	}
	if err := WriteOMLua(dir, omContent); err != nil {
		t.Fatalf("WriteOMLua: %v", err)
	}

	// 3. Propagate config (like LoadAndPropagateConfig)
	mac, _ := net.ParseMAC("aa:bb:cc:dd:ee:ff")
	dc := masterConfigToDeviceConfig(cfg, mac)
	keys := masterConfigToKeys(cfg)

	if err := PropagateConfig(dir, localIP, dc, keys); err != nil {
		t.Fatalf("PropagateConfig: %v", err)
	}

	// 4. Verify all files exist
	for _, f := range []string{"CONFIG.JSON", "config.txt", "om.lua", "main.lua"} {
		path := filepath.Join(dir, "a", f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("file %s should exist", f)
		}
	}

	// 5. config.txt: modules present
	configTxt, _ := os.ReadFile(filepath.Join(dir, "a", "config.txt"))
	if !contains(string(configTxt), "13ab669b:1e:02:01") {
		t.Error("config.txt missing module after full pipeline")
	}

	// 6. CONFIG.JSON: tfbusDevices populated
	configJSON, _ := os.ReadFile(filepath.Join(dir, "a", "CONFIG.JSON"))
	var parsed OMConfigJSON
	if err := json.Unmarshal(configJSON, &parsed); err != nil {
		t.Fatalf("parse CONFIG.JSON: %v", err)
	}
	if len(parsed.TFBusDevices) != 2 {
		t.Errorf("CONFIG.JSON tfbusDevices = %d entries, want 2", len(parsed.TFBusDevices))
	}

	// 7. om.lua: base CLU object only (OM manages modules/IO via config.txt)
	omLua, _ := os.ReadFile(filepath.Join(dir, "a", "om.lua"))
	omStr := string(omLua)
	if !contains(omStr, "CLU212345678 = OBJECT:new(0,") {
		t.Error("om.lua missing CLU base object after full pipeline")
	}
	if contains(omStr, "mm_330000027") {
		t.Error("om.lua should NOT contain module objects after full pipeline")
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range splitByNewline(s) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitByNewline(s string) []string {
	var result []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			result = append(result, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		result = append(result, s[start:])
	}
	return result
}

// The write goes through a temp file and a rename: nothing is left behind and
// the file never holds a partial document.
func TestWriteMasterConfigAtomic(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		if err := WriteMasterConfig(dir, &VCLUConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != ".vclu.json" {
		t.Fatalf("left in dir: %v", entries)
	}
	if _, err := ReadMasterConfig(dir); err != nil {
		t.Fatal(err)
	}
}
