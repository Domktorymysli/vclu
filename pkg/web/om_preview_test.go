package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/omchange"
)

type previewAnswer struct {
	Project bool              `json:"project"`
	CLUs    []omchange.Change `json:"clus"`
	Breaks  []omchange.Ref    `json:"breaks"`
	Broken  []omchange.Ref    `json:"broken"`
	Error   string            `json:"error"`
}

func previewFile(t *testing.T, s *Server, name string, data []byte) (int, previewAnswer) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/om/preview", s.handleAPIOMPreview)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/om/preview", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var ans previewAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &ans); err != nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	return rec.Code, ans
}

// A new om.lua of a CLU that loses an object and is renamed: the preview says so and names
// the places in the user's Lua that would break, stores nothing, and the upload then lists them.
func TestOMPreviewAndBrokenRefs(t *testing.T) {
	pack := zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "pack"))
	s := &Server{serverDir: t.TempDir()}
	if code, body := uploadFile(t, s, "pack.zip", pack); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	module := `local r = _registry
r:get("CLU220000002.DOU1001"):switchOn()
upstairs.DOU1002:switchOff()
r:get("CLU220000002.DOU1002"):switchOn() -- r:get("CLU220000002.DOU1001")
r:get("CLU220000002.NOPE")
`
	modules := filepath.Join(s.serverDir, modulesDirName)
	if err := os.MkdirAll(modules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modules, "auto.lua"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(filepath.Join("testdata", "omproject", "om-1.8.0", "pack", "CLU220000002", "om.lua"))
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Replace(strings.Replace(string(old), "-- NAME_CLU Upstairs=CLU220000002", "-- NAME_CLU Pietro=CLU220000002", 1),
		` DOU1001 = OBJECT:new(4, mm_330000101, 0, "DOU1001")`, "", 1)

	before := dataTree(t, s.serverDir)
	code, ans := previewFile(t, s, "om.lua", []byte(newer))
	if code != http.StatusOK || ans.Project {
		t.Fatalf("%d %+v", code, ans)
	}
	sameTree(t, "the preview", before, dataTree(t, s.serverDir))
	var changed []omchange.Change
	for _, c := range ans.CLUs {
		if c.Kind != "same" {
			changed = append(changed, c)
		}
	}
	if len(ans.CLUs) != 6 || len(changed) != 1 || changed[0].ID != "CLU220000002" || changed[0].Renamed != "Upstairs" ||
		len(changed[0].Removed) != 1 || changed[0].Removed[0].ID != "DOU1001" {
		t.Errorf("clus %+v", ans.CLUs)
	}
	wantBreaks := []omchange.Ref{{File: "auto.lua", Line: 2, Path: "CLU220000002.DOU1001"}, {File: "auto.lua", Line: 3, Path: "upstairs.DOU1002"}}
	wantBroken := []omchange.Ref{{File: "auto.lua", Line: 5, Path: "CLU220000002.NOPE"}}
	if !sameRefs(ans.Breaks, wantBreaks) || !sameRefs(ans.Broken, wantBroken) {
		t.Errorf("breaks %v broken %v", ans.Breaks, ans.Broken)
	}

	code, body := uploadFile(t, s, "om.lua", []byte(newer))
	var up struct {
		BrokenRefs []omchange.Ref `json:"brokenRefs"`
	}
	if err := json.Unmarshal(body, &up); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if !sameRefs(up.BrokenRefs, append(wantBreaks, wantBroken...)) {
		t.Errorf("after the upload: %v", up.BrokenRefs)
	}
	// the same file again breaks nothing more; the old table's name is no longer known (only
	// an import or a delete looks among the names before it), so upstairs is not listed
	if _, again := previewFile(t, s, "om.lua", []byte(newer)); len(again.Breaks) != 0 || len(again.Broken) != 2 {
		t.Errorf("again %+v", again)
	}
}

// A project removes the CLUs it does not have: the preview lists them as removed.
func TestOMPreviewProject(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	other := zipDir(t, filepath.Join("testdata", "omproject", "om-1.3.5", "project"))
	if code, body := uploadFile(t, s, "other.omp", other); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	house := zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "project"))
	code, ans := previewFile(t, s, "house.omp", house)
	if code != http.StatusOK || !ans.Project {
		t.Fatalf("%d %+v", code, ans)
	}
	kinds := map[string]string{}
	for _, c := range ans.CLUs {
		kinds[c.ID] = c.Kind
	}
	if kinds["CLU470000005"] != "removed" || len(kinds) < 6 {
		t.Errorf("%v", kinds)
	}
	if code, ans := previewFile(t, s, "x.zip", []byte("not a zip")); code != http.StatusBadRequest || ans.Error == "" {
		t.Errorf("not a zip: %d %+v", code, ans)
	}
}

func sameRefs(got, want []omchange.Ref) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[omchange.Ref]bool{}
	for _, r := range got {
		seen[r] = true
	}
	for _, r := range want {
		if !seen[r] {
			return false
		}
	}
	return true
}

// What the preview says an upload leaves is what the upload leaves: a project with this vCLU's
// own CLU in the house, a pack, an om.lua; an empty om.lua is refused as the upload refuses it.
func TestOMPreviewMatchesUpload(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	if code, body := uploadFile(t, s, "old.omp", zipDir(t, filepath.Join("testdata", "omproject", "om-1.3.5", "project"))); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	// CLU470000005 is not in the newer project, but it is this vCLU: it stays
	if err := config.UpdateMasterConfig(s.serverDir, func(c *config.VCLUConfig) error {
		c.Device = &config.VCLUDeviceConfig{DeviceTypeID: "clu", SerialNumber: 470000005}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	om, err := os.ReadFile(filepath.Join("testdata", "omproject", "om-1.8.0", "pack", "CLU220000002", "om.lua"))
	if err != nil {
		t.Fatal(err)
	}
	for _, up := range []struct {
		name string
		data []byte
	}{
		{"new.omp", zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "project"))},
		{"pack.zip", zipDir(t, filepath.Join("testdata", "omproject", "om-1.8.0", "pack"))},
		{"om.lua", om},
	} {
		code, ans := previewFile(t, s, up.name, up.data)
		if code != http.StatusOK {
			t.Fatalf("%s: %d %+v", up.name, code, ans)
		}
		want := map[string]int{}
		for _, c := range ans.CLUs {
			if c.Kind != "removed" {
				want[c.ID] = len(c.Removed)
			}
		}
		if code, body := uploadFile(t, s, up.name, up.data); code != http.StatusOK {
			t.Fatalf("%s: %d %s", up.name, code, body)
		}
		got := s.house()
		if len(got) != len(want) {
			t.Errorf("%s: preview %v, upload left %d CLUs", up.name, want, len(got))
		}
		for id := range want {
			if got[id] == nil {
				t.Errorf("%s: preview keeps %s, the upload does not", up.name, id)
			}
		}
		if _, ok := want["CLU470000005"]; !ok {
			t.Errorf("%s: this vCLU's own CLU removed", up.name)
		}
	}
	if code, ans := previewFile(t, s, "om.lua", []byte(" \n")); code != http.StatusBadRequest || !strings.Contains(ans.Error, "empty") {
		t.Errorf("empty om.lua: %d %+v", code, ans)
	}
}
