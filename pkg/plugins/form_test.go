package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFormRefusesBadDeclarations(t *testing.T) {
	for what, raw := range map[string]string{
		"unknown type":           `{"settings": {"a": {"type": "color"}}}`,
		"enum without values":    `{"settings": {"a": {"type": "enum"}}}`,
		"bad name":               `{"settings": {"1a": {"type": "string"}}}`,
		"secret in a row":        `{"settings": {}, "devices": {"fields": {"k": {"type": "secret"}}}}`,
		"devices taken":          `{"settings": {"devices": {"type": "string"}}, "devices": {"fields": {"a": {"type": "string"}}}}`,
		"showIf elsewhere":       `{"settings": {"a": {"type": "string", "showIf": {"b": "x"}}}}`,
		"devices without fields": `{"settings": {}, "devices": {}}`,
		"label of no option":     `{"settings": {"a": {"type": "enum", "options": ["x"], "optionLabels": {"y": "Y"}}}}`,
	} {
		if _, err := ParseForm([]byte(raw)); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
}

// Labels name options; the step with the module code is asked for.
func TestParseFormOptionLabelsAndCode(t *testing.T) {
	f, err := ParseForm([]byte(`{"code": true, "settings": {"a": {"type": "enum", "options": ["x", "y"],
		"optionLabels": {"x": {"pl": "Iks", "en": "Ex"}}}}}`))
	if err != nil || !f.Code || string(f.Settings["a"].OptionLabels["x"]) != `{"pl": "Iks", "en": "Ex"}` {
		t.Fatalf("form %+v, %v", f, err)
	}
}

func TestFormCheck(t *testing.T) {
	form, err := ParseForm([]byte(`{
	  "settings": {
	    "mode":     {"type": "enum", "options": ["local", "cloud"], "default": "local"},
	    "key":      {"type": "secret", "required": true, "showIf": {"mode": "cloud"}},
	    "interval": {"type": "number", "min": 1, "max": 60, "default": 10},
	    "debug":    {"type": "boolean"},
	    "units":    {"type": "string", "enum": ["metric", "imperial"]}
	  },
	  "devices": {"max": 2, "fields": {"ip": {"type": "host", "required": true}}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	v, err := form.Check(map[string]any{"interval": "5", "debug": "true"}, []map[string]any{{"ip": "::ffff:10.0.0.7"}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Config["mode"] != "local" || v.Config["interval"] != 5.0 || v.Config["debug"] != true {
		t.Fatalf("config: %v", v.Config)
	}
	if strings.Join(v.Hosts, ",") != "10.0.0.7" {
		t.Fatalf("hosts: %v", v.Hosts)
	}
	for what, c := range map[string]struct {
		settings map[string]any
		devices  []map[string]any
	}{
		"number over max":   {map[string]any{"interval": 61}, nil},
		"not an option":     {map[string]any{"mode": "lan"}, nil},
		"older enum":        {map[string]any{"units": "kelvin"}, nil},
		"unknown field":     {map[string]any{"color": "red"}, nil},
		"not a boolean":     {map[string]any{"debug": "yes"}, nil},
		"too many rows":     {nil, []map[string]any{{"ip": "10.0.0.1"}, {"ip": "10.0.0.2"}, {"ip": "10.0.0.3"}}},
		"row field unknown": {nil, []map[string]any{{"ip": "10.0.0.1", "x": 1}}},
		"row host":          {nil, []map[string]any{{"ip": "shelly.local"}}},
	} {
		if _, err := form.Check(c.settings, c.devices); err == nil {
			t.Errorf("%s: accepted", what)
		}
	}
	// a secret asked only in cloud mode: left out keeps the stored one, set in cloud mode is kept
	v, err = form.Check(map[string]any{"mode": "cloud", "key": "abc"}, nil)
	if err != nil || v.Secrets["key"] != "abc" {
		t.Fatalf("secret: %v %v", v.Secrets, err)
	}
}

func TestReadFormFallsBackToConfig(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id": "w", "config": {"city": {"type": "string", "default": "Warsaw"}}}`), 0o644)
	form, raw, err := ReadForm(dir)
	if err != nil || form == nil || form.Settings["city"].Type != "string" || !strings.HasPrefix(string(raw), `{"settings":`) {
		t.Fatalf("form %v raw %s err %v", form, raw, err)
	}
	_ = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id": "w", "form": "../x.json"}`), 0o644)
	if _, _, err := ReadForm(dir); err == nil {
		t.Fatal("a form outside the plugin directory")
	}
	_ = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id": "w"}`), 0o644)
	if form, _, err := ReadForm(dir); form != nil || err != nil {
		t.Fatalf("no form: %v %v", form, err)
	}
}

func TestModuleNamesNeverMeet(t *testing.T) {
	seen := map[string]string{}
	for _, id := range []string{"@vclu/shelly", "@vclu/shelly.plus", "@vclu.x/shelly", "@a/b-c", "@a-b/c", "@a/b.c"} {
		n := ModuleName(id)
		if other, ok := seen[n]; ok {
			t.Fatalf("%s and %s are both %s", id, other, n)
		}
		seen[n] = id
	}
	if ModuleName("@vclu/shelly") != "vclu__shelly" {
		t.Fatal(ModuleName("@vclu/shelly"))
	}
}

func TestPluginSecrets(t *testing.T) {
	dir := t.TempDir()
	if err := SetPluginSecrets(dir, "@a/b", map[string]string{"k": "v", "k2": "w"}); err != nil {
		t.Fatal(err)
	}
	_ = SetPluginSecrets(dir, "@a/c", map[string]string{"k": "other"})
	_ = SetPluginSecrets(dir, "@a/b", map[string]string{"k2": ""})
	got, _ := PluginSecrets(dir, "@a/b")
	if len(got) != 1 || got["k"] != "v" {
		t.Fatalf("secrets: %v", got)
	}
	st, _ := os.Stat(filepath.Join(dir, SecretsFile))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	_ = DeletePluginSecrets(dir, "@a/b")
	if got, _ := PluginSecrets(dir, "@a/b"); len(got) != 0 {
		t.Fatalf("after delete: %v", got)
	}
	if got, _ := PluginSecrets(dir, "@a/c"); got["k"] != "other" {
		t.Fatalf("another plugin's: %v", got)
	}
}
