package omparser

import (
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	glua "github.com/yuin/gopher-lua"
)

// om.lua comes from an upload: a NAME_IO that is no identifier must not become code.
const namesOM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
-- MODULES
mm_330000027 = OBJECT:new(2, 0x13ab669b, 0x1e, "13ab669b")
-- IO_MODULES
 DOU5048 = OBJECT:new(4, mm_330000027, 0)
-- NAME_IO a=1;os.exit(1);b=DOU5048
 DOU5049 = OBJECT:new(4, mm_330000027, 1)
-- NAME_IO back\";os.exit(1)--=DOU5049
 DOU5050 = OBJECT:new(4, mm_330000027, 2)
-- NAME_IO LAMP-1=DOU5050
`

func TestGenerateProxyNamesAreNoCode(t *testing.T) {
	parsed, err := Parse(namesOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	for name, out := range map[string]string{
		"proxy":       GenerateProxy(parsed, config),
		"local proxy": GenerateLocalProxy(parsed),
	} {
		if _, err := glua.NewState().LoadString(out); err != nil {
			t.Fatalf("%s does not compile: %v\n%s", name, err, out)
		}
	}
	// The names stay inside strings, the objects go by their IDs.
	out := GenerateProxy(parsed, config)
	for _, want := range []string{
		`dom.DOU5048 = RemoteSwitch:new(_clu, "DOU5048", "a=1;os.exit(1);b")`,
		`dom.DOU5049 = RemoteSwitch:new(_clu, "DOU5049", "back\\\";os.exit(1)--")`,
		`dom.LAMP_1 = RemoteSwitch:new(_clu, "DOU5050", "LAMP-1")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s\n%s", want, out)
		}
	}
}

// A CLU's namespace is a global: it must not be a keyword or replace one of
// the runtime's.
func TestGenerateProxyNamespaceIsSafe(t *testing.T) {
	for name, want := range map[string]string{
		"Home": "home", "_": "clu212345678", "OS": "clu212345678", "color": "clu212345678",
		"end": "clu212345678", "1st": "clu212345678", "_private": "clu212345678",
	} {
		om := strings.Replace(namesOM, "-- NAME_CLU DOM20=", "-- NAME_CLU "+name+"=", 1)
		parsed, err := Parse(om)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := GenerateProxy(parsed, DefaultConfig())
		if !strings.Contains(out, "\n"+want+" = {}\n") {
			t.Errorf("CLU %q: namespace is not %s:\n%s", name, want, out)
		}
		if _, err := glua.NewState().LoadString(out); err != nil {
			t.Errorf("CLU %q: proxy does not compile: %v", name, err)
		}
	}
}

// An object named with a keyword, or with another object's name or ID, goes
// by its ID; the proxy still compiles.
func TestGenerateProxyObjectNamesAreUnique(t *testing.T) {
	om := strings.NewReplacer(
		"-- NAME_IO a=1;os.exit(1);b=DOU5048", "-- NAME_IO end=DOU5048",
		`-- NAME_IO back\";os.exit(1)--=DOU5049`, "-- NAME_IO lamp=DOU5049",
		"-- NAME_IO LAMP-1=DOU5050", "-- NAME_IO lamp=DOU5050",
	).Replace(namesOM)
	om += " DOU5051 = OBJECT:new(4, mm_330000027, 3)\n-- NAME_IO DOU5050=DOU5051\n"
	parsed, err := Parse(om)
	if err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	out := GenerateProxy(parsed, config)
	if _, err := glua.NewState().LoadString(out); err != nil {
		t.Fatalf("proxy does not compile: %v\n%s", err, out)
	}
	for _, want := range []string{
		`dom.DOU5048 = RemoteSwitch:new(_clu, "DOU5048", "end")`,
		`dom.lamp = RemoteSwitch:new(_clu, "DOU5049", "lamp")`,
		`dom.DOU5050 = RemoteSwitch:new(_clu, "DOU5050", "lamp")`,
		`dom.DOU5051 = RemoteSwitch:new(_clu, "DOU5051", "DOU5050")`,
		`registerObject("CLU212345678.DOU5050", dom.DOU5050)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s\n%s", want, out)
		}
	}
}

// runtimeGlobals lists every lower-case global the runtime defines.
func TestNamespaceIsNoRuntimeGlobal(t *testing.T) {
	e := lua.NewLuaEngine()
	defer e.Close()
	out, err := e.Execute(`local t = {}
		for k in pairs(_G) do
			if type(k) == "string" and k == k:lower() and k:sub(1, 1) ~= "_" then t[#t + 1] = k end
		end
		table.sort(t)
		return table.concat(t, " ")`)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range strings.Fields(out) {
		if !runtimeGlobals[g] {
			t.Errorf("the runtime defines %q, which is not in runtimeGlobals", g)
		}
	}
}
