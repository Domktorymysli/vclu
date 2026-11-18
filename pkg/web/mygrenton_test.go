package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	glua "github.com/yuin/gopher-lua"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

// The myGrenton interface of an imported project (#219) and the dashboards made from it (#220).

const codeLabel = "\"); os.exit(1) --]] \\ \x01 zażółć"

// mgProject is the test project with an interface naming its first output and a script.
func mgProject(t *testing.T) (files map[string][]byte, clu, out string) {
	t.Helper()
	dir := filepath.Join("testdata", "omproject", "om-1.8.0", "project")
	data := zipDir(t, dir)
	p, err := omproject.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	var script string
	for _, c := range p.CLUs {
		for _, o := range c.Objects {
			if o.Type == "DOUT" && out == "" {
				clu, out = c.ID, o.ID
			}
		}
		if clu == c.ID {
			for _, sc := range c.Scripts {
				if len(sc.Params) == 0 && script == "" {
					script = sc.Name
				}
			}
		}
	}
	if out == "" || script == "" {
		t.Fatalf("the test project has no output %q or script %q", out, script)
	}
	files = map[string][]byte{}
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		files[filepath.ToSlash(rel)], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	call := func(typ, extra string) string {
		return fmt.Sprintf(`{"callType":%q,"cluId":%q,"objectName":%q,"index":"0"%s}`, typ, clu, out, extra)
	}
	label, _ := json.Marshal(codeLabel)
	iface := fmt.Sprintf(`{"id":"if-1","label":"Home","pages":[
	  {"id":1700000000001,"label":"Hall","widgets":[
	    {"id":2,"type":"ON_OFF","buttonBistable":{"label":"Light","state":%s,"actionOn":%s,"actionOff":%s}},
	    {"id":3,"type":"SCENE","buttonMonostable":{"label":%s,"actionClick":{"callType":"SCRIPT","cluId":%q,"objectName":%q,"index":null,"value":"\"a\",1"}}},
	    {"id":4,"type":"VALUE","value":{"label":"Dim","min":0,"max":100,"state":%s,"actionClick":%s}},
	    {"id":5,"type":"SCHEDULER","label":"Week"}]},
	  {"id":1700000000002,"label":"Garden","widgets":[
	    {"id":6,"type":"TEXT","label":%s,"text":%s}]}]}`,
		call("ATTRIBUTE", ""), call("PSEUDO_METHOD", `,"type":"ON","value":"1"`), call("PSEUDO_METHOD", `,"type":"OFF","value":"0"`),
		label, clu, script, call("ATTRIBUTE", ""), call("PSEUDO_METHOD", `,"value":"$value$"`), label, call("ATTRIBUTE", ""))
	files["nodes/173/data.json"] = []byte(iface)
	return files, clu, out
}

// moduleReloader reloads as the server does: a fresh runtime, the proxies, the user's modules.
type moduleReloader struct {
	eng     *lua.LuaEngine
	dir     string
	reloads int
	modules []lua.ModuleLoadResult
}

func (r *moduleReloader) Reload() error {
	r.reloads++
	if err := r.eng.Reinitialize("VCLU"); err != nil {
		return err
	}
	if err := r.eng.LoadProxyFiles(config.GetOMFilesDir(r.dir)); err != nil {
		return err
	}
	r.modules = r.eng.LoadUserModules(r.dir)
	return nil
}

func (r *moduleReloader) LastModuleLoad() []lua.ModuleLoadResult { return r.modules }

// sentList records the commands sent to the CLUs.
type sentList struct {
	mu   sync.Mutex
	list []string
}

func (s *sentList) send(ip, script string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.list = append(s.list, script)
	return nil
}

func (s *sentList) take() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := strings.Join(s.list, ";")
	s.list = nil
	return out
}

func mgServer(t *testing.T) (*Server, *moduleReloader, *sentList) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	eng := lua.NewLuaEngine()
	t.Cleanup(eng.Close)
	sent := &sentList{}
	eng.SetCommandNoReplySender(sent.send)
	r := &moduleReloader{eng: eng, dir: dir}
	return &Server{serverDir: dir, luaExecutor: eng, luaReloader: r}, r, sent
}

