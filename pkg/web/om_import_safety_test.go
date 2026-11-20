package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/omproject"
)

// projectXML is a system.xml of CLUs, each given in full.
func projectXML(clus ...string) []byte {
	return []byte("<object-stream><TreeObject><children>" + strings.Join(clus, "") + "</children></TreeObject></object-stream>")
}

// projectCLU is a CLU of a project with the inputs and outputs in ios.
func projectCLU(id, name, ios string) string {
	return fmt.Sprintf(`<specificObject class="CLU"><name>%s</name><ipAddress>192.0.2.9</ipAddress><nameOnCLU>%s</nameOnCLU>`+
		`<iosList>%s</iosList><modulesList/><firmwareType>00000003</firmwareType><hardwareType>00000013</hardwareType></specificObject>`, name, id, ios)
}

// An upload or a preview over the limit is refused before it is read, and stores nothing.
func TestOMUploadTooLarge(t *testing.T) {
	old := maxOMUpload
	maxOMUpload = 1 << 10
	defer func() { maxOMUpload = old }()
	s := &Server{serverDir: t.TempDir()}
	big := []byte(namesOMLua + strings.Repeat("-- padding\n", 200))
	if code, body := uploadFile(t, s, "om.lua", big); code != http.StatusRequestEntityTooLarge {
		t.Errorf("upload: %d %s", code, body)
	}
	if code, ans := previewFile(t, s, "om.lua", big); code != http.StatusRequestEntityTooLarge || ans.Error == "" {
		t.Errorf("preview: %d %+v", code, ans)
	}
	if len(config.GetOMFiles(s.serverDir)) != 0 {
		t.Error("stored after a refused upload")
	}
	if code, body := uploadFile(t, s, "om.lua", []byte(namesOMLua)); code != http.StatusOK {
		t.Errorf("under the limit: %d %s", code, body)
	}
}

// A CLU the project has and vclu cannot read is not taken for gone: neither the preview nor
// an import asked to remove what the project lacks removes it.
func TestProjectKeepsCLUItCouldNotRead(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	if code, body := uploadFile(t, s, "om.lua", []byte(namesOMLua)); code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	unread := `<specificObject class="CLU"><name>home</name><nameOnCLU>CLU212345678</nameOnCLU><modulesList/></specificObject>`
	omp := zipBytes(t, map[string][]byte{"system.xml": projectXML(projectCLU("CLU221000009", "garage", ""), unread)})

	code, pre := previewFile(t, s, "house.omp", omp)
	if code != http.StatusOK {
		t.Fatalf("preview %d %+v", code, pre)
	}
	for _, c := range pre.CLUs {
		if c.ID == "CLU212345678" && c.Kind == "removed" {
			t.Errorf("the preview removes the CLU it could not read: %+v", c)
		}
	}
	_, body := uploadFile(t, s, "house.omp", omp, "removeMissing", "true")
	var ans map[string]any
	if err := json.Unmarshal(body, &ans); err != nil || ans["success"] != true {
		t.Fatalf("%s", body)
	}
	if r, _ := ans["removed"].([]any); len(r) != 0 {
		t.Errorf("removed %v", r)
	}
	if _, ok := registered(t, s.serverDir)[212345678]; !ok {
		t.Error("the CLU the project could not read is gone")
	}
}

// The answer of a project import carries at most omproject.MaxWarnings warnings and the count
// of the rest.
func TestProjectUploadCapsWarnings(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	bad := strings.Repeat("<Input><name>x</name></Input>", 300)
	omp := zipBytes(t, map[string][]byte{"system.xml": projectXML(projectCLU("CLU221000009", "garage", bad))})
	code, body := uploadFile(t, s, "house.omp", omp)
	var ans projectAnswer
	if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	if len(ans.Warnings) != omproject.MaxWarnings+1 || !strings.HasPrefix(ans.Warnings[omproject.MaxWarnings], "... and ") {
		t.Errorf("%d warnings, last %q", len(ans.Warnings), ans.Warnings[len(ans.Warnings)-1])
	}
}

func omBackupDirs(t *testing.T, s *Server) []string {
	t.Helper()
	return omBackups(filepath.Join(s.serverDir, filepath.FromSlash(omBackupsDir)))
}

// Before an import or a delete changes them, the stored om files are copied to a backup: once
// for the same files, the newest omBackupsKept kept.
func TestOMFilesBackedUp(t *testing.T) {
	s := &Server{serverDir: t.TempDir()}
	upload := func(i int) map[string]any {
		t.Helper()
		om := strings.Replace(namesOMLua, "home", fmt.Sprintf("home%d", i), 1)
		code, body := uploadFile(t, s, "om.lua", []byte(om))
		var ans map[string]any
		if err := json.Unmarshal(body, &ans); err != nil || code != http.StatusOK {
			t.Fatalf("%d %s", code, body)
		}
		return ans
	}
	if ans := upload(0); ans["backup"] != nil {
		t.Errorf("a backup of nothing: %v", ans["backup"])
	}
	ans := upload(1)
	backup, _ := ans["backup"].(string)
	if backup == "" {
		t.Fatal("no backup")
	}
	b, err := os.ReadFile(filepath.Join(s.serverDir, filepath.FromSlash(backup), "om_212345678.lua"))
	if err != nil || !strings.Contains(string(b), "NAME_CLU home0=") {
		t.Errorf("the backup has %q, %v", b, err)
	}
	for _, f := range []string{"proxy_212345678.lua", omFilesList} {
		if _, err := os.Stat(filepath.Join(s.serverDir, filepath.FromSlash(backup), f)); err != nil {
			t.Error(err)
		}
	}

	// the same files again: no new copy
	n := len(omBackupDirs(t, s))
	upload(1)
	if again := upload(1); again["backup"] == nil || len(omBackupDirs(t, s)) != n+1 {
		t.Errorf("%d copies after the same files, %d before", len(omBackupDirs(t, s)), n)
	}
	for i := 2; i < omBackupsKept+5; i++ {
		upload(i)
	}
	if got := len(omBackupDirs(t, s)); got != omBackupsKept {
		t.Errorf("%d copies kept", got)
	}

	// a delete copies the file it removes
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/api/om/:serial", s.handleAPIOMDelete)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/om/212345678", nil))
	var del map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &del); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	backup, _ = del["backup"].(string)
	if _, err := os.Stat(filepath.Join(s.serverDir, filepath.FromSlash(backup), "om_212345678.lua")); backup == "" || err != nil {
		t.Errorf("delete backup %q: %v", backup, err)
	}
}

// writeFileAtomic replaces the file whole and leaves nothing beside it.
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "om_1.lua")
	for _, content := range []string{"old", "new"} {
		if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Errorf("%q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d files", len(entries))
	}
	if err := writeFileAtomic(filepath.Join(dir, "missing", "x"), nil, 0o644); err == nil {
		t.Error("no error for a missing directory")
	}
}
