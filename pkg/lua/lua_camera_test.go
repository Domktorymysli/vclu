package lua

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/camera"
)

// ==================== Camera Module Tests ====================

func TestCameraModuleExists(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute("type(Camera)")
	if err != nil {
		t.Fatalf("Camera check failed: %v", err)
	}
	if result != "table" {
		t.Errorf("Camera type is %q, expected 'table'", result)
	}

	result, _ = engine.Execute("type(Camera.register)")
	if result != "function" {
		t.Errorf("Camera.register type is %q, expected 'function'", result)
	}

	result, _ = engine.Execute("type(Camera.snapshotUrl)")
	if result != "function" {
		t.Errorf("Camera.snapshotUrl type is %q, expected 'function'", result)
	}
}

func TestCameraSnapshotUrl(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	result, err := engine.Execute(`Camera.snapshotUrl("front")`)
	if err != nil {
		t.Fatalf("snapshotUrl failed: %v", err)
	}
	if result != "/api/camera/front/snapshot" {
		t.Errorf("snapshotUrl = %q, expected '/api/camera/front/snapshot'", result)
	}

	result, _ = engine.Execute(`Camera.snapshotUrl("back_yard")`)
	if result != "/api/camera/back_yard/snapshot" {
		t.Errorf("snapshotUrl = %q", result)
	}
}

func TestCameraRegisterRequiresIDAndURL(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Missing url
	_, err := engine.Execute(`Camera.register("cam1", {})`)
	if err == nil {
		t.Errorf("expected error for missing url")
	}

	// Missing id
	_, err = engine.Execute(`Camera.register(nil, {url="http://example.com"})`)
	if err == nil {
		t.Errorf("expected error for missing id")
	}
}

func TestCameraRegisterWithoutGoBridge(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Without Go bridge connected, register should log warning but not error
	_, err := engine.Execute(`
		Camera.register("cam1", {
			url = "http://192.168.0.1/snapshot",
			auth = "digest",
			username = "admin",
			password = "secret",
		})
	`)
	if err != nil {
		t.Fatalf("register without bridge should not error: %v", err)
	}
}

func TestCameraRegisterWithGoBridge(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Wire up camera manager
	cm := camera.NewManager()
	engine.SetCameraManager(cm)

	// Register from Lua
	_, err := engine.Execute(`
		Camera.register("front", {
			url = "http://192.168.0.203/ISAPI/Streaming/channels/102/picture",
			auth = "digest",
			username = "view",
			password = "secret123",
		})
	`)
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	// Verify camera was registered in Go manager
	ids := cm.List()
	found := false
	for _, id := range ids {
		if id == "front" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("camera 'front' not found in manager, got: %v", ids)
	}
}

func TestCameraRegisterMultiple(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	cm := camera.NewManager()
	engine.SetCameraManager(cm)

	_, err := engine.Execute(`
		Camera.register("front", {
			url = "http://192.168.0.1/snap", auth = "digest",
			username = "admin", password = "pass1",
		})
		Camera.register("back", {
			url = "http://192.168.0.2/snap", auth = "basic",
			username = "user", password = "pass2",
		})
		Camera.register("gate", {
			url = "http://192.168.0.3/snap",
		})
	`)
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}

	ids := cm.List()
	if len(ids) != 3 {
		t.Errorf("expected 3 cameras, got %d: %v", len(ids), ids)
	}
}

func TestCameraClear(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	cm := camera.NewManager()
	engine.SetCameraManager(cm)

	_, _ = engine.Execute(`
		Camera.register("cam1", {url = "http://192.168.0.1/snap"})
		Camera.register("cam2", {url = "http://192.168.0.2/snap"})
	`)

	if len(cm.List()) != 2 {
		t.Fatalf("expected 2 cameras registered")
	}

	// Clear via Go bridge
	_, err := engine.Execute("__go_camera_clear()")
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}

	if len(cm.List()) != 0 {
		t.Errorf("expected 0 cameras after clear, got %d", len(cm.List()))
	}
}

func TestCameraManagerSnapshotNotFound(t *testing.T) {
	cm := camera.NewManager()

	_, _, err := cm.GetSnapshot("nonexistent")
	if err == nil {
		t.Errorf("expected error for nonexistent camera")
	}
}

func TestCameraIntegrationWithDashboard(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	cm := camera.NewManager()
	engine.SetCameraManager(cm)

	// Register camera and use its URL in a dashboard
	_, err := engine.Execute(`
		_dashboards = {}
		Camera.register("front", {
			url = "http://192.168.0.203/snap",
			auth = "digest",
			username = "view",
			password = "pass",
		})

		Dashboard.new('cam_test')
			:section('Camera', 'camera-video')
			:image('cam1', 'Front', Camera.snapshotUrl("front"), {refresh=10, span=3})
			:build()
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, _ := engine.Execute(`Dashboard.API.getDefinition("cam_test")`)
	if result == "" {
		t.Fatal("empty result")
	}

	// Check the image widget URL points to the proxy
	if !contains(result, "/api/camera/front/snapshot") {
		t.Errorf("image widget URL should point to camera proxy: %s", result)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