func dashCall(t *testing.T, s *Server, method, body string) (int, map[string]any) {
	t.Helper()
	r := gin.New()
	r.GET("/api/grenton/dashboards", s.handleAPIGrentonDashboards)
	r.POST("/api/grenton/dashboards", s.handleAPIGrentonDashboardsCreate)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/grenton/dashboards", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var ans map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	return rec.Code, ans
}

func TestInterfaceStoredOnImport(t *testing.T) {
	files, _, _ := mgProject(t)
	s := &Server{serverDir: t.TempDir()}
	stored := filepath.Join(s.myGrentonPath(), "if-1.json")

	if code, body := uploadFile(t, s, "house.omp", zipBytes(t, files)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("not stored: %v", err)
	}
	// an om.lua leaves it alone
	if code, body := uploadFile(t, s, "om.lua", []byte(namesOMLua)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(stored); err != nil {
		t.Fatalf("om.lua removed it: %v", err)
	}
	// a pack that carries the project stores it too; one without a project removes it
	if code, body := uploadFile(t, s, "pack.zip", zipBytes(t, map[string][]byte{
		"CLU212345678/om.lua": []byte(namesOMLua),
	})); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("a pack without a project kept it: %v", err)
	}
	if code, body := uploadFile(t, s, "pack.zip", zipBytes(t, map[string][]byte{
		"CLU212345678/om.lua": []byte(namesOMLua),
		"project.zip":         zipBytes(t, files),
	})); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(stored); err != nil {
		t.Errorf("the pack's project was not stored: %v", err)
	}
	// a project without an interface removes it
	delete(files, "nodes/173/data.json")
	if code, body := uploadFile(t, s, "house.omp", zipBytes(t, files)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Errorf("a project without an interface kept it: %v", err)
	}
}

func TestDashboardsCreated(t *testing.T) {
	files, _, out := mgProject(t)
	s, r, sent := mgServer(t)
	if code, body := uploadFile(t, s, "house.omp", zipBytes(t, files)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}

	code, list := dashCall(t, s, http.MethodGet, "")
	ifs, _ := list["interfaces"].([]any)
	if code != http.StatusOK || len(ifs) != 1 || list["required"] != false {
		t.Fatalf("%d %v", code, list)
	}
	pages := ifs[0].(map[string]any)["pages"].([]any)
	hall := pages[0].(map[string]any)
	kinds := []string{}
	for _, w := range hall["widgets"].([]any) {
		kinds = append(kinds, w.(map[string]any)["kind"].(string))
	}
	if strings.Join(kinds, ",") != "toggle,button,slider" || hall["state"] != "none" || len(hall["leftOut"].([]any)) != 1 {
		t.Errorf("hall %v", hall)
	}

	// without the require: written, not loaded
	code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001","1700000000002"]}`)
	if code != http.StatusOK || ans["required"] != false || ans["reloaded"] != nil {
		t.Fatalf("%d %v", code, ans)
	}
	dir := s.grentonDashPath()
	for _, f := range []string{"page_1700000000001.lua", "page_1700000000002.lua", "init.lua"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		// the files compile and keep the label as it was
		if _, err := glua.NewState().LoadString(string(b)); err != nil {
			t.Errorf("%s does not compile: %v\n%s", f, err, b)
		}
	}

	// asked again with the require: it is added once, Lua reloads and the dashboards work
	code, ans = dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"addRequire":true}`)
	if code != http.StatusConflict || len(ans["ask"].([]any)) != 1 {
		t.Fatalf("written again without asking: %d %v", code, ans)
	}
	code, ans = dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"addRequire":true,"replace":true}`)
	if code != http.StatusOK || ans["requireAdded"] != true || ans["reloaded"] != true || ans["moduleErrors"] != nil || ans["pageErrors"] != nil {
		t.Fatalf("%d %v", code, ans)
	}
	code, ans = dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"addRequire":true,"replace":true}`)
	if code != http.StatusOK || ans["requireAdded"] != false {
		t.Fatalf("%d %v", code, ans)
	}
	init, _ := os.ReadFile(filepath.Join(s.serverDir, "modules", "init.lua"))
	if strings.Count(string(init), grentonRequire) != 1 {
		t.Errorf("init.lua:\n%s", init)
	}
	if r.reloads == 0 {
		t.Error("no reload")
	}

	// both pages, one tab group; the label reads back unchanged, nothing ran
	res, err := s.luaExecutor.Execute(`EXITED = false; os.exit = function() EXITED = true end
		local d = Dashboard.API.getDefinition("grenton_page_1700000000002")
		return d`)
	if err != nil {
		t.Fatal(err)
	}
	var def struct {
		Name   string `json:"name"`
		Groups []struct {
			Name string
			Tabs []struct{ ID string }
		} `json:"groupTabs"`
		Widgets []map[string]any `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(res), &def); err != nil {
		t.Fatal(err, res)
	}
	if len(def.Groups) != 1 || def.Groups[0].Name != "Home" || len(def.Groups[0].Tabs) != 2 || len(def.Widgets) == 0 || def.Widgets[len(def.Widgets)-1]["label"] != codeLabel {
		t.Errorf("garden %s", res)
	}
	if got, _ := s.luaExecutor.Execute(`return tostring(EXITED)`); got != "false" {
		t.Error("the label ran")
	}

	// the toggle switches, the slider sets, the scene runs its script
	for _, tc := range []struct{ action, want string }{
		{`"mg2", "toggle", true`, out + `:set(0, 1)`},
		{`"mg4", "set", 40`, out + `:set(0, 40)`},
		{`"mg3", "tap"`, `("a", 1)`},
	} {
		if _, err := s.luaExecutor.Execute(`return Dashboard.API.executeAction("grenton_page_1700000000001", ` + tc.action + `)`); err != nil {
			t.Fatal(err)
		}
		if got := sent.take(); !strings.Contains(got, tc.want) {
			t.Errorf("%s sent %q, want %q", tc.action, got, tc.want)
		}
	}
}

