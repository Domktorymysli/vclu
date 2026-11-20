package omproject

import (
	"regexp"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/omparser"
)

// redactedValue stands for a GATE Alarm's password, code, key or address in the agent's files.
const redactedValue = `"(removed by vclu)"`

// reGateSetter finds a call that writes a feature, in OM's notation or om.lua's:
// zone->SetUserPassword("1234"), SAT0001:set(4, "...").
var reGateSetter = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*(?:->|:)\s*(set|Set[A-Za-z]+)\s*\(`)

// gateSecretFeatures are the features of a GATE Alarm's objects that hold the panel's
// secrets (RemoteGateAlarm.lua's SECRET tables).
var gateSecretFeatures = []string{"UserPassword", "AdminPassword", "EncryptionKey", "AdminCode", "AccessCode", "IP"}

// reSecretWord finds a secret feature's name; reStringLit a quoted string.
var (
	reSecretWord = regexp.MustCompile(`UserPassword|AdminPassword|EncryptionKey|AdminCode|AccessCode`)
	reStringLit  = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
)

// gateObjects are the names and IDs of the project's GATE Alarm objects.
func (p *Project) gateObjects() map[string]bool {
	names := map[string]bool{}
	for _, c := range p.CLUs {
		for _, per := range c.Peripherals {
			if omparser.ObjectClass(per.Class).IsGateAlarm() {
				names[per.ID] = true
				if per.Name != "" {
					names[per.Name] = true
				}
			}
		}
	}
	return names
}

// redactGate removes the values a script or binding writes into a GATE Alarm object's
// features (any feature by set(n, v), the secret ones by name), and the strings of a
// line that names a secret feature.
func redactGate(src string, gate map[string]bool) string {
	if len(gate) > 0 {
		var b strings.Builder
		last := 0
		for _, m := range reGateSetter.FindAllStringSubmatchIndex(src, -1) {
			if m[0] < last || !gate[src[m[2]:m[3]]] {
				continue
			}
			method := src[m[4]:m[5]]
			if method != "set" && !secretSetter(method) {
				continue
			}
			open := m[1] // after "("
			end := closing(src, open)
			args := src[open:end]
			keep := ""
			if method == "set" {
				if i := strings.IndexByte(args, ','); i >= 0 {
					keep = args[:i+1] + " "
				} else {
					continue
				}
			}
			b.WriteString(src[last:open])
			b.WriteString(keep + redactedValue)
			last = end
		}
		b.WriteString(src[last:])
		src = b.String()
	}
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		if reSecretWord.MatchString(l) {
			lines[i] = reStringLit.ReplaceAllString(l, redactedValue)
		}
	}
	return strings.Join(lines, "\n")
}

func secretSetter(method string) bool {
	for _, f := range gateSecretFeatures {
		if method == "Set"+f {
			return true
		}
	}
	return false
}

// closing is the index of the ")" that closes the call whose arguments start at i (past
// strings and nested parentheses), or len(s).
func closing(s string, i int) int {
	depth := 0
	for ; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\'':
			for i++; i < len(s) && s[i] != c; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return i
			}
			depth--
		case '\n':
			return i
		}
	}
	return len(s)
}

// withGateRedacted is the CLU with its scripts and bindings passed through redactGate.
func withGateRedacted(c CLU, gate map[string]bool) CLU {
	bindings := func(in []Binding) []Binding {
		out := make([]Binding, len(in))
		for i, e := range in {
			e.Commands = append([]string(nil), e.Commands...)
			for j, cmd := range e.Commands {
				e.Commands[j] = redactGate(cmd, gate)
			}
			out[i] = e
		}
		return out
	}
	c.Events = bindings(c.Events)
	c.Objects = append([]Object(nil), c.Objects...)
	for i := range c.Objects {
		c.Objects[i].Events = bindings(c.Objects[i].Events)
	}
	c.Peripherals = append([]Peripheral(nil), c.Peripherals...)
	for i := range c.Peripherals {
		c.Peripherals[i].Events = bindings(c.Peripherals[i].Events)
	}
	c.Scripts = append([]Script(nil), c.Scripts...)
	for i := range c.Scripts {
		c.Scripts[i].Source = redactGate(c.Scripts[i].Source, gate)
	}
	return c
}
