package plugins

import (
	"os"
	"path/filepath"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// luaStateExecutor runs scripts in a Lua state with stand-ins for the
// plugin runtime, so the scripts the loader builds are parsed for real.
type luaStateExecutor struct{ L *lua.LState }

func (e *luaStateExecutor) Execute(script string) (string, error) {
	return "", e.L.DoString(script)
}

func TestLoadPluginQuotesConfigAndCode(t *testing.T) {
	loader, _ := newTestLoader(t)
	dir := filepath.Join(loader.pluginsDir, safePluginDir("@vclu/quote"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"@vclu/quote","name":"Quote","version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	initLua := "local s = \"a \\\"quoted\\\" \\\\ word\"\r\nreturn s\n"
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(initLua), 0644); err != nil {
		t.Fatal(err)
	}

	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
		got = {}
		JSON = {}
		function JSON:decode(s) return s end
		PluginSandbox = {}
		function PluginSandbox:create(id) got.created = id end
		function PluginSandbox:setConfig(id, cfg, dir) got.config = cfg; got.dir = dir end
		function PluginSandbox:execute(id, code) got.code = code; return true end
		function PluginSandbox:cleanup(id) got.cleaned = id end
		Plugin = {}
		function Plugin.initFromConfig(id, cfg) got.initConfig = cfg end
	`); err != nil {
		t.Fatal(err)
	}
	loader.SetLuaExecutor(&luaStateExecutor{L})

	cfg := map[string]interface{}{"password": `pa"ss]]`, "path": `C:\dir`}
	if err := loader.LoadPlugin("@vclu/quote", cfg); err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}

	got := L.GetGlobal("got").(*lua.LTable)
	field := func(k string) string { return got.RawGetString(k).String() }
	wantJSON := `{"password":"pa\"ss]]","path":"C:\\dir"}`
	if field("config") != wantJSON || field("initConfig") != wantJSON {
		t.Errorf("config reached Lua as %q and %q, want %q", field("config"), field("initConfig"), wantJSON)
	}
	if field("created") != "@vclu/quote" || field("dir") != dir {
		t.Errorf("id %q, dir %q", field("created"), field("dir"))
	}
	if want := "local s = \"a \\\"quoted\\\" \\\\ word\"\nreturn s\n"; field("code") != want {
		t.Errorf("init.lua reached the sandbox as %q, want %q", field("code"), want)
	}
}
