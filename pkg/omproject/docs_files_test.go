package omproject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A deleted CLU's files go, a CLU whose ID starts the same keeps its own, the index notes it.
func TestForgetDocs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(DocsDir))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"CLU1.md": "", "CLU1-scripts.md": "", "CLU1-scripts-2.md": "", "CLU12.md": "", "CLU12-scripts.md": "",
		"index.md": "# Object Manager project\n\n| A | CLU1 | x |\n| B | CLU12 | y |\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ForgetDocs(root, "CLU1"); err != nil {
		t.Fatal(err)
	}
	for name := range files {
		_, err := os.Stat(filepath.Join(dir, name))
		gone := strings.HasPrefix(name, "CLU1.") || strings.HasPrefix(name, "CLU1-")
		if gone != os.IsNotExist(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "index.md")); strings.Contains(string(b), "| A |") || !strings.Contains(string(b), "| B | CLU12 |") {
		t.Errorf("index:\n%s", b)
	}
	if err := RemoveDocs(root); err != nil {
		t.Fatal(err)
	}
	if err := ForgetDocs(root, "CLU12"); err != nil {
		t.Errorf("without docs: %v", err)
	}
}
