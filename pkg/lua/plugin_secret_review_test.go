package lua

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// Review of #206: a secret stays out of the log however the plugin's code lets it out.
func TestReviewExecSecretMaskedInFormatsAndErrors(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/clu", map[string]interface{}{"api": 2, "form": "form.json"}, `
local plugin = Plugin:new("clu", {})
plugin:onInit(function(cfg)
    local token = plugin:secret("token")
    Logger:info("format %s", token)
    Logger:warn("%s|%s", "a", token)
    setTimeout(1, function() error("timer " .. token) end)
    setInterval(100000, function() end)
    plugin:on("t:ev", function() error("event " .. token) end)
    EventBus.emit("@jan/clu:x")
    local p = plugin:poller("p", { interval = 50, onTick = function() error("tick " .. token) end })
    p:start()
    plugin:httpRequest({ url = "http://127.0.0.1:1/" .. token .. "/x", log = { level = "info" } }, function() end)
end)
return plugin`, false)
	mustWrite(t, filepath.Join(dir, "plugins", "jan--clu", "1.0.0", "form.json"), []byte(wizardForm))
	if err := plugins.SetPluginSecrets(dir, "@jan/clu", map[string]string{"token": "S3cr3tTok"}); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)
	engine := apiEngine(t, loader, dir)
	engine.StartTimers()
	defer engine.StopTimers()
	if err := loader.LoadPlugin("@jan/clu", map[string]any{"ip": "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(`EventBus:getShared():emit("t:ev")`); err != nil {
		t.Fatal(err)
	}
	pumpTimers(engine, 300*time.Millisecond)
	out := logs.String()
	if strings.Contains(out, "S3cr3tTok") {
		var leaks []string
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, "S3cr3tTok") {
				leaks = append(leaks, l)
			}
		}
		t.Errorf("a secret in the log:\n%s", strings.Join(leaks, "\n"))
	}
	for _, want := range []string{"format ***", "a|***", "timer ***", "event ***", "tick ***"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in the log", want)
		}
	}
}

// An error raised by the plugin's init with its secret in it: masked in the loader's error too.
func TestReviewExecSecretMaskedInLoadError(t *testing.T) {
	for name, init := range map[string]string{
		"init.lua": `local plugin = Plugin:new("clu", {}); error("bad " .. plugin:secret("token"))`,
		"onInit":   `local plugin = Plugin:new("clu", {}); plugin:onInit(function() error("bad " .. plugin:secret("token")) end); return plugin`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			loader := installTestPlugin(t, dir, "@jan/clu", map[string]interface{}{"api": 2, "form": "form.json"}, init, false)
			mustWrite(t, filepath.Join(dir, "plugins", "jan--clu", "1.0.0", "form.json"), []byte(wizardForm))
			if err := plugins.SetPluginSecrets(dir, "@jan/clu", map[string]string{"token": "S3cr3tTok"}); err != nil {
				t.Fatal(err)
			}
			logs := captureLog(t)
			apiEngine(t, loader, dir)
			err := loader.LoadPlugin("@jan/clu", map[string]any{"ip": "10.0.0.1"})
			if err == nil {
				t.Fatal("no error")
			}
			if strings.Contains(err.Error(), "S3cr3tTok") {
				t.Errorf("secret in the error: %v", err)
			}
			if strings.Contains(logs.String(), "S3cr3tTok") {
				t.Errorf("secret in the log")
			}
		})
	}
}
