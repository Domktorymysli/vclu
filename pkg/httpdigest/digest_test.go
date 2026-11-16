package httpdigest

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// RFC 7616, section 3.9.1: the same request answered with MD5 and SHA-256.
func TestAuthorizationRFC7616(t *testing.T) {
	defer fixCnonce("f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ")()
	header := `Digest realm="http-auth@example.org", qop="auth, auth-int", ` +
		`nonce="7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", ` +
		`opaque="FQhe/qaU925kfnzjCev0ciny7QMkPqMAFRtzCUYo5tdS"`
	for alg, want := range map[string]string{
		"MD5":     "8ca523f5e9506fed4657c9700eebdbec",
		"SHA-256": "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1",
	} {
		h := http.Header{}
		h.Add("WWW-Authenticate", header+", algorithm="+alg)
		c, ok := Find(h)
		if !ok {
			t.Fatal("no challenge")
		}
		got, err := c.Authorization("GET", "/dir/index.html", "Mufasa", "Circle of Life")
		if err != nil {
			t.Fatal(err)
		}
		p := parseParams(strings.TrimPrefix(got, "Digest "))
		if p["response"] != want || p["qop"] != "auth" || p["opaque"] != c.Opaque || p["algorithm"] != alg {
			t.Errorf("%s: %s", alg, got)
		}
	}
}

func fixCnonce(v string) func() {
	old := newCnonce
	newCnonce = func() (string, error) { return v, nil }
	return func() { newCnonce = old }
}

func TestFind(t *testing.T) {
	h := http.Header{}
	h.Add("WWW-Authenticate", `Basic realm="x"`)
	h.Add("WWW-Authenticate", `digest realm="a \"b\"", nonce=abc, qop="auth,auth-int", stale=FALSE`)
	c, ok := Find(h)
	if !ok || c.Realm != `a "b"` || c.Nonce != "abc" || strings.Join(c.Qop, "|") != "auth|auth-int" {
		t.Errorf("%+v", c)
	}
	h = http.Header{}
	h.Add("WWW-Authenticate", `Basic realm="x"`)
	if _, ok := Find(h); ok {
		t.Error("Basic is not a Digest challenge")
	}
}

func TestAuthorizationUnsupported(t *testing.T) {
	for _, c := range []Challenge{{Algorithm: "SHA-512-256"}, {Qop: []string{"auth-int"}}} {
		if _, err := c.Authorization("GET", "/", "u", "p"); err == nil {
			t.Errorf("%+v: no error", c)
		}
	}
}

// device is a server that accepts only Digest, like a Hikvision ISAPI endpoint.
type device struct {
	alg, qop, opaque string
	mu               sync.Mutex
	seen             []string // Authorization of each request, "" without one
	bodies           []string
}

func (d *device) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	auth := r.Header.Get("Authorization")
	d.mu.Lock()
	d.seen = append(d.seen, auth)
	d.bodies = append(d.bodies, string(body))
	d.mu.Unlock()
	if d.valid(r.Method, r.URL.RequestURI(), auth) {
		_, _ = fmt.Fprint(w, "open")
		return
	}
	ch := `Digest realm="DS-KV6124", nonce="n0nce"`
	if d.qop != "" {
		ch += `, qop="` + d.qop + `"`
	}
	if d.opaque != "" {
		ch += `, opaque="` + d.opaque + `"`
	}
	if d.alg != "" {
		ch += ", algorithm=" + d.alg
	}
	w.Header().Add("WWW-Authenticate", `Basic realm="DS-KV6124"`)
	w.Header().Add("WWW-Authenticate", ch)
	w.WriteHeader(http.StatusUnauthorized)
}

// valid checks the answer as the server computes it (RFC 7616, section 3.4.1).
func (d *device) valid(method, uri, auth string) bool {
	if !strings.HasPrefix(auth, "Digest ") {
		return false
	}
	p := parseParams(strings.TrimPrefix(auth, "Digest "))
	h := func(s string) string {
		if strings.HasPrefix(d.alg, "SHA-256") {
			x := sha256.Sum256([]byte(s))
			return hex.EncodeToString(x[:])
		}
		x := md5.Sum([]byte(s))
		return hex.EncodeToString(x[:])
	}
	ha1 := h("admin:DS-KV6124:secret")
	if strings.HasSuffix(d.alg, "-sess") {
		ha1 = h(ha1 + ":n0nce:" + p["cnonce"])
	}
	ha2 := h(method + ":" + uri)
	want := h(ha1 + ":n0nce:" + ha2)
	if d.qop != "" {
		want = h(ha1 + ":n0nce:" + p["nc"] + ":" + p["cnonce"] + ":auth:" + ha2)
	}
	return p["username"] == "admin" && p["uri"] == uri && p["opaque"] == d.opaque && p["response"] == want
}

func put(t *testing.T, url, password string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("PUT", url, strings.NewReader("<RemoteControlDoor><cmd>open</cmd></RemoteControlDoor>"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := Do(http.DefaultClient, req, "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestDoAnswersChallenge(t *testing.T) {
	for _, d := range []*device{
		{qop: "auth"},
		{qop: "auth,auth-int", opaque: "op"},
		{},
		{alg: "MD5-sess", qop: "auth"},
		{alg: "SHA-256", qop: "auth"},
	} {
		srv := httptest.NewServer(d)
		resp, body := put(t, srv.URL+"/ISAPI/AccessControl/RemoteControl/door/1?format=xml", "secret")
		srv.Close()
		if resp.StatusCode != 200 || body != "open" || len(d.seen) != 2 || d.seen[0] != "" {
			t.Errorf("%+v: %d %q", d, resp.StatusCode, body)
			continue
		}
		if d.bodies[1] != d.bodies[0] || d.bodies[1] == "" {
			t.Errorf("%+v: body sent again as %q", d, d.bodies[1])
		}
	}
}

// A wrong password ends in the second 401, without a loop.
func TestDoWrongPassword(t *testing.T) {
	d := &device{qop: "auth"}
	srv := httptest.NewServer(d)
	defer srv.Close()
	if resp, _ := put(t, srv.URL+"/door", "wrong"); resp.StatusCode != 401 || len(d.seen) != 2 {
		t.Errorf("%d after %d requests", resp.StatusCode, len(d.seen))
	}
}

// A server that asks for nothing, or for Basic only, gets one request and no credentials.
func TestDoWithoutChallenge(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.URL.Path == "/basic" {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	for path, code := range map[string]int{"/open": 200, "/basic": 401} {
		seen = nil
		if resp, _ := put(t, srv.URL+path, "secret"); resp.StatusCode != code || len(seen) != 1 || seen[0] != "" {
			t.Errorf("%s: %d, %q", path, resp.StatusCode, seen)
		}
	}
}

// Of several Digest challenges the first one this package can answer is used.
func TestFindSkipsUnsupported(t *testing.T) {
	h := http.Header{}
	h.Add("WWW-Authenticate", `Digest realm="r", nonce="1", algorithm=SHA-512-256, qop="auth"`)
	h.Add("WWW-Authenticate", "Digest\trealm=\"r\", nonce=\"2\", algorithm=MD5, qop=\"AUTH\"")
	c, ok := Find(h)
	if !ok || c.Nonce != "2" {
		t.Fatalf("%+v", c)
	}
	if got, err := c.Authorization("GET", "/", "u", "p"); err != nil || !strings.Contains(got, "qop=auth") {
		t.Errorf("%s %v", got, err)
	}
}
