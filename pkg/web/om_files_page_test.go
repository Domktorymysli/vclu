package web

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// The OM Files page imports only after a preview: a failed one shows its error and uploads
// nothing, and only a confirmed one lets a project remove the CLUs it lacks.
func TestOMFilesImportOnlyAfterPreview(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/om_files_preview.js", "../../web/static/js/om-files.js").Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var got map[string]struct {
		Requests []string `json:"requests"`
		Shown    string   `json:"shown"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	const preview = "POST /api/om/preview file=<file>"
	for name, want := range map[string][]string{
		"previewError":     {preview},
		"previewRejected":  {preview},
		"previewNotJSON":   {preview},
		"cancelled":        {preview},
		"nothingToConfirm": {preview, "POST /api/om/upload file=<file>"},
		"confirmed":        {preview, "POST /api/om/upload file=<file>&removeMissing=true"},
	} {
		if g := got[name].Requests; strings.Join(g, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: sent %q, want %q", name, g, want)
		}
	}
	for _, name := range []string{"previewError", "previewRejected", "previewNotJSON"} {
		if !strings.Contains(got[name].Shown, "Not imported") {
			t.Errorf("%s: shows %q", name, got[name].Shown)
		}
	}
}