// A file vclu did not write, or wrote and someone changed, is the user's: never replaced.
func TestDashboardsKeepTheUsersFiles(t *testing.T) {
	files, _, _ := mgProject(t)
	s, _, _ := mgServer(t)
	if code, body := uploadFile(t, s, "house.omp", zipBytes(t, files)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"]}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, ans)
	}
	page := filepath.Join(s.grentonDashPath(), "page_1700000000001.lua")
	b, _ := os.ReadFile(page)
	edited := strings.Replace(string(b), "s:build()", "s:poll(5)\ns:build()", 1)
	if err := os.WriteFile(page, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	if st := fileState(page); st != fileEdited {
		t.Fatalf("edited file is %s", st)
	}
	code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"replace":true}`)
	if code != http.StatusConflict || len(ans["kept"].([]any)) != 1 {
		t.Errorf("%d %v", code, ans)
	}
	if b, _ := os.ReadFile(page); string(b) != edited {
		t.Error("the edited file was replaced")
	}

	// the user's own file
	other := filepath.Join(s.grentonDashPath(), "page_1700000000002.lua")
	if err := os.WriteFile(other, []byte("-- mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000002"],"replace":true}`); code != http.StatusConflict {
		t.Errorf("%d %v", code, ans)
	}
	if b, _ := os.ReadFile(other); string(b) != "-- mine\n" {
		t.Error("the user's file was replaced")
	}

	// a flat dashboards/grenton.lua would be loaded instead of the directory
	flat := filepath.Join(s.serverDir, "modules", "dashboards", "grenton.lua")
	if err := os.WriteFile(flat, []byte("-- mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"replace":true}`); code != http.StatusConflict || ans["flatFile"] == nil {
		t.Errorf("%d %v", code, ans)
	}
	if _, list := dashCall(t, s, http.MethodGet, ""); list["flatFile"] != "modules/dashboards/grenton.lua" {
		t.Errorf("list %v", list)
	}
}

