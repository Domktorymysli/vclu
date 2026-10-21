package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// error(msg, 2) in a library names the line of the module that called it, as in Lua 5.1.
func TestErrorLevelNamesTheCaller(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib.lua")
	if err := os.WriteFile(lib, []byte("local B = {}\nfunction B:two() error('two', 2) end\nfunction B:one() error('one') end\nfunction B:zero() error('zero', 0) end\nreturn B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	L := lua.NewState()
	defer L.Close()
	RegisterErrorLevel(L)
	L.SetGlobal("LIB", lua.LString(lib))
	if err := L.DoString("local B = dofile(LIB)\nlocal _, two = pcall(function()\n  B:two()\nend)\n" +
		"local _, one = pcall(function() B:one() end)\nlocal _, zero = pcall(function() B:zero() end)\n" +
		"local _, tbl = pcall(function() error({ code = 7 }, 2) end)\n" +
		"RESULT = two .. '|' .. one .. '|' .. zero .. '|' .. tostring(tbl.code)"); err != nil {
		t.Fatal(err)
	}
	got := L.GetGlobal("RESULT").String()
	want := []string{`<string>:3: two`, "lib.lua:3: one", "|zero|", "|7"}
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("%q: want %q", got, w)
		}
	}
}
