package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

type budgetEngine struct {
	usage   map[string]lua.PluginUsage
	resumed []string
}

func (e *budgetEngine) Execute(string) (string, error)          { return "", nil }
func (e *budgetEngine) PluginUsage() map[string]lua.PluginUsage { return e.usage }
func (e *budgetEngine) ResumePlugin(id string)                  { e.resumed = append(e.resumed, id) }

type suspendingLoader struct {
	suspended map[string]bool
	loaded    []string
}

func (l *suspendingLoader) LoadPlugin(id string, _ map[string]interface{}) error {
	l.loaded = append(l.loaded, id)
	return nil
}
func (l *suspendingLoader) UnloadPlugin(string) error  { return nil }
func (l *suspendingLoader) IsLoaded(string) bool       { return false }
func (l *suspendingLoader) IsSuspended(id string) bool { return l.suspended[id] }
func (l *suspendingLoader) Resume(id string)           { delete(l.suspended, id) }

// A suspended plugin shows on its card with its time; turning it on resumes and loads it.
func TestPluginsAPISuspendedPluginResumes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Installed: []config.InstalledPlugin{{ID: "@jan/slow", Version: "1.0.0", Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	engine := &budgetEngine{usage: map[string]lua.PluginUsage{
		"@jan/slow": {CPU: 6 * time.Second, Since: time.Now().Add(-2 * time.Minute), Overruns: 3, Suspended: true},
	}}
	loader := &suspendingLoader{suspended: map[string]bool{"@jan/slow": true}}
	s := &Server{serverDir: dir, luaExecutor: engine, pluginController: loader}

	view := s.installedPluginsView([]config.InstalledPlugin{{ID: "@jan/slow", Enabled: true}})
	if u := view[0].Usage; u == nil || !u.Suspended || u.Overruns != 3 || u.CPUmsPerMin < 2900 || u.CPUmsPerMin > 3100 {
		t.Fatalf("usage %+v", view[0].Usage)
	}

	r := gin.New()
	r.UseRawPath, r.UnescapePathValues = true, true
	r.POST("/api/plugins/:id/resume", s.handlePluginsAPIResume)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/plugins/@jan%2Fslow/resume", nil))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK || out["success"] != true {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	if loader.suspended["@jan/slow"] || len(engine.resumed) != 1 || len(loader.loaded) != 1 {
		t.Errorf("suspended %v, resumed %v, loaded %v", loader.suspended, engine.resumed, loader.loaded)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/plugins/@jan%2Fnone/resume", nil))
	if w.Code != http.StatusNotFound {
		t.Errorf("not installed: %d", w.Code)
	}
}
