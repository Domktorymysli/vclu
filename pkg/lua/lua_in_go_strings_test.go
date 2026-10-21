package lua

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// luaLogic matches a Lua `local` statement or a function definition
// (`function name(`, `function(`, `local function f(`).
var luaLogic = regexp.MustCompile(`(?m)^\s*local\s|\bfunction\s*[\w.:]*\s*\(`)

// luaInGoAllowed lists Lua that Go writes for the user, not runs: the om.lua
// templates and the MQTT discovery snippets ("file" or "file:function").
var luaInGoAllowed = map[string]bool{
	"pkg/config/om_generator.go":                               true,
	"pkg/mcp/tool_mqtt_discover.go:generateZigbee2MQTTSnippet": true,
	"pkg/mcp/tool_mqtt_discover.go:generateTasmotaSnippet":     true,
	"pkg/mcp/tool_mqtt_discover.go:generateESPHomeSnippet":     true,
}

// TestNoLuaLogicInGoStrings keeps AGENTS.md rule 4 (#55): Lua with its own logic lives in a
// .lua file loaded with go:embed; a Go raw string holds at most a call into Lua.
func TestNoLuaLogicInGoStrings(t *testing.T) {
	for _, f := range scanLuaInGoStrings(t, "pkg") {
		t.Errorf("%s: raw string with Lua logic; move it to a .lua file with go:embed (AGENTS.md rule 4)", f)
	}
}

// scanLuaInGoStrings returns the positions of raw string literals with Lua logic
// in the non-test Go files under root.
func scanLuaInGoStrings(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	err := walkGoFiles(root, func(path string, fset *token.FileSet, file *ast.File) {
		if luaInGoAllowed[path] {
			return
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && luaInGoAllowed[path+":"+fn.Name.Name] {
				continue
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || !strings.HasPrefix(lit.Value, "`") {
					return true
				}
				if s, err := strconv.Unquote(lit.Value); err == nil && looksLikeLuaLogic(s) {
					found = append(found, fset.Position(lit.Pos()).String())
				}
				return true
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// walkGoFiles parses every non-test Go file under root, testdata skipped.
func walkGoFiles(root string, fn func(path string, fset *token.FileSet, file *ast.File)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		path = filepath.ToSlash(path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		fn(path, fset, file)
		return nil
	})
}

// looksLikeLuaLogic reports a line starting with `local` or a function definition
// (`function name(`, `function(`); a plain call such as `Metrics._syncToGo()` passes.
func looksLikeLuaLogic(s string) bool {
	return luaLogic.MatchString(s)
}

func TestLooksLikeLuaLogic(t *testing.T) {
	for _, s := range []string{
		"local x = 1\nreturn x",
		"  local function f() end",
		"function M.run(a) end",
		"return function(x) return x end",
		"function obj:method() end",
	} {
		if !looksLikeLuaLogic(s) {
			t.Errorf("not detected: %q", s)
		}
	}
	for _, s := range []string{
		"return __vclu_diag()",
		"_G.__helpers.run(...)",
		"SELECT local_time FROM t",
		"the function returns a value",
		`{"type": "object", "description": "a Lua function name"}`,
		"localhost:8080",
	} {
		if looksLikeLuaLogic(s) {
			t.Errorf("false positive: %q", s)
		}
	}
}

// TestEmbeddedLuaCompiles compiles every .lua file a go:embed directive under pkg/
// names, without running it, so a syntax error fails go test and not a draft test.
// The om.lua templates (pkg/config/templates) are text/template sources, not Lua.
func TestEmbeddedLuaCompiles(t *testing.T) {
	files := map[string]bool{}
	err := walkGoFiles("pkg", func(path string, _ *token.FileSet, file *ast.File) {
		for _, group := range file.Comments {
			for _, c := range group.List {
				patterns, ok := strings.CutPrefix(c.Text, "//go:embed ")
				if !ok {
					continue
				}
				for _, pattern := range strings.Fields(patterns) {
					matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), strings.Trim(pattern, "`\"")))
					for _, m := range matches {
						_ = filepath.WalkDir(m, func(p string, d fs.DirEntry, err error) error {
							if err == nil && !d.IsDir() && strings.HasSuffix(p, ".lua") {
								files[filepath.ToSlash(p)] = true
							}
							return nil
						})
					}
				}
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	L := lua.NewState()
	defer L.Close()
	n := 0
	for f := range files {
		if strings.HasPrefix(f, "pkg/config/templates/") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := L.LoadString(string(src)); err != nil {
			t.Errorf("%s: %v", f, err)
		}
		n++
	}
	for _, want := range []string{"pkg/mcp/lua/helpers.lua", "pkg/homekit/lua/command.lua", "pkg/lua/glue/load_user.lua"} {
		if !files[want] {
			t.Errorf("%s not found through go:embed", want)
		}
	}
	t.Logf("%d embedded .lua files compiled", n)
}

// glue/check_alive.lua gets the serial as an argument, not spliced into code.
func TestCheckAliveFromMainLua(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.lua"), []byte("return \"0d2c9586\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := NewLuaEngine()
	defer engine.Close()
	engine.loadCheckAliveFromMainLua(dir)
	if got, err := engine.Execute("checkAlive()"); err != nil || got != "0d2c9586" {
		t.Fatalf("checkAlive() = %q, %v", got, err)
	}
}
