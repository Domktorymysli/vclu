package mcp

import (
	"testing"
)

func TestValidateDraft_ValidCode(t *testing.T) {
	code := `-- Scene: Movie Mode
local function movieMode()
    _registry:get("custom:living_main"):set(0, 20)
end

local scene = { execute = function(self) movieMode() end }
expose(scene, "scene", {
    name = "Film", path = "scene:movie", area = "Salon",
})
`
	report := ValidateDraft(code)
	if !report.Valid {
		t.Errorf("expected valid, got errors: %v", report.Errors)
	}
}

func TestValidateDraft_SyntaxError(t *testing.T) {
	code := `function broken(
    -- missing closing paren
end`
	report := ValidateDraft(code)
	if report.Valid {
		t.Error("expected invalid for syntax error")
	}
	if len(report.Errors) == 0 {
		t.Error("expected syntax error message")
	}
}

func TestValidateDraft_BlockedFunctions(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"os.execute", `os.execute("rm -rf /")`},
		{"io.open", `local f = io.open("/etc/passwd")`},
		{"os.remove", `os.remove("/tmp/file")`},
		{"loadfile", `loadfile("evil.lua")()`},
		{"dofile", `dofile("evil.lua")`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := ValidateDraft(tt.code)
			if report.Valid {
				t.Errorf("expected invalid for %s", tt.name)
			}
		})
	}
}

func TestValidateDraft_RequireWarning(t *testing.T) {
	code := `local utils = require("helpers/utils")
utils.doSomething()
`
	report := ValidateDraft(code)
	if !report.Valid {
		t.Error("require should produce warning, not error")
	}
	if len(report.Warnings) == 0 {
		t.Error("expected warning about require")
	}
}
