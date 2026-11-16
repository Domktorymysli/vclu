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

// Find returns the first Digest challenge this package can answer among the WWW-Authenticate
// headers; a device often sends Basic next to it, or several algorithms.
func Find(h http.Header) (*Challenge, bool) {
	var first *Challenge
	for _, v := range h.Values("WWW-Authenticate") {
		v = strings.TrimSpace(v)
		end := strings.IndexAny(v, " \t")
		if end < 0 || !strings.EqualFold(v[:end], "Digest") {
			continue
		}
		rest := v[end+1:]
		p := parseParams(rest)
		c := &Challenge{Realm: p["realm"], Nonce: p["nonce"], Opaque: p["opaque"], Algorithm: p["algorithm"]}
		for _, q := range strings.Split(p["qop"], ",") {
			if q = strings.TrimSpace(q); q != "" {
				c.Qop = append(c.Qop, q)
			}
		}
		if _, err := c.hash(); err == nil && (len(c.Qop) == 0 || c.qop() != "") {
			return c, true
		}
		if first == nil {
			first = c
		}
	}
	// none answerable: Authorization says why
	return first, first != nil
}

func (c *Challenge) algorithm() string {
	if c.Algorithm == "" {
		return "MD5"
	}
	return strings.ToUpper(c.Algorithm)
}

func (c *Challenge) hash() (func() hash.Hash, error) {
	switch strings.TrimSuffix(c.algorithm(), "-SESS") {
	case "MD5":
		return md5.New, nil
	case "SHA-256":
		return sha256.New, nil
	}
	return nil, fmt.Errorf("digest algorithm %s not supported", c.Algorithm)
}

// qop is "auth" when the server offers it, else "".
func (c *Challenge) qop() string {
	for _, q := range c.Qop {
		if strings.EqualFold(q, "auth") {
			return "auth"
		}
	}
	return ""
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
	newHash, err := c.hash()
	if err != nil {
		return "", err
	}
	h := func(s string) string {
		d := newHash()
		d.Write([]byte(s))
		return hex.EncodeToString(d.Sum(nil))
	}

	qop := c.qop()
	if qop == "" && len(c.Qop) > 0 {
		return "", fmt.Errorf("digest qop %s not supported", strings.Join(c.Qop, ","))
	}

	cnonce, err := newCnonce()
	if err != nil {
		return "", err
	}
	nc := "00000001"

	ha1 := h(user + ":" + c.Realm + ":" + password)
	if strings.HasSuffix(c.algorithm(), "-SESS") {
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
		_ = resp.Body.Close()
		return nil, err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()

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
