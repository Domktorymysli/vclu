package updatecheck

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceID(t *testing.T) {
	dir := t.TempDir()
	id, err := LoadOrCreateID(dir)
	if err != nil || !uuidRe.MatchString(id) {
		t.Fatalf("new id %q, %v", id, err)
	}
	again, err := LoadOrCreateID(dir) // a restart
	if err != nil || again != id {
		t.Fatalf("after a restart %q, want %q (%v)", again, id, err)
	}
	if other, _ := LoadOrCreateID(t.TempDir()); other == id {
		t.Fatal("two installations got the same id")
	}

	path := filepath.Join(dir, IDFile)
	if err := os.Remove(path); err != nil { // the user wants a fresh id
		t.Fatal(err)
	}
	fresh, err := LoadOrCreateID(dir)
	if err != nil || fresh == id || !uuidRe.MatchString(fresh) {
		t.Fatalf("after deleting the file %q, %v", fresh, err)
	}
	if err := os.WriteFile(path, []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fixed, _ := LoadOrCreateID(dir); !uuidRe.MatchString(fixed) {
		t.Fatalf("a broken file gives %q", fixed)
	}
}
