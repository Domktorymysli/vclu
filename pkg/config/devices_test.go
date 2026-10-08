package config

import "testing"

func TestGetDefaultDevice_ReturnsFirst(t *testing.T) {
	d := GetDefaultDevice()
	if d.ID != PredefinedDevices[0].ID {
		t.Errorf("ID = %q, want %q", d.ID, PredefinedDevices[0].ID)
	}
	if d.Category != PredefinedDevices[0].Category {
		t.Errorf("Category = %q, want %q", d.Category, PredefinedDevices[0].Category)
	}
}

func TestGetDeviceByID_Gate(t *testing.T) {
	d := GetDeviceByID("gate")
	if d == nil {
		t.Fatal("expected non-nil for 'gate'")
	}
	if d.Name != "CLU" {
		t.Errorf("Name = %q, want %q", d.Name, "CLU")
	}
	if d.SupportsModules {
		t.Error("Gate should not support modules")
	}
	if d.Category != DeviceCategoryGate {
		t.Errorf("Category = %q, want %q", d.Category, DeviceCategoryGate)
	}
}

func TestGetDeviceByID_CLU(t *testing.T) {
	d := GetDeviceByID("clu")
	if d == nil {
		t.Fatal("expected non-nil for 'clu'")
	}
	if d.Name != "CLU" {
		t.Errorf("Name = %q, want %q", d.Name, "CLU")
	}
	if !d.SupportsModules {
		t.Error("CLU should support modules")
	}
	if d.Category != DeviceCategoryCLU {
		t.Errorf("Category = %q, want %q", d.Category, DeviceCategoryCLU)
	}
}

func TestGetDeviceByID_Standalone(t *testing.T) {
	d := GetDeviceByID("standalone")
	if d == nil {
		t.Fatal("expected non-nil for 'standalone'")
	}
	if d.Name != "Standalone" {
		t.Errorf("Name = %q, want %q", d.Name, "Standalone")
	}
	if d.SupportsModules {
		t.Error("Standalone should not support modules")
	}
	if d.Category != DeviceCategoryStandalone {
		t.Errorf("Category = %q, want %q", d.Category, DeviceCategoryStandalone)
	}
	if d.Icon != "bi-hdd-network" {
		t.Errorf("Icon = %q, want %q", d.Icon, "bi-hdd-network")
	}
}

func TestGetDeviceByID_Unknown(t *testing.T) {
	d := GetDeviceByID("nonexistent")
	if d != nil {
		t.Errorf("expected nil for unknown ID, got %+v", d)
	}
}

func TestGetDeviceByID_Empty(t *testing.T) {
	d := GetDeviceByID("")
	if d != nil {
		t.Errorf("expected nil for empty ID, got %+v", d)
	}
}

func TestPredefinedDevices_Count(t *testing.T) {
	if len(PredefinedDevices) < 2 {
		t.Errorf("expected at least 2 predefined devices, got %d", len(PredefinedDevices))
	}
}

func TestPredefinedDevices_UniqueIDs(t *testing.T) {
	seen := map[string]bool{}
	for _, d := range PredefinedDevices {
		if seen[d.ID] {
			t.Errorf("duplicate device ID: %q", d.ID)
		}
		seen[d.ID] = true
	}
}

func TestPredefinedDevices_HaveIcons(t *testing.T) {
	for _, d := range PredefinedDevices {
		if d.Icon == "" {
			t.Errorf("device %q has no icon", d.ID)
		}
	}
}

func TestPredefinedDevices_HaveSerialPrefix(t *testing.T) {
	for _, d := range PredefinedDevices {
		if d.SerialPrefix == 0 {
			t.Errorf("device %q has zero SerialPrefix", d.ID)
		}
	}
}

func TestGetDeviceByID_ReturnsPointerToOriginal(t *testing.T) {
	d := GetDeviceByID(PredefinedDevices[0].ID)
	if d == nil {
		t.Fatal("expected non-nil")
	}
	// Verify it points to the actual slice element
	if d != &PredefinedDevices[0] {
		t.Error("should return pointer to PredefinedDevices element")
	}
}
