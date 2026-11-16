package camera

import (
	"crypto/md5"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// A camera with auth "digest" gets its snapshot through the shared Digest helper (#232).
func TestDigestSnapshot(t *testing.T) {
	param := regexp.MustCompile(`(\w+)="?([^",]*)"?`)
	h := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := map[string]string{}
		for _, m := range param.FindAllStringSubmatch(strings.TrimPrefix(r.Header.Get("Authorization"), "Digest "), -1) {
			p[m[1]] = m[2]
		}
		want := h(h("admin:cam:secret") + ":n:" + p["nc"] + ":" + p["cnonce"] + ":auth:" + h("GET:"+r.URL.RequestURI()))
		if p["response"] != want {
			w.Header().Set("WWW-Authenticate", `Digest realm="cam", nonce="n", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg"))
	}))
	defer srv.Close()

	m := NewManager()
	m.Register(&Camera{ID: "gate", URL: srv.URL + "/ISAPI/Streaming/channels/101/picture", Auth: "digest", Username: "admin", Password: "secret"})
	m.Register(&Camera{ID: "bad", URL: srv.URL + "/x", Auth: "digest", Username: "admin", Password: "nope"})
	if data, ct, err := m.GetSnapshot("gate"); err != nil || string(data) != "jpeg" || ct != "image/jpeg" {
		t.Errorf("gate: %q %q %v", data, ct, err)
	}
	if _, _, err := m.GetSnapshot("bad"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("wrong password: %v", err)
	}
}
