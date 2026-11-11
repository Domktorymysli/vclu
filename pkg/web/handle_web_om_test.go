package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type zipUploadAnswer struct {
	Imported    int      `json:"imported"`
	SkippedList []string `json:"skippedList"`
	Errors      []string `json:"errors"`
	Results     []struct {
		CLUName string `json:"cluName"`
	} `json:"results"`
}

func uploadPack(t *testing.T, s *Server, pack []byte) zipUploadAnswer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	s.handleZipUpload(c, pack, "pack.zip")
	var ans zipUploadAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("answer %d %s: %v", rec.Code, rec.Body, err)
	}
	return ans
}

// A diagnostic pack names CLUs in errors by their project name, read from the
// Object Manager project it carries, and reports an empty om.lua as such.
func TestZipUploadNamesCLUInErrors(t *testing.T) {
	// As OM writes it: a CLU in full where XStream first meets it, a reference after.
	systemXML := `<object-stream><TreeObject id="1"><children id="2">
          <TreeObject id="3"><specificObject class="HardwarePeripheryCLU" id="4"><clu id="14">
                    <name>Alarm</name>
                    <ipAddress>192.168.1.4</ipAddress>
                    <nameOnCLU>CLU511000000</nameOnCLU>
                    <iosList id="15"/>
                    <modulesList class="linked-list" id="16"/>
                  </clu></specificObject></TreeObject>
          <TreeObject id="5"><specificObject class="CLU" reference="14"/></TreeObject>
          <TreeObject id="6"><specificObject class="CLU" id="15546">
              <name>Dom1</name>
              <ipAddress>192.168.1.5</ipAddress>
              <nameOnCLU>CLU220000000</nameOnCLU>
              <iosList id="15547"/>
              <modulesList class="linked-list" id="15548"/>
          </specificObject></TreeObject>
        </children></TreeObject></object-stream>`
	project := zipBytes(t, map[string][]byte{"system.xml": []byte(systemXML)})
	pack := zipBytes(t, map[string][]byte{
		"project.zip":         project,
		"CLU511000000/om.lua": {},
		"CLU220000000/om.lua": []byte("CLU220000000 = OBJECT:new(0, 0xC0A80005, \"CLU220000000\")\n" +
			"-- NAME_CLU Dom1=CLU220000000\n"),
	})

	ans := uploadPack(t, &Server{serverDir: t.TempDir()}, pack)

	if ans.Imported != 1 || len(ans.Results) != 1 || ans.Results[0].CLUName != "Dom1" {
		t.Errorf("imported %d %+v", ans.Imported, ans.Results)
	}
	if len(ans.Errors) != 1 {
		t.Fatalf("errors %q", ans.Errors)
	}
	if e := ans.Errors[0]; !strings.HasPrefix(e, "Alarm (CLU511000000): ") || !strings.Contains(e, "om.lua is empty") {
		t.Errorf("error %q", e)
	}
}

// Without a project in the pack the error falls back to the bare CLU ID.
func TestZipUploadErrorWithoutProject(t *testing.T) {
	pack := zipBytes(t, map[string][]byte{"CLU511000000/om.lua": {}})

	ans := uploadPack(t, &Server{serverDir: t.TempDir()}, pack)

	if len(ans.Errors) != 1 || !strings.HasPrefix(ans.Errors[0], "CLU511000000: om.lua is empty") {
		t.Errorf("errors %q", ans.Errors)
	}
}

// zipDir zips a directory of testdata, its paths relative to the directory.
func zipDir(t *testing.T, dir string) []byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		files[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return zipBytes(t, files)
}

type projectAnswer struct {
	IsProject   bool     `json:"isProject"`
	AgentDocs   string   `json:"agentDocs"`
	OMVersion   string   `json:"omVersion"`
	Imported    int      `json:"imported"`
	SkippedList []string `json:"skippedList"`
	Errors      []string `json:"errors"`
	Warnings    []string `json:"warnings"`
}

