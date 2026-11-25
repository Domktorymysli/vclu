package plugins

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"golang.org/x/net/idna"
)

// InternetHosts is "internet" in a plugin's permissions: host names, "*.domain" for its
// subdomains, exact IP addresses, or "*" for any host. In plugin.json a list or the string "*".
type InternetHosts []string

// UnmarshalJSON reads a list or a single string and refuses a host that is not one.
func (h *InternetHosts) UnmarshalJSON(data []byte) error {
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		var one string
		if json.Unmarshal(data, &one) != nil {
			return fmt.Errorf(`"internet" is a list of hosts or "*"`)
		}
		list = []string{one}
	}
	norm, err := NormalizeInternetHosts(list)
	if err != nil {
		return err
	}
	*h = norm
	return nil
}

// Any: every host on the internet.
func (h InternetHosts) Any() bool { return slices.Contains(h, "*") }

// NormalizeInternetHosts checks each entry and returns them lower case, without a trailing
// dot, without repeats; "*" among them is the list ["*"]. Nil for an empty list.
func NormalizeInternetHosts(list []string) (InternetHosts, error) {
	var out InternetHosts
	for _, raw := range list {
		e, err := normalizeInternetHost(raw)
		if err != nil {
			return nil, err
		}
		if e == "*" {
			return InternetHosts{"*"}, nil
		}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

func normalizeInternetHost(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "*" {
		return s, nil
	}
	bad := func(why string) error {
		return fmt.Errorf("internet: %q is not a host (%s); use a name such as api.example.com, *.example.com, an IP address or \"*\"", raw, why)
	}
	switch {
	case s == "":
		return "", bad("empty")
	case strings.Contains(s, "://"):
		return "", bad("no scheme")
	case strings.ContainsAny(s, "/?#@ "):
		return "", bad("no path, user or spaces")
	}
	if ip, err := netip.ParseAddr(s); err == nil {
		if ip.Zone() != "" {
			return "", bad("no zone")
		}
		return ip.Unmap().String(), nil
	}
	if strings.Contains(s, ":") {
		return "", bad("no port")
	}
	name := strings.TrimSuffix(strings.ToLower(s), ".")
	wild := strings.HasPrefix(name, "*.")
	if wild {
		name = name[2:]
		if !strings.Contains(name, ".") {
			return "", bad("a pattern covers the subdomains of a domain, not a whole top-level domain")
		}
	}
	if !validHostName(name) {
		return "", bad("letters, digits, - and . only, * only as the leading *.; a non-ASCII name in its xn-- form")
	}
	if wild {
		return "*." + name, nil
	}
	return name, nil
}

// validHostName: ASCII labels of letters, digits, - and _, none empty.
func validHostName(name string) bool {
	if name == "" || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return false
			}
		}
	}
	return true
}

// CanonicalHost is a URL's host as the list is matched against: an IP address in its
// plain form, a name lower case, in its xn-- form, without the trailing dot. False when
// it is neither.
func CanonicalHost(host string) (string, bool) {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.WithZone("").Unmap().String(), true
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if !isASCII(name) { // as net/http dials it
		var err error
		if name, err = idna.Lookup.ToASCII(name); err != nil {
			return "", false
		}
	}
	if !validHostName(name) {
		return "", false
	}
	return name, true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Allows: the host (as in a URL, without the port) is on the list. "*.example.com"
// matches its subdomains, not example.com; an IP address only the same address.
func (h InternetHosts) Allows(host string) bool {
	if h.Any() {
		return true
	}
	c, ok := CanonicalHost(host)
	if !ok {
		return false
	}
	for _, e := range h {
		if e == c || strings.HasPrefix(e, "*.") && strings.HasSuffix(c, e[1:]) {
			return true
		}
	}
	return false
}

// covers: an allowed list that includes entry, so an entry the user allowed once is
// not asked for again.
func (h InternetHosts) covers(entry string) bool {
	return h.Any() || slices.Contains(h, entry)
}
