package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

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
	systemXML := `<TreeObject><specificObject class="HardwarePeripheryCLU"><clu id="14">
                    <name>Alarm</name>
                    <ipAddress>192.168.1.4</ipAddress>
                    <nameOnCLU>CLU511000000</nameOnCLU>
                  </clu></specificObject></TreeObject>
            <specificObject class="CLU" id="15546">
              <name>Dom1</name>
              <ipAddress>192.168.1.5</ipAddress>
              <nameOnCLU>CLU220000000</nameOnCLU>`
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
