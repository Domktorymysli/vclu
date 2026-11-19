package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// End to end through the panel's own router (auth, CSRF, every route): a diagnostic pack of
// Object Manager carrying the project is uploaded, its CLUs reach Lua, the agent's files and
// the myGrenton pages are written, and vclu's own diagnostic package is made afterwards.
// None of the project's or the GATE's secrets, nor the panel's, end up in either.
func TestE2E_DiagnosticPackAndReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	key, keyHash, _ := config.GenerateAPIKey()
	if err := writeTestConfig(dir, &config.VCLUConfig{
		Auth: &config.VCLUAuthConfig{Username: "admin", PasswordHash: hash, APIKeyHash: keyHash},
	}); err != nil {
		t.Fatal(err)
	}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s := NewServer(0, "../../web", dir, &config.CLUDeviceConfig{}, &config.CLUKeys{}, engine)
	t.Cleanup(func() {
		if s.live != nil {
			s.live.shutdown()
		}
	})
	do := func(method, path, contentType string, body io.Reader, authed bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, body)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if authed {
			req.Header.Set("X-API-Key", key)
		}
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w
	}

	// the pack as Object Manager makes it: om.lua per CLU, the project next to them
	mg, _, _ := mgProject(t)
	pack := map[string][]byte{"house.zip": zipBytes(t, mg)}
	base := filepath.Join("testdata", "omproject", "om-1.8.0", "pack")
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(base, path)
			pack[filepath.ToSlash(rel)], _ = os.ReadFile(path)
		}
		return err
	})
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	fw, _ := mw.CreateFormFile("file", "diag.zip")
	_, _ = fw.Write(zipBytes(t, pack))
	_ = mw.Close()
	formBytes := form.Bytes()

	if w := do("POST", "/api/om/upload", mw.FormDataContentType(), bytes.NewReader(formBytes), false); w.Code != http.StatusUnauthorized {
		t.Fatalf("upload without a login: %d", w.Code)
	}
	w := do("POST", "/api/om/upload", mw.FormDataContentType(), bytes.NewReader(formBytes), true)
	var up map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil || w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if up["success"] != true || up["isZip"] != true || up["imported"] != float64(6) || up["agentDocs"] != "ai/om" || up["omVersion"] != "1.8.0_2301" {
		t.Fatalf("upload answer: %s", w.Body)
	}

	// the CLUs are in the panel's list and in Lua
	w = do("GET", "/api/om/list", "", nil, true)
	if w.Code != http.StatusOK || strings.Count(w.Body.String(), `"serial"`) != 6 {
		t.Errorf("OM files: %d %s", w.Code, w.Body)
	}
	if out, err := engine.Execute(`return tostring(CLU220000002 ~= nil)`); err != nil || out != "true" {
		t.Errorf("CLU220000002 in Lua: %s %v", out, err)
	}
	// the agent's files and the myGrenton pages
	for _, f := range []string{"ai/om/index.md", "ai/om/CLU220000002.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	w = do("GET", "/api/grenton/dashboards", "", nil, true)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"if-1"`) {
		t.Errorf("myGrenton pages: %d %s", w.Code, w.Body)
	}

	// vclu's diagnostic package, everything in it
	opts := `{"include_modules":true,"include_om_user":true,"include_proxy":true,"include_device_names":false,"include_config":true}`
	if w := do("POST", "/api/diagnostic", "application/json", strings.NewReader(opts), false); w.Code != http.StatusUnauthorized {
		t.Fatalf("diagnostic without a login: %d", w.Code)
	}
	w = do("POST", "/api/diagnostic", "application/json", strings.NewReader(opts), true)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" ||
		!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment; filename=vclu-diag-") {
		t.Fatalf("diagnostic: %d %v", w.Code, w.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		got[f.Name] = string(b)
	}
	for _, name := range []string{"info.json", "stats.json", "metrics.json", "logs.txt", "devices.json", "config.json"} {
		if _, ok := got[name]; !ok {
			t.Errorf("the package has no %s", name)
		}
	}
	proxies := 0
	for name := range got {
		if strings.HasPrefix(name, "imports/proxy_") {
			proxies++
		}
	}
	if proxies != 6 {
		t.Errorf("%d proxies in the package, want 6", proxies)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(got["config.json"]), &cfg); err != nil {
		t.Fatalf("config.json: %v", err)
	}
	if omf, _ := cfg["omFiles"].([]any); len(omf) != 6 {
		t.Errorf("config.json lists %d OM files", len(omf))
	}
	for name, content := range got {
		for _, secret := range []string{gateMarker, "FIXTURE-SECRET", hash, keyHash, key, "passwordHash", "apiKeyHash"} {
			if strings.Contains(content, secret) {
				t.Errorf("%s holds %q", name, secret)
			}
		}
	}
	noMarker(t, dir)
}
