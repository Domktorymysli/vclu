package lua

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// pkg/lua's tests share compiled runtime files between their engines (see runtime_cache.go).
func init() { shareRuntimeChunks = true }

// protos are the compiled chunks the cache holds for a path.
func protos(path string) map[*lua.FunctionProto]bool {
	out := map[*lua.FunctionProto]bool{}
	runtimeChunks.Range(func(k, v any) bool {
		if k.(runtimeChunkKey).path == path {
			out[v.(*lua.FunctionProto)] = true
		}
		return true
	})
	return out
}

// require of a runtime file goes through the cache: compiled once, again when the file
// changes (also at the same size); require of a file outside the runtime directory never.
func TestRuntimeLoader_CachesRuntimeFilesOnly(t *testing.T) {
	rt := t.TempDir()
	user := rt + "2" // shares the runtime directory's prefix, without its separator
	if err := os.Mkdir(user, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(user) })
	write := func(dir, src string, mtime time.Time) string {
		t.Helper()
		path := filepath.Join(dir, "m.lua")
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return path
	}
	require := func(dir string) lua.LValue {
		t.Helper()
		L := lua.NewState()
		defer L.Close()
		installRuntimeLoader(L, rt)
		if err := L.DoString(`package.path = "` + dir + `/?.lua"; M = require("m")`); err != nil {
			t.Fatal(err)
		}
		return L.GetGlobal("M")
	}

	at := time.Now().Add(-time.Hour)
	path := write(rt, `return 1`, at)
	if v1, v2 := require(rt), require(rt); v1 != lua.LNumber(1) || v2 != lua.LNumber(1) {
		t.Fatalf("runtime file gave %v %v, want 1 1", v1, v2)
	}
	if n := len(protos(path)); n != 1 {
		t.Fatalf("two requires of a runtime file left %d compiled chunks, want 1", n)
	}
	write(rt, `return 2`, at.Add(time.Second)) // same size, later mtime
	if v := require(rt); v != lua.LNumber(2) {
		t.Fatalf("changed runtime file gave %v, want 2", v)
	}
	if n := len(protos(path)); n != 2 {
		t.Fatalf("a changed runtime file left %d compiled chunks, want 2", n)
	}

	userPath := write(user, `return 3`, at)
	if v := require(user); v != lua.LNumber(3) {
		t.Fatalf("user file gave %v, want 3", v)
	}
	if n := len(protos(userPath)); n != 0 {
		t.Fatalf("a file outside the runtime directory was cached (%d chunks)", n)
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
