package mcp

import "testing"

// hasRequire decides whether draft_promote warns that a subdirectory module will
// not be loaded. It replaced a plain strings.Contains, which was wrong in both
// directions: it matched requires inside comments, and it rejected the dotted
// form even though Lua resolves "a.b" and "a/b" to the same file.

func TestHasRequire(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		modPath string
		want    bool
	}{
		{
			name:    "slash form",
			src:     `require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "dotted form resolves to the same file",
			src:     `require("dashboards.homelab")`,
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "single quotes",
			src:     `require('dashboards/homelab')`,
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "no parentheses",
			src:     `require "dashboards/homelab"`,
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "assigned to a local",
			src:     `local d = require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "commented out does not count",
			src:     `-- require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "mentioned only in a comment does not count",
			src:     "-- remember to add dashboards/homelab here\n",
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "trailing comment on a real require still counts",
			src:     `require("dashboards/homelab") -- the lab page`,
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "different module",
			src:     `require("dashboards/kosiarka")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "prefix must not match a longer path",
			src:     `require("dashboards/homelab_old")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "the modules. prefix is not a valid require and must not match",
			src:     `require("modules.dashboards.homelab")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "found among several requires",
			src:     "require(\"dashboards/a\")\nrequire(\"dashboards/homelab\")\nrequire(\"dashboards/b\")\n",
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "a directory module's init.lua is required by the directory name",
			src:     `require("pogoda")`,
			modPath: "pogoda/init",
			want:    true,
		},
		{
			name:    "a directory module's init.lua by its full path",
			src:     `require("pogoda/init")`,
			modPath: "pogoda/init",
			want:    true,
		},
		{
			name:    "another file of the module is not loaded by the directory name",
			src:     `require("pogoda")`,
			modPath: "pogoda/influx",
			want:    false,
		},
		{
			name:    "empty source",
			src:     "",
			modPath: "dashboards/homelab",
			want:    false,
		},

		// False positives are the dangerous direction: each of these would
		// suppress the "will not be loaded" warning and restore the silent
		// failure the check exists to catch.
		{
			name:    "inside a block comment does not count",
			src:     "--[[\nrequire(\"dashboards/homelab\")\n]]",
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "inside a long block comment with equals does not count",
			src:     "--[==[\nrequire(\"dashboards/homelab\")\n]==]",
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "a different function ending in require does not count",
			src:     `safe_require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "a method call named require does not count",
			src:     `helpers.require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "a colon method call named require does not count",
			src:     `loader:require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
		{
			name:    "a real require after a block comment still counts",
			src:     "--[[ old notes ]]\nrequire(\"dashboards/homelab\")",
			modPath: "dashboards/homelab",
			want:    true,
		},
		{
			name:    "an underscore-prefixed require is still a distinct name",
			src:     `my_require("dashboards/homelab")`,
			modPath: "dashboards/homelab",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasRequire(tt.src, tt.modPath); got != tt.want {
				t.Errorf("hasRequire(%q, %q) = %v, want %v", tt.src, tt.modPath, got, tt.want)
			}
		})
	}
}

// Lua's Logger writes "[DEBUG] ..." and LogStream captures that tag as the
// source while defaulting the level to "info". Without resolving it, a level
// filter would not remove Lua debug output — the flood that hid a real error.
func TestEffectiveLogLevel(t *testing.T) {
	tests := []struct {
		level, source, want string
	}{
		{"info", "DEBUG", "debug"},
		{"info", "WARN", "warning"},
		{"info", "WARNING", "warning"},
		{"info", "ERROR", "error"},
		{"error", "MQTT", "error"},
		{"warning", "MQTT", "warning"},
		{"info", "MQTT", "info"},
		{"info", "", "info"},
		{"", "", "info"},
		{"nonsense", "", "info"},

		// The two signals must combine as max-severity, never source-overrides.
		// parseLine tags a line containing "failed" as level=error while Lua's
		// Logger stamps source=DEBUG; letting the source win would hide a real
		// failure from vclu_logs(level:"error").
		{"error", "DEBUG", "error"},
		{"warning", "DEBUG", "warning"},
		{"error", "WARN", "error"},
		{"debug", "ERROR", "error"},
		{"warn", "", "warning"},
	}

	for _, tt := range tests {
		if got := effectiveLogLevel(tt.level, tt.source); got != tt.want {
			t.Errorf("effectiveLogLevel(%q, %q) = %q, want %q", tt.level, tt.source, got, tt.want)
		}
	}
}

func TestLogLevelRankOrdering(t *testing.T) {
	if !(logLevelRank["debug"] < logLevelRank["info"] &&
		logLevelRank["info"] < logLevelRank["warning"] &&
		logLevelRank["warning"] < logLevelRank["error"]) {
		t.Errorf("log levels are not ordered debug < info < warning < error: %+v", logLevelRank)
	}
	if logLevelRank["warn"] != logLevelRank["warning"] {
		t.Error("warn and warning must rank equally")
	}
}
