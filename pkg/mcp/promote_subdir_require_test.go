package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A promoted file in a subdirectory of modules/ is loaded when modules/init.lua requires it
// (require("pogoda") for modules/pogoda/init.lua, too) or when another module requires it
// (require("energia/power") in modules/energia/init.lua). Only a file nothing requires
// gets the warning (#20).
func TestSubdirRequireWarnings(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("modules/init.lua", `require("pogoda")
require("energia")
require("dashboards/dom")
`)
	write("modules/pogoda/init.lua", `return {}`)
	write("modules/energia/init.lua", `local power = require("energia/power")
return { power = power }`)
	write("modules/energia/power.lua", `return {}`)
	write("modules/dashboards/dom.lua", `local x = 1`)
	write("modules/zapomniany/init.lua", `return {}`)
	write("modules/energia/stary.lua", `-- require("energia/stary") only in a comment of itself
return {}`)

	s := &MCPServer{files: NewFileProvider(dir)}
	got := s.subdirRequireWarnings([]string{
		"modules/pogoda/init.lua",     // required by its directory name
		"modules/energia/power.lua",   // required by another module
		"modules/dashboards/dom.lua",  // required by modules/init.lua
		"modules/zapomniany/init.lua", // nothing requires it
		"modules/energia/stary.lua",   // only itself mentions it
		"modules/flat.lua",            // flat: auto-loaded, no check
	})
	joined := strings.Join(got, "\n")
	if len(got) != 2 || !strings.Contains(joined, "modules/zapomniany/init.lua") || !strings.Contains(joined, "modules/energia/stary.lua") {
		t.Errorf("warnings = %d:\n%s\nwant only zapomniany/init.lua and energia/stary.lua", len(got), joined)
	}
}
