package web

import (
	"bytes"
	"encoding/json"
	"errors"
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

// An OM import, delete and regenerate reload the Lua runtime, so what was made from the old
// proxy (subscriptions, expose(), dashboards) is made again from the new one (#226).

const reloadOM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
mm_400000001 = OBJECT:new(2, 0x17d78401, 0x28, "17d78401") -- 400000001 02 01
 ROL1001 = OBJECT:new(24, mm_400000001, 0)
-- NAME_IO SALON_ZALUZJA=ROL1001
ROL1001:set(10, %d)
`

func reloadOMWith(lamel string) []byte {
	return []byte(strings.Replace(reloadOM, "%d", lamel, 1))
}

// engineReloader reloads as the server does: a fresh runtime, the proxies, then a module
// that keeps the roller, as a dashboard or expose() would.
type engineReloader struct {
	eng     *lua.LuaEngine
	dir     string
	reloads int
	err     error
}

func (r *engineReloader) Reload() error {
	r.reloads++
	if r.err != nil {
		return r.err
	}
	if err := r.eng.Reinitialize("VCLU"); err != nil {
		return err
	}
	if err := r.eng.LoadProxyFiles(config.GetOMFilesDir(r.dir)); err != nil {
		return err
	}
	_, err := r.eng.Execute(`_kept = _registry["CLU212345678.ROL1001"]`)
	return err
}

func (r *engineReloader) LastModuleLoad() []lua.ModuleLoadResult {
	return []lua.ModuleLoadResult{{Name: "ok", OK: true}}
}

func (r *engineReloader) LastProxyLoad() []string { return r.eng.LastProxyLoad() }

func omServer(t *testing.T) (*Server, *engineReloader) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	eng := lua.NewLuaEngine()
	t.Cleanup(eng.Close)
	r := &engineReloader{eng: eng, dir: dir}
	return &Server{serverDir: dir, luaExecutor: eng, luaReloader: r}, r
}

func omUpload(t *testing.T, s *Server, name string, content []byte) map[string]any {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	f, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/om/upload", &body)
	c.Request.Header.Set("Content-Type", w.FormDataContentType())
	s.handleAPIOMUpload(c)
	return omAnswer(t, rec)
}

func omAnswer(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var ans map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("answer %d %s: %v", rec.Code, rec.Body, err)
	}
	return ans
}

func kept(t *testing.T, s *Server) string {
	t.Helper()
	out, err := s.luaExecutor.Execute(`return tostring(_kept and _kept._hasTilt) .. " " ..
		(_kept and table.concat(_kept:subscribeFeatures(), ",") or "-")`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOMUploadReloadsRuntime(t *testing.T) {
	s, r := omServer(t)
	if ans := omUpload(t, s, "om.lua", reloadOMWith("0")); ans["reloaded"] != true {
		t.Fatalf("first import: %v", ans)
	}
	if got := kept(t, s); got != "false 0,7" {
		t.Errorf("without slats: %s", got)
	}
	// the same CLU again, now with slats: no reload call of its own
	if ans := omUpload(t, s, "om.lua", reloadOMWith("1500")); ans["reloaded"] != true || ans["reloadError"] != nil {
		t.Fatalf("second import: %v", ans)
	}
	if got := kept(t, s); got != "true 0,7,8" {
		t.Errorf("with slats: %s", got)
	}
	if r.reloads != 2 {
		t.Errorf("%d reloads", r.reloads)
	}
}

func TestOMZipReloadsOnce(t *testing.T) {
	s, r := omServer(t)
	second := bytes.ReplaceAll(reloadOMWith("0"), []byte("212345678"), []byte("212345679"))
	ans := omUpload(t, s, "pack.zip", zipBytes(t, map[string][]byte{
		"CLU212345678/om.lua": reloadOMWith("1500"),
		"CLU212345679/om.lua": second,
	}))
	if ans["imported"] != 2.0 || ans["reloaded"] != true {
		t.Fatalf("pack: %v", ans)
	}
	if r.reloads != 1 {
		t.Errorf("%d reloads for two CLUs", r.reloads)
	}
	if got := kept(t, s); got != "true 0,7,8" {
		t.Errorf("roller: %s", got)
	}
}

func TestOMDeleteAndRegenerateReload(t *testing.T) {
	s, r := omServer(t)
	omUpload(t, s, "om.lua", reloadOMWith("1500"))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s.handleAPIOMRegenerate(c)
	if ans := omAnswer(t, rec); ans["regenerated"] != 1.0 || ans["reloaded"] != true {
		t.Errorf("regenerate: %v", ans)
	}

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "serial", Value: "212345678"}}
	s.handleAPIOMDelete(c)
	if ans := omAnswer(t, rec); ans["success"] != true || ans["reloaded"] != true {
		t.Errorf("delete: %v", ans)
	}
	if got := kept(t, s); got != "nil -" {
		t.Errorf("after delete the roller is still there: %s", got)
	}
	if r.reloads != 3 {
		t.Errorf("%d reloads", r.reloads)
	}

	// a failed reload is in the answer; the file is saved all the same
	r.err = errors.New("user.lua: boom")
	if ans := omUpload(t, s, "om.lua", reloadOMWith("0")); ans["success"] != true || ans["reloaded"] != false || ans["reloadError"] != "user.lua: boom" {
		t.Errorf("failed reload: %v", ans)
	}
}

// A user module that fails to load after the reload is named in the answer.
type failingModules struct{ engineReloader }

func (f *failingModules) LastModuleLoad() []lua.ModuleLoadResult {
	return []lua.ModuleLoadResult{{Name: "rolety", OK: false, Error: "attempt to index nil"}, {Name: "ok", OK: true}}
}

func TestOMReloadNamesFailedModules(t *testing.T) {
	s, _ := omServer(t)
	s.luaReloader = &failingModules{engineReloader{eng: s.luaExecutor.(*lua.LuaEngine), dir: s.serverDir}}
	ans := omUpload(t, s, "om.lua", reloadOMWith("0"))
	errs, _ := ans["moduleErrors"].([]any)
	if ans["reloaded"] != true || len(errs) != 1 || errs[0] != "modules/rolety: attempt to index nil" {
		t.Errorf("module errors: %v", ans)
	}
}

// A proxy that fails in the reload leaves its CLU without objects: the answer says so.
func TestOMReloadReportsFailedProxy(t *testing.T) {
	s, _ := omServer(t)
	omUpload(t, s, "om.lua", reloadOMWith("0"))
	bad := filepath.Join(config.GetOMFilesDir(s.serverDir), "proxy_999.lua")
	if err := os.WriteFile(bad, []byte(`error("boom")`), 0644); err != nil {
		t.Fatal(err)
	}
	ans := omUpload(t, s, "om.lua", reloadOMWith("1500"))
	errs, _ := ans["proxyErrors"].([]any)
	if ans["success"] != false || len(errs) != 1 || !strings.HasPrefix(errs[0].(string), "proxy_999.lua: ") {
		t.Errorf("failed proxy: %v", ans)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if ans := omUpload(t, s, "om.lua", reloadOMWith("1500")); ans["success"] != true || ans["proxyErrors"] != nil {
		t.Errorf("after the fix: %v", ans)
	}
}
