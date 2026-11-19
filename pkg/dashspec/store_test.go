package dashspec

import (
	"os"
	"path/filepath"
	"testing"
)

// Stored interfaces read back by label; IDs that may not name files are left out.
func TestStoreInterfaces(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "panels")
	list := []Interface{
		{ID: "b", Label: "Second", Pages: []Page{{ID: "1"}, {ID: "../x"}}},
		{ID: "a", Label: "First"},
	}
	if err := SaveInterfaces(dir, list); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{"id":"../bad"}`), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadInterfaces(dir)
	if err != nil || len(got) != 2 || got[0].ID != "a" || len(got[1].Pages) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if err := SaveInterfaces(dir, []Interface{{ID: "../x"}}); err == nil {
		t.Error("a bad id was stored")
	}
	if got, _ := LoadInterfaces(dir); len(got) != 2 {
		t.Errorf("a failed save changed the stored ones: %v", got)
	}
	if err := SaveInterfaces(dir, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("an empty list left %v", err)
	}
}

func TestWidgetCLUs(t *testing.T) {
	w := Widget{Read: &Read{CLU: "CLU1"}, On: &Action{CLU: "CLU2"}, Off: &Action{CLU: "CLU1"}, State: &Read{}}
	if got := w.CLUs(); len(got) != 2 || got[0] != "CLU1" || got[1] != "CLU2" {
		t.Errorf("%v", got)
	}
}
