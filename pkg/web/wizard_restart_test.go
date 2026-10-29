package web

import (
	"net/http"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// The wizard's security step turns the login on, so the page's own POST /api/restart after
// finish got 401 and vCLU kept running without its keys and the Grenton protocol until someone
// restarted it. Finish restarts vCLU itself.
func TestWizardFinishRestartsVCLU(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := writeTestConfig(dir, &config.VCLUConfig{
		Auth:   &config.VCLUAuthConfig{Username: "admin", PasswordHash: hash},
		Device: &config.VCLUDeviceConfig{DeviceTypeID: "gate", SerialNumber: 0x1f000ff8},
	}); err != nil {
		t.Fatal(err)
	}
	restarts := 0
	s := &Server{serverDir: dir, restart: func() { restarts++ }}

	if w := postJSON(t, s.handleAPIWizardFinish, `{}`); w.Code != http.StatusOK {
		t.Fatalf("finish: %d %s", w.Code, w.Body.String())
	}
	if restarts != 1 {
		t.Errorf("restarts after finish: %d, want 1", restarts)
	}

	// A refused finish changes nothing and restarts nothing
	restarts = 0
	if err := writeTestConfig(dir, &config.VCLUConfig{Device: &config.VCLUDeviceConfig{DeviceTypeID: "gate"}}); err != nil {
		t.Fatal(err)
	}
	if w := postJSON(t, s.handleAPIWizardFinish, `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("finish without a password: %d, want 400", w.Code)
	}
	if restarts != 0 {
		t.Errorf("restarts after a refused finish: %d, want 0", restarts)
	}
}
