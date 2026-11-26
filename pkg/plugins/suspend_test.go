package plugins

import (
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

type noLua struct{ scripts int }

func (n *noLua) Execute(string) (string, error) { n.scripts++; return "", nil }

// A suspended plugin does not load, by hand or on a reload, until resumed.
func TestSuspendedPluginStaysOff(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/slow", Enabled: true}})
	lua := &noLua{}
	l.SetLuaExecutor(lua)
	l.Suspend("@jan/slow")
	if !l.IsSuspended("@jan/slow") {
		t.Fatal("not suspended")
	}
	if err := l.LoadPlugin("@jan/slow", nil); err == nil || !strings.Contains(err.Error(), "suspended") {
		t.Errorf("load: %v", err)
	}
	if err := l.LoadAllEnabled(); err != nil || lua.scripts != 0 || l.IsLoaded("@jan/slow") {
		t.Errorf("reload: %v, %d scripts", err, lua.scripts)
	}
	l.Resume("@jan/slow")
	if l.IsSuspended("@jan/slow") {
		t.Error("still suspended")
	}
	if err := l.LoadPlugin("@jan/slow", nil); err != nil && strings.Contains(err.Error(), "suspended") {
		t.Errorf("after resume: %v", err)
	}
}
