package omproject

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The myGrenton interface the user designed in Object Manager: nodes/<id>/data.json.
// Everything in it reaches Lua (#219, #220), so it is checked here and kept in a
// form of its own: only the keys MyGrenton reads, calls with checked IDs, indexes
// as numbers and script arguments as a list of numbers and strings.

const (
	maxInterface  = 4 << 20 // one data.json; a house of 12 pages has 135 KB
	maxInterfaces = 8
	maxText       = 200 // runes of a label or unit
)

var (
	reInterfaceID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
	reIdent       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	reIndex       = regexp.MustCompile(`^\d{1,4}$`)
	rePageID      = regexp.MustCompile(`^\d{1,19}$`)
	// a script's arguments as OM writes them: numbers and "strings", comma separated
	reArg = regexp.MustCompile(`^\s*(?:(-?\d+(?:\.\d+)?)|"([^"\\\x00-\x1f]*)")\s*(?:,|$)`)
)

// keywords are Lua 5.1's reserved words, which are no identifiers.
var keywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true, "end": true,
	"false": true, "for": true, "function": true, "if": true, "in": true, "local": true,
	"nil": true, "not": true, "or": true, "repeat": true, "return": true, "then": true,
	"true": true, "until": true, "while": true,
}

func isIdent(s string) bool { return reIdent.MatchString(s) && !keywords[s] }

// Interface is one myGrenton interface of the project.
type Interface struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Pages []Page `json:"pages"`
}

// Page is one page of an interface: a vclu dashboard.
type Page struct {
	ID      string   `json:"id"` // digits, as OM gives it
	Label   string   `json:"label"`
	Widgets []Widget `json:"widgets"`
	// LeftOut says, per widget or call, what was not taken and why.
	LeftOut []string `json:"leftOut,omitempty"`
}

// Widget is one widget, its keys as in the interface: a string, a number, a
// call (Call) or a part (a map of these). MyGrenton.widget takes it as a table.
type Widget map[string]any

// Call is a call of the interface, checked.
type Call struct {
	CallType   string `json:"callType"`
	CluID      string `json:"cluId"`
	ObjectName string `json:"objectName"`
	// Index is the feature or method number, or for VARIABLE the user feature's name.
	Index any    `json:"index,omitempty"`
	Type  string `json:"type,omitempty"` // ON, OFF or CLICK
	// Value is a number, a string or "$value$" (what the user enters).
	Value any `json:"value,omitempty"`
	// Args are a SCRIPT's arguments.
	Args []any `json:"args,omitempty"`
}

// widgetTypes are the widget types MyGrenton maps; SCHEDULER and anything else are left out.
var widgetTypes = map[string]bool{
	"ON_OFF": true, "VALUE": true, "VALUE_V2": true, "TEXT": true, "CONTACT_SENSOR": true,
	"SCENE": true, "SCENE_DOUBLE": true, "HEADER": true, "THERMOSTAT": true,
}

// dropKeys are what the panel draws differently anyway.
var dropKeys = map[string]bool{"image": true, "icon": true, "background": true, "rowId": true, "iconVisible": true}

// textKeys are free text: kept as strings, shown as text.
var textKeys = map[string]bool{
	"label": true, "unit": true, "onIndication": true, "offIndication": true, "indication": true,
	"valueType": true, "type": true,
}

// readInterfaces reads nodes/<id>/data.json of the project.
func (p *Project) readInterfaces(files map[string]*zip.File) {
	var names []string
	for name := range files {
		if strings.HasPrefix(name, "nodes/") && strings.HasSuffix(name, "/data.json") && strings.Count(name, "/") == 2 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if len(p.Interfaces) == maxInterfaces {
			p.warn(fmt.Sprintf("%s: more than %d myGrenton interfaces, left out", name, maxInterfaces))
			continue
		}
		rc, err := files[name].Open()
		if err != nil {
			p.warn(fmt.Sprintf("%s: %v", name, err))
			continue
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxInterface+1))
		_ = rc.Close()
		if err != nil || len(b) > maxInterface {
			p.warn(fmt.Sprintf("%s: unreadable or over %d MB, left out", name, maxInterface>>20))
			continue
		}
		in, err := p.ReadInterface(b)
		if err != nil {
			p.warn(fmt.Sprintf("%s: %v", name, err))
			continue
		}
		p.Interfaces = append(p.Interfaces, *in)
	}
}

