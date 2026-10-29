package lua

import (
	"encoding/json"
	"strings"
	"testing"
)

// :remote() decides which dashboards vclu cloud lists, :roles() for whom; without it nothing.
func TestDashboardRemoteList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		Dashboard.new('home'):name('Dom'):icon('house'):remote():section('S', 'i'):build()
		Dashboard.new('off'):remote():remote(false):section('S', 'i'):build()
		Dashboard.new('alarm'):name('Alarm'):icon('shield')
			:remote({ pin = true }):roles('owner', 'ochrona'):pin('1234')
			:section('S', 'i'):build()
		Dashboard.new('nopin'):remote({ pin = true }):section('S', 'i'):build()
		Dashboard.new('local'):section('S', 'i'):build()
		Dashboard.dynamic('dyn', function(d) d:name('Dyn'):remote():roles('gosia'):section('S', 'i') end)
	`); err != nil {
		t.Fatal(err)
	}
	out, err := engine.Execute(`Dashboard.API.remoteList()`)
	if err != nil {
		t.Fatal(err)
	}
	var list []struct {
		ID    string   `json:"id"`
		Name  string   `json:"name"`
		Icon  string   `json:"icon"`
		Roles []string `json:"roles"`
		Pin   bool     `json:"pin"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	got := map[string]int{}
	for i, d := range list {
		got[d.ID] = i
	}
	if len(list) != 3 {
		t.Fatalf("remote dashboards %s; want home, alarm, dyn (nopin has no PIN to ask, local is not remote)", out)
	}
	home, alarm, dyn := list[got["home"]], list[got["alarm"]], list[got["dyn"]]
	if len(home.Roles) != 0 || !strings.Contains(out, `"roles":[]`) || home.Name != "Dom" || home.Icon != "house" || home.Pin {
		t.Errorf("home (every member) %+v", home)
	}
	if strings.Join(alarm.Roles, ",") != "owner,ochrona" || !alarm.Pin {
		t.Errorf("alarm %+v", alarm)
	}
	if strings.Join(dyn.Roles, ",") != "gosia" || dyn.Name != "Dyn" {
		t.Errorf("dynamic %+v", dyn)
	}

	// none at all is an empty list, not an object
	empty := NewLuaEngine()
	defer empty.Close()
	if out, _ := empty.Execute(`Dashboard.API.remoteList()`); out != "[]" {
		t.Errorf("no dashboards: %s", out)
	}
}

func TestDashboardRemoteErrors(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	for script, want := range map[string]string{
		`Dashboard.new('a'):remote('yes')`:                                                                "expects nothing, true, false or a table",
		`Dashboard.new('a'):remote({ role = { 'owner' } })`:                                               `unknown option "role"`,
		`Dashboard.new('a'):remote({ roles = { 'owner' } })`:                                              `unknown option "roles"`,
		`Dashboard.new('a'):roles('owner')`:                                                               "call :remote() first",
		`Dashboard.new('a'):remote():roles()`:                                                             "at least one role",
		`Dashboard.new('a'):remote():roles('Biuro')`:                                                      `invalid role "Biuro"`,
		`Dashboard.new('a'):remote({ poll = 'fast' })`:                                                    "poll must be a number",
		`Dashboard.new('a'):section('S','i'):status('x','X','i',nil,nil,{ remote = 'hidden' })`:           "opts.remote is false",
		`Dashboard.new('a'):section('S','i'):toggle('x','X','i',nil,nil,{ remote = { confirm = true } })`: "is for buttons",
	} {
		if _, err := engine.Execute(script); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", script, err, want)
		}
	}
	// poll has a floor; tabs show only dashboards the remote viewer sees
	if _, err := engine.Execute(`
		Dashboard.new('a'):remote({ poll = 1 }):tabGroup('g'):section('S', 'i'):build()
		Dashboard.new('b'):tabGroup('g'):section('S', 'i'):build()
		Dashboard.new('c'):remote(true):tabGroup('g'):section('S', 'i'):build()
	`); err != nil {
		t.Fatal(err)
	}
	out, _ := engine.Execute(`Dashboard.API.getDefinition('a', 'member')`)
	var def struct {
		Poll      float64 `json:"poll"`
		GroupTabs []struct {
			Tabs []struct {
				ID string `json:"id"`
			} `json:"tabs"`
		} `json:"groupTabs"`
	}
	json.Unmarshal([]byte(out), &def)
	if def.Poll != 2 || len(def.GroupTabs) != 1 || len(def.GroupTabs[0].Tabs) != 2 || def.GroupTabs[0].Tabs[1].ID != "c" {
		t.Errorf("remote definition %s", out)
	}
	local, _ := engine.Execute(`Dashboard.API.getDefinition('a')`)
	if !strings.Contains(local, `"id":"b"`) {
		t.Errorf("the house's tabs lost b: %s", local)
	}
}
