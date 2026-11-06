package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// Dashboards in the viewer's language (#85): every text the page draws comes from the
// "dashboard" section of web/locales.

// dashboardScripts are the scripts of the page /d/:id that draw text.
var dashboardScripts = []string{"../../web/static/js/dashboard.js", "../../web/static/js/dashboard-live.js"}

func dashboardLocale(t *testing.T, lang string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile("../../web/locales/" + lang + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]interface{}
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	sec, ok := all["dashboard"].(map[string]interface{})
	if !ok {
		t.Fatalf("%s.json has no dashboard section", lang)
	}
	return sec
}

// localeStrings lists every text of a section, those in lists and nested objects too.
func localeStrings(v interface{}, out *[]string) {
	switch x := v.(type) {
	case string:
		*out = append(*out, x)
	case []interface{}:
		for _, e := range x {
			localeStrings(e, out)
		}
	case map[string]interface{}:
		for _, e := range x {
			localeStrings(e, out)
		}
	}
}

var jsComment = regexp.MustCompile(`(?m)(?s:/\*.*?\*/)|^[ \t]*//.*$|[ \t]//[^'"\n]*$`)

func TestDashboardJSHasNoTexts(t *testing.T) {
	plain := regexp.MustCompile(`^[a-z]+$`) // "on", "open": also state names in the code
	var known []string
	for _, lang := range []string{"en", "pl"} {
		var s []string
		localeStrings(dashboardLocale(t, lang), &s)
		for _, v := range s {
			if !plain.MatchString(v) && !strings.Contains(v, "-") {
				known = append(known, v)
			}
		}
	}
	if len(known) < 60 {
		t.Fatalf("only %d texts to look for: the dashboard section shrank", len(known))
	}
	polish := regexp.MustCompile(`[ąćęłńóśźżĄĆĘŁŃÓŚŹŻ]`)
	for _, path := range dashboardScripts {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src := jsComment.ReplaceAllString(string(b), "")
		if strings.Contains(src, "navigator.language") {
			t.Errorf("%s reads navigator.language: the server picks the language", path)
		}
		for _, v := range known {
			for _, q := range []string{"'", `"`} {
				if strings.Contains(src, q+v+q) {
					t.Errorf("%s has the literal %s%s%s: take it from T (web/locales, dashboard)", path, q, v, q)
				}
			}
		}
		if m := polish.FindString(src); m != "" {
			i := strings.Index(src, m)
			t.Errorf("%s has Polish text in code near %q: move it to web/locales", path, src[max(0, i-30):i+10])
		}
	}
}

// localeShape: the keys of a section and the length of its lists, for comparing languages.
func localeShape(prefix string, v interface{}, out map[string]string) {
	switch x := v.(type) {
	case map[string]interface{}:
		for k, e := range x {
			localeShape(prefix+"."+k, e, out)
		}
	case []interface{}:
		out[prefix] = fmt.Sprintf("list of %d", len(x))
	default:
		out[prefix] = fmt.Sprintf("%T", x)
	}
}

func TestDashboardLocalesSameKeys(t *testing.T) {
	en, pl := map[string]string{}, map[string]string{}
	localeShape("dashboard", dashboardLocale(t, "en"), en)
	localeShape("dashboard", dashboardLocale(t, "pl"), pl)
	for k, v := range en {
		if pl[k] != v {
			t.Errorf("%s: en.json %s, pl.json %q", k, v, pl[k])
		}
	}
	for k := range pl {
		if _, ok := en[k]; !ok {
			t.Errorf("%s is in pl.json only", k)
		}
	}

	// every T.name the script reads is translated
	b, err := os.ReadFile(dashboardScripts[0])
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bT\.([A-Za-z_]\w*)`).FindAllStringSubmatch(string(b), -1) {
		used[m[1]] = true
	}
	if len(used) < 50 {
		t.Fatalf("only %d T.name found in dashboard.js", len(used))
	}
	sec := dashboardLocale(t, "en")
	var missing []string
	for k := range used {
		if _, ok := sec[k]; !ok {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("dashboard.js reads T.%s, not in the dashboard section", strings.Join(missing, ", T."))
	}
}

// newDashI18nServer: the dashboard routes with the real translations and languageMiddleware.
func newDashI18nServer(t *testing.T) *gin.Engine {
	t.Helper()
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(dashAppSetup); err != nil {
		t.Fatal(err)
	}
	if err := i18n.Load("../../web/locales"); err != nil {
		t.Fatal(err)
	}
	s := &Server{sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tmpl := template.New("").Funcs(template.FuncMap{
		"t":    func(lang, key string) string { return i18n.Get(lang, key) },
		"json": func(v interface{}) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
	})
	r.SetHTMLTemplate(template.Must(tmpl.ParseGlob("../../web/templates/*.html")))
	r.Use(s.languageMiddleware())
	r.GET("/d/:id", s.handleDashboardPage)
	return r
}

var dashTextsRe = regexp.MustCompile(`(?s)<script type="application/json" id="dashTexts">(.*?)</script>`)

func TestDashboardPageLanguage(t *testing.T) {
	r := newDashI18nServer(t)
	get := func(path, acceptLang string) string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if acceptLang != "" {
			req.Header.Set("Accept-Language", acceptLang)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	cases := []struct {
		path, accept, lang string
		texts              []string // drawn by the template itself
	}{
		{"/d/pub?lang=pl", "", "pl", []string{"Wczytywanie…", "Odświeżanie dashboardu…"}},
		{"/d/pub?lang=en", "", "en", []string{"Loading…", "Dashboard reloading…"}},
		{"/d/pub", "en-GB,en;q=0.9", "en", []string{"Loading…"}},
		{"/d/pub?lang=xx", "", i18n.DefaultLang, nil}, // no such translation: the default, not "xx"
	}
	for _, tc := range cases {
		page := get(tc.path, tc.accept)
		if !strings.Contains(page, `<html lang="`+tc.lang+`"`) {
			t.Errorf("%s: no <html lang=%q>", tc.path, tc.lang)
		}
		for _, s := range tc.texts {
			if !strings.Contains(page, s) {
				t.Errorf("%s: %q missing", tc.path, s)
			}
		}
		m := dashTextsRe.FindStringSubmatch(page)
		if m == nil {
			t.Fatalf("%s: no dashTexts script", tc.path)
		}
		var got map[string]interface{}
		if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
			t.Fatalf("%s: dashTexts is not JSON: %v", tc.path, err)
		}
		if want := dashboardLocale(t, tc.lang); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: dashTexts differs from %s.json", tc.path, tc.lang)
		}
	}

	// the PIN page too: its prompt and the error its script writes
	pl, en := get("/d/pinned?lang=pl", ""), get("/d/pinned?lang=en", "")
	for page, want := range map[string][]string{
		pl: {`<html lang="pl"`, "Wpisz PIN, aby otworzyć dashboard", `"Błędny PIN"`},
		en: {`<html lang="en"`, "Enter PIN to access this dashboard", `"Invalid PIN"`},
	} {
		for _, s := range want {
			if !strings.Contains(page, s) {
				t.Errorf("PIN page: %q missing", s)
			}
		}
	}
}