// ReadInterface checks one interface's data.json.
func (p *Project) ReadInterface(data []byte) (*Interface, error) {
	var raw struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Pages []struct {
			ID      json.Number      `json:"id"`
			Label   string           `json:"label"`
			Widgets []map[string]any `json:"widgets"`
		} `json:"pages"`
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("not a myGrenton interface: %w", err)
	}
	if !reInterfaceID.MatchString(raw.ID) {
		return nil, fmt.Errorf("interface id %q is no id, left out", raw.ID)
	}
	in := &Interface{ID: raw.ID, Label: text(raw.Label), Pages: []Page{}}
	seen := map[string]bool{}
	for i, rp := range raw.Pages {
		id := rp.ID.String()
		if !rePageID.MatchString(id) || seen[id] {
			p.warn(fmt.Sprintf("interface %s: page %d has no id or one already used, left out", in.Label, i+1))
			continue
		}
		seen[id] = true
		pg := Page{ID: id, Label: text(rp.Label), Widgets: []Widget{}}
		for _, rw := range rp.Widgets {
			w, why := p.widget(rw)
			if why != nil {
				pg.LeftOut = append(pg.LeftOut, why...)
			}
			if w != nil {
				pg.Widgets = append(pg.Widgets, w)
			}
		}
		in.Pages = append(in.Pages, pg)
	}
	return in, nil
}

// widget checks one widget. A call that fails is dropped with the reason; the
// widget is kept when anything of it is left.
func (p *Project) widget(raw map[string]any) (Widget, []string) {
	typ, _ := raw["type"].(string)
	label := widgetLabel(raw)
	name := strings.TrimSpace(typ + " " + strconv.Quote(label))
	if !widgetTypes[typ] {
		if typ == "SCHEDULER" {
			return nil, []string{name + ": schedulers have no vclu widget yet"}
		}
		return nil, []string{name + ": widget type not supported"}
	}
	var why []string
	w := Widget{}
	for k, v := range raw {
		if dropKeys[k] || !isIdent(k) {
			continue
		}
		if v := p.value(k, v, name, &why); v != nil {
			w[k] = v
		}
	}
	if typ != "HEADER" && !hasCall(w) {
		return nil, append(why, name+": no call left, left out")
	}
	w["type"] = typ
	if id, ok := raw["id"].(json.Number); ok {
		if n, err := id.Int64(); err == nil {
			w["id"] = n
		}
	}
	return w, why
}

// value checks one value of a widget or part; nil leaves it out.
func (p *Project) value(key string, v any, widget string, why *[]string) any {
	switch v := v.(type) {
	case string:
		if textKeys[key] {
			return text(v)
		}
		return nil
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return f
		}
		return nil
	case bool:
		return v
	case map[string]any:
		if _, isCall := v["callType"]; isCall {
			c, err := call(v)
			if err == nil {
				err = p.inProject(c)
			}
			if err != nil {
				*why = append(*why, fmt.Sprintf("%s: %s: %v", widget, key, err))
				return nil
			}
			return c
		}
		part := map[string]any{}
		for k, pv := range v {
			if dropKeys[k] || !isIdent(k) {
				continue
			}
			if pv := p.value(k, pv, widget, why); pv != nil {
				part[k] = pv
			}
		}
		if len(part) == 0 {
			return nil
		}
		return part
	}
	return nil
}

// call checks a call: IDs against the patterns om.lua uses, names as identifiers,
// indexes and values as numbers.
func call(v map[string]any) (*Call, error) {
	s := func(k string) string { x, _ := v[k].(string); return x }
	c := &Call{CallType: s("callType"), CluID: s("cluId"), ObjectName: s("objectName"), Type: s("type")}
	if !reCLUID.MatchString(c.CluID) {
		return nil, fmt.Errorf("CLU %q is no CLU ID", c.CluID)
	}
	switch c.Type {
	case "", "ON", "OFF", "CLICK":
	default:
		return nil, fmt.Errorf("action type %q unknown", c.Type)
	}
	index := s("index")
	value := s("value")
	switch c.CallType {
	case "ATTRIBUTE", "PSEUDO_METHOD", "METHOD":
		if !reObjID.MatchString(c.ObjectName) || len(c.ObjectName) > 64 {
			return nil, fmt.Errorf("object %q is no object ID", c.ObjectName)
		}
		if !reIndex.MatchString(index) {
			return nil, fmt.Errorf("%s.%s: index %q is no number", c.CluID, c.ObjectName, index)
		}
		n, _ := strconv.Atoi(index)
		c.Index = n
		if c.CallType != "ATTRIBUTE" {
			c.Value = callValue(value)
		}
	case "SCRIPT":
		if !isIdent(c.ObjectName) {
			return nil, fmt.Errorf("script %q is no Lua name", c.ObjectName)
		}
		args, err := scriptArgs(value)
		if err != nil {
			return nil, fmt.Errorf("script %s: %v", c.ObjectName, err)
		}
		c.Args = args
	case "VARIABLE":
		if c.ObjectName != c.CluID {
			return nil, fmt.Errorf("user feature of %q, not of its CLU %s", c.ObjectName, c.CluID)
		}
		if !isIdent(index) {
			return nil, fmt.Errorf("user feature %q is no Lua name", index)
		}
		c.Index = index
	default:
		return nil, fmt.Errorf("call type %q unknown", c.CallType)
	}
	return c, nil
}

