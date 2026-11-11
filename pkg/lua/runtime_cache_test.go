package lua

import (
	"os"
	"path/filepath"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// pkg/lua's tests share compiled runtime files between their engines (see runtime_cache.go).
func init() { shareRuntimeChunks = true }

// A runtime file is compiled once per process; a changed file is compiled again, and files
// outside the runtime directory are not kept.
func TestRuntimeLoader_CachesRuntimeFilesOnly(t *testing.T) {
	rt, user := t.TempDir(), t.TempDir()
	write := func(dir, src string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "m.lua"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	require := func(dir string) (lua.LValue, *lua.FunctionProto) {
		t.Helper()
		L := lua.NewState()
		defer L.Close()
		installRuntimeLoader(L, rt)
		if err := L.DoString(`package.path = "` + dir + `/?.lua"; M = require("m")`); err != nil {
			t.Fatal(err)
		}
		fn, err := loadRuntimeFile(L, filepath.Join(dir, "m.lua"), dir == rt)
		if err != nil {
			t.Fatal(err)
		}
		return L.GetGlobal("M"), fn.Proto
	}

	write(rt, `return 1`)
	v1, p1 := require(rt)
	v2, p2 := require(rt)
	if v1 != lua.LNumber(1) || v2 != lua.LNumber(1) || p1 != p2 {
		t.Fatalf("runtime file: %v %v, same proto %v; want 1 1 true", v1, v2, p1 == p2)
	}
	write(rt, `return 22`)
	if v, _ := require(rt); v != lua.LNumber(22) {
		t.Fatalf("changed runtime file gave %v, want 22", v)
	}

	write(user, `return 1`)
	_, u1 := require(user)
	_, u2 := require(user)
	if u1 == u2 {
		t.Fatal("a file outside the runtime directory was cached")
	}
}

func TestRuntimeLoader_MissingModuleError(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	installRuntimeLoader(L, t.TempDir())
	err := L.DoString(`package.path = "/nonexistent/?.lua"; require("nope")`)
	if err == nil {
		t.Fatal("require of a missing module succeeded")
	}
}
