package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

var hostileTexts = []string{
	`"); os.exit(1) --`, "a]]b", "x\n os.exit(1)", "x\r\nend", "nul\x00z", `back\`, "--[[", "]=]", "zażółć",
	`\"`, "\\\n",
} // invalid UTF-8 cannot come: the config is JSON

// The access block, from a config edited by hand, reads back as written and runs nothing else.
func TestDashboardAccessCodeRoundTripsHostileConfig(t *testing.T) {
	declared := []ManifestDashboard{{ID: "lista", Name: LocalText{Text: "Lista"}}}
	for _, s := range hostileTexts {
		cfg := map[string]any{DashboardsKey: map[string]any{"lista": map[string]any{
			"name": s, "auth": s, "pin": s + "1234", "remote": true, "remotePin": true,
			"roles": []any{s, "ok", "a-b"},
		}}}
		code := DashboardAccessCode("@jan/do-it", declared, SavedDashboardAccess(declared, cfg))
		L := lua.NewState()
		if err := L.DoString(`
			calls = {}
			ran = false
			os = { exit = function() ran = true end }
			local p = {}
			local b = {}
			for _, m in ipairs({ "name", "auth", "pin", "remote", "roles" }) do
				b[m] = function(self, ...)
					local opts = calls[#calls].opts
					if m == "remote" then opts.remote = { pin = (...).pin, roles = {} }
					elseif m == "roles" then opts.remote.roles = { ... }
					else opts[m] = ... end
					return self
				end
			end
			function p:dashboard(id) calls[#calls + 1] = { id = id, opts = {} }; return b end
			Plugin = { getPlugin = function(id) pluginId = id; return p end }`); err != nil {
			t.Fatal(err)
		}
		if err := L.DoString(code); err != nil {
			t.Errorf("%q: %v\n%s", s, err, code)
			L.Close()
			continue
		}
		if L.GetGlobal("ran") != lua.LFalse || L.GetGlobal("pluginId").String() != "@jan/do-it" {
			t.Errorf("%q: extra code ran\n%s", s, code)
		}
		calls := L.GetGlobal("calls").(*lua.LTable)
		if calls.Len() != 1 {
			t.Fatalf("%q: %d calls", s, calls.Len())
		}
		opts := calls.RawGetInt(1).(*lua.LTable).RawGetString("opts").(*lua.LTable)
		if got := opts.RawGetString("name").String(); got != s {
			t.Errorf("name %q reads back as %q", s, got)
		}
		if got := opts.RawGetString("auth").String(); got != s {
			t.Errorf("auth %q reads back as %q", s, got)
		}
		if opts.RawGetString("pin") != lua.LNil {
			t.Errorf("%q: a pin that is not 4 digits went in", s)
		}
		roles := opts.RawGetString("remote").(*lua.LTable).RawGetString("roles").(*lua.LTable)
		if roles.Len() != 2 || roles.RawGetInt(1).String() != "ok" || roles.RawGetInt(2).String() != "a-b" {
			t.Errorf("%q: roles %d", s, roles.Len())
		}
		L.Close()
	}
}

