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

// Keywords start after anything but a letter or a digit (so "_" and "." separate, unlike RE2's
// \b) and the English ones, "gate" and "satel" end the same way ("gateway", "Satelita" are not
// them). CamelCase is split before matching ("WjazdBrama" is "Wjazd Brama").
const (
	wordStart = `(?:^|[^\p{L}\p{N}])`
	wordEnd   = `(?:$|[^\p{L}\p{N}])`
)

var (
	// A Satel partition, or a GATE peripheral (CLU....SAT0001). GATE Alarm's SatelInput and
	// SatelOutput objects are SATnnnn too and the source does not say which is which: they warn
	// as well.
	alarmObject = regexp.MustCompile(`(?i)satel\.partition|\.SAT\d`)
	alarmWords  = regexp.MustCompile(`(?i)` + wordStart + `(?:alarm|satel` + wordEnd + `)`)
	lockWords   = regexp.MustCompile(`(?i)` + wordStart + `(?:zam[eo]k|zamka|drzwi|rygiel|(?:locks?|doors?)` + wordEnd + `)`)
	gateWords   = regexp.MustCompile(`(?i)` + wordStart + `(?:bram|furtk|gara[zż]|domofon|(?:gates?|garage|intercom)` + wordEnd + `)`)
	coverGate   = regexp.MustCompile(`(?i)` + wordStart + `(?:bram|furtk)`) // not garaż: a roller in the garage
	camelCase   = regexp.MustCompile(`(\p{Ll})(\p{Lu})`)
	classValue  = regexp.MustCompile(`device_class\s*=\s*["']([a-z_]+)["']`)
	pulseOpt    = regexp.MustCompile(`\bpulse\s*=`)
	readonlyOpt = regexp.MustCompile(`\breadonly\s*=\s*true`)
	hiddenOpt   = regexp.MustCompile(`\bhidden\s*=\s*true`)
	pinOpt      = regexp.MustCompile(`\bpin\s*=\s*true`)
	confirmOpt  = regexp.MustCompile(`\bconfirm\s*=\s*true`)
)

// commanded are the expose types the cloud sends commands to; a sensor only shows a state.
var commanded = map[string]bool{"switch": true, "light": true, "dimmer": true, "cover": true,
	"garage_door": true, "lock": true, "scene": true, "button": true}

// exposeCall is one expose(...) statement with the methods chained to it, or called later on the
// variable it was assigned to.
type exposeCall struct {
	line    int
	args    string // without comments
	typ     string // the type when it is a string literal, else ""
	methods []chainedCall
}

type chainedCall struct{ name, args string }

