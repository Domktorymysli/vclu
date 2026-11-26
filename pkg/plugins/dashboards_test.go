package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestManifestDashboards(t *testing.T) {
	var m PluginManifest
	ok := `{"id": "zakupy", "api": 2, "permissions": {}, "dashboards": [
		{"id": "lista", "name": {"pl": "Zakupy", "en": "Shopping"}, "description": {"pl": "Lista", "en": "List"}},
		{"id": "kuchnia_2", "name": "Kuchnia"}]}`
	if err := json.Unmarshal([]byte(ok), &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Dashboards) != 2 || m.Dashboards[0].Name.In("pl") != "Zakupy" || m.Dashboards[0].Name.In("en") != "Shopping" ||
		m.Dashboards[1].Name.In("en") != "Kuchnia" {
		t.Fatalf("dashboards: %+v", m.Dashboards)
	}
	if RealDashboardID("@vclu/sun-position", "a_b") != "sun_position_a_b" {
		t.Fatal(RealDashboardID("@vclu/sun-position", "a_b"))
	}

	nine := strings.Repeat(`{"id": "a", "name": "A"},`, 8)
	for what, list := range map[string]string{
		"not a list":    `{"id": "a"}`,
		"upper id":      `[{"id": "Lista", "name": "A"}]`,
		"dash id":       `[{"id": "a-b", "name": "A"}]`,
		"empty id":      `[{"id": "", "name": "A"}]`,
		"long id":       `[{"id": "` + strings.Repeat("a", 65) + `", "name": "A"}]`,
		"twice":         `[{"id": "a", "name": "A"}, {"id": "a", "name": "B"}]`,
		"no name":       `[{"id": "a"}]`,
		"empty name":    `[{"id": "a", "name": {"pl": ""}}]`,
		"language":      `[{"id": "a", "name": {"de": "A"}}]`,
		"name type":     `[{"id": "a", "name": 5}]`,
		"newline":       `[{"id": "a", "name": "A\nB"}]`,
		"long name":     `[{"id": "a", "name": "` + strings.Repeat("ż", 61) + `"}]`,
		"description":   `[{"id": "a", "name": "A", "description": {"pl": "x\u0000"}}]`,
		"more than 8":   `[` + nine + `{"id": "b", "name": "B"}]`,
		"unknown field": `[{"id": "a", "name": "A", "auth": "public"}]`,
	} {
		var m PluginManifest
		err := json.Unmarshal([]byte(`{"id": "x", "dashboards": `+list+`}`), &m)
		if err == nil && what != "unknown field" {
			t.Errorf("%s: accepted", what)
		}
		if what == "unknown field" && err == nil && len(m.Dashboards) != 1 {
			t.Errorf("%s: %+v", what, m.Dashboards) // an unknown key is ignored, as elsewhere in the manifest
		}
	}

	// refused at download like other manifest errors
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	_ = os.WriteFile(path, []byte(`{"id": "x", "api": 2, "permissions": {}, "dashboards": [{"id": "A", "name": "A"}]}`), 0o644)
	if err := checkDownloadedManifest(path); err == nil {
		t.Fatal("download accepted a bad dashboards list")
	}
}

var declaredZakupy = []ManifestDashboard{{ID: "lista", Name: LocalText{PL: "Zakupy", EN: "Shopping"}}}

