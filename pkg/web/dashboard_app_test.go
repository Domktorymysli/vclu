package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// Dashboards on a wall tablet (#99): a manifest and meta tags for the home screen, :width('full').

const dashAppSetup = `
	_dashboards = {}
	Dashboard.new('pub'):name('Hall'):section('S', 'grid'):build()
	Dashboard.new('wall'):name('Kuchnia "A" <b>&</b>'):width('full')
		:theme({ background = '#f4f4f7', accent = '#ff9500' })
		:section('S', 'grid'):build()
	Dashboard.new('pinned'):name('Garage'):auth('pin'):pin('4321')
		:theme({ background = '#f4f4f7' })
		:section('Secret', 'eye')
			:status('s1', 'Hidden label', 'eye', function() return 'value-42' end)
		:build()
	Dashboard.new('sess'):name('Office'):auth('session'):width('full')
		:theme({ background = 'linear-gradient(red, blue)' })
		:section('S', 'grid')
			:status('s2', 'Hidden label', 'eye', function() return 'value-43' end)
		:build()
`

// newDashAppServer: a server with a login configured, its auth and CSRF middleware and the
// dashboard routes, the templates of web/templates.
func newDashAppServer(t *testing.T) *gin.Engine {
	t.Helper()
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(dashAppSetup); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := config.WriteMasterConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine}
	dashAppSession = s.sessions.create()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tmpl := template.New("").Funcs(template.FuncMap{
		"t":    func(lang, key string) string { return key },
		"json": func(v interface{}) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
	})
	r.SetHTMLTemplate(template.Must(tmpl.ParseGlob("../../web/templates/*.html")))
	r.Use(s.authMiddleware(), s.csrfMiddleware())
	r.GET("/d/:id", s.handleDashboardPage)
	r.GET("/d/:id/manifest.webmanifest", s.handleDashboardManifest)
	return r
}

// dashAppSession is a logged-in session of the last newDashAppServer
var dashAppSession string

func dashAppGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestDashboardManifest(t *testing.T) {
	r := newDashAppServer(t)
	cases := []struct {
		id, name, display, color string
	}{
		{"pub", "Hall", "standalone", "#1a1a2e"},
		{"wall", `Kuchnia "A" <b>&</b>`, "fullscreen", "#f4f4f7"},
		{"pinned", "Garage", "standalone", "#f4f4f7"},
		{"sess", "sess", "fullscreen", "#1a1a2e"}, // name only after login; a gradient is not a colour
	}
	for _, c := range cases {
		// no cookie: browsers fetch the manifest without one, PIN and session dashboards too
		w := dashAppGet(r, "/d/"+c.id+"/manifest.webmanifest")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", c.id, w.Code, w.Body)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/manifest+json" {
			t.Errorf("%s: Content-Type %q", c.id, ct)
		}
		body := w.Body.String()
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("%s: %v %s", c.id, err, body)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if got := strings.Join(keys, ","); got != "background_color,display,icons,id,name,scope,short_name,start_url,theme_color" {
			t.Errorf("%s: fields %s", c.id, got)
		}
		// scope: the whole site, so tabs, chip links and the login page stay in the app
		want := map[string]any{"name": c.name, "short_name": c.name, "start_url": "/d/" + c.id, "scope": "/",
			"id": "/d/" + c.id, "display": c.display, "background_color": c.color, "theme_color": c.color}
		for k, v := range want {
			if m[k] != v {
				t.Errorf("%s: %s = %v, want %v", c.id, k, m[k], v)
			}
		}
		icons, _ := m["icons"].([]any)
		if len(icons) == 0 {
			t.Errorf("%s: no icons", c.id)
		}
		for _, ic := range icons {
			src, _ := ic.(map[string]any)["src"].(string)
			if !strings.HasPrefix(src, "/static/img/") {
				t.Errorf("%s: icon %v", c.id, ic)
			}
		}
		// nothing of the dashboard beyond its name and colours
		for _, secret := range []string{"4321", "value-4", "Hidden label", "Secret", "Office", "s1", "s2", `"pin"`, `"auth"`, "session", "widgets"} {
			if strings.Contains(body, secret) {
				t.Errorf("%s: manifest contains %q: %s", c.id, secret, body)
			}
		}
	}
	// logged in, the session dashboard's manifest has its name
	req := httptest.NewRequest(http.MethodGet, "/d/sess/manifest.webmanifest", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: dashAppSession})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"name":"Office"`) {
		t.Errorf("session dashboard after login: %s", w.Body)
	}
	if w := dashAppGet(r, "/d/nope/manifest.webmanifest"); w.Code != http.StatusNotFound {
		t.Errorf("unknown dashboard: %d", w.Code)
	}
	// the dashboards themselves still need their auth
	if w := dashAppGet(r, "/d/sess"); w.Code != http.StatusFound {
		t.Errorf("session dashboard without login: %d", w.Code)
	}
	if w := dashAppGet(r, "/d/pinned"); strings.Contains(w.Body.String(), "dashboardApp") {
		t.Errorf("PIN dashboard without a cookie shows the dashboard")
	}
}

func TestDashboardPageAppMode(t *testing.T) {
	r := newDashAppServer(t)
	page := func(id string) string {
		w := dashAppGet(r, "/d/"+id)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", id, w.Code)
		}
		return w.Body.String()
	}
	for _, id := range []string{"pub", "wall", "pinned"} { // pinned: the PIN page, added to the home screen first
		body := page(id)
		for _, tag := range []string{
			`<meta name="apple-mobile-web-app-capable" content="yes">`,
			`<meta name="mobile-web-app-capable" content="yes">`,
			`<link rel="apple-touch-icon" href="/static/img/vclu-180.png">`,
			`<link rel="manifest" href="/d/` + id + `/manifest.webmanifest">`,
			`user-scalable=no, viewport-fit=cover`,
		} {
			if !strings.Contains(body, tag) {
				t.Errorf("%s: no %s", id, tag)
			}
		}
	}

	// the PIN page is drawn with the default dark look, whatever the theme
	pinPage := page("pinned")
	for _, tag := range []string{`content="black-translucent"`, `<meta name="theme-color" content="#1a1a2e">`, `content="Garage"`} {
		if !strings.Contains(pinPage, tag) {
			t.Errorf("PIN page: no %s", tag)
		}
	}

	pub := page("pub")
	for _, tag := range []string{
		`<meta name="apple-mobile-web-app-status-bar-style" content="black-translucent">`,
		`<meta name="apple-mobile-web-app-title" content="Hall">`,
		`<meta name="theme-color" content="#1a1a2e">`,
		`<body>`,
	} {
		if !strings.Contains(pub, tag) {
			t.Errorf("pub: no %s", tag)
		}
	}

	wall := page("wall")
	for _, tag := range []string{
		`<body class="dash-full">`,
		// a light theme keeps the status bar off the page
		`<meta name="apple-mobile-web-app-status-bar-style" content="default">`,
		`<meta name="theme-color" content="#f4f4f7">`,
		`<meta name="apple-mobile-web-app-title" content="Kuchnia &#34;A&#34; &lt;b&gt;&amp;&lt;/b&gt;">`,
	} {
		if !strings.Contains(wall, tag) {
			t.Errorf("wall: no %s", tag)
		}
	}
	if strings.Contains(wall, "<b>&</b>") {
		t.Errorf("wall: name not escaped")
	}
}

func TestIsLightColor(t *testing.T) {
	for c, want := range map[string]bool{
		"#fff": true, "#f4f4f7": true, "#ffffffcc": true, "white": true, "Ivory": true, "whitesmoke": true,
		"rgb(240, 240, 245)": true, "rgba(250,250,250,0.9)": true, "rgb(100% 100% 100%)": true,
		"hsl(220, 20%, 95%)": true, "hsla(0 0% 90% / 0.8)": true,
		"#1a1a2e": false, "#000": false, "black": false, "navy": false, "#zzz": false,
		"rgb(26, 26, 46)": false, "rgba(0,0,0,0.5)": false, "hsl(240, 28%, 14%)": false, "rgb(": false, "hsl(1,2)": false,
	} {
		if got := isLightColor(c); got != want {
			t.Errorf("isLightColor(%q) = %v", c, got)
		}
	}
}
