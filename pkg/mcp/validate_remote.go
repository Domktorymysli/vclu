package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

// Devices that open the house or switch its protection off should not reach the cloud without a
// PIN (or, for a gate, at least a confirmation): a stolen phone or a taken account would then be
// enough. vclu_draft_validate warns when a draft does that, so the agent sees it before promoting.
// It reads the source, so it guesses from what the expose() call names: the object, its type,
// device_class, pulse and the id and name. A warning, never an error: the owner may want it.

// remoteGuard is how much protection a kind of device needs through the cloud.
type remoteGuard int

const (
	guardNone    remoteGuard = iota
	guardConfirm             // a gate, a garage, an intercom: :confirm() or :pin()
	guardPin                 // an alarm partition, a lock, a door: :pin()
)

var (
	alarmObject = regexp.MustCompile(`(?i)satel\.partition|\.SAT\d|\balarm|\bstref|\bsatel`)
	lockWords   = regexp.MustCompile(`(?i)\b(zam[eo]k|zamka|drzwi|rygiel|lock|door)`)
	gateWords   = regexp.MustCompile(`(?i)\b(bram|furtk|gara[zż]|gate|garage|domofon|intercom)`)
	classValue  = regexp.MustCompile(`device_class\s*=\s*["']([a-z_]+)["']`)
	pulseOpt    = regexp.MustCompile(`\bpulse\s*=`)
	readonlyOpt = regexp.MustCompile(`\breadonly\s*=\s*true`)
	pinOpt      = regexp.MustCompile(`\bpin\s*=\s*true`)
	confirmOpt  = regexp.MustCompile(`\bconfirm\s*=\s*true`)
	typeArg     = regexp.MustCompile(`^[^,]*,\s*["']([a-z_]+)["']`)
)

// commanded are the expose types the cloud sends commands to; a sensor only shows a state.
var commanded = map[string]bool{"switch": true, "light": true, "dimmer": true, "cover": true,
	"garage_door": true, "lock": true, "scene": true, "button": true}

// exposeCall is one expose(...) statement with the methods chained to it.
type exposeCall struct {
	line    int
	args    string
	methods []chainedCall
}

type chainedCall struct{ name, args string }

// remoteWarnings lists the expose() calls that send a sensitive device to the cloud unguarded.
func remoteWarnings(src string) []string {
	var out []string
	for _, e := range exposeCalls(src) {
		need := guardFor(e.args)
		if need == guardNone || !goesRemote(e) {
			continue
		}
		pin, confirm := guards(e)
		switch {
		case need == guardPin && !pin:
			out = append(out, fmt.Sprintf("line %d: expose() sends what looks like an alarm partition, a lock or a door to the cloud (:remote()) without :pin(...): whoever has the account or the phone could disarm or open it. Add :pin(\"<4-8 digits>\") or use :remote({ readonly = true }); leave it only if the owner asked for it knowingly.", e.line))
		case need == guardConfirm && !pin && !confirm:
			out = append(out, fmt.Sprintf("line %d: expose() sends what looks like a gate, a garage or an intercom to the cloud (:remote()) without :confirm() or :pin(...): it would open with a single tap or voice command. Add :confirm() (better :pin(\"<4-8 digits>\")); leave it only if the owner asked for it knowingly.", e.line))
		}
	}
	return out
}

func guardFor(args string) remoteGuard {
	if readonlyOpt.MatchString(args) {
		return guardNone
	}
	if m := typeArg.FindStringSubmatch(args); m != nil && !commanded[m[1]] {
		return guardNone
	}
	class := ""
	if m := classValue.FindStringSubmatch(args); m != nil {
		class = m[1]
	}
	switch {
	case alarmObject.MatchString(args), lockWords.MatchString(args), class == "lock", class == "door":
		return guardPin
	case gateWords.MatchString(args), pulseOpt.MatchString(args), strings.Contains(args, `"garage_door"`),
		class == "gate", class == "garage":
		return guardConfirm
	}
	return guardNone
}

// goesRemote: :remote() or :remote(true) or a table without readonly; not :remote(false).
func goesRemote(e exposeCall) bool {
	remote := false
	for _, m := range e.methods {
		if m.name != "remote" {
			continue
		}
		a := strings.TrimSpace(m.args)
		remote = a != "false" && !readonlyOpt.MatchString(a)
	}
	return remote
}

func guards(e exposeCall) (pin, confirm bool) {
	for _, m := range e.methods {
		switch m.name {
		case "pin":
			pin = true
		case "confirm":
			confirm = strings.TrimSpace(m.args) != "false"
		case "remote":
			pin = pin || pinOpt.MatchString(m.args)
			confirm = confirm || confirmOpt.MatchString(m.args)
		}
	}
	return pin, confirm
}

// exposeCalls finds expose(...) calls in Lua source and the :method(...) calls chained to them,
// skipping comments and strings.
func exposeCalls(src string) []exposeCall {
	var out []exposeCall
	code := maskLua(src)
	for i := 0; i < len(code); i++ {
		if !strings.HasPrefix(code[i:], "expose") || (i > 0 && isIdent(code[i-1])) {
			continue
		}
		j := skipSpace(code, i+len("expose"))
		if j >= len(code) || code[j] != '(' {
			continue
		}
		end := closeParen(code, j)
		if end < 0 {
			continue
		}
		e := exposeCall{line: strings.Count(src[:i], "\n") + 1, args: src[j+1 : end]}
		k := skipSpace(code, end+1)
		for k < len(code) && code[k] == ':' {
			n := k + 1
			for n < len(code) && isIdent(code[n]) {
				n++
			}
			p := skipSpace(code, n)
			if p >= len(code) || code[p] != '(' {
				break
			}
			c := closeParen(code, p)
			if c < 0 {
				break
			}
			e.methods = append(e.methods, chainedCall{name: code[k+1 : n], args: src[p+1 : c]})
			k = skipSpace(code, c+1)
		}
		out = append(out, e)
		i = k - 1
	}
	return out
}

// maskLua returns src with comments and the insides of strings blanked out (same length, same
// newlines), so a scan for calls sees only code.
func maskLua(src string) string {
	b := []byte(src)
	blank := func(from, to int) {
		for i := from; i < to && i < len(b); i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	for i := 0; i < len(b); i++ {
		switch {
		case strings.HasPrefix(src[i:], "--[["):
			end := strings.Index(src[i:], "]]")
			if end < 0 {
				end = len(src) - i
			}
			blank(i, i+end+2)
			i += end + 1
		case strings.HasPrefix(src[i:], "--"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			blank(i, i+end)
			i += end
		case strings.HasPrefix(src[i:], "[["):
			end := strings.Index(src[i+2:], "]]")
			if end < 0 {
				end = len(src) - i - 2
			}
			blank(i+2, i+2+end)
			i += end + 3
		case b[i] == '"' || b[i] == '\'':
			q := b[i]
			j := i + 1
			for j < len(b) && b[j] != q && b[j] != '\n' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			blank(i+1, j)
			i = j
		}
	}
	return string(b)
}

func closeParen(code string, open int) int {
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
