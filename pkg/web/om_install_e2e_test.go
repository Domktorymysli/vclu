package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// End to end through the panel's own router, as a user installs a house: the project is
// previewed and imported, the myGrenton pages become dashboards that switch and dim through
// the CLU, the same project again changes nothing, and an om.lua that drops an object the
// user's Lua and the dashboards use is shown before it is imported, and listed after.
func TestE2E_ProjectToDashboards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	key, keyHash, _ := config.GenerateAPIKey()
	if err := writeTestConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "admin", PasswordHash: hash, APIKeyHash: keyHash}}); err != nil {
		t.Fatal(err)
	}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	sent := &sentList{}
	engine.SetCommandNoReplySender(sent.send)
	s := NewServer(0, "../../web", dir, &config.CLUDeviceConfig{}, &config.CLUKeys{}, engine)
	t.Cleanup(func() {
		if s.live != nil {
			s.live.shutdown()
		}
	})
	s.SetLuaReloader(&moduleReloader{eng: engine, dir: dir})

	do := func(method, path, contentType string, body []byte, authed bool) (int, map[string]any) {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		if authed {
			req.Header.Set("X-API-Key", key)
		}
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		var ans map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &ans)
		return w.Code, ans
	}
	file := func(name string, data []byte) (string, []byte) {
		var form bytes.Buffer
		mw := multipart.NewWriter(&form)
		fw, _ := mw.CreateFormFile("file", name)
		_, _ = fw.Write(data)
		_ = mw.Close()
		return mw.FormDataContentType(), form.Bytes()
	}
	upload := func(path, name string, data []byte) map[string]any {
		t.Helper()
		ct, body := file(name, data)
		code, ans := do("POST", path, ct, body, true)
		if code != http.StatusOK {
			t.Fatalf("%s %s: %d %v", path, name, code, ans)
		}
		return ans
	}
	action := func(dash, body string) {
		t.Helper()
		if code, ans := do("POST", "/api/dashboard/"+dash+"/action", "application/json", []byte(body), true); code != http.StatusOK || ans["success"] != true {
			t.Fatalf("%s %s: %d %v", dash, body, code, ans)
		}
	}

	// the test project, with a page of a dimmer as Object Manager 1.8 writes it (0-1)
	files, clu, out := mgProject(t)
	var iface map[string]any
	if err := json.Unmarshal(files["nodes/173/data.json"], &iface); err != nil {
		t.Fatal(err)
	}
	call := func(typ, extra string) map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(`{"callType":"`+typ+`","cluId":"`+clu+`","objectName":"`+out+`","index":"0"`+extra+`}`), &m)
		return m
	}
	iface["pages"] = append(iface["pages"].([]any), map[string]any{"id": 1700000000003, "label": "Living", "widgets": []any{
		map[string]any{"id": 7, "type": "DIMMER_V2", "label": "Lamp", "min": 0, "max": 1, "precision": 2, "object": map[string]any{
			"onAction":       call("PSEUDO_METHOD", `,"type":"ON","value":"1"`),
			"offAction":      call("PSEUDO_METHOD", `,"type":"OFF","value":"0"`),
			"setValueAction": call("PSEUDO_METHOD", `,"type":"CLICK","value":"$value$"`),
			"value":          call("ATTRIBUTE", ""),
		}},
	}})
	files["nodes/173/data.json"], _ = json.Marshal(iface)
	project := zipBytes(t, files)

	// the user's own Lua names the output, by its registry path
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules", "auto.lua"), []byte("local lamp = _registry:get(\""+clu+"."+out+"\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// preview: needs a login, stores nothing, every CLU new
	ct, body := file("house.omp", project)
	if code, _ := do("POST", "/api/om/preview", ct, body, false); code != http.StatusUnauthorized {
		t.Fatalf("preview without a login: %d", code)
	}
	before := dataTree(t, dir)
	pre := upload("/api/om/preview", "house.omp", project)
	sameTree(t, "the preview", before, dataTree(t, dir))
	clus, _ := pre["clus"].([]any)
	if pre["project"] != true || len(clus) == 0 {
		t.Fatalf("preview %v", pre)
	}
	for _, c := range clus {
		if c.(map[string]any)["change"] != "new" {
			t.Errorf("preview of an empty vclu: %v", c)
		}
	}

	// import, and the pages as dashboards with the require: Lua reloads with them
	if up := upload("/api/om/upload", "house.omp", project); up["success"] != true || len(up["brokenRefs"].([]any)) != 0 {
		t.Fatalf("upload %v", up)
	}
	code, ans := do("POST", "/api/grenton/dashboards", "application/json",
		[]byte(`{"interface":"if-1","pages":["1700000000001","1700000000003"],"addRequire":true}`), true)
	if code != http.StatusOK || ans["reloaded"] != true || ans["moduleErrors"] != nil || ans["pageErrors"] != nil {
		t.Fatalf("dashboards %d %v", code, ans)
	}
	if code, def := do("GET", "/api/dashboard/imported_1700000000003", "", nil, true); code != http.StatusOK || def["name"] != "Living" {
		t.Fatalf("dashboard %d %v", code, def)
	}

	// the dashboards reach the CLU: a toggle switches, the dimmer's 0-100% sends 0-1
	for _, tc := range []struct{ dash, body, want string }{
		{"imported_1700000000001", `{"widgetId":"w2","action":"toggle","value":true}`, out + ":set(0, 1)"},
		{"imported_1700000000003", `{"widgetId":"w7","action":"toggle","value":false}`, out + ":set(0, 0)"},
		{"imported_1700000000003", `{"widgetId":"w7_level","action":"set","value":40}`, out + ":set(0, 0.4)"},
	} {
		action(tc.dash, tc.body)
		if got := sent.take(); !strings.Contains(got, tc.want) {
			t.Errorf("%s %s sent %q, want %q", tc.dash, tc.body, got, tc.want)
		}
	}
	if _, err := engine.Execute(`_registry:get("` + clu + `.` + out + `"):updateState(0.25)`); err != nil {
		t.Fatal(err)
	}
	if code, vals := do("GET", "/api/dashboard/imported_1700000000003/values", "", nil, true); code != http.StatusOK ||
		!strings.Contains(mustJSON(vals), `"value":25`) {
		t.Errorf("the dimmer at 0.25 reads %d %v", code, vals)
	}

	// the same project again: nothing to confirm, the same files
	if again := upload("/api/om/preview", "house.omp", project); !allSame(again) || len(again["breaks"].([]any)) != 0 {
		t.Errorf("preview of the same project %v", again)
	}
	settled := dataTree(t, dir)
	upload("/api/om/upload", "house.omp", project)
	sameTree(t, "the same project again", settled, dataTree(t, dir))

	// an om.lua without the output: the preview names what would break, the upload lists it
	var stored string
	for _, f := range config.GetOMFiles(dir) {
		if strings.Contains(f.Filename, strings.TrimPrefix(clu, "CLU")) {
			stored = filepath.Join(config.GetOMFilesDir(dir), f.Filename)
		}
	}
	om, err := os.ReadFile(stored)
	if err != nil {
		t.Fatal(err)
	}
	without := regexp.MustCompile(`(?m)^\s*`+out+` = .*\n`).ReplaceAll(om, nil)
	if bytes.Equal(without, om) {
		t.Fatalf("%s is not in %s", out, stored)
	}
	pre = upload("/api/om/preview", "om.lua", without)
	breaks := mustJSON(pre["breaks"])
	for _, f := range []string{"auto.lua", "dashboards/imported/page_1700000000001.lua", "dashboards/imported/page_1700000000003.lua"} {
		if !strings.Contains(breaks, `"file":"`+f+`"`) {
			t.Errorf("the preview does not name %s: %s", f, breaks)
		}
	}
	if !strings.Contains(mustJSON(pre["clus"]), `"id":"`+out+`"`) {
		t.Errorf("the preview does not list %s as removed: %v", out, pre["clus"])
	}
	up := upload("/api/om/upload", "om.lua", without)
	if !strings.Contains(mustJSON(up["brokenRefs"]), `"file":"auto.lua"`) {
		t.Errorf("the upload does not list auto.lua: %v", up["brokenRefs"])
	}
	// the dashboard's toggle now names an object vclu has not: nothing is sent
	action("imported_1700000000001", `{"widgetId":"w2","action":"toggle","value":true}`)
	if got := sent.take(); got != "" {
		t.Errorf("sent to an object that is gone: %q", got)
	}
}

func allSame(preview map[string]any) bool {
	clus, _ := preview["clus"].([]any)
	for _, c := range clus {
		if c.(map[string]any)["change"] != "same" {
			return false
		}
	}
	return len(clus) > 0
}
