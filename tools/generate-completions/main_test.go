package main

import (
	"encoding/json"
	"os"
	"testing"
)

// The Lua editor and the registry browser fetch this file and drop it silently when it does not
// parse (it lacked a comma from v1.2.0 to #82).
func TestCompletionsFileParses(t *testing.T) {
	data, err := os.ReadFile("../../web/static/js/lua-completions.json")
	if err != nil {
		t.Fatal(err)
	}
	var c Completions
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("lua-completions.json does not parse: %v", err)
	}
	if len(c.Globals) == 0 || len(c.Methods["_"]) == 0 {
		t.Fatalf("lua-completions.json has no globals or no registry methods")
	}
	for _, m := range c.Methods["_"] {
		if m.Label == "value" {
			return
		}
	}
	t.Errorf("registry methods in lua-completions.json lack value")
}
