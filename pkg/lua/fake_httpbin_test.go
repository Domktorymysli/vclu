package lua

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeHTTPBin answers like httpbin.org (/get, /post, /headers) on localhost,
// so HTTP tests do not depend on an outside service.
type fakeHTTPBin struct {
	url  string // http://127.0.0.1:port
	host string // 127.0.0.1:port
}

func newFakeHTTPBin(t *testing.T) fakeHTTPBin {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := map[string]string{}
		for k, v := range r.Header {
			headers[k] = strings.Join(v, ",")
		}
		out := map[string]any{"headers": headers}
		switch r.URL.Path {
		case "/get":
			out["url"] = "http://" + r.Host + r.URL.RequestURI()
			out["args"] = r.URL.Query()
		case "/post":
			raw, _ := io.ReadAll(r.Body)
			out["data"] = string(raw)
			var js any
			if json.Unmarshal(raw, &js) == nil {
				out["json"] = js
			}
			form := map[string]string{}
			if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
				if vals, err := url.ParseQuery(string(raw)); err == nil {
					for k := range vals {
						form[k] = vals.Get(k)
					}
				}
			}
			out["form"] = form
		case "/headers":
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return fakeHTTPBin{url: srv.URL, host: strings.TrimPrefix(srv.URL, "http://")}
}

// lua points a script's httpbin.org addresses at the fake.
func (b fakeHTTPBin) lua(script string) string {
	script = strings.ReplaceAll(script, "https://httpbin.org", b.url)
	return strings.ReplaceAll(script, `"httpbin.org"`, `"`+b.host+`"`)
}
