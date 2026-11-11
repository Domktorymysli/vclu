package lua

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Every engine requires the same ~1 MB of runtime Lua, and parsing it is nearly
// all of NewLuaEngine's time (0.4 s under -race, about 30 MB live). pkg/lua's
// tests create an engine in almost every one of their ~850 tests, which took
// the package past go test's 10-minute default under -race. With
// shareRuntimeChunks (set by those tests only) engines share the compiled
// chunks of runtime files: gopher-lua's FunctionProto is read-only and may be
// shared between LStates. A file that changed on disk (size or mtime) is
// compiled again; files outside the runtime directory are never cached.
//
// It is off in vCLU: the cache would keep ~30 MB alive with no engine using
// it, which the MCP sandbox's memory guard counts (pkg/mcp
// TestSandbox_MemoryGuardBacksOff then sees 20+ forced GCs instead of ~6).
var shareRuntimeChunks = false

type runtimeChunkKey struct {
	path  string
	size  int64
	mtime time.Time
}

var runtimeChunks sync.Map // runtimeChunkKey -> *lua.FunctionProto

// installRuntimeLoader replaces require's file loader (package.loaders[2])
// with one that behaves the same but reuses compiled runtime files.
// initializeLuaState calls it only with shareRuntimeChunks.
func installRuntimeLoader(L *lua.LState, runtimePath string) {
	loaders, ok := L.GetField(L.GetGlobal("package"), "loaders").(*lua.LTable)
	if !ok || runtimePath == "" {
		return
	}
	dir := filepath.Clean(runtimePath) + string(os.PathSeparator)
	L.RawSetInt(loaders, 2, L.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		path, msg := findLuaFile(L, name)
		if path == "" {
			L.Push(lua.LString(msg))
			return 1
		}
		fn, err := loadRuntimeFile(L, path, strings.HasPrefix(filepath.Clean(path), dir))
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		L.Push(fn)
		return 1
	}))
}

// findLuaFile searches package.path like gopher-lua's loader.
func findLuaFile(L *lua.LState, name string) (string, string) {
	name = strings.ReplaceAll(name, ".", string(os.PathSeparator))
	path, ok := L.GetField(L.GetField(L.Get(lua.EnvironIndex), "package"), "path").(lua.LString)
	if !ok {
		L.RaiseError("package.path must be a string")
	}
	var messages []string
	for _, pattern := range strings.Split(string(path), ";") {
		luapath := strings.ReplaceAll(pattern, "?", name)
		if _, err := os.Stat(luapath); err == nil {
			return luapath, ""
		} else {
			messages = append(messages, err.Error())
		}
	}
	return "", strings.Join(messages, "\n\t")
}

func loadRuntimeFile(L *lua.LState, path string, cache bool) (*lua.LFunction, error) {
	if !cache {
		return L.LoadFile(path)
	}
	st, err := os.Stat(path)
	if err != nil {
		return L.LoadFile(path)
	}
	key := runtimeChunkKey{path, st.Size(), st.ModTime()}
	if proto, ok := runtimeChunks.Load(key); ok {
		return L.NewFunctionFromProto(proto.(*lua.FunctionProto)), nil
	}
	fn, err := L.LoadFile(path)
	if err != nil {
		return nil, err
	}
	runtimeChunks.Store(key, fn.Proto)
	return fn, nil
}