// uploadFile posts a file to /api/om/upload, as the page does. A small
// MaxMultipartMemory makes the request spool the upload to a temporary file;
// without net/http's server around it, only the handler can remove it.
func uploadFile(t *testing.T, s *Server, name string, data []byte) (int, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.MaxMultipartMemory = 1 << 10
	r.POST("/api/om/upload", s.handleAPIOMUpload)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/om/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// registered is what an import left: the .vclu.json entries and the proxies,
// by serial.
func registered(t *testing.T, dir string) map[uint64]string {
	t.Helper()
	out := map[uint64]string{}
	for _, f := range config.GetOMFiles(dir) {
		proxy, err := os.ReadFile(filepath.Join(config.GetOMFilesDir(dir), fmt.Sprintf("proxy_%d.lua", f.Serial)))
		if err != nil {
			t.Fatal(err)
		}
		f.UploadedAt = ""
		out[f.Serial] = fmt.Sprintf("%+v\n%s", f, proxy)
	}
	return out
}

func withSerial(t *testing.T, serial uint64) string {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(`{"device": {"serialNumber": %d}}`, serial)
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The fixtures are projects of Object Manager 1.3.5 and 1.8.0, anonymised,
// with the om.lua the CLUs got from them (pack/). The project registers the
// same CLUs, objects and proxies as the diagnostic pack built from it.
func TestProjectUploadMatchesDiagnosticPack(t *testing.T) {
	for _, tc := range []struct {
		dir, version string
		clus         int
	}{
		{"om-1.3.5", "204201", 5},
		{"om-1.8.0", "1.8.0_2301", 6},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			base := filepath.Join("testdata", "omproject", tc.dir)
			pack := zipDir(t, filepath.Join(base, "pack"))
			omp := zipDir(t, filepath.Join(base, "project"))

			fromPack := &Server{serverDir: t.TempDir()}
			if code, body := uploadFile(t, fromPack, "pack.zip", pack); code != http.StatusOK {
				t.Fatalf("pack: %d %s", code, body)
			}
			fromProject := &Server{serverDir: t.TempDir()}
			code, body := uploadFile(t, fromProject, "house.omp", omp)
			if code != http.StatusOK {
				t.Fatalf("project: %d %s", code, body)
			}
			var ans projectAnswer
			if err := json.Unmarshal(body, &ans); err != nil {
				t.Fatal(err)
			}
			if !ans.IsProject || ans.AgentDocs != "ai/om" || ans.OMVersion != tc.version || ans.Imported != tc.clus || len(ans.Errors) != 0 {
				t.Errorf("answer %+v", ans)
			}

			want := registered(t, fromPack.serverDir)
			got := registered(t, fromProject.serverDir)
			if len(want) == 0 {
				t.Fatal("the pack registered nothing")
			}
			for serial, w := range want {
				if got[serial] != w {
					t.Errorf("CLU%d from the project:\n%s\nfrom the pack:\n%s", serial, got[serial], w)
				}
			}
			if len(got) != tc.clus {
				t.Errorf("project registered %d CLUs, want %d", len(got), tc.clus)
			}
		})
	}
}

// vCLU skips itself in a project as it does in a pack.
func TestProjectUploadSkipsThisVCLU(t *testing.T) {
	omp := zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "project"))
	s := &Server{serverDir: withSerial(t, 220000005)}

	_, body := uploadFile(t, s, "house.omp", omp)
	var ans projectAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	if ans.Imported != 5 || len(ans.SkippedList) != 1 || ans.SkippedList[0] != "Vclu (CLU220000005) (this vCLU)" {
		t.Errorf("answer %+v", ans)
	}
	if _, ok := registered(t, s.serverDir)[220000005]; ok {
		t.Error("this vCLU is registered")
	}
}

