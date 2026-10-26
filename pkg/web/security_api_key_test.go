package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// postSecurityForm submits the Settings → Security form.
func postSecurityForm(t *testing.T, s *Server, form url.Values) {
	t.Helper()
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)
	engine.SetHTMLTemplate(template.Must(template.New("settings.html").Parse(`{{.error}}`)))
	c.Request = httptest.NewRequest(http.MethodPost, "/settings/security", strings.NewReader(form.Encode()))
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.handleSecurityPost(c)
	if body := strings.TrimSpace(w.Body.String()); w.Code != http.StatusOK || body != "" {
		t.Fatalf("security form: %d %q", w.Code, body)
	}
}

// newAuthWithAPIKey turns auth on and returns a freshly generated API key.
func newAuthWithAPIKey(t *testing.T, s *Server) string {
	t.Helper()
	if err := config.SaveAuth(s.serverDir, "admin", "password1"); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, s.handleAPIGenerateAPIKey, `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("generate key: %d %s", w.Code, w.Body.String())
	}
	var resp struct{ APIKey string }
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !s.validateAPIKey(resp.APIKey) {
		t.Fatalf("new key %q not accepted", resp.APIKey)
	}
	return resp.APIKey
}

// Saving the security form with auth on keeps the API key (#98).
func TestSecuritySaveKeepsAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, form := range map[string]url.Values{
		"new password": {"authEnabled": {"on"}, "authUsername": {"admin"},
			"authPassword": {"password2"}, "authPasswordConfirm": {"password2"}},
		"new username": {"authEnabled": {"on"}, "authUsername": {"owner"}},
	} {
		t.Run(name, func(t *testing.T) {
			s := &Server{serverDir: t.TempDir()}
			key := newAuthWithAPIKey(t, s)

			postSecurityForm(t, s, form)

			if !s.validateAPIKey(key) {
				t.Error("API key stopped working after the security form was saved")
			}
			if pw := form.Get("authPassword"); pw != "" && !config.ValidateAuth(s.serverDir, "admin", pw) {
				t.Error("new password not saved")
			}
		})
	}
}

// Turning auth off clears the whole auth section, the API key with it.
func TestSecurityDisableAuthClearsAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{serverDir: t.TempDir()}
	key := newAuthWithAPIKey(t, s)

	postSecurityForm(t, s, url.Values{})

	cfg, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || cfg == nil {
		t.Fatalf("read: %v %v", cfg, err)
	}
	if cfg.Auth != nil {
		t.Errorf("auth section kept: %+v", cfg.Auth)
	}
	if s.validateAPIKey(key) {
		t.Error("API key still works with auth off")
	}
}

// Revoking the key removes only apiKeyHash; the login stays.
func TestRevokeAPIKeyKeepsLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{serverDir: t.TempDir()}
	key := newAuthWithAPIKey(t, s)

	if w := postJSON(t, s.handleAPIRevokeAPIKey, `{}`); w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}

	if s.validateAPIKey(key) {
		t.Error("revoked API key still works")
	}
	if !config.ValidateAuth(s.serverDir, "admin", "password1") {
		t.Error("login lost after revoking the API key")
	}
}