// init.lua loads the pages of every interface; a user's init.lua is left alone and the
// lines to add are given; page files of pages no longer imported are listed.
func TestDashboardsInitAndOrphans(t *testing.T) {
	files, _, _ := mgProject(t)
	second := strings.NewReplacer(`"id":"if-1"`, `"id":"if-2"`, "17000000000", "18000000000").Replace(string(files["nodes/173/data.json"]))
	files["nodes/174/data.json"] = []byte(second)
	s, _, _ := mgServer(t)
	if code, body := uploadFile(t, s, "house.omp", zipBytes(t, files)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"]}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, ans)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-2","pages":["1800000000002"]}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, ans)
	}
	initPath := filepath.Join(s.grentonDashPath(), "init.lua")
	b, _ := os.ReadFile(initPath)
	if !strings.Contains(string(b), "page_1700000000001") || !strings.Contains(string(b), "page_1800000000002") {
		t.Errorf("init.lua lost a page:\n%s", b)
	}

	// a page file vclu does not know is loaded too
	if err := os.WriteFile(filepath.Join(s.grentonDashPath(), "page_42.lua"), []byte("-- old\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-2","pages":["1800000000002"],"replace":true}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, ans)
	}
	b, _ = os.ReadFile(initPath)
	if !strings.Contains(string(b), "page_42") || !strings.Contains(string(b), "page_1700000000001") {
		t.Errorf("init.lua:\n%s", b)
	}

	// the user drops a line from init.lua: it is theirs now, creating still works
	edited := strings.Replace(string(b), `MyGrenton.load("dashboards/grenton/page_1700000000001")`+"\n", "", 1)
	if err := os.WriteFile(initPath, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000002"]}`)
	lines, _ := ans["initLines"].([]any)
	if code != http.StatusOK || ans["initKept"] != true || len(lines) != 1 || lines[0] != `MyGrenton.load("dashboards/grenton/page_1700000000002")` {
		t.Fatalf("%d %v", code, ans)
	}
	if b, _ := os.ReadFile(initPath); string(b) != edited {
		t.Error("the user's init.lua was replaced")
	}

	// a page no longer imported is listed
	_, list := dashCall(t, s, http.MethodGet, "")
	orphans, _ := list["orphans"].([]any)
	if len(orphans) != 1 || orphans[0].(map[string]any)["file"] != "page_42.lua" {
		t.Errorf("orphans %v", list["orphans"])
	}
}

// A require inside a block comment does not load the dashboards.
func TestRequireStateIgnoresComments(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	path := filepath.Join(s.serverDir, "modules", "init.lua")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]bool{
		grentonRequire + "\n":                           true,
		"local x = 1\n  require 'dashboards.grenton'\n": true,
		"-- " + grentonRequire + "\n":                   false,
		"--[[\n" + grentonRequire + "\n]]\n":            false,
		"--[==[\n" + grentonRequire + "\n]] ]==]\n":     false,
		"--[[ a ]]\n" + grentonRequire + "\n":           true,
	} {
		if err := os.WriteFile(path, []byte(code), 0644); err != nil {
			t.Fatal(err)
		}
		if got := s.requireState(); got != want {
			t.Errorf("%q: %v", code, got)
		}
	}
}

// A page the user broke is reported after the reload; the other pages still load.
func TestDashboardsReportBrokenPage(t *testing.T) {
	files, _, _ := mgProject(t)
	s, _, _ := mgServer(t)
	if code, body := uploadFile(t, s, "house.omp", zipBytes(t, files)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"addRequire":true}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, ans)
	}
	page := filepath.Join(s.grentonDashPath(), "page_1700000000001.lua")
	if err := os.WriteFile(page, []byte(`error("my edit")`), 0644); err != nil {
		t.Fatal(err)
	}
	code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000002"]}`)
	errs, _ := ans["pageErrors"].([]any)
	if code != http.StatusOK || len(errs) != 1 || !strings.Contains(errs[0].(string), "page_1700000000001: ") ||
		!strings.Contains(errs[0].(string), "my edit") || ans["moduleErrors"] != nil {
		t.Fatalf("%d %v", code, ans)
	}
	if out, err := s.luaExecutor.Execute(`return tostring(_dashboards["grenton_page_1700000000002"] ~= nil)`); err != nil || out != "true" {
		t.Errorf("the other page: %s %v", out, err)
	}
}
