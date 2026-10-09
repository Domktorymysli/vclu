package lua

import (
	"encoding/json"
	"testing"
)

// One odd reading or name must not break the list HomeKit is built from
// (an unparsable list left the bridge with no accessories).
func TestExposedToJSON_SurvivesOddValuesAndNames(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	res, err := engine.Execute(`
		local nan = VirtualNumber:new("nan")
		expose(nan, "temperature", { name = "Licznik", path = "t:nan" })
		nan._state = 0/0
		local inf = VirtualNumber:new("inf")
		expose(inf, "number", { name = "Inf", path = "t:inf" })
		inf._state = 1/0
		local odd = VirtualSwitch:new("odd")
		expose(odd, "switch", { name = "a\\b\n\"c\001", path = "t:odd", area = "Pokój \"x\"" })
		local p = SatelPartition:new(1)
		registerObject("satel.partition.1", p)
		expose(p, "switch", { path = "satel.partition.1" })
		return ExposedObjects:toJSON({ homekit = true })
	`)
	if err != nil {
		t.Fatal(err)
	}
	var objs []struct {
		Path  string          `json:"path"`
		Name  string          `json:"name"`
		Area  string          `json:"area"`
		Value json.RawMessage `json:"value"`
		Satel bool            `json:"satel"`
		Caps  map[string]bool `json:"caps"`
	}
	if err := json.Unmarshal([]byte(res), &objs); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, res)
	}
	by := map[string]int{}
	for i, o := range objs {
		by[o.Path] = i
	}
	for _, p := range []string{"t:nan", "t:inf"} {
		if string(objs[by[p]].Value) != "null" {
			t.Errorf("%s value %s, want null", p, objs[by[p]].Value)
		}
	}
	if o := objs[by["t:odd"]]; o.Name != "a\\b\n\"c\001" || o.Area != `Pokój "x"` {
		t.Errorf("name/area not round-tripped: %q %q", o.Name, o.Area)
	}
	if !objs[by["satel.partition.1"]].Satel {
		t.Error("Satel partition not marked")
	}
	if objs[by["t:odd"]].Satel {
		t.Error("plain switch marked as Satel")
	}
}
