package lua

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// waitLogin polls the sign-in until it leaves "pending" with what the panel saw last.
func waitLogin(t *testing.T, loader *plugins.Loader, id, req string, until func(plugins.LoginState) bool) plugins.LoginState {
	t.Helper()
	var st plugins.LoginState
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var err error
		st, err = loader.LoginPoll(id, req)
		if err != nil {
			t.Fatal(err)
		}
		if until(st) {
			return st
		}
	}
	t.Fatalf("sign-in state stayed %+v", st)
	return st
}

// An OAuth device-code flow (RFC 8628) from the wizard, end to end: the code and link reach
// the panel, the token the plugin gets is saved as its secret and never shown.
func TestPluginLoginDeviceCode(t *testing.T) {
	var approved atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device/code":
			_, _ = w.Write([]byte(`{"device_code":"dc-1","user_code":"WDJB-MJHT","verification_uri":"https://example.com/device","interval":1}`))
		case "/token":
			if !approved.Load() {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"at-secret-123","refresh_token":"rt-456"}`))
		}
	}))
	defer srv.Close()
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]

	init := `
local plugin = Plugin:new("dev", { name = "Device" })
plugin:login(function(cfg, show, done)
    plugin:httpRequest({ url = "` + srv.URL + `/device/code", method = "POST" }, function(resp, err)
        if err then return done(nil, err) end
        local d = JSON:decode(resp.body)
        show({ code = d.user_code, url = d.verification_uri, hint = "Code for vclu" })
        local timer
        timer = setInterval(100, function()
            plugin:httpRequest({ url = "` + srv.URL + `/token", method = "POST" }, function(r)
                local t = JSON:decode(r.body)
                if t.access_token then
                    clearInterval(timer)
                    done({ token = t.access_token, refresh = t.refresh_token })
                end
            end)
        end)
    end)
end)
return plugin
`
	dir := t.TempDir()
	p, _ := strconv.Atoi(port)
	loader := installTestPlugin(t, dir, "@jan/dev", map[string]interface{}{"api": 2,
		"permissions": map[string]any{"lan": map[string]any{"ports": []int{p}}}}, init, false)
	engine := apiEngine(t, loader, dir)
	engine.httpManager.ownAddr = func(netip.Addr) bool { return false }
	engine.StartTimers()
	stop := make(chan struct{})
	go engine.RunLoop(stop)
	t.Cleanup(func() { close(stop) })
	if err := loader.LoadPlugin("@jan/dev", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if !loader.HasLogin("@jan/dev") {
		t.Fatal("a plugin with plugin:login has a sign-in")
	}

	req, err := loader.LoginStart("@jan/dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.LoginStart("@jan/dev"); !errors.Is(err, plugins.ErrLoginRunning) {
		t.Fatalf("a second sign-in while one runs: %v", err)
	}
	st := waitLogin(t, loader, "@jan/dev", req, func(s plugins.LoginState) bool { return s.Code != "" })
	if st.State != "pending" || st.Code != "WDJB-MJHT" || st.URL != "https://example.com/device" || st.Hint != "Code for vclu" || st.QR != "" {
		t.Fatalf("shown: %+v", st)
	}

	approved.Store(true)
	st = waitLogin(t, loader, "@jan/dev", req, func(s plugins.LoginState) bool { return s.State != "pending" })
	if st.State != "done" {
		t.Fatalf("done: %+v", st)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "at-secret") {
		t.Fatalf("the panel's answer carries the token: %s", b)
	}
	saved, _ := plugins.PluginSecrets(dir, "@jan/dev")
	if saved["token"] != "at-secret-123" || saved["refresh"] != "rt-456" {
		t.Fatalf("secrets: %v", saved)
	}
	// the running plugin has them at once
	mustLua(t, engine, `Plugin._lookup("@jan/dev"):secret("token")`, "at-secret-123")
	if st, _ := loader.LoginPoll("@jan/dev", req); st.State != "gone" {
		t.Fatalf("a finished sign-in is forgotten: %+v", st)
	}
}

const loginInit = `
local plugin = Plugin:new("qr", { name = "QR" })
plugin:login(function(cfg, show, done)
    __show, __done = show, done
    if cfg.boom then error("boom") end
    show({ qr = "tuyaSmart--qrLogin?token=abc" })
end)
return plugin
`

// show replaces what the panel sees; cancel, a timeout and bad calls end the sign-in, and
// nothing the plugin does after that is saved.
func TestPluginLoginQRCancelAndErrors(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/qr", map[string]interface{}{"api": 2}, loginInit, false)
	engine := apiEngine(t, loader, dir)
	if err := loader.LoadPlugin("@jan/qr", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	env := `PluginSandbox.sandboxes["@jan/qr"].environment`
	mustLua(t, engine, `tostring(Plugin._lookup("@jan/qr"):has("login"))`, "true")
	start := func() string {
		t.Helper()
		req, err := loader.LoginStart("@jan/qr")
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	poll := func(req string) plugins.LoginState {
		t.Helper()
		st, err := loader.LoginPoll("@jan/qr", req)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	call := func(code string) string {
		t.Helper()
		out, err := engine.Execute(`local ok, e = pcall(function() ` + code + ` end) return ok and "ok" or tostring(e)`)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	req := start()
	if st := poll(req); st.State != "pending" || st.QR != "tuyaSmart--qrLogin?token=abc" {
		t.Fatalf("qr: %+v", st)
	}
	call(env + `.__show({ qr = "second" })`)
	if st := poll(req); st.QR != "second" {
		t.Fatalf("a later show replaces the first: %+v", st)
	}
	for code, want := range map[string]string{
		`{ qr = string.rep("a", 1025) }`:              "qr is text of 1 to 1024 bytes",
		`{ code = "X", url = "http://example.com/" }`: "url is an https:// link",
		`{ hint = "only a hint" }`:                    "show needs qr or code",
		`"text"`:                                      "show takes a table",
	} {
		if got := call(env + `.__show(` + code + `)`); !strings.Contains(got, want) {
			t.Errorf("show(%s): %q, want %q", code, got, want)
		}
	}
	if st := poll(req); st.QR != "second" {
		t.Fatalf("a refused show changes nothing: %+v", st)
	}

	// cancel: a later done saves nothing
	if err := loader.LoginCancel("@jan/qr", req); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `tostring(`+env+`.__done({ token = "late" }))`, "false")
	mustLua(t, engine, `tostring(`+env+`.__show({ qr = "late" }))`, "false")
	if st := poll(req); st.State != "gone" {
		t.Fatalf("cancelled: %+v", st)
	}
	if saved, _ := plugins.PluginSecrets(dir, "@jan/qr"); saved["token"] != "" {
		t.Fatalf("saved after cancel: %v", saved)
	}

	// done: the plugin has the secret at once, before vclu saves it on the next poll
	req = start()
	mustLua(t, engine, `tostring(`+env+`.__done({ token = "tok-1" }))`, "true")
	mustLua(t, engine, `Plugin._lookup("@jan/qr"):secret("token")`, "tok-1")
	if st := poll(req); st.State != "done" {
		t.Fatalf("done: %+v", st)
	}
	if saved, _ := plugins.PluginSecrets(dir, "@jan/qr"); saved["token"] != "tok-1" {
		t.Fatalf("saved: %v", saved)
	}
	_ = plugins.SetPluginSecrets(dir, "@jan/qr", map[string]string{"token": ""})

	// done with bad secrets or with an error ends it with the reason
	for code, want := range map[string]string{
		`({ token = 5 })`:          "secret token is text",
		`({ ["bad name"] = "x" })`: "secret names are letters",
		`({})`:                     "done gave no secrets",
		`(nil, "access denied")`:   "access denied",
	} {
		req = start()
		call(env + `.__done` + code)
		if st := poll(req); st.State != "error" || !strings.Contains(st.Error, want) {
			t.Errorf("done%s: %+v, want %q", code, st, want)
		}
	}

	// an error in fn
	_ = loader.UnloadPlugin("@jan/qr")
	if err := loader.LoadPlugin("@jan/qr", map[string]interface{}{"boom": true}); err != nil {
		t.Fatal(err)
	}
	req = start()
	if st := poll(req); st.State != "error" || !strings.Contains(st.Error, "boom") {
		t.Fatalf("fn raised: %+v", st)
	}

	// the timeout ends it, and a new one may start
	_ = loader.UnloadPlugin("@jan/qr")
	if err := loader.LoadPlugin("@jan/qr", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `(function() PluginSandbox.LOGIN_SECONDS = 0 return "ok" end)()`, "ok")
	req = start()
	call(env + `.__done({ token = "too late" })`)
	if st := poll(req); st.State != "error" || !strings.Contains(st.Error, "timed out") {
		t.Fatalf("timeout: %+v", st)
	}
	start()
	if saved, _ := plugins.PluginSecrets(dir, "@jan/qr"); saved["token"] != "" {
		t.Fatalf("saved after the timeout: %v", saved)
	}

	// a stopped plugin ends its sign-in; one without login has none
	mustLua(t, engine, `(function() PluginSandbox.LOGIN_SECONDS = 300 return "ok" end)()`, "ok")
	req = start()
	_ = loader.UnloadPlugin("@jan/qr")
	if st := poll(req); st.State != "error" || !strings.Contains(st.Error, "stopped") {
		t.Fatalf("stopped plugin: %+v", st)
	}
	if loader.HasLogin("@jan/qr") {
		t.Fatal("a stopped plugin offers no sign-in")
	}
}
