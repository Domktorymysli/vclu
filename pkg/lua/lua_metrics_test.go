package lua

import (
	"strings"
	"testing"
)

// ==================== Metrics Module Tests ====================

func TestMetricsModuleExists(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute("type(Metrics)")
	if err != nil {
		t.Fatalf("Metrics check failed: %v", err)
	}
	if result != "table" {
		t.Errorf("Metrics type is %q, expected 'table'", result)
	}

	result, err = engine.Execute("type(metric)")
	if err != nil {
		t.Fatalf("metric() check failed: %v", err)
	}
	if result != "function" {
		t.Errorf("metric type is %q, expected 'function'", result)
	}
}

func TestMetricsRegister(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Register a metric
	_, err := engine.Execute(`
		test_val = 21.5
		metric("test:temp", function() return test_val end, {unit="°C", interval=30})
	`)
	if err != nil {
		t.Fatalf("metric() call failed: %v", err)
	}

	// Verify it's in the registry
	result, err := engine.Execute("Metrics._registered['test:temp'] ~= nil")
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("metric not registered")
	}

	// Check stored options
	result, _ = engine.Execute("Metrics._registered['test:temp'].unit")
	if result != "°C" {
		t.Errorf("unit = %q, expected '°C'", result)
	}

	result, _ = engine.Execute("Metrics._registered['test:temp'].interval")
	if result != "30" {
		t.Errorf("interval = %q, expected '30'", result)
	}
}

func TestMetricsCollect(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Register two metrics
	_, err := engine.Execute(`
		metric("sensor:a", function() return 21.3 end, {unit="°C"})
		metric("sensor:b", function() return 55 end, {unit="%"})
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Collect values
	result, err := engine.Execute(`Metrics._collect({"sensor:a", "sensor:b"})`)
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if !strings.Contains(result, `"sensor:a"`) || !strings.Contains(result, "21.3") {
		t.Errorf("collect result missing sensor:a data: %s", result)
	}
	if !strings.Contains(result, `"sensor:b"`) || !strings.Contains(result, "55") {
		t.Errorf("collect result missing sensor:b data: %s", result)
	}
}

func TestMetricsCollectWithDecimals(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		metric("sensor:precise", function() return 21.3456 end, {decimals=1})
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`Metrics._collect({"sensor:precise"})`)
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	// Should be rounded to 1 decimal
	if !strings.Contains(result, "21.3") {
		t.Errorf("expected rounded value 21.3, got: %s", result)
	}
	if strings.Contains(result, "21.3456") {
		t.Errorf("value should be rounded, got raw: %s", result)
	}
}

func TestMetricsCollectSkipsErroringGetter(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		metric("sensor:ok", function() return 10 end)
		metric("sensor:broken", function() error("device offline") end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`Metrics._collect({"sensor:ok", "sensor:broken"})`)
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	// Should have sensor:ok but not sensor:broken
	if !strings.Contains(result, "sensor:ok") {
		t.Errorf("should contain sensor:ok: %s", result)
	}
	if strings.Contains(result, "sensor:broken") {
		t.Errorf("should NOT contain sensor:broken (erroring getter): %s", result)
	}
}

func TestMetricsCollectUnknownPath(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute(`Metrics._collect({"nonexistent:path"})`)
	if err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if result != "[]" {
		t.Errorf("expected empty array, got: %s", result)
	}
}

func TestMetricsList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		metric("temp:salon", function() return 21 end, {unit="°C", interval=60})
		metric("hum:salon", function() return 55 end, {unit="%", interval=30})
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute("Metrics._list()")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}

	if !strings.Contains(result, "temp:salon") || !strings.Contains(result, "hum:salon") {
		t.Errorf("list missing metrics: %s", result)
	}
}

func TestMetricsSyncToGo(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Without Go bridge, sync should return 0
	_, err := engine.Execute(`
		metric("x:1", function() return 1 end)
		metric("x:2", function() return 2 end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute("Metrics._syncToGo()")
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	// No __go_metric_register available → returns 0
	if result != "0" {
		t.Errorf("syncToGo without bridge should return 0, got: %s", result)
	}
}

func TestMetricsReturnPath(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute(`metric("test:path", function() return 0 end)`)
	if err != nil {
		t.Fatalf("metric() failed: %v", err)
	}

	if result != "test:path" {
		t.Errorf("metric() should return path, got: %q", result)
	}
}

func TestMetricsDefaultOptions(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`metric("test:defaults", function() return 0 end)`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, _ := engine.Execute("Metrics._registered['test:defaults'].unit")
	if result != "" {
		t.Errorf("default unit should be empty, got: %q", result)
	}

	result, _ = engine.Execute("Metrics._registered['test:defaults'].interval")
	if result != "60" {
		t.Errorf("default interval should be 60, got: %q", result)
	}
}
