package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestAccessLogOnlyFailedOrSlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const slow = 50 * time.Millisecond
	var out bytes.Buffer
	r := gin.New()
	r.Use(accessLog(&out, slow))
	r.GET("/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/fail", func(c *gin.Context) { c.String(http.StatusInternalServerError, "x") })
	r.GET("/slow", func(c *gin.Context) {
		time.Sleep(slow + 10*time.Millisecond)
		c.String(http.StatusOK, "late")
	})
	r.GET("/events", func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		time.Sleep(slow + 10*time.Millisecond)
		c.String(http.StatusOK, "data: x\n\n")
	})

	get := func(path string) string {
		t.Helper()
		out.Reset()
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		return out.String()
	}

	if got := get("/ok?x=1"); got != "" {
		t.Errorf("fast 200 logged: %q", got)
	}
	if got := get("/events"); got != "" {
		t.Errorf("event stream logged for its length: %q", got)
	}
	for path, status := range map[string]string{"/missing": "404", "/fail": "500", "/slow": "200"} {
		got := get(path)
		if !strings.HasPrefix(got, "[GIN] ") || !strings.Contains(got, "| "+status+" |") || !strings.Contains(got, `"`+path+`"`) {
			t.Errorf("%s: want a GIN line with %s, got %q", path, status, got)
		}
	}
}

// The server's router uses the filtered access log: a fast 200 writes nothing.
func TestServerAccessLogSkipsFastOK(t *testing.T) {
	var out bytes.Buffer
	old := gin.DefaultWriter
	gin.DefaultWriter = &out
	t.Cleanup(func() { gin.DefaultWriter = old })

	s := NewServer(0, "../../web", t.TempDir(), nil, nil, nil)
	s.router.GET("/test-ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	s.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/test-ok", nil))
	if strings.Contains(out.String(), "/test-ok") {
		t.Errorf("fast 200 in the access log: %q", out.String())
	}
	s.router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/no-such-page", nil))
	if !strings.Contains(out.String(), "| 404 |") {
		t.Errorf("404 missing from the access log: %q", out.String())
	}
}
