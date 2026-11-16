package lua

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A server that accepts only Digest (MD5, qop=auth), like a Hikvision intercom: it answers
// "<method> <body>" once the answer is right (#232).
func digestDevice(t *testing.T) *httptest.Server {
	t.Helper()
	param := regexp.MustCompile(`(\w+)="?([^",]*)"?`)
	h := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p := map[string]string{}
		for _, m := range param.FindAllStringSubmatch(strings.TrimPrefix(r.Header.Get("Authorization"), "Digest "), -1) {
			p[m[1]] = m[2]
		}
		ha2 := h(r.Method + ":" + r.URL.RequestURI())
		want := h(h("admin:door:secret") + ":n0nce:" + p["nc"] + ":" + p["cnonce"] + ":auth:" + ha2)
		if p["response"] != want || p["uri"] != r.URL.RequestURI() {
			w.Header().Set("WWW-Authenticate", `Digest realm="door", nonce="n0nce", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = fmt.Fprintf(w, "%s %s", r.Method, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPDigestAuth(t *testing.T) {
	srv := digestDevice(t)
	engine := NewLuaEngine()
	defer engine.Close()
	got := mustExec(t, engine, `(function()
		local c = HTTP:new("door"):setContentType("application/xml"):setDigestAuth("admin", "secret")
		local open = c:put("`+srv.URL+`/ISAPI/door/1?format=xml", "<cmd>open</cmd>")
		local auth = tostring(c._requestLog[1].headers.Authorization)
		local wrong = HTTP:new("door2"):setDigestAuth("admin", "nope"):get("`+srv.URL+`/x")
		local bearer = HTTP:new("door3"):setDigestAuth("admin", "secret"):setBearerToken("t"):get("`+srv.URL+`/x")
		return open.status .. " " .. open.body .. "|" .. auth .. "|" .. wrong.status .. "|" .. bearer.status
	end)()`)
	if want := "200 PUT <cmd>open</cmd>|nil|401|401"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestHTTPDigestAuthAsync(t *testing.T) {
	srv := digestDevice(t)
	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()
	mustExec(t, engine, `(function()
		results = {}
		HTTP:new("door-async"):setDigestAuth("admin", "secret"):asyncRequest("POST", "`+srv.URL+`/a", "x", nil,
			function(resp, err) results.client = resp.status .. " " .. resp.body end)
		require("PluginHelpers").httpRequest("door-plugin",
			{ method = "PUT", url = "`+srv.URL+`/p", body = "y", digest = { user = "admin", password = "secret" } },
			function(resp, err) results.plugin = resp.status .. " " .. resp.body end)
		return ""
	end)()`)
	got := map[string]string{}
	for i := 0; i < 250 && len(got) < 2; i++ {
		time.Sleep(20 * time.Millisecond)
		engine.ProcessTimers()
		for _, k := range []string{"client", "plugin"} {
			if v, _ := engine.Execute(`results.` + k); v != "" && v != "nil" {
				got[k] = v
			}
		}
	}
	if got["client"] != "200 POST x" || got["plugin"] != "200 PUT y" {
		t.Errorf("client %q, plugin %q", got["client"], got["plugin"])
	}
}