// remoteWarnings lists the expose() calls that send a sensitive device to the cloud unguarded.
func remoteWarnings(src string) []string {
	var out []string
	for _, e := range exposeCalls(src) {
		need := guardFor(e)
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

func guardFor(e exposeCall) remoteGuard {
	// hidden = true makes :remote() a no-op; readonly is seen in the cloud, never commanded
	if readonlyOpt.MatchString(e.args) || hiddenOpt.MatchString(e.args) {
		return guardNone
	}
	if e.typ != "" && !commanded[e.typ] {
		return guardNone
	}
	class := ""
	if m := classValue.FindStringSubmatch(e.args); m != nil {
		class = m[1]
	}
	text := e.args
	for _, m := range e.methods {
		if m.name == "name" {
			text += " " + m.args
		}
	}
	words := camelCase.ReplaceAllString(text, "$1 $2")
	switch e.typ {
	case "lock":
		return guardPin
	case "garage_door":
		return guardConfirm
	case "light", "dimmer":
		// a lamp's name says where it is ("Brama", "Strefa dzienna"), not what it does
		return guardNone
	case "cover":
		// most covers are rollers, also in the garage: only a gate or a door by class or name
		switch {
		case class == "door":
			return guardPin
		case class == "gate", class == "garage", coverGate.MatchString(words):
			return guardConfirm
		}
		return guardNone
	}
	switch {
	case alarmObject.MatchString(text), alarmWords.MatchString(words), lockWords.MatchString(words),
		class == "lock", class == "door":
		return guardPin
	case gateWords.MatchString(words), pulseOpt.MatchString(e.args), class == "gate", class == "garage":
		return guardConfirm
	}
	return guardNone
}

// goesRemote: :remote() or :remote(true) or a table without readonly; not :remote(false), and
// not when :readonly() makes it read-only in the cloud.
func goesRemote(e exposeCall) bool {
	remote, readonly := false, false
	for _, m := range e.methods {
		switch m.name {
		case "remote":
			a := strings.TrimSpace(m.args)
			remote = a != "false" && !readonlyOpt.MatchString(a)
		case "readonly":
			readonly = true
		}
	}
	return remote && !readonly
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

// exposeCalls finds expose(...) calls in Lua source and the :method(...) calls chained to them or,
// for local h = expose(...), made later on h; comments and strings are skipped.
func exposeCalls(src string) []exposeCall {
	var out []exposeCall
	text, code := maskLua(src)
	for i := 0; i < len(code); i++ {
		if !strings.HasPrefix(code[i:], "expose") || (i > 0 && isIdent(code[i-1])) {
			continue
		}
		j := skipSpace(code, i+len("expose"))
		if j >= len(code) || code[j] != '(' {
			continue
		}
		end := closing(code, j)
		if end < 0 {
			continue
		}
		e := exposeCall{line: strings.Count(src[:i], "\n") + 1, args: text[j+1 : end],
			typ: typeArg(code, text, j+1, end)}
		var k int
		e.methods, k = chain(code, text, end+1)
		if v := assignedTo(code, i); v != "" {
			e.methods = append(e.methods, laterCalls(code, text, k, v)...)
		}
		out = append(out, e)
		i = k - 1
	}
	return out
}

// chain reads :name(...), :name{...}, :name"..." and :name[[...]] calls from k on.
func chain(code, text string, k int) ([]chainedCall, int) {
	var out []chainedCall
	for {
		k = skipSpace(code, k)
		if k >= len(code) || code[k] != ':' {
			return out, k
		}
		n := k + 1
		for n < len(code) && isIdent(code[n]) {
			n++
		}
		from, to, next := callArgs(code, skipSpace(code, n))
		if n == k+1 || next < 0 {
			return out, k
		}
		out = append(out, chainedCall{name: code[k+1 : n], args: text[from:to]})
		k = next
	}
}

// callArgs finds the arguments of a call at p: (...), a table or a string. next is past the call,
// -1 when there is none.
func callArgs(code string, p int) (from, to, next int) {
	if p >= len(code) {
		return 0, 0, -1
	}
	switch c := code[p]; c {
	case '(', '{':
		e := closing(code, p)
		if e < 0 {
			return 0, 0, -1
		}
		if c == '(' {
			return p + 1, e, e + 1
		}
		return p, e + 1, e + 1
	case '"', '\'':
		e := strings.IndexByte(code[p+1:], c) // the inside is blanked
		if e < 0 {
			return 0, 0, -1
		}
		return p + 1, p + 1 + e, p + e + 2
	case '[':
		if level, ok := longBracket(code, p); ok {
			open, shut := p+level+2, "]"+strings.Repeat("=", level)+"]"
			e := strings.Index(code[open:], shut)
			if e < 0 {
				return 0, 0, -1
			}
			return open, open + e, open + e + len(shut)
		}
	}
	return 0, 0, -1
}

// typeArg is the expose type: the string literal after the first top-level comma.
func typeArg(code, text string, from, to int) string {
	depth := 0
	for i := from; i < to; i++ {
		switch code[i] {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		case ',':
			if depth != 0 {
				continue
			}
			p := skipSpace(code, i+1)
			if p < to && (code[p] == '"' || code[p] == '\'') {
				if e := strings.IndexByte(code[p+1:to], code[p]); e >= 0 {
					return text[p+1 : p+1+e]
				}
			}
			return ""
		}
	}
	return ""
}

// assignedTo is the variable in "local h = expose(...)" or "h = expose(...)", else "".
func assignedTo(code string, i int) string {
	p := i - 1
	for p >= 0 && (code[p] == ' ' || code[p] == '\t') {
		p--
	}
	if p < 1 || code[p] != '=' || strings.IndexByte("=~<>", code[p-1]) >= 0 {
		return ""
	}
	p--
	for p >= 0 && (code[p] == ' ' || code[p] == '\t') {
		p--
	}
	end := p + 1
	for p >= 0 && isIdent(code[p]) {
		p--
	}
	if p+1 == end || code[p+1] >= '0' && code[p+1] <= '9' || p >= 0 && (code[p] == '.' || code[p] == ':') {
		return ""
	}
	return code[p+1 : end]
}

// laterCalls are the method calls made on v after k, until v is assigned again.
func laterCalls(code, text string, k int, v string) []chainedCall {
	var out []chainedCall
	for k < len(code) {
		n := strings.Index(code[k:], v)
		if n < 0 {
			break
		}
		at := k + n
		k = at + len(v)
		if at > 0 && (isIdent(code[at-1]) || code[at-1] == '.' || code[at-1] == ':') || k < len(code) && isIdent(code[k]) {
			continue
		}
		p := skipSpace(code, k)
		if p < len(code) && code[p] == '=' && (p+1 >= len(code) || code[p+1] != '=') {
			break
		}
		m, next := chain(code, text, k)
		out = append(out, m...)
		k = next
	}
	return out
}

// maskLua returns src without comments (text) and without comments and the insides of strings
// (code), both of the same length and with the same newlines, so a scan for calls sees only code
// and what it reads keeps the strings.
func maskLua(src string) (text, code string) {
	t, c := []byte(src), []byte(src)
	blank := func(b []byte, from, to int) {
		for i := from; i < to && i < len(b); i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	// longEnd is where the long bracket of this level opened at from closes, past its "]=*]"
	longEnd := func(from, level int) int {
		shut := "]" + strings.Repeat("=", level) + "]"
		if e := strings.Index(src[from:], shut); e >= 0 {
			return from + e + len(shut)
		}
		return len(src)
	}
	for i := 0; i < len(src); i++ {
		if strings.HasPrefix(src[i:], "--") {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = len(src) - i
			}
			end += i
			if level, ok := longBracket(src, i+2); ok {
				end = longEnd(i+2+level+2, level)
			}
			blank(t, i, end)
			blank(c, i, end)
			i = end - 1
			continue
		}
		if level, ok := longBracket(src, i); ok {
			open := i + level + 2
			end := longEnd(open, level)
			blank(c, open, end-level-2)
			i = end - 1
			continue
		}
		if q := src[i]; q == '"' || q == '\'' {
			j := i + 1
			for j < len(src) && src[j] != q && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			blank(c, i+1, j)
			i = j
		}
	}
	return string(t), string(c)
}

// longBracket reports a Lua long bracket "[", "="*level, "[" at i.
func longBracket(s string, i int) (level int, ok bool) {
	if i >= len(s) || s[i] != '[' {
		return 0, false
	}
	j := i + 1
	for j < len(s) && s[j] == '=' {
		j++
	}
	return j - i - 1, j < len(s) && s[j] == '['
}

// closing finds the bracket that closes the one at open, in code with strings blanked.
func closing(code string, open int) int {
	o := code[open]
	c := map[byte]byte{'(': ')', '{': '}', '[': ']'}[o]
	depth := 0
	for i := open; i < len(code); i++ {
		switch code[i] {
		case o:
			depth++
		case c:
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
