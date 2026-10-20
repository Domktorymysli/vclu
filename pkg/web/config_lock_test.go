package web

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// writeTestConfig replaces .vclu.json with cfg.
func writeTestConfig(dir string, cfg *config.VCLUConfig) error {
	return config.UpdateMasterConfig(dir, func(mc *config.VCLUConfig) error {
		*mc = *cfg
		return nil
	})
}

func postJSON(t *testing.T, h gin.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	h(c)
	return w
}

// Two saves of different sections at the same time both end up in .vclu.json (#6).
func TestConfigSavesOfDifferentSectionsKeepEachOther(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	s := &Server{serverDir: dir}
	const n = 50

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 1; i <= n; i++ {
			body := fmt.Sprintf(`{"enabled":false,"host":"10.0.0.1","port":%d}`, 7000+i)
			if w := postJSON(t, s.handleAPISatelSave, body); w.Code != http.StatusOK {
				t.Errorf("satel save: %d %s", w.Code, w.Body.String())
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 1; i <= n; i++ {
			body := fmt.Sprintf(`{"ttl":%d,"batchSize":4,"batchDelay":50}`, 100+i)
			if w := postJSON(t, s.handleAPIGrentonSyncSave, body); w.Code != http.StatusOK {
				t.Errorf("sync save: %d %s", w.Code, w.Body.String())
				return
			}
		}
	}()
	wg.Wait()

	cfg, err := config.ReadMasterConfig(dir)
	if err != nil || cfg == nil {
		t.Fatalf("read: %v %v", cfg, err)
	}
	if cfg.Satel == nil || cfg.Satel.Port != 7000+n {
		t.Errorf("satel section lost: %+v", cfg.Satel)
	}
	if cfg.Sync == nil || cfg.Sync.TTL != 100+n {
		t.Errorf("sync section lost: %+v", cfg.Sync)
	}
}

// A save refused inside the lock leaves the file untouched.
func TestConfigRefusedSaveDoesNotWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{MCP: &config.VCLUMCPConfig{
		Bots: []config.MCPBot{{ID: "a", Name: "bot"}},
	}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".vclu.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "missing"}}
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"name":"x"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handleAPIMCPBotUpdate(c)

	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "Bot not found") {
		t.Errorf("answer %d %s", w.Code, w.Body.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf(".vclu.json changed:\n%s\n->\n%s", before, after)
	}
}