func TestCheckDashboardAccess(t *testing.T) {
	in := map[string]json.RawMessage{"lista": json.RawMessage(
		`{"name": " Kuchnia ", "auth": "public", "remote": true, "remotePin": true, "pin": "0123", "roles": "Domownicy, gosia,,gosia"}`)}
	out, bad := CheckDashboardAccess(declaredZakupy, in)
	if bad != nil {
		t.Fatal(bad)
	}
	a := out["lista"]
	if a.Name != "Kuchnia" || a.Auth != "public" || a.PIN != "0123" || strings.Join(a.Roles, ",") != "domownicy,gosia" || !a.RemotePIN {
		t.Fatalf("%+v", a)
	}
	// empty name: the manifest's; off drops the rest; no app: no roles nor app PIN
	out, _ = CheckDashboardAccess(declaredZakupy, map[string]json.RawMessage{"lista": json.RawMessage(`{"name": "", "auth": "off", "remote": true, "roles": ["a"]}`)})
	if a := out["lista"]; a.Name != "Zakupy" || a.Remote || len(a.Roles) != 0 {
		t.Fatalf("off: %+v", a)
	}
	out, _ = CheckDashboardAccess(declaredZakupy, map[string]json.RawMessage{"lista": json.RawMessage(`{"auth": "session", "remotePin": true, "roles": ["a"]}`)})
	if a := out["lista"]; a.RemotePIN || len(a.Roles) != 0 || a.PIN != "" {
		t.Fatalf("no app: %+v", a)
	}

	for what, c := range map[string]struct{ body, field string }{
		"auth":      {`{"auth": "everyone"}`, "lista.auth"},
		"pin":       {`{"auth": "pin", "pin": "12345"}`, "lista.pin"},
		"no pin":    {`{"auth": "pin"}`, "lista.pin"},
		"app pin":   {`{"auth": "session", "remote": true, "remotePin": true}`, "lista.pin"},
		"role":      {`{"auth": "session", "remote": true, "roles": ["a b"]}`, "lista.roles"},
		"role type": {`{"auth": "session", "remote": true, "roles": [1]}`, "lista.roles"},
		"name":      {`{"auth": "session", "name": "a\nb"}`, "lista.name"},
		"long name": {`{"auth": "session", "name": "` + strings.Repeat("x", 61) + `"}`, "lista.name"},
		"unknown":   {`{"auth": "session", "shell": "x"}`, "lista"},
	} {
		_, bad := CheckDashboardAccess(declaredZakupy, map[string]json.RawMessage{"lista": json.RawMessage(c.body)})
		if bad[c.field] == "" {
			t.Errorf("%s: %v", what, bad)
		}
	}
	if _, bad := CheckDashboardAccess(declaredZakupy, map[string]json.RawMessage{"inna": json.RawMessage(`{}`)}); bad["inna"] == "" {
		t.Error("an undeclared dashboard")
	}
}

// The generated block, run against a stub Plugin: what was saved reaches dashboardAccess as
// data, whatever the name holds.
func TestDashboardAccessCode(t *testing.T) {
	nasty := `a"]] ..os.exit()--` + "\\" + `'` + "\"\n"
	access := map[string]DashboardAccess{"lista": {Name: nasty, Auth: "pin", PIN: "1234", Remote: true, RemotePIN: true,
		Roles: []string{"domownicy", `x")os.exit(`}}}
	code := DashboardAccessCode("@vclu/zakupy", declaredZakupy, access)
	if !strings.HasPrefix(code, "-- Dostęp do dashboardów (kreator pluginu)") || !strings.Contains(code, "\nlocal zakupy = Plugin.getPlugin(\"@vclu/zakupy\")\n") {
		t.Fatalf("code:\n%s", code)
	}
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
		calls = {}
		Plugin = { getPlugin = function(id)
			return { dashboardAccess = function(self, dash, opts) calls[#calls + 1] = { id = id, dash = dash, opts = opts } end }
		end }
		os = nil`); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(code); err != nil {
		t.Fatalf("%v\n%s", err, code)
	}
	if err := L.DoString(`
		local c = calls[1]
		assert(#calls == 1 and c.id == "@vclu/zakupy" and c.dash == "lista")
		local o = c.opts
		assert(o.auth == "pin" and o.pin == "1234" and o.remote.pin == true)
		got = o.name
		roles = table.concat(o.remote.roles, "|")`); err != nil {
		t.Fatalf("%v\n%s", err, code)
	}
	if L.GetGlobal("got").String() != nasty {
		t.Fatalf("name came back as %q", L.GetGlobal("got").String())
	}
	if L.GetGlobal("roles").String() != "domownicy" {
		t.Fatalf("an invalid role went in: %s", L.GetGlobal("roles").String())
	}

	// off and session without the app say only that
	code = DashboardAccessCode("@jan/end", declaredZakupy, map[string]DashboardAccess{"lista": {Name: "Z", Auth: "off", Remote: true}})
	if !strings.Contains(code, `local plugin_end = Plugin.getPlugin("@jan/end")`) || !strings.Contains(code, "name = \"Z\",\n        auth = \"off\",\n    })") {
		t.Fatalf("off:\n%s", code)
	}
	if DashboardAccessCode("@jan/x", declaredZakupy, nil) != "" {
		t.Fatal("nothing saved, nothing written")
	}
}

func TestSavedDashboardAccess(t *testing.T) {
	got := SavedDashboardAccess(declaredZakupy, nil)
	if a := got["lista"]; a.Name != "Zakupy" || a.Auth != "session" || a.Roles == nil {
		t.Fatalf("defaults: %+v", a)
	}
	cfg := map[string]any{DashboardsKey: map[string]any{"lista": map[string]any{"name": "K", "auth": "public"}, "gone": map[string]any{}}}
	got = SavedDashboardAccess(declaredZakupy, cfg)
	if a := got["lista"]; a.Name != "K" || a.Auth != "public" || len(got) != 1 {
		t.Fatalf("saved: %+v", got)
	}
}
