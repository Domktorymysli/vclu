// Package httpdigest answers an HTTP Digest challenge (RFC 7616), as cameras, intercoms and
// other devices that refuse Basic ask for.
package httpdigest

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
)

// Challenge is the server's WWW-Authenticate: Digest header.
type Challenge struct {
	Realm     string
	Nonce     string
	Opaque    string
	Algorithm string
	Qop       []string
}

// Find returns the Digest challenge among the WWW-Authenticate headers; a device often sends
// Basic next to it.
func Find(h http.Header) (*Challenge, bool) {
	for _, v := range h.Values("WWW-Authenticate") {
		scheme, rest, _ := strings.Cut(strings.TrimSpace(v), " ")
		if !strings.EqualFold(scheme, "Digest") {
			continue
		}
		p := parseParams(rest)
		c := &Challenge{Realm: p["realm"], Nonce: p["nonce"], Opaque: p["opaque"], Algorithm: p["algorithm"]}
		for _, q := range strings.Split(p["qop"], ",") {
			if q = strings.TrimSpace(q); q != "" {
				c.Qop = append(c.Qop, q)
			}
		}
		return c, true
	}
	return nil, false
}

// parseParams reads key=value pairs, where a quoted value may hold commas (qop="auth,auth-int").
func parseParams(s string) map[string]string {
	params := make(map[string]string)
	for s != "" {
		s = strings.TrimLeft(s, " \t,")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(s[:eq]))
		s = strings.TrimLeft(s[eq+1:], " \t")
		var val string
		if strings.HasPrefix(s, `"`) {
			var b strings.Builder
			i := 1
			for ; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
			}
			val, s = b.String(), s[min(i+1, len(s)):]
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				end = len(s)
			}
			val, s = strings.TrimSpace(s[:end]), s[end:]
		}
		params[key] = val
	}
	return params
}

// Authorization is the header value answering c for a request of method to uri (path and query).
func (c *Challenge) Authorization(method, uri, user, password string) (string, error) {
	alg := c.Algorithm
	if alg == "" {
		alg = "MD5"
	}
	var newHash func() hash.Hash
	switch strings.TrimSuffix(strings.ToUpper(alg), "-SESS") {
	case "MD5":
		newHash = md5.New
	case "SHA-256":
		newHash = sha256.New
	default:
		return "", fmt.Errorf("digest algorithm %s not supported", alg)
	}
	h := func(s string) string {
		d := newHash()
		d.Write([]byte(s))
		return hex.EncodeToString(d.Sum(nil))
	}

	qop := ""
	for _, q := range c.Qop {
		if q == "auth" {
			qop = q
		}
	}
	if qop == "" && len(c.Qop) > 0 {
		return "", fmt.Errorf("digest qop %s not supported", strings.Join(c.Qop, ","))
	}

	cnonce, err := newCnonce()
	if err != nil {
		return "", err
	}
	nc := "00000001"

	ha1 := h(user + ":" + c.Realm + ":" + password)
	if strings.HasSuffix(strings.ToUpper(alg), "-SESS") {
		ha1 = h(ha1 + ":" + c.Nonce + ":" + cnonce)
	}
	ha2 := h(method + ":" + uri)
	response := h(ha1 + ":" + c.Nonce + ":" + ha2)
	if qop != "" {
		response = h(ha1 + ":" + c.Nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
	}

	out := fmt.Sprintf(`Digest username=%s, realm=%s, nonce=%s, uri=%s, response="%s"`,
		quote(user), quote(c.Realm), quote(c.Nonce), quote(uri), response)
	if c.Algorithm != "" {
		out += ", algorithm=" + c.Algorithm
	}
	if qop != "" {
		out += fmt.Sprintf(`, qop=%s, nc=%s, cnonce="%s"`, qop, nc, cnonce)
	}
	if c.Opaque != "" {
		out += ", opaque=" + quote(c.Opaque)
	}
	return out, nil
}

// newCnonce is the client nonce; tests set a fixed one.
var newCnonce = func() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Do sends req; on a 401 with a Digest challenge it answers once and sends req again with its
// body. Without a challenge the first response is returned and no credentials are sent.
func Do(client *http.Client, req *http.Request, user, password string) (*http.Response, error) {
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	c, ok := Find(resp.Header)
	if !ok {
		return resp, nil
	}
	auth, err := c.Authorization(req.Method, req.URL.RequestURI(), user, password)
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()

	again := req.Clone(req.Context())
	if req.GetBody != nil {
		if again.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	} else if req.Body != nil && req.Body != http.NoBody {
		return nil, fmt.Errorf("digest: the request body cannot be sent again")
	}
	again.Header.Set("Authorization", auth)
	return client.Do(again)
}
