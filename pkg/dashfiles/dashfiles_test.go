package dashfiles

import (
	"os"
	"path/filepath"
	"testing"
)

// A require inside a block comment does not load the dashboards.
func TestRequiredIgnoresComments(t *testing.T) {
	modules := t.TempDir()
	f := New(modules)
	path := filepath.Join(modules, "init.lua")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for code, want := range map[string]bool{
		Require + "\n": true,
		"local x = 1\n  require 'dashboards.imported'\n": true,
		"-- " + Require + "\n":                           false,
		"--[[\n" + Require + "\n]]\n":                    false,
		"--[==[\n" + Require + "\n]] ]==]\n":             false,
		"--[[ a ]]\n" + Require + "\n":                   true,
		"local d = require(\"dashboards/imported\")\n":   true,
		"require \"dashboards/imported\"\n":              true,
		"require(\"dashboards/imported')\n":              false,
		"require(\"dashboards/imported\"\n":              false,
		"require(\"dashboards/imported/page_1\")\n":      false,
		"x = 1 require(\"dashboards/imported\")\n":       false,
	} {
		if err := os.WriteFile(path, []byte(code), 0644); err != nil {
			t.Fatal(err)
		}
		if got := f.Required(); got != want {
			t.Errorf("%q: %v", code, got)
		}
	}
}

// A require that would not compile at the end of modules/init.lua is left for the user to add.
func TestAddRequireAfterReturn(t *testing.T) {
	modules := t.TempDir()
	f := New(modules)
	path := filepath.Join(modules, "init.lua")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	const code = "local M = {}\nreturn M\n"
	if err := os.WriteFile(path, []byte(code), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.AddRequire(); ok || err != nil {
		t.Fatalf("added after a return: %v %v", ok, err)
	}
	if b, _ := os.ReadFile(path); string(b) != code {
		t.Errorf("init.lua changed:\n%s", b)
	}
	if err := os.WriteFile(path, []byte("local x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if ok, err := f.AddRequire(); !ok || err != nil || !f.Required() {
		t.Fatalf("not added: %v %v", ok, err)
	}
}

// A file vclu wrote stays vclu's until someone changes it; one without the header is the user's.
func TestStates(t *testing.T) {
	f := New(t.TempDir())
	if f.State("page_1.lua") != None {
		t.Fatal("missing file")
	}
	written, err := f.WritePages([]Page{{File: "page_1.lua", Content: "return 1\n"}})
	if err != nil || len(written) != 1 {
		t.Fatal(written, err)
	}
	if st := f.State("page_1.lua"); st != VCLU {
		t.Fatalf("written: %s", st)
	}
	// the same content again is nothing to ask about, other content is
	if ask, kept := f.Conflicts([]Page{{File: "page_1.lua", Content: "return 1\n"}}); ask != nil || kept != nil {
		t.Errorf("same: %v %v", ask, kept)
	}
	if ask, _ := f.Conflicts([]Page{{File: "page_1.lua", Content: "return 2\n"}}); len(ask) != 1 {
		t.Errorf("changed: %v", ask)
	}
	path := filepath.Join(f.Path(), "page_1.lua")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(b, "-- mine\n"...), 0644); err != nil {
		t.Fatal(err)
	}
	if st := f.State("page_1.lua"); st != Edited {
		t.Errorf("edited: %s", st)
	}
	if err := os.WriteFile(path, []byte("return 3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, kept := f.Conflicts([]Page{{File: "page_1.lua", Content: "x"}}); len(kept) != 1 || f.State("page_1.lua") != User {
		t.Errorf("user's: %v", kept)
	}
}

// init.lua loads the given order first, then the other page files; a user's init.lua gets lines to add.
func TestInit(t *testing.T) {
	f := New(t.TempDir())
	if _, err := f.WritePages([]Page{{File: "page_2.lua"}, {File: "page_1.lua"}, {File: "page_12.lua"}}); err != nil {
		t.Fatal(err)
	}
	mods := f.Modules([]string{"page_2.lua", "page_9.lua", "page_2.lua"})
	want := []string{Dir + "/page_2", Dir + "/page_1", Dir + "/page_12"}
	if len(mods) != 3 || mods[0] != want[0] || mods[1] != want[1] || mods[2] != want[2] {
		t.Fatalf("modules %v", mods)
	}
	if ok, lines, err := f.WriteInit(mods, nil); !ok || lines != nil || err != nil || f.State("init.lua") != VCLU {
		t.Fatalf("init: %v %v %v", ok, lines, err)
	}
	user := "Wire.load(\"" + Dir + "/page_12\")\n-- Wire.load(\"" + Dir + "/page_2\")\n"
	if err := os.WriteFile(filepath.Join(f.Path(), "init.lua"), []byte(user), 0644); err != nil {
		t.Fatal(err)
	}
	ok, lines, err := f.WriteInit(mods, []string{"page_1.lua", "page_2.lua", "page_12.lua"})
	if ok || err != nil || len(lines) != 2 {
		t.Fatalf("user's init: %v %v %v", ok, lines, err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.Path(), "init.lua")); string(b) != user {
		t.Error("the user's init.lua changed")
	}
}

// A page that cannot be written stops the rest; written lists those before it, the old file stays.
func TestWritePagesStopsAtError(t *testing.T) {
	f := New(t.TempDir())
	// a directory where page_2.lua would go: the rename onto it fails
	if err := os.MkdirAll(filepath.Join(f.Path(), "page_2.lua", "x"), 0755); err != nil {
		t.Fatal(err)
	}
	written, err := f.WritePages([]Page{{File: "page_1.lua"}, {File: "page_2.lua"}, {File: "page_3.lua"}})
	if err == nil || len(written) != 1 || written[0] != "page_1.lua" {
		t.Fatalf("%v %v", written, err)
	}
	if f.State("page_3.lua") != None {
		t.Error("written after the error")
	}
	if _, err := os.Stat(filepath.Join(f.Path(), "page_2.lua.tmp")); !os.IsNotExist(err) {
		t.Errorf("tmp left: %v", err)
	}
}
