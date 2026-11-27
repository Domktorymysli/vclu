package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"rsc.io/qr"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

type loginWizard struct {
	state     plugins.LoginState
	started   int
	cancelled []string
}

func (w *loginWizard) LoadPlugin(string, map[string]interface{}) error { return nil }
func (w *loginWizard) UnloadPlugin(string) error                       { return nil }
func (w *loginWizard) IsLoaded(string) bool                            { return true }
func (w *loginWizard) Form(string) (*plugins.Form, json.RawMessage, error) {
	return nil, nil, nil
}
func (w *loginWizard) Discover(context.Context, string) ([]map[string]any, []string, error) {
	return nil, nil, nil
}
func (w *loginWizard) ModuleCode(string) (string, error)                      { return "", nil }
func (w *loginWizard) Dashboards(string) ([]plugins.ManifestDashboard, error) { return nil, nil }
func (w *loginWizard) HasLogin(string) bool                                   { return true }
func (w *loginWizard) LoginStart(string) (string, error) {
	w.started++
	if w.started > 1 {
		return "", plugins.ErrLoginRunning
	}
	return "r1", nil
}
func (w *loginWizard) LoginPoll(string, string) (plugins.LoginState, error) { return w.state, nil }
func (w *loginWizard) LoginCancel(_, req string) error {
	w.cancelled = append(w.cancelled, req)
	return nil
}

// The panel gets the QR code as a PNG made in vclu from the plugin's text; a second start is a
// conflict; cancel reaches the plugin.
func TestPluginsAPILogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := &loginWizard{state: plugins.LoginState{State: "pending", QR: "tuyaSmart--qrLogin?token=abc", Hint: "Smart Life"}}
	s := &Server{serverDir: t.TempDir(), pluginController: w}
	r := gin.New()
	r.UseRawPath, r.UnescapePathValues = true, true
	r.POST("/api/plugins/:id/login", s.handlePluginsAPILoginStart)
	r.GET("/api/plugins/:id/login/:req", s.handlePluginsAPILoginPoll)
	r.DELETE("/api/plugins/:id/login/:req", s.handlePluginsAPILoginCancel)
	do := func(method, path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, out := do(http.MethodPost, "/api/plugins/@jan%2Ftuya/login"); code != http.StatusOK || out["req"] != "r1" {
		t.Fatalf("start: %d %v", code, out)
	}
	if code, _ := do(http.MethodPost, "/api/plugins/@jan%2Ftuya/login"); code != http.StatusConflict {
		t.Fatalf("second start: %d", code)
	}

	code, out := do(http.MethodGet, "/api/plugins/@jan%2Ftuya/login/r1")
	if code != http.StatusOK || out["state"] != "pending" || out["hint"] != "Smart Life" {
		t.Fatalf("poll: %d %v", code, out)
	}
	img, _ := out["qr"].(string)
	want, _ := qr.Encode("tuyaSmart--qrLogin?token=abc", qr.M)
	want.Scale = 6
	if img != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(want.PNG()) {
		t.Fatalf("qr image: %.60s…", img)
	}

	w.state = plugins.LoginState{State: "pending", Code: "WDJB-MJHT", URL: "https://example.com/device"}
	if _, out := do(http.MethodGet, "/api/plugins/@jan%2Ftuya/login/r1"); out["qr"] != nil || out["code"] != "WDJB-MJHT" || out["url"] != "https://example.com/device" {
		t.Fatalf("device code: %v", out)
	}

	if code, _ := do(http.MethodDelete, "/api/plugins/@jan%2Ftuya/login/r1"); code != http.StatusOK || len(w.cancelled) != 1 || w.cancelled[0] != "r1" {
		t.Fatalf("cancel: %d %v", code, w.cancelled)
	}
	if code, _ := do(http.MethodPost, "/api/plugins/bad%20id/login"); code != http.StatusBadRequest {
		t.Fatalf("bad id: %d", code)
	}
}
