package web

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// Names with quotes reach Lua unchanged in the scripts Go builds for it.
func TestSyncScriptsQuoteNames(t *testing.T) {
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		_got = {}
		LuaAPI.syncProfiles = function(p, a) _got.profiles = p; _got.assignments = a; return "OK" end
		LuaAPI.syncRemoteConfig = function(c) _got.sync = c; return "OK" end
	`); err != nil {
		t.Fatal(err)
	}
	am := accesscontrol.NewManager(t.TempDir())
	name := `Gość "ogród"`
	if err := am.SaveProfile(name, accesscontrol.Profile{
		Default: accesscontrol.AccessReadonly,
		Objects: map[string]accesscontrol.AccessLevel{`CLU1."x"`: accesscontrol.AccessHidden},
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine, accessManager: am}

	s.syncProfilesToLua()
	got, err := engine.Execute(`local p = _got.profiles["Gość \"ogród\""]
		return p and (p.default .. "/" .. p.objects["CLU1.\"x\""]) or "missing"`)
	if err != nil || got != "readonly/hidden" {
		t.Errorf("profile %q reached Lua as %q (%v)", name, got, err)
	}

	s.syncRemoteConfigToLua(&config.VCLUSyncConfig{Enabled: false, Objects: []string{`CLU1.a"b`}})
	got, err = engine.Execute(`return _got.sync and _got.sync.objects[1] or "missing"`)
	if err != nil || got != `CLU1.a"b` {
		t.Errorf("sync object reached Lua as %q (%v)", got, err)
	}
}
