package lua

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// A plugin stopped at its budget, also inside its own error handler, logs the stop with its
// secret masked, and its next timer still runs.
func TestPluginBudgetStopIsMaskedAndEnds(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/spin", map[string]interface{}{"api": 2, "form": "form.json"}, `
local plugin = Plugin:new("spin", {})
plugin.after = 0
plugin:onInit(function(cfg)
    local token = plugin:secret("token")
    setTimeout(10, function()
        xpcall(function() while true do end end, function(m)
            Logger:warn("handler %s %s", token, tostring(m))
            while true do end
        end)
    end)
    setTimeout(10, function() error("after " .. token) end)
    setTimeout(60, function() plugin.after = plugin.after + 1 end)
end)
return plugin`, false)
	mustWrite(t, filepath.Join(dir, "plugins", "jan--spin", "1.0.0", "form.json"), []byte(wizardForm))
	if err := plugins.SetPluginSecrets(dir, "@jan/spin", map[string]string{"token": "S3cr3tTok"}); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)
	engine := apiEngine(t, loader, dir)
	engine.SetPluginTimeBudget(200*time.Millisecond, time.Second)
	engine.StartTimers()
	defer engine.StopTimers()
	if err := loader.LoadPlugin("@jan/spin", map[string]any{"ip": "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	waitPlugin(t, engine, `Plugin.getPlugin("@jan/spin").after`, "1")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the next timer ran after %v", d)
	}
	out := logs.String()
	if strings.Contains(out, "S3cr3tTok") {
		t.Errorf("secret in the log:\n%s", out)
	}
	if !strings.Contains(out, "callback over") || !strings.Contains(out, "after ***") {
		t.Errorf("the stop or the later error, masked, not in the log:\n%s", out)
	}
}
