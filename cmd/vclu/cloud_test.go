package main

import (
	"testing"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"

	"github.com/Domktorymysli/vclu/pkg/lua"
)

// The device list and the commands go between the cloud client and Lua as the protocol has them.
func TestCloudObjectsAndCommand(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_on = 0
		local lamp = { _lastState = 0, get = function(s) return s._lastState end, set = function() end,
			switchOn = function() _on = _on + 1 end, onChange = function() return function() end end }
		expose(lamp, "light", { id = "lamp", name = "Lampa", area = "Salon" }):pin("0420"):remote({ pin = true }):roles("domownicy")
	`); err != nil {
		t.Fatal(err)
	}
	list, err := cloudObjects(engine)()
	if err != nil || len(list) != 1 {
		t.Fatalf("objects %+v %v", list, err)
	}
	if o := list[0]; o.Path != "vclu:lamp" || o.Type != "light" || o.Name != "Lampa" || o.Area != "Salon" ||
		o.Access != proto.AccessPin || len(o.Roles) != 1 || o.Roles[0] != "domownicy" {
		t.Errorf("object %+v", o)
	}

	run := cloudCommand(engine, nil)
	cmd := proto.Cmd{ID: "c1", Path: "vclu:lamp", Action: proto.ActionOn, Pin: "0420",
		Actor: proto.Actor{User: "cloud:7", Roles: []string{"domownicy"}}}
	if r := run(cmd, time.Now().Add(time.Minute)); !r.OK {
		t.Errorf("command = %+v", r)
	}
	if r := run(cmd, time.Now().Add(-time.Second)); r.Reason != proto.ReasonExpired {
		t.Errorf("past its deadline = %+v", r)
	}
	cmd.Actor.Roles = nil
	if r := run(cmd, time.Now().Add(time.Minute)); r.Reason != proto.ReasonForbidden {
		t.Errorf("without the role = %+v", r)
	}
	if out, _ := engine.Execute(`_on`); out != "1" {
		t.Errorf("switchOn ran %s times", out)
	}
}

// A home's cloud app is recorded apart from its member's phone, and learns its name for the history;
// only a well-formed app id gets there.
func TestCmdActor(t *testing.T) {
	app := &proto.ActorApp{ID: "3", Name: "MacBar"}
	for _, c := range []struct {
		actor proto.Actor
		want  string
	}{
		{proto.Actor{User: "cloud:7", Via: "app"}, "cloud:7"},
		{proto.Actor{User: "cloud:7", Via: "home_app", App: app}, "app:3@cloud:7"},
		{proto.Actor{User: "cloud:7", Via: "app", App: app}, "cloud:7"},
		{proto.Actor{User: "cloud:7", Via: "home_app"}, "cloud:7"},
		{proto.Actor{User: "cloud:7", Via: "home_app", App: &proto.ActorApp{ID: "3@cloud:1"}}, "cloud:7"},
	} {
		if got := cmdActor(c.actor); got != c.want {
			t.Errorf("%+v = %q, want %q", c.actor, got, c.want)
		}
	}

	engine := lua.NewLuaEngine()
	defer engine.Close()
	var noted []string
	run := cloudCommand(engine, func(id, name string) { noted = append(noted, id+"="+name) })
	run(proto.Cmd{ID: "c1", Path: "vclu:none", Action: proto.ActionOn, Actor: proto.Actor{User: "cloud:7", Via: "home_app", App: app}}, time.Now().Add(time.Minute))
	run(proto.Cmd{ID: "c2", Path: "vclu:none", Action: proto.ActionOn, Actor: proto.Actor{User: "cloud:7", Via: "app"}}, time.Now().Add(time.Minute))
	if len(noted) != 1 || noted[0] != "3=MacBar" {
		t.Errorf("noted %v", noted)
	}
}
