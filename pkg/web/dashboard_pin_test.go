package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// A PIN cookie opens its dashboard only if this server issued it for the
// current PIN; guessing the PIN is rate limited.
func TestDashboardPinCookie(t *testing.T) {
	dashCache.Invalidate()
	pinLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1.0 / 6, burst: 5}
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`Dashboard.new('p'):auth('pin'):pin('1234'):section('S','i'):build()`); err != nil {
		t.Fatal(err)
	}
	// with no login configured a PIN page is open anyway
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := config.WriteMasterConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/dashboard/:id/auth", s.handleAPIDashboardAuth)
	r.GET("/api/dashboard/:id/values", s.handleAPIDashboardValues)

	login := func(pin string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/p/auth", strings.NewReader(`{"pin":"`+pin+`"}`)))
		return w
	}
	values := func(cookie string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/p/values", nil)
		req.AddCookie(&http.Cookie{Name: "vclu_dash_p", Value: cookie})
		r.ServeHTTP(w, req)
		return w.Code
	}

	w := login("1234")
	if w.Code != 200 {
		t.Fatalf("right PIN: %d %s", w.Code, w.Body)
	}
	var cookie string
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "vclu_dash_p" {
			cookie = ck.Value
		}
	}
	if code := values(cookie); code != 200 {
		t.Errorf("issued cookie: %d", code)
	}
	for name, forged := range map[string]string{
		"any 64 hex":   strings.Repeat("a", 64),
		"other PIN":    s.makeDashPinCookie("p", "0000", time.Now().Add(time.Hour).Unix()),
		"expired":      s.makeDashPinCookie("p", "1234", time.Now().Add(-time.Minute).Unix()),
		"other board":  s.makeDashPinCookie("q", "1234", time.Now().Add(time.Hour).Unix()),
		"altered time": "9999999999" + cookie[strings.Index(cookie, "."):],
	} {
		if code := values(forged); code != 401 {
			t.Errorf("%s: %d, want 401", name, code)
		}
	}
	// a changed PIN ends cookies issued for the old one
	if _, err := engine.Execute(`Dashboard.new('p'):auth('pin'):pin(9876):section('S','i'):build()`); err != nil {
		t.Fatal(err)
	}
	dashCache.Invalidate()
	if code := values(cookie); code != 401 {
		t.Errorf("cookie after PIN change: %d, want 401", code)
	}
	// 1 try used above; 4 more wrong ones pass the limiter, the next is refused
	for i := 0; i < 4; i++ {
		if w := login("0000"); w.Code != 401 {
			t.Fatalf("wrong PIN %d: %d", i, w.Code)
		}
	}
	if w := login("9876"); w.Code != 429 {
		t.Errorf("after 5 tries: %d, want 429", w.Code)
	}
	// a numeric :pin(9876) accepts the typed "9876"
	pinLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1.0 / 6, burst: 5}
	if w := login("9876"); w.Code != 200 {
		t.Errorf("numeric PIN: %d %s", w.Code, w.Body)
	}
}