// The agent reads the event bindings and scripts in ai/om/, from a project
// and from the project inside a diagnostic pack; an import replaces the files.
func TestProjectUploadWritesAgentDocs(t *testing.T) {
	base := filepath.Join("testdata", "omproject", "om-1.8.0")
	project := zipDir(t, filepath.Join(base, "project"))
	packFiles := map[string][]byte{"house.zip": project}
	_ = filepath.WalkDir(filepath.Join(base, "pack"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(filepath.Join(base, "pack"), path)
			packFiles[filepath.ToSlash(rel)], _ = os.ReadFile(path)
		}
		return err
	})

	for name, upload := range map[string][]byte{"house.omp": project, "pack.zip": zipBytes(t, packFiles)} {
		t.Run(name, func(t *testing.T) {
			s := &Server{serverDir: t.TempDir()}
			stale := filepath.Join(s.serverDir, "ai", "om", "CLU999.md")
			if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
				t.Fatal(err)
			}
			if code, body := uploadFile(t, s, name, upload); code != http.StatusOK {
				t.Fatalf("%d %s", code, body)
			}
			read := func(f string) string {
				b, err := os.ReadFile(filepath.Join(s.serverDir, "ai", "om", f))
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			if idx := read("index.md"); !strings.Contains(idx, "OM 1.8.0_2301") || !strings.Contains(idx, "[CLU220000002.md](CLU220000002.md)") {
				t.Errorf("index.md:\n%s", idx)
			}
			clu := read("CLU220000002.md")
			for _, want := range []string{
				"**Upstairs** (the CLU) OnInit: `Upstairs->sunriseSunsetCalendar5->Start(); Upstairs->scheduler6->Start()`",
				"(DIN1002) OnSwitchOn: `Upstairs->x330000101_DOUT6_ROOM2->SwitchOn(0)`",
				"| x330000101_DOUT1_ROOM3 | DOU1001 | DOUT | 330000101 | 0 |",
			} {
				if !strings.Contains(clu, want) {
					t.Errorf("CLU220000002.md has no %q:\n%s", want, clu)
				}
			}
			if sc := read("CLU520000004-scripts.md"); !strings.Contains(sc, "## script27()\n\n```lua\n-- script27") {
				t.Errorf("scripts:\n%s", sc)
			}
			if _, err := os.Stat(stale); !os.IsNotExist(err) {
				t.Error("a file of the previous import is still there")
			}
			if left, _ := os.ReadDir(filepath.Join(s.serverDir, "ai")); len(left) != 1 {
				t.Errorf("ai/ has %v, want only om", left)
			}
		})
	}

	// A pack without a project leaves no files about an earlier one.
	t.Run("pack without project", func(t *testing.T) {
		s := &Server{serverDir: t.TempDir()}
		if code, body := uploadFile(t, s, "house.omp", project); code != http.StatusOK {
			t.Fatalf("%d %s", code, body)
		}
		delete(packFiles, "house.zip")
		if code, body := uploadFile(t, s, "pack.zip", zipBytes(t, packFiles)); code != http.StatusOK {
			t.Fatalf("%d %s", code, body)
		}
		if _, err := os.Stat(filepath.Join(s.serverDir, "ai", "om")); !os.IsNotExist(err) {
			t.Error("ai/om of the earlier project is still there")
		}
	})
}

// Of properties.xml vCLU keeps the OM version only, and neither the upload
// nor the parts of the project it does not use stay on disk.
func TestProjectUploadKeepsNothingElse(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	omp := zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "project"))
	s := &Server{serverDir: t.TempDir()}

	code, body := uploadFile(t, s, "house.omp", omp)
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if strings.Contains(string(body), "FIXTURE-SECRET") {
		t.Errorf("the answer has a secret: %s", body)
	}
	for _, dir := range []string{s.serverDir, tmp} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if dir == tmp {
				t.Errorf("upload left %s", path)
				return nil
			}
			switch base := filepath.Base(path); {
			case base == "system.xml", base == "properties.xml", strings.HasSuffix(base, ".omp"), strings.HasSuffix(base, ".zip"):
				t.Errorf("project file kept: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(data, []byte("FIXTURE-SECRET")) || bytes.Contains(data, omp[:64]) {
				t.Errorf("%s keeps a part of the project it should not", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// A project with a part vCLU cannot read imports the rest and says what it
// left out.
func TestProjectUploadWarns(t *testing.T) {
	sys, err := os.ReadFile(filepath.Join("testdata", "omproject", "om-1.8.0", "project", "system.xml"))
	if err != nil {
		t.Fatal(err)
	}
	// An object without its class, and a peripheral of a type vCLU does not know.
	broken := strings.Replace(string(sys), "<classTypeId>3</classTypeId>", "", 1)
	broken = strings.Replace(broken, "<typeName>Scheduler</typeName>", "<typeName>FutureThing</typeName>", 1)
	omp := zipBytes(t, map[string][]byte{"system.xml": []byte(broken)}) // and no properties.xml

	s := &Server{serverDir: t.TempDir()}
	code, body := uploadFile(t, s, "house.omp", omp)
	var ans projectAnswer
	if err := json.Unmarshal(body, &ans); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || ans.Imported != 6 || len(ans.Errors) != 0 {
		t.Fatalf("%d %s", code, body)
	}
	w := strings.Join(ans.Warnings, "\n")
	for _, want := range []string{"no OM version", "has no object class, left out", `"FutureThing", a type vCLU does not know`} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings have no %q:\n%s", want, w)
		}
	}
}

// A zip that is no project and no pack is refused, as before.
func TestProjectUploadRejectsNonProject(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	code, body := uploadFile(t, s, "house.omp", []byte("not a zip"))
	if code != http.StatusBadRequest || !strings.Contains(string(body), "Invalid ZIP") {
		t.Errorf("%d %s", code, body)
	}
}
