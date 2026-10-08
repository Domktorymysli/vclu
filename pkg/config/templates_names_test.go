package config

import "testing"

// Template names must match the files in git exactly: macOS ignores case,
// Linux builds (CI, goreleaser) do not.
func TestTemplatesUsedByPropagateConfigExist(t *testing.T) {
	for _, name := range []string{"config.txt", "main.lua", "om.lua", "user.lua"} {
		if _, err := GetTemplate(name); err != nil {
			t.Errorf("template %s: %v", name, err)
		}
	}
}
