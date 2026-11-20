package web

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

var reTimes = regexp.MustCompile(`"(uploadedAt|lastModified)":\s*"[^"]*"`)

// dataTree is every file under dir with its content; times and the backups of om files are
// left out.
func dataTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path == filepath.Join(dir, filepath.FromSlash(omBackupsDir)) {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[rel] = reTimes.ReplaceAllString(string(b), `"$1": ""`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(t *testing.T, step string, want, got map[string]string) {
	t.Helper()
	var diff []string
	for p, w := range want {
		if g, ok := got[p]; !ok {
			diff = append(diff, "gone: "+p)
		} else if g != w {
			diff = append(diff, "changed: "+p)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			diff = append(diff, "new: "+p)
		}
	}
	sort.Strings(diff)
	if len(diff) > 0 {
		t.Errorf("%s:\n%s", step, strings.Join(diff, "\n"))
	}
}

// Importing again never changes what an import left: the same project any number of times,
// after a diagnostic pack of it, after another project, and with dashboards made from it.
// Nothing is left behind on the way and Lua reloads cleanly every time.
func TestRepeatedImportsSettle(t *testing.T) {
	mg, _, _ := mgProject(t)
	house := zipBytes(t, mg)
	pack := zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "pack"))
	other := zipDir(t, filepath.Join("testdata", "omproject", "om-1.3.5", "project"))
	s, _, _ := mgServer(t)

	upload := func(name string, data []byte) {
		t.Helper()
		code, body := uploadFile(t, s, name, data, "removeMissing", "true")
		var ans map[string]any
		if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK || ans["success"] != true {
			t.Fatalf("%s: %d %s", name, code, body)
		}
		for _, k := range []string{"errors", "luaError", "moduleErrors", "proxyErrors"} {
			if v, ok := ans[k]; ok && v != nil {
				if l, isList := v.([]any); !isList || len(l) > 0 {
					t.Fatalf("%s: %s = %v", name, k, v)
				}
			}
		}
		if out, err := s.luaExecutor.Execute(`return "ok"`); err != nil || out != "ok" {
			t.Fatalf("%s: Lua after the import: %s %v", name, out, err)
		}
		// what a write cut short would leave
		_ = filepath.WalkDir(s.serverDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			b := d.Name()
			if strings.HasSuffix(b, ".tmp") || strings.HasSuffix(b, ".old") || strings.HasPrefix(b, ".om-") || strings.HasPrefix(b, ".mygrenton-") {
				t.Errorf("%s: left %s", name, path)
			}
			return nil
		})
	}

	upload("house.omp", house)
	first := dataTree(t, s.serverDir)
	for i := 0; i < 3; i++ {
		upload("house.omp", house)
	}
	sameTree(t, "the same project again", first, dataTree(t, s.serverDir))

	upload("pack.zip", pack)
	upload("house.omp", house)
	sameTree(t, "after its diagnostic pack", first, dataTree(t, s.serverDir))

	upload("other.omp", other)
	upload("house.omp", house)
	sameTree(t, "after another project", first, dataTree(t, s.serverDir))

	// dashboards made from it: further imports leave them as they are and they keep loading
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"],"addRequire":true}`); code != http.StatusOK {
		t.Fatalf("%d %v", code, ans)
	}
	made := dataTree(t, s.serverDir)
	for i := 0; i < 3; i++ {
		upload("house.omp", house)
	}
	sameTree(t, "with dashboards", made, dataTree(t, s.serverDir))
	if out, err := s.luaExecutor.Execute(`return tostring(_dashboards["imported_1700000000001"] ~= nil)`); err != nil || out != "true" {
		t.Errorf("the dashboard after the imports: %s %v", out, err)
	}
	if code, ans := dashCall(t, s, http.MethodPost, `{"interface":"if-1","pages":["1700000000001"]}`); code != http.StatusOK {
		t.Errorf("the same page again: %d %v", code, ans)
	}
	sameTree(t, "the same page again", made, dataTree(t, s.serverDir))
}

// A project is the whole house: asked to, its import takes out the CLUs it no longer has,
// says which, and leaves this vCLU's own file. An import with errors takes nothing out, nor
// one not asked to; the files taken out are in the backup.
func TestProjectRemovesCLUsNotInIt(t *testing.T) {
	house := zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "project"))
	other := zipDir(t, filepath.Join("testdata", "omproject", "om-1.3.5", "project"))
	s := &Server{serverDir: withSerial(t, 212345678)}
	if code, body := uploadFile(t, s, "om.lua", []byte(namesOMLua)); code != http.StatusOK {
		t.Fatalf("own om.lua: %d %s", code, body)
	}
	if code, body := uploadFile(t, s, "other.omp", other); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	before := registered(t, s.serverDir)

	// with errors: nothing goes
	s.luaExecutor = failingLua{}
	_, body := uploadFile(t, s, "house.omp", house, "removeMissing", "true")
	var ans map[string]any
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	if r, _ := ans["removed"].([]any); len(r) != 0 || ans["success"] == true {
		t.Fatalf("removed after a failed import: %s", body)
	}
	for serial := range before {
		if _, ok := registered(t, s.serverDir)[serial]; !ok {
			t.Errorf("CLU%d gone after a failed import", serial)
		}
	}

	// not asked to: nothing goes, the answer lists them
	s.luaExecutor = nil
	_, body = uploadFile(t, s, "house.omp", house)
	ans = nil
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	if r, _ := ans["removed"].([]any); len(r) != 0 || ans["success"] != true {
		t.Fatalf("removed without removeMissing: %s", body)
	}
	kept, _ := ans["notInProject"].([]any)
	for serial := range before {
		if _, ok := registered(t, s.serverDir)[serial]; !ok {
			t.Errorf("CLU%d gone without removeMissing", serial)
		}
	}

	_, body = uploadFile(t, s, "house.omp", house, "removeMissing", "true")
	ans = nil
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	removed, _ := ans["removed"].([]any)
	if len(kept) != len(removed) {
		t.Errorf("notInProject %v, removed %v", kept, removed)
	}
	after := registered(t, s.serverDir)
	if _, ok := after[212345678]; !ok {
		t.Error("this vCLU's own file was removed")
	}
	var want []string
	for serial := range before {
		if _, ok := after[serial]; !ok {
			want = append(want, fmt.Sprintf("CLU%d", serial))
		}
	}
	if len(want) == 0 || len(removed) != len(want) {
		t.Fatalf("removed %v, gone %v", removed, want)
	}
	backup, _ := ans["backup"].(string)
	for _, r := range removed {
		om := "om_" + strings.TrimPrefix(r.(string), "CLU") + ".lua"
		if _, err := os.Stat(filepath.Join(config.GetOMFilesDir(s.serverDir), om)); !os.IsNotExist(err) {
			t.Errorf("%s: om file %v", r, err)
		}
		if backup == "" {
			t.Fatalf("no backup: %s", body)
		}
		if _, err := os.Stat(filepath.Join(s.serverDir, filepath.FromSlash(backup), om)); err != nil {
			t.Errorf("%s not in the backup: %v", om, err)
		}
	}
	// the same project again removes nothing more
	_, body = uploadFile(t, s, "house.omp", house, "removeMissing", "true")
	if strings.Contains(string(body), `"removed":["`) {
		t.Errorf("removed on the same project: %s", body)
	}
}