// A field hidden by a hidden field is hidden too: the panel does not ask it, so vclu
// neither requires nor keeps it, in any order of the fields.
func TestFormCheckShowIfCascade(t *testing.T) {
	form, err := ParseForm([]byte(`{"settings": {
		"zMode": {"type": "enum", "options": ["x", "y"], "showIf": {"advanced": true}},
		"advanced": {"type": "boolean"},
		"aDetail": {"type": "string", "required": true, "showIf": {"zMode": "x"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	// the panel sends what its hidden inputs still hold
	v, err := form.Check(map[string]any{"advanced": false, "zMode": "x"}, nil)
	if err != nil {
		t.Fatalf("hidden by a hidden field, yet required: %v", err)
	}
	if _, kept := v.Config["zMode"]; kept {
		t.Errorf("a hidden field was kept: %v", v.Config)
	}
	if _, err := form.Check(map[string]any{"advanced": true, "zMode": "x"}, nil); err == nil {
		t.Errorf("shown and required, yet not asked")
	}
	if v, err := form.Check(map[string]any{"advanced": true, "zMode": "x", "aDetail": "d"}, nil); err != nil || v.Config["aDetail"] != "d" {
		t.Errorf("all shown: %v %v", v.Config, err)
	}
}

// Text fields are one line in the panel; a value with control characters (from discover or a
// client other than the panel) is refused, so it cannot end a string in a plugin's module code.
func TestFormCheckRefusesControlCharacters(t *testing.T) {
	form, err := ParseForm([]byte(`{"settings": {"name": {"type": "string"}, "obj": {"type": "object"}},
		"devices": {"fields": {"id": {"type": "string", "readonly": true}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"a\nb", "a\rb", "a\x00b", "a\x1bb", "a b", "\xff"} {
		if _, err := form.Check(map[string]any{"name": s}, nil); err == nil {
			t.Errorf("setting %q accepted", s)
		}
		if _, err := form.Check(map[string]any{"obj": s}, nil); err == nil {
			t.Errorf("object %q accepted", s)
		}
		ok, dropped := form.Devices.DiscoveredRows([]map[string]any{{"id": s}})
		if len(ok) != 0 || len(dropped) != 1 {
			t.Errorf("discovered %q kept", s)
		}
	}
	if v, err := form.Check(map[string]any{"name": "Salon \"duży\" ]] ż\tok"}, nil); err != nil || v.Config["name"] != "Salon \"duży\" ]] ż\tok" {
		t.Errorf("plain text: %v %v", v.Config, err)
	}
}

// A plugin's module never takes the place of the list of modules.
func TestWriteModuleCodeKeepsTheList(t *testing.T) {
	dir := t.TempDir()
	if err := WriteModuleCode(dir, "@jan/a", "x = 1"); err != nil {
		t.Fatal(err)
	}
	if err := WriteModuleCode(dir, "init", "x = 2"); err == nil {
		t.Errorf("a plugin named init was written over modules/plugins/init.lua")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "modules", "plugins", "init.lua"))
	if !strings.Contains(string(b), `require("plugins/jan__a")`) {
		t.Errorf("init.lua: %q", b)
	}
}

// Uninstalling turns the wizard's module off and keeps it; the wizard run again turns it on,
// a line the user turned off stays off, another plugin's module is not touched.
func TestTurnOffModule(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "modules", "plugins", "init.lua")
	read := func() string { b, _ := os.ReadFile(list); return string(b) }
	for _, id := range []string{"@jan/a", "@jan/b", "@jan/c"} {
		if err := WriteModuleCode(dir, id, "x = 1"); err != nil {
			t.Fatal(err)
		}
	}
	text := strings.Replace(read(), `require("plugins/jan__c")`, `-- require("plugins/jan__c")`, 1)
	if err := os.WriteFile(list, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	if changed, err := TurnOffModule(dir, "@jan/a"); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if changed, err := TurnOffModule(dir, "@jan/c"); err != nil || changed {
		t.Fatal(changed, err)
	}
	got := read()
	if !strings.Contains(got, `-- require("plugins/jan__a")`+offMark) || !strings.Contains(got, "\n"+`require("plugins/jan__b")`) {
		t.Errorf("after uninstalling a: %q", got)
	}
	if strings.Contains(got, `require("plugins/jan__c")`+offMark) {
		t.Errorf("a line the user turned off was marked: %q", got)
	}
	if _, err := os.Stat(ModulePath(dir, "@jan/a")); err != nil {
		t.Errorf("the module was not kept: %v", err)
	}
	if changed, err := TurnOffModule(dir, "@jan/none"); err != nil || changed {
		t.Errorf("a plugin without a module: %v %v", changed, err)
	}

	// installed again: on without the wizard
	if changed, err := TurnOnModule(dir, "@jan/a"); err != nil || !changed || strings.Contains(read(), offMark) {
		t.Errorf("installed again: %v %v %q", changed, err, read())
	}
	if _, err := TurnOffModule(dir, "@jan/a"); err != nil {
		t.Fatal(err)
	}
	if err := WriteModuleCode(dir, "@jan/a", "x = 2"); err != nil {
		t.Fatal(err)
	}
	if err := WriteModuleCode(dir, "@jan/c", "x = 2"); err != nil {
		t.Fatal(err)
	}
	got = read()
	if strings.Contains(got, offMark) || strings.Count(got, `require("plugins/jan__a")`) != 1 || !strings.Contains(got, "\n"+`require("plugins/jan__a")`) {
		t.Errorf("the wizard run again: %q", got)
	}
	if !strings.Contains(got, `-- require("plugins/jan__c")`) {
		t.Errorf("the user's line was turned on: %q", got)
	}

	// the module file deleted by hand: its line goes off too, so it does not fail to load
	if err := os.Remove(ModulePath(dir, "@jan/b")); err != nil {
		t.Fatal(err)
	}
	if changed, err := TurnOffModule(dir, "@jan/b"); err != nil || !changed {
		t.Errorf("deleted module: %v %v %q", changed, err, read())
	}
	if _, err := os.Stat(list + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left: %v", err)
	}
}

// What a plugin proposes is code the user reads in a diff: characters that hide or reorder
// what is shown are refused.
func TestModuleCodeRefusesHiddenCharacters(t *testing.T) {
	for _, code := range []string{"x = 1 -- \u202e)1(tixe.so\u202c", "a\x00", "a\x1b[2K", "a\u2066b\u2069", "a\vb", "a\fb"} {
		if err := checkModuleCode(code); err == nil {
			t.Errorf("%q accepted", code)
		}
	}
	if err := checkModuleCode("local s = \"Kuchnia ż 👍\"\n\tx = 1\n"); err != nil {
		t.Errorf("plain code refused: %v", err)
	}
}