// callValue is what a set or a method takes: nothing, a number, what the user
// enters ("$value$") or a string.
func callValue(v string) any {
	if v == "" {
		return nil
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && !strings.ContainsAny(v, "xXnN") {
		return f
	}
	return text(v)
}

// scriptArgs reads a script's arguments, which OM keeps as Lua source: `1,-1`,
// `"on","2"`. Only numbers and plain strings are taken.
func scriptArgs(v string) ([]any, error) {
	args := []any{}
	rest := strings.TrimSpace(v)
	for rest != "" {
		m := reArg.FindStringSubmatchIndex(rest)
		if m == nil {
			return nil, fmt.Errorf("arguments %q are not numbers and plain strings", text(v))
		}
		if m[2] >= 0 {
			f, _ := strconv.ParseFloat(rest[m[2]:m[3]], 64)
			args = append(args, f)
		} else {
			args = append(args, rest[m[4]:m[5]])
		}
		rest = strings.TrimSpace(rest[m[1]:])
		if len(args) > 16 {
			return nil, fmt.Errorf("more than 16 arguments")
		}
	}
	if len(args) > 0 && strings.HasSuffix(strings.TrimSpace(v), ",") {
		return nil, fmt.Errorf("arguments %q end with a comma", text(v))
	}
	return args, nil
}

// text keeps free text as valid UTF-8 of at most maxText runes.
func text(s string) string {
	s = strings.ToValidUTF8(s, "?")
	if utf8.RuneCountInString(s) > maxText {
		s = string([]rune(s)[:maxText])
	}
	return s
}

// widgetLabel is the label a widget shows: its own or its first part's.
func widgetLabel(w map[string]any) string {
	if l, ok := w["label"].(string); ok && l != "" {
		return text(l)
	}
	keys := make([]string, 0, len(w))
	for k := range w {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if part, ok := w[k].(map[string]any); ok {
			if l, ok := part["label"].(string); ok && l != "" {
				return text(l)
			}
		}
	}
	return ""
}

// inProject says whether what a call names is in the project, as the proxies
// register it: the CLU, its object or peripheral, its script.
func (p *Project) inProject(c *Call) error {
	if p.known == nil {
		p.known = map[string]bool{}
		for _, clu := range p.CLUs {
			p.known[clu.ID] = true
			for _, o := range clu.Objects {
				p.known[clu.ID+"."+o.ID] = true
			}
			for _, o := range clu.Peripherals {
				if o.Class != 0 {
					p.known[clu.ID+"."+o.ID] = true
				}
			}
			for _, sc := range clu.Scripts {
				p.known[clu.ID+":"+sc.Name] = true
			}
		}
	}
	switch {
	case !p.known[c.CluID]:
		return fmt.Errorf("CLU %s is not in the project", c.CluID)
	case c.CallType == "SCRIPT":
		if !p.known[c.CluID+":"+c.ObjectName] {
			return fmt.Errorf("script %s is not on %s", c.ObjectName, c.CluID)
		}
	case c.CallType != "VARIABLE" && c.ObjectName != c.CluID && !p.known[c.CluID+"."+c.ObjectName]:
		return fmt.Errorf("%s.%s is not in the project, or vclu does not know its type", c.CluID, c.ObjectName)
	}
	return nil
}

// hasCall says whether a widget or part has a call.
func hasCall(m map[string]any) bool {
	for _, v := range m {
		switch v := v.(type) {
		case *Call:
			return true
		case map[string]any:
			if hasCall(v) {
				return true
			}
		}
	}
	return false
}
