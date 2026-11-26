package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Texts from plugin.json (names, descriptions, why, actions, the older configSchema) reach the
// plugins page only escaped: every concatenation of such a value into HTML goes through
// VCLU.esc or textContent.
func TestFablePluginsPageEscapesManifestTexts(t *testing.T) {
	src, err := os.ReadFile("../../web/static/js/plugins.js")
	if err != nil {
		t.Skip(err)
	}
	js := string(src)
	// the config form built from configSchema / config of plugin.json
	start := strings.Index(js, "PluginsManager.prototype._renderConfigForm")
	end := strings.Index(js, "PluginsManager.prototype._renderSimpleConfigForm")
	if start < 0 || end < start {
		t.Fatal("config form renderers not found")
	}
	form := js[start:end]
	for _, raw := range []string{"+ field.description +", "+ opt +", "+ currentValue +", "+ rawKey +", "+ configSchema[key]"} {
		if strings.Contains(form, raw) {
			t.Errorf("_renderConfigForm concatenates %q into HTML without escaping", raw)
		}
	}
	simple := js[end:]
	simple = simple[:strings.Index(simple, "\n};")]
	if strings.Contains(simple, "+ config[key] +") || strings.Contains(simple, "'\" value=\"' + key") {
		t.Error("_renderSimpleConfigForm concatenates values into HTML without escaping")
	}
	// why, descriptions and names of actions and permissions in the sentences and dialogs
	unescaped := regexp.MustCompile(`\+ (localText\([^)]*\)|a\.description|a\.name|why|perm\.why|plugin\.name|plugin\.description|field\.description) \+`)
	for _, line := range strings.Split(js, "\n") {
		if strings.Contains(line, "innerHTML") || !strings.Contains(line, "'<") {
			continue
		}
		if m := unescaped.FindString(line); m != "" && !strings.Contains(line, "VCLU.esc("+strings.Trim(m, "+ ")) {
			t.Errorf("unescaped %q in: %s", m, strings.TrimSpace(line))
		}
	}
}
