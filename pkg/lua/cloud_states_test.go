package lua

import (
	"encoding/json"
	"testing"
)

// Contract (vclu-cloud's protocol, "states"): Cloud._states tells what the devices exposed with
// :remote() are doing, for the person's roles; anything else is left out, never refused.
func TestCloudStates(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	mustExec(t, engine, `
		local function dev(v) return { _lastState = v, get = function(s) return s._lastState end, onChange = function() return function() end end } end
		expose(dev(1), "binary_sensor", { id = "okno" }):remote({ readonly = true })
		expose(dev(0), "binary_sensor", { id = "drzwi" }):remote({ readonly = true })
		expose(dev(true), "light", { id = "lampa" }):remote()
		expose(dev(21.46), "temperature", { id = "temp" }):remote()
		expose(dev("x"), "temperature", { id = "zepsuty" }):remote()
		local roller = dev(0)
		roller.getPosition = function() return 40 end
		expose(roller, "cover", { id = "roleta" }):remote()
		local old = dev(0) -- a Grenton roller without a position
		old._hasPosition, old.getPosition = false, function() return 0 end
		expose(old, "cover", { id = "stara" }):remote()
		expose(dev(1), "switch", { id = "brama" }):remote():roles("domownicy")
		expose(dev(1), "light", { id = "lokalna" })
		expose(dev(1), "scene", { id = "kino" }):remote()`)

	ask := func(paths []string, roles string) map[string]map[string]any {
		t.Helper()
		b, _ := json.Marshal(paths)
		out, err := engine.Call("Cloud._states", string(b), roles)
		if err != nil {
			t.Fatal(err)
		}
		var list []map[string]any
		if err := json.Unmarshal([]byte(out), &list); err != nil {
			t.Fatalf("answer %q: %v", out, err)
		}
		got := map[string]map[string]any{}
		for _, s := range list {
			got[s["path"].(string)] = s
		}
		return got
	}
	all := []string{"vclu:okno", "vclu:drzwi", "vclu:lampa", "vclu:temp", "vclu:zepsuty", "vclu:roleta",
		"vclu:stara", "vclu:brama", "vclu:lokalna", "vclu:kino", "vclu:nie-ma"}
	got := ask(all, "[]")
	if got["vclu:okno"]["on"] != true || got["vclu:drzwi"]["on"] != false || got["vclu:lampa"]["on"] != true {
		t.Errorf("on = %v", got)
	}
	if got["vclu:temp"]["value"] != 21.46 || got["vclu:roleta"]["position"] != 40.0 {
		t.Errorf("readings = %v %v", got["vclu:temp"], got["vclu:roleta"])
	}
	for _, p := range []string{"vclu:zepsuty", "vclu:stara", "vclu:brama", "vclu:lokalna", "vclu:kino", "vclu:nie-ma"} {
		if s, ok := got[p]; ok {
			t.Errorf("%s answered: %v", p, s)
		}
	}
	if got := ask([]string{"vclu:brama"}, `["domownicy"]`); got["vclu:brama"]["on"] != true {
		t.Errorf("with the role = %v", got)
	}
	if out, _ := engine.Call("Cloud._states", "nonsense", "[]"); out != "[]" {
		t.Errorf("bad paths = %s", out)
	}
}
