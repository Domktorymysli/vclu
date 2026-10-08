package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

func testContext(t *testing.T, cookie string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/dashboard/salon/action", nil)
	if cookie != "" {
		c.Request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	return c
}

// The actor comes from what auth established, never from the request.
func TestRequestActor(t *testing.T) {
	dir := t.TempDir()
	s := &Server{serverDir: dir, sessions: newSessionStore()}

	if o, a := s.requestActor(testContext(t, ""), "dashboard:salon"); o != "web" || a != "user:local" {
		t.Errorf("auth disabled: %s %s", o, a)
	}

	hash, _ := config.HashPassword("secret")
	if err := config.WriteMasterConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "tytus", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	if o, a := s.requestActor(testContext(t, ""), "dashboard:salon"); o != "web" || a != "dashboard:salon" {
		t.Errorf("public dashboard by PIN: %s %s", o, a)
	}
	c := testContext(t, "")
	c.Set("auth_method", "apikey")
	if o, a := s.requestActor(c, "x"); o != "api" || a != "service:web-api" {
		t.Errorf("API key: %s %s", o, a)
	}
	c = testContext(t, "")
	c.Set("auth_method", "session")
	if o, a := s.requestActor(c, "x"); o != "web" || a != "user:tytus" {
		t.Errorf("session: %s %s", o, a)
	}
	// a logged-in user on a public dashboard page is that user
	if o, a := s.requestActor(testContext(t, s.sessions.create()), "dashboard:salon"); o != "web" || a != "user:tytus" {
		t.Errorf("session on a public page: %s %s", o, a)
	}
	if _, a := s.requestActor(testContext(t, "forged"), "dashboard:salon"); a != "dashboard:salon" {
		t.Errorf("an invalid session cookie must not give a user: %s", a)
	}
}
